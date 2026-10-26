package main

import (
	"coldchain-app/internal/ledger"
	"coldchain-app/internal/pharma"
	"coldchain-app/web"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	_ "modernc.org/sqlite"
)

const mandatoryPhrase = "Helooowww world eng. Computacional!!!!!!!"

type Telemetry struct {
	DeviceID string  `json:"device_id,omitempty"`
	TS       int64   `json:"ts"`
	T        float64 `json:"t"`
	H        float64 `json:"h"`
	Door     int     `json:"door"`
	Seq      int64   `json:"seq"`
	Sync     int     `json:"sync"`
	FW       string  `json:"fw"`
}

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	IsBlocked    int    `json:"is_blocked"`
	CreatedAt    string `json:"created_at"`
	FirstLogin   int    `json:"first_login"`
	HasCryptoKey bool   `json:"has_crypto_key"`
}

type AuditLog struct {
	ID        int64  `json:"id"`
	Timestamp string `json:"timestamp"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	Action    string `json:"action"`
	Details   string `json:"details"`
	IPAddress string `json:"ip_address"`
}

type Session struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expires_at"`
}

type App struct {
	db             *sql.DB
	ledger         *ledger.Ledger
	signer         ledger.Signer
	telemetryCount uint64
	mqttClient     mqtt.Client
	mu             sync.Mutex
	subscribers    map[chan Telemetry]struct{}
	latestSeq      map[string]int64
	devicesSeen    map[string]time.Time
	batchQueue     []Telemetry
	batchMu        sync.Mutex
}

func main() {
	healthCheckFlag := flag.Bool("healthcheck", false, "Executa o healthcheck do container e sai")
	flag.Parse()

	if *healthCheckFlag {
		resp, err := http.Get("http://127.0.0.1:5000/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	slog.Info("Iniciando Web-Server Cold Chain Gateway com RBAC e Auditoria", "versao", "3.3.0")

	// 1. Inicializa o banco de dados SQLite com WAL e synchronous=FULL
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/coldchain/coldchain.db"
	}
	db, err := initDB(dbPath)
	if err != nil {
		slog.Error("Falha ao inicializar SQLite", "erro", err)
		os.Exit(1)
	}
	defer db.Close()

	// 2. Inicializa o Assinador e o Ledger Criptográfico Imutável (Seção 6 & 7)
	keyPath := os.Getenv("LEDGER_KEY_PATH")
	if keyPath == "" {
		keyPath = "/data/coldchain/server_ledger_key.pem"
	}
	signer, err := ledger.NewFileSigner(keyPath)
	if err != nil {
		slog.Error("Falha ao inicializar assinador criptográfico", "erro", err)
		os.Exit(1)
	}
	led, err := ledger.NewLedger(db, signer, ledger.EnvTest)
	if err != nil {
		slog.Error("Falha ao inicializar ledger criptográfico", "erro", err)
		os.Exit(1)
	}

	app := &App{
		db:          db,
		ledger:      led,
		signer:      signer,
		subscribers: make(map[chan Telemetry]struct{}),
		latestSeq:   make(map[string]int64),
		devicesSeen: make(map[string]time.Time),
	}

	// 2. Loop de commit de lotes em background (~1 segundo)
	ctx, cancel := context.WithCancel(context.Background())
	go app.batchCommitLoop(ctx)
	go app.pharmaSimulatorLoop(ctx)

	// 3. Conexão com o broker MQTT (Eclipse Mosquitto)
	mqttURL := os.Getenv("MQTT_URL")
	if mqttURL == "" {
		mqttURL = "tcp://127.0.0.1:1883"
	}
	app.initMQTT(mqttURL)

	// 4. Servidor Web HTTP na porta 5000
	server := &http.Server{
		Addr:         "0.0.0.0:5000",
		Handler:      app.routes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("Servidor Web HTTP ouvindo", "porta", 5000)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Erro no servidor HTTP", "erro", err)
		}
	}()

	// 5. Parada limpa capturando SIGTERM e SIGINT
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigChan
	slog.Info("Recebido sinal de desligamento", "sinal", sig.String())

	cancel()
	app.flushBatch()

	// wal_checkpoint(TRUNCATE) para segurança de energia
	_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE);")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	slog.Info("Gateway encerrado de forma limpa.")
}

func generateCryptoKey() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("CC-KEY-%04X-%04X-%04X-%04X", time.Now().UnixNano()&0xFFFF, time.Now().Unix()&0xFFFF, 0xABCD, 0x1234)
	}
	hexStr := strings.ToUpper(hex.EncodeToString(b))
	return fmt.Sprintf("CC-KEY-%s-%s-%s-%s", hexStr[0:4], hexStr[4:8], hexStr[8:12], hexStr[12:16])
}

func hashPassword(password, salt string) string {
	h := sha256.New()
	h.Write([]byte(password + ":" + salt))
	return hex.EncodeToString(h.Sum(nil))
}

func generateSalt() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// getClientIP extrai o IP real da estação cliente que realizou o acesso/operação.
// Suporta detecção direta via socket TCP do kernel (net.SplitHostPort) e cabeçalhos
// de proxy reverso / gateway (X-Real-IP, X-Forwarded-For, CF-Connecting-IP, True-Client-IP).
// Remove prefixos IPv6 mapped IPv4 (::ffff:) para garantir formatação limpa.
func getClientIP(r *http.Request) string {
	for _, h := range []string{"X-Real-IP", "X-Forwarded-For", "X-Client-IP", "CF-Connecting-IP", "True-Client-IP"} {
		val := strings.TrimSpace(r.Header.Get(h))
		if val != "" {
			parts := strings.Split(val, ",")
			candidate := strings.TrimSpace(parts[0])
			if candidate != "" {
				if host, _, err := net.SplitHostPort(candidate); err == nil {
					candidate = host
				}
				if strings.HasPrefix(candidate, "::ffff:") {
					candidate = strings.TrimPrefix(candidate, "::ffff:")
				}
				return candidate
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		if strings.HasPrefix(host, "::ffff:") {
			host = strings.TrimPrefix(host, "::ffff:")
		}
		return host
	}
	raw := strings.TrimSpace(r.RemoteAddr)
	if strings.HasPrefix(raw, "::ffff:") {
		raw = strings.TrimPrefix(raw, "::ffff:")
	}
	return raw
}

func (a *App) logAudit(username, role, action, details, ip string) {
	if ip == "" {
		ip = "127.0.0.1"
	}
	_, err := a.db.Exec(`
		INSERT INTO audit_logs (username, role, action, details, ip_address)
		VALUES (?, ?, ?, ?, ?)
	`, username, role, action, details, ip)
	if err != nil {
		slog.Error("Falha ao registrar auditoria", "erro", err, "action", action)
	}
}

// writeSessionExpired responde 401 padronizado quando a sessão não existe ou expirou
// (ex.: reinício do servidor). O frontend detecta "session_expired" e reabre o login,
// evitando confundir sessão expirada com falta de permissão de perfil (403).
func (a *App) writeSessionExpired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]any{
		"error":           "Sessão expirada ou inválida. Faça login novamente.",
		"session_expired": true,
	})
}

func (a *App) getSession(r *http.Request) (*Session, bool) {
	cookie, err := r.Cookie("session_token")
	if err != nil || cookie.Value == "" {
		return nil, false
	}

	var sess Session
	var expiresStr string
	err = a.db.QueryRow(`
		SELECT token, username, role, name, expires_at 
		FROM sessions 
		WHERE token = ?
	`, cookie.Value).Scan(&sess.Token, &sess.Username, &sess.Role, &sess.Name, &expiresStr)

	if err != nil {
		return nil, false
	}

	expires, err := time.Parse("2006-01-02 15:04:05", expiresStr)
	if err != nil {
		expires, err = time.Parse(time.RFC3339, expiresStr)
	}
	if err == nil && time.Now().After(expires) {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE token = ?", sess.Token)
		return nil, false
	}

	var isBlocked int
	err = a.db.QueryRow("SELECT is_blocked FROM users WHERE username = ?", sess.Username).Scan(&isBlocked)
	if err == nil && isBlocked == 1 {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE token = ?", sess.Token)
		return nil, false
	}

	return &sess, true
}

func (a *App) verifySignature(username, passwordOrKey string) bool {
	key := strings.TrimSpace(passwordOrKey)
	if key == "" {
		return false
	}
	var salt, cryptoKeyHash string
	if err := a.db.QueryRow("SELECT salt, COALESCE(crypto_key_hash, '') FROM users WHERE username = ?", username).Scan(&salt, &cryptoKeyHash); err != nil {
		return false
	}
	if cryptoKeyHash == "" {
		return false
	}
	h := hashPassword(key, salt)
	// Segurança estrita 21 CFR Part 11: apenas a Chave Criptográfica Pessoal gerada é válida para assinaturas digitais.
	// A senha de login na conta é terminantemente REJEITADA como assinatura.
	return subtle.ConstantTimeCompare([]byte(h), []byte(cryptoKeyHash)) == 1
}

func seedUsers(db *sql.DB) error {
	_, _ = db.Exec(`
		CREATE TRIGGER IF NOT EXISTS trg_audit_logs_no_delete
		BEFORE DELETE ON audit_logs
		BEGIN
			SELECT RAISE(ABORT, 'Segurança: Logs de auditoria são estritamente append-only e não podem ser excluídos.');
		END;
	`)

	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count > 0 {
		return nil
	}

	seeds := []struct {
		username   string
		password   string
		name       string
		role       string
		firstLogin int
	}{
		{"admin", "admin123", "Administrador Geral", "admin", 1},
	}

	for _, s := range seeds {
		salt := generateSalt()
		hash := hashPassword(s.password, salt)
		_, err := db.Exec(`
			INSERT INTO users (username, password_hash, salt, name, role, is_blocked, first_login, crypto_key_hash)
			VALUES (?, ?, ?, ?, ?, 0, ?, '')
		`, s.username, hash, salt, s.name, s.role, s.firstLogin)
		if err != nil {
			return err
		}
	}

	return nil
}

func initDB(path string) (*sql.DB, error) {
	_ = os.MkdirAll("/data/coldchain", 0700)
	dsn := path
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=cache_size(-20000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=FULL;",
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA mmap_size=33554432;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA journal_size_limit=6291456;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return nil, fmt.Errorf("erro no pragma %s: %w", p, err)
		}
	}

	schema := `
	CREATE TABLE IF NOT EXISTS readings (
		device_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		ts INTEGER NOT NULL,
		t REAL NOT NULL,
		h REAL NOT NULL,
		door INTEGER NOT NULL,
		sync INTEGER NOT NULL,
		fw TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (device_id, seq)
	);
	CREATE INDEX IF NOT EXISTS idx_readings_device_ts ON readings (device_id, ts);

	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		salt TEXT NOT NULL,
		name TEXT NOT NULL,
		role TEXT NOT NULL,
		is_blocked INTEGER DEFAULT 0,
		first_login INTEGER DEFAULT 0,
		crypto_key_hash TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		username TEXT NOT NULL,
		role TEXT NOT NULL,
		action TEXT NOT NULL,
		details TEXT NOT NULL,
		ip_address TEXT NOT NULL
	);

	CREATE TRIGGER IF NOT EXISTS trg_audit_logs_no_update
	BEFORE UPDATE ON audit_logs
	BEGIN
		SELECT RAISE(ABORT, 'Segurança: Logs de auditoria são estritamente imutáveis.');
	END;

	CREATE TRIGGER IF NOT EXISTS trg_audit_logs_no_delete
	BEFORE DELETE ON audit_logs
	BEGIN
		SELECT RAISE(ABORT, 'Segurança: Logs de auditoria são estritamente append-only e não podem ser excluídos.');
	END;

	CREATE TABLE IF NOT EXISTS sessions (
		token TEXT PRIMARY KEY,
		username TEXT NOT NULL,
		role TEXT NOT NULL,
		name TEXT NOT NULL,
		expires_at DATETIME NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("erro no schema: %w", err)
	}

	// Migrations de colunas para users existentes
	_, _ = db.Exec("ALTER TABLE users ADD COLUMN crypto_key_hash TEXT DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE users ADD COLUMN first_login INTEGER DEFAULT 0")

	if err := pharma.InitPharmaSchema(db); err != nil {
		return nil, fmt.Errorf("erro ao inicializar schema pharma: %w", err)
	}

	if err := seedUsers(db); err != nil {
		return nil, fmt.Errorf("erro ao popular usuários padrão: %w", err)
	}

	return db, nil
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	// Requisito 13: Rota / com negociação por cabeçalho Accept e Vary: Accept
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Vary", "Accept")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")

		accept := r.Header.Get("Accept")
		if strings.Contains(accept, "text/html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			data, err := fs.ReadFile(web.Files, "index.html")
			if err != nil {
				http.Error(w, "Erro ao carregar painel", http.StatusInternalServerError)
				return
			}
			w.Write(data)
			return
		}

		// Resposta pura para curl (Requisito 13)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, mandatoryPhrase)
	})

	// Arquivos estáticos
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(web.Files))))

	// Healthcheck
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":   "ok",
			"version":  "3.3.0",
			"database": "ok",
		})
	})

	// API Status
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var count int
		_ = a.db.QueryRow("SELECT COUNT(*) FROM readings").Scan(&count)
		a.mu.Lock()
		devCount := len(a.devicesSeen)
		a.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"status":         "running",
			"version":        "3.3.0",
			"readings_count": count,
			"devices_count":  devCount,
			"phrase":         mandatoryPhrase,
		})
	})

	// API Auth: Login
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Requisição inválida", http.StatusBadRequest)
			return
		}

		req.Username = strings.ToLower(strings.TrimSpace(req.Username))
		ip := getClientIP(r)

		var (
			id            int64
			passwordHash  string
			salt          string
			name          string
			role          string
			isBlocked     int
			firstLogin    int
			cryptoKeyHash string
		)
		err := a.db.QueryRow(`
			SELECT id, password_hash, salt, name, role, is_blocked, COALESCE(first_login, 0), COALESCE(crypto_key_hash, '')
			FROM users WHERE username = ?
		`, req.Username).Scan(&id, &passwordHash, &salt, &name, &role, &isBlocked, &firstLogin, &cryptoKeyHash)

		if err != nil {
			a.logAudit(req.Username, "desconhecido", "LOGIN_FAILED", "Tentativa de login com usuário inexistente", ip)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": "Usuário ou credencial de acesso incorretos."})
			return
		}

		computed := hashPassword(req.Password, salt)
		isPwdMatch := subtle.ConstantTimeCompare([]byte(computed), []byte(passwordHash)) == 1
		isKeyMatch := cryptoKeyHash != "" && subtle.ConstantTimeCompare([]byte(computed), []byte(cryptoKeyHash)) == 1

		if !isPwdMatch && !isKeyMatch {
			a.logAudit(req.Username, role, "LOGIN_FAILED", "Credencial incorreta informada", ip)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": "Usuário ou credencial de acesso incorretos."})
			return
		}

		if isBlocked == 1 {
			a.logAudit(req.Username, role, "LOGIN_BLOCKED", "Tentativa de login em conta bloqueada", ip)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Esta conta de usuário foi bloqueada pelo administrador."})
			return
		}

		// Geração de chave criptográfica única no primeiro acesso (RDC 430 e 21 CFR Part 11)
		var generatedCryptoKey string
		isFirstLogin := false
		if firstLogin == 1 {
			isFirstLogin = true
			generatedCryptoKey = generateCryptoKey()
			newKeyHash := hashPassword(generatedCryptoKey, salt)
			_, err = a.db.Exec(`
				UPDATE users 
				SET crypto_key_hash = ?, first_login = 0 
				WHERE id = ?
			`, newKeyHash, id)
			if err != nil {
				slog.Error("Erro ao registrar chave criptográfica", "err", err, "username", req.Username)
				http.Error(w, fmt.Sprintf("Erro ao registrar chave criptográfica: %v", err), http.StatusInternalServerError)
				return
			}
			a.logAudit(req.Username, role, "CRYPTO_KEY_ACTIVATED", fmt.Sprintf("Primeiro acesso confirmado. Chave criptográfica única gerada com sucesso para '%s'.", req.Username), ip)
		}

		token := generateToken()
		expires := time.Now().Add(24 * time.Hour)
		_, err = a.db.Exec(`
			INSERT INTO sessions (token, username, role, name, expires_at)
			VALUES (?, ?, ?, ?, ?)
		`, token, req.Username, role, name, expires.Format("2006-01-02 15:04:05"))

		if err != nil {
			slog.Error("Erro ao criar sessão", "err", err, "username", req.Username)
			http.Error(w, fmt.Sprintf("Erro ao criar sessão: %v", err), http.StatusInternalServerError)
			return
		}

		a.logAudit(req.Username, role, "LOGIN_SUCCESS", "Autenticação bem-sucedida no sistema", ip)

		http.SetCookie(w, &http.Cookie{
			Name:     "session_token",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  expires,
		})

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"status":      "ok",
			"first_login": isFirstLogin,
			"user": map[string]any{
				"username": req.Username,
				"name":     name,
				"role":     role,
			},
		}
		if isFirstLogin {
			resp["crypto_key"] = generatedCryptoKey
		}
		json.NewEncoder(w).Encode(resp)
	})

	// API Auth: Logout
	mux.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		ip := getClientIP(r)
		if sess, ok := a.getSession(r); ok {
			a.logAudit(sess.Username, sess.Role, "LOGOUT", "Encerramento voluntário de sessão", ip)
			_, _ = a.db.Exec("DELETE FROM sessions WHERE token = ?", sess.Token)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "session_token",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})

	// API Auth: Me
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"authenticated": true,
			"user": map[string]any{
				"username": sess.Username,
				"name":     sess.Name,
				"role":     sess.Role,
			},
		})
	})


	// API Auditoria: Logs de Auditoria (Restrito a Gerente e Admin)
	mux.HandleFunc("/api/audit/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "tecnico" && sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{
				"error": "Acesso não autorizado. Apenas Gerente ou Administrador podem visualizar a trilha de auditoria.",
			})
			return
		}

		rows, err := a.db.Query(`
			SELECT id, timestamp, username, role, action, details, ip_address
			FROM audit_logs
			ORDER BY id DESC
			LIMIT 100
		`)
		if err != nil {
			http.Error(w, "Erro ao buscar logs", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var logs []AuditLog
		for rows.Next() {
			var l AuditLog
			if err := rows.Scan(&l.ID, &l.Timestamp, &l.Username, &l.Role, &l.Action, &l.Details, &l.IPAddress); err == nil {
				logs = append(logs, l)
			}
		}
		if logs == nil {
			logs = []AuditLog{}
		}
		json.NewEncoder(w).Encode(logs)
	})

	// API Admin: Listar Usuários
	mux.HandleFunc("/api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Acesso restrito ao Administrador do Sistema."})
			return
		}

		rows, err := a.db.Query(`
			SELECT id, username, name, role, is_blocked, created_at, COALESCE(first_login, 0), CASE WHEN crypto_key_hash != '' THEN 1 ELSE 0 END
			FROM users
			ORDER BY id ASC
		`)
		if err != nil {
			http.Error(w, "Erro ao buscar usuários", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var users []User
		for rows.Next() {
			var u User
			var hasKeyInt int
			if err := rows.Scan(&u.ID, &u.Username, &u.Name, &u.Role, &u.IsBlocked, &u.CreatedAt, &u.FirstLogin, &hasKeyInt); err == nil {
				u.HasCryptoKey = (hasKeyInt == 1)
				users = append(users, u)
			}
		}
		if users == nil {
			users = []User{}
		}
		json.NewEncoder(w).Encode(users)
	})

	// API Admin: Criar Usuário
	mux.HandleFunc("/api/admin/users/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Apenas o Administrador pode cadastrar novos usuários."})
			return
		}

		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Name     string `json:"name"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Dados inválidos."})
			return
		}

		req.Username = strings.ToLower(strings.TrimSpace(req.Username))
		req.Name = strings.TrimSpace(req.Name)
		req.Role = strings.ToLower(strings.TrimSpace(req.Role))

		if req.Username == "" || req.Password == "" || req.Name == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Todos os campos são obrigatórios."})
			return
		}

		if req.Role != "enfermeiro" && req.Role != "tecnico" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Perfil inválido. Permitidos: enfermeiro, tecnico (Responsável Técnico)."})
			return
		}

		salt := generateSalt()
		hash := hashPassword(req.Password, salt)

		var insertErr error
		for attempt := 0; attempt < 8; attempt++ {
			_, insertErr = a.db.Exec(`
				INSERT INTO users (username, password_hash, salt, name, role, is_blocked, first_login, crypto_key_hash)
				VALUES (?, ?, ?, ?, ?, 0, 1, '')
			`, req.Username, hash, salt, req.Name, req.Role)
			if insertErr == nil {
				break
			}
			errStr := strings.ToLower(insertErr.Error())
			if strings.Contains(errStr, "locked") || strings.Contains(errStr, "busy") {
				time.Sleep(120 * time.Millisecond)
				continue
			}
			break
		}

		if insertErr != nil {
			var exists int
			_ = a.db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", req.Username).Scan(&exists)
			if exists > 0 {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]any{"error": "Nome de usuário já existe no sistema."})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": fmt.Sprintf("Erro ao cadastrar usuário: %v", insertErr)})
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "USER_CREATED", fmt.Sprintf("Criado usuário '%s' (%s) com perfil '%s'", req.Username, req.Name, req.Role), ip)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "Usuário criado com sucesso!"})
	})

	// API Admin: Bloquear / Desbloquear Usuário
	mux.HandleFunc("/api/admin/users/toggle-block", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Acesso restrito ao Administrador."})
			return
		}

		var req struct {
			UserID    int64  `json:"user_id"`
			Username  string `json:"username"`
			IsBlocked *int   `json:"is_blocked"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Dados inválidos."})
			return
		}

		var targetUser string
		var currentBlock int
		var err error

		if req.UserID > 0 {
			err = a.db.QueryRow("SELECT username, is_blocked FROM users WHERE id = ?", req.UserID).Scan(&targetUser, &currentBlock)
		} else if req.Username != "" {
			err = a.db.QueryRow("SELECT username, is_blocked FROM users WHERE username = ?", strings.ToLower(strings.TrimSpace(req.Username))).Scan(&targetUser, &currentBlock)
		} else {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Identificador do usuário não informado."})
			return
		}

		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": "Usuário não encontrado."})
			return
		}

		if targetUser == "admin" || targetUser == sess.Username {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Não é permitido bloquear a conta de Administrador Geral."})
			return
		}

		newBlock := 1 - currentBlock
		if req.IsBlocked != nil {
			newBlock = *req.IsBlocked
		}

		_, err = a.db.Exec("UPDATE users SET is_blocked = ? WHERE username = ?", newBlock, targetUser)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": fmt.Sprintf("Erro ao atualizar status: %v", err)})
			return
		}

		if newBlock == 1 {
			_, _ = a.db.Exec("DELETE FROM sessions WHERE username = ?", targetUser)
		}

		ip := getClientIP(r)
		actionDesc := "desbloqueado"
		if newBlock == 1 {
			actionDesc = "bloqueado"
		}
		a.logAudit(sess.Username, sess.Role, "USER_STATUS_CHANGE", fmt.Sprintf("Usuário '%s' foi %s pelo administrador", targetUser, actionDesc), ip)

		json.NewEncoder(w).Encode(map[string]any{
			"status":     "ok",
			"is_blocked": newBlock,
			"message":    fmt.Sprintf("Usuário '%s' %s com sucesso.", targetUser, actionDesc),
		})
	})

	// API Admin: Alterar Grau de Permissão

	// API Admin: Excluir Usuário (apenas não-seed)
	mux.HandleFunc("/api/admin/users/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Acesso restrito ao Administrador."})
			return
		}

		var req struct {
			UserID int64 `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Dados inválidos."})
			return
		}

		var targetUser string
		_ = a.db.QueryRow("SELECT username FROM users WHERE id = ?", req.UserID).Scan(&targetUser)
		if targetUser == "admin" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Não é permitido excluir os perfis padrão do sistema."})
			return
		}

		_, err := a.db.Exec("DELETE FROM users WHERE id = ?", req.UserID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "Erro ao excluir usuário."})
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "USER_DELETED", fmt.Sprintf("Excluído usuário '%s' (ID #%d)", targetUser, req.UserID), ip)

		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "Usuário excluído com sucesso."})
	})

	mux.HandleFunc("/api/admin/users/update-role", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Acesso restrito ao Administrador."})
			return
		}

		var req struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Dados inválidos."})
			return
		}

		req.Role = strings.ToLower(strings.TrimSpace(req.Role))
		if req.Role != "enfermeiro" && req.Role != "tecnico" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Perfil inválido. Permitidos: enfermeiro, tecnico (Responsável Técnico)."})
			return
		}

		_, err := a.db.Exec("UPDATE users SET role = ? WHERE username = ?", req.Role, req.Username)
		if err != nil {
			http.Error(w, "Erro ao atualizar perfil", http.StatusInternalServerError)
			return
		}

		_, _ = a.db.Exec("UPDATE sessions SET role = ? WHERE username = ?", req.Role, req.Username)

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "USER_ROLE_CHANGE", fmt.Sprintf("Perfil do usuário '%s' alterado para '%s' pelo administrador", req.Username, req.Role), ip)

		json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"role":    req.Role,
			"message": fmt.Sprintf("Perfil de %s alterado para %s.", req.Username, req.Role),
		})
	})

	// Exportação CSV com sanitização contra fórmula injection
	mux.HandleFunc("/api/export.csv", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment;filename=coldchain_readings.csv")

		writer := csv.NewWriter(w)
		defer writer.Flush()
		_ = writer.Write([]string{"device_id", "seq", "ts", "temperature", "humidity", "door"})

		rows, err := a.db.Query("SELECT device_id, seq, ts, t, h, door FROM readings ORDER BY ts DESC LIMIT 1000")
		if err != nil {
			return
		}
		defer rows.Close()

		for rows.Next() {
			var dev string
			var seq, ts int64
			var t, h float64
			var door int
			if err := rows.Scan(&dev, &seq, &ts, &t, &h, &door); err == nil {
				safeDev := dev
				if strings.HasPrefix(safeDev, "=") || strings.HasPrefix(safeDev, "+") || strings.HasPrefix(safeDev, "-") || strings.HasPrefix(safeDev, "@") {
					safeDev = "'" + safeDev
				}
				_ = writer.Write([]string{
					safeDev,
					strconv.FormatInt(seq, 10),
					strconv.FormatInt(ts, 10),
					fmt.Sprintf("%.2f", t),
					fmt.Sprintf("%.1f", h),
					strconv.Itoa(door),
				})
			}
		}
	})

	// Server-Sent Events (SSE)
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE não suportado", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch := make(chan Telemetry, 10)
		a.mu.Lock()
		a.subscribers[ch] = struct{}{}
		a.mu.Unlock()

		defer func() {
			a.mu.Lock()
			delete(a.subscribers, ch)
			a.mu.Unlock()
			close(ch)
		}()

		for {
			select {
			case <-r.Context().Done():
				return
			case t := <-ch:
				payload, _ := json.Marshal(map[string]any{"type": "telemetry", "payload": t})
				fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}
	})

	// =========================================================================
	// ROTAS DE AUDITORIA CRIPTOGRÁFICA E LEDGER WORM (ANVISA RDC 430/2020)
	// =========================================================================

	// API Ledger: Consulta de Blocos Paginados
	mux.HandleFunc("/api/v1/ledger/records", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if a.ledger == nil {
			json.NewEncoder(w).Encode(map[string]any{"records": []any{}, "total": 0})
			return
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 50
		}
		records, total, err := a.ledger.GetRecords(offset, limit)
		if err != nil {
			http.Error(w, "Erro ao buscar blocos do ledger", http.StatusInternalServerError)
			return
		}
		kid := a.signer.KeyID()
		json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"total":     total,
			"records":   records,
			"key_id":    hex.EncodeToString(kid[:]),
			"algorithm": "ECDSA-P256-SHA256",
			"standard":  "ANVISA RDC 430/2020 & 21 CFR Part 11",
		})
	})

	// API Ledger: Chave Pública e Metadados do Assinador
	mux.HandleFunc("/api/v1/ledger/public-key", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if a.signer == nil {
			http.Error(w, "Assinador não inicializado", http.StatusInternalServerError)
			return
		}
		pemStr, _ := a.signer.PublicKeyPEM()
		rawHex := hex.EncodeToString(a.signer.PublicKeyRaw())
		kid := a.signer.KeyID()
		json.NewEncoder(w).Encode(map[string]any{
			"status":           "ok",
			"key_id":           hex.EncodeToString(kid[:]),
			"public_key_pem":   pemStr,
			"public_key_raw":   rawHex,
			"curve":            "P-256 (secp256r1)",
			"signature_format": "raw r||s (64 bytes)",
			"phase":            "TEST (Chave em flash; Migração ATECC pronta)",
		})
	})

	// API Ledger: Auditoria Matemática em Tempo Real (Watchdog Pericial)
	mux.HandleFunc("/api/v1/ledger/verify", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if a.db == nil || a.signer == nil {
			json.NewEncoder(w).Encode(map[string]any{"valid": true, "total_records": 0})
			return
		}
		pubKey, ok := a.signer.Public().(*ecdsa.PublicKey)
		if !ok {
			http.Error(w, "Chave pública não é ECDSA", http.StatusInternalServerError)
			return
		}
		report, err := ledger.VerifyChain(a.db, pubKey)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(report)
	})

	// API Ledger: Exportação de Laudo Pericial Regulatório Completo (.JSON)
	mux.HandleFunc("/api/v1/ledger/export", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment;filename=coldchain_laudo_pericial_anvisa.json")
		if a.ledger == nil || a.signer == nil {
			http.Error(w, "Ledger não inicializado", http.StatusInternalServerError)
			return
		}
		pubKey, _ := a.signer.Public().(*ecdsa.PublicKey)
		report, _ := ledger.VerifyChain(a.db, pubKey)
		records, total, _ := a.ledger.GetRecords(0, 50000)
		pemStr, _ := a.signer.PublicKeyPEM()
		kid := a.signer.KeyID()

		exportData := map[string]any{
			"regulatory_standard": "ANVISA RDC 430/2020, RDC 304/2019 & 21 CFR Part 11",
			"document_title":      "Laudo Pericial Criptográfico de Cadeia de Frio (Ledger WORM)",
			"generated_at":        time.Now().UTC().Format(time.RFC3339Nano),
			"total_records":       total,
			"verification_report": report,
			"server_authority": map[string]any{
				"algorithm":      "ECDSA P-256 + SHA-256",
				"key_id":         hex.EncodeToString(kid[:]),
				"public_key_pem": pemStr,
			},
			"records": records,
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(exportData)
	})

	// API Ledger: Emissão Manual de Âncora Criptográfica
	mux.HandleFunc("/api/v1/ledger/anchor", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "tecnico" && sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Acesso restrito ao Gerente ou Administrador."})
			return
		}
		if a.ledger == nil {
			http.Error(w, "Ledger indisponível", http.StatusInternalServerError)
			return
		}
		receipt := fmt.Sprintf("MANUAL_RFC3161_TSA_RECEIPT_%s_%d", sess.Username, time.Now().Unix())
		rec, err := a.ledger.CreateAnchor(receipt)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "LEDGER_ANCHOR", fmt.Sprintf("Âncora criptográfica #%d emitida manualmente", rec.ID), ip)
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"anchor":  rec,
			"message": fmt.Sprintf("Âncora #%d gravada com sucesso.", rec.ID),
		})
	})

	// =========================================================================
	// ROTAS PHARMA & ANVISA (RDC 430/2020 & RDC 197/2017)
	// =========================================================================

	// 1. Câmaras Frias & Equipamentos de Armazenagem (GET / POST / PUT)
	mux.HandleFunc("/api/pharma/chambers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			sess, ok := a.getSession(r)
			if !ok {
				a.writeSessionExpired(w)
				return
			}
			if sess.Role != "tecnico" && sess.Role != "admin" {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"error": "Cadastro de câmaras restrito a Farmacêutico RT / Gerente / Administrador."})
				return
			}
			var payload struct {
				pharma.Chamber
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "JSON inválido", http.StatusBadRequest)
				return
			}
			if payload.Password == "" || !a.verifySignature(sess.Username, payload.Password) {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital obrigatória: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar cadastros (21 CFR Part 11)."})
				return
			}
			created, err := pharma.RegisterChamber(a.db, payload.Chamber)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			ip := getClientIP(r)
			a.logAudit(sess.Username, sess.Role, "CADASTRO_CAMARA", fmt.Sprintf("Equipamento '%s' (ID %s, Tipo: %s, Faixa: %.1f a %.1f °C) cadastrado por %s", created.Name, created.ID, created.Type, created.TempMin, created.TempMax, sess.Name), ip)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(created)
			return
		}

		if r.Method == http.MethodPut {
			sess, ok := a.getSession(r)
			if !ok {
				a.writeSessionExpired(w)
				return
			}
			if sess.Role != "tecnico" && sess.Role != "admin" {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"error": "Edição de câmaras restrita a Farmacêutico RT / Gerente / Administrador."})
				return
			}
			var payload pharma.Chamber
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "JSON inválido", http.StatusBadRequest)
				return
			}
			if err := pharma.UpdateChamber(a.db, payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			ip := getClientIP(r)
			a.logAudit(sess.Username, sess.Role, "EDICAO_CAMARA", fmt.Sprintf("Parâmetros da câmara '%s' alterados por %s (Temp: %.1f a %.1f °C, Delay: %d min)", payload.ID, sess.Name, payload.TempMin, payload.TempMax, payload.AlarmDelayMin), ip)
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "Câmara atualizada com sucesso."})
			return
		}

		if r.Method == http.MethodDelete {
			sess, ok := a.getSession(r)
			if !ok {
				a.writeSessionExpired(w)
				return
			}
			if sess.Role != "tecnico" && sess.Role != "admin" {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"error": "Descomissionamento restrito a Gerente ou Administrador."})
				return
			}

			var req struct {
				ChamberID     string `json:"chamber_id"`
				Reason        string `json:"reason"`
				Justification string `json:"justification"`
				SensorAction  string `json:"sensor_action"`
				Password      string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				req.ChamberID = r.URL.Query().Get("id")
			}

			if req.ChamberID == "" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": "ID da câmara é obrigatório."})
				return
			}

			if req.Password == "" || !a.verifySignature(sess.Username, req.Password) {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital inválida: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar assinaturas (21 CFR Part 11)."})
				return
			}

			if err := pharma.DecommissionChamber(a.db, req.ChamberID, req.Reason, req.Justification, sess.Name, req.SensorAction); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}

			ip := getClientIP(r)
			a.logAudit(sess.Username, sess.Role, "DESCOMISSIONAMENTO_CAMARA", fmt.Sprintf("Equipamento '%s' descomissionado por %s. Motivo: %s. Parecer: %s", req.ChamberID, sess.Name, req.Reason, req.Justification), ip)

			if a.ledger != nil {
				ledgerData, _ := json.Marshal(map[string]any{
					"event":         "CHAMBER_DECOMMISSION",
					"chamber_id":    req.ChamberID,
					"reason":        req.Reason,
					"justification": req.Justification,
					"responsible":   sess.Name,
					"role":          sess.Role,
					"timestamp":     time.Now().Format(time.RFC3339),
				})
				_, _ = a.ledger.AppendData("DECOMMISSION-"+req.ChamberID, uint64(time.Now().Unix()), ledgerData, ledger.ClockTrusted)
			}

			json.NewEncoder(w).Encode(map[string]any{
				"status":  "ok",
				"message": fmt.Sprintf("Equipamento '%s' descomissionado e registrado no ledger com sucesso.", req.ChamberID),
			})
			return
		}

		list, err := pharma.GetChambers(a.db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(list)
	})

	// 2. Sensores Metrológicos RBC & Mapeamento Térmico (GET / POST / PUT / DELETE)
	mux.HandleFunc("/api/pharma/sensors", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			sess, ok := a.getSession(r)
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{"error": "Autenticação requerida."})
				return
			}
			if sess.Role != "tecnico" && sess.Role != "admin" {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"error": "Acesso negado: apenas Responsável Técnico ou Admin podem cadastrar sensores."})
				return
			}

			var payload struct {
				pharma.Sensor
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": "Payload JSON inválido."})
				return
			}

			if payload.Password == "" || !a.verifySignature(sess.Username, payload.Password) {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital obrigatória: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar cadastros (21 CFR Part 11)."})
				return
			}

			created, err := pharma.RegisterSensor(a.db, payload.Sensor)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}

			ip := getClientIP(r)
			a.logAudit(sess.Username, sess.Role, "CADASTRO_SENSOR", fmt.Sprintf("Novo sensor '%s' cadastrado na câmara '%s' (Série: %s, Cert: %s, Validade: %s)", created.ID, created.ChamberID, created.SerialNumber, created.CertNumber, created.CalibValidUntil), ip)

			if a.ledger != nil {
				ledgerData, _ := json.Marshal(map[string]any{
					"event":       "SENSOR_REGISTER",
					"sensor_id":   created.ID,
					"chamber_id":  created.ChamberID,
					"cert_number": created.CertNumber,
					"by":          sess.Username,
					"timestamp":   time.Now().Format(time.RFC3339),
				})
				_, _ = a.ledger.AppendData("PHARMA-GATEWAY", uint64(time.Now().UnixNano()), ledgerData, 1)
			}

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(created)
			return
		}

		if r.Method == http.MethodPut {
			sess, ok := a.getSession(r)
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{"error": "Autenticação requerida."})
				return
			}
			if sess.Role != "tecnico" && sess.Role != "admin" {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"error": "Acesso negado: apenas Responsável Técnico ou Admin podem atualizar/recalibrar sensores."})
				return
			}

			var payload struct {
				pharma.Sensor
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": "Payload JSON inválido."})
				return
			}

			if payload.Password == "" || !a.verifySignature(sess.Username, payload.Password) {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital obrigatória: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar cadastros (21 CFR Part 11)."})
				return
			}

			if err := pharma.UpdateSensor(a.db, payload.Sensor); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}

			ip := getClientIP(r)
			a.logAudit(sess.Username, sess.Role, "RECALIBRACAO_SENSOR", fmt.Sprintf("Sensor '%s' recalibrado/atualizado por %s (Cert: %s, Nova Validade: %s, Status: %s)", payload.ID, sess.Name, payload.CertNumber, payload.CalibValidUntil, payload.Status), ip)

			if a.ledger != nil {
				ledgerData, _ := json.Marshal(map[string]any{
					"event":       "SENSOR_RECALIBRATION",
					"sensor_id":   payload.ID,
					"cert_number": payload.CertNumber,
					"by":          sess.Username,
					"timestamp":   time.Now().Format(time.RFC3339),
				})
				_, _ = a.ledger.AppendData("PHARMA-GATEWAY", uint64(time.Now().UnixNano()), ledgerData, 1)
			}

			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "Sensor atualizado com sucesso."})
			return
		}

		if r.Method == http.MethodDelete {
			a.handleDecommissionSensor(w, r)
			return
		}

		chID := r.URL.Query().Get("chamber")
		list, err := pharma.GetSensors(a.db, chID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("/api/pharma/sensors/decommission", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		a.handleDecommissionSensor(w, r)
	})

	// 3. Catálogo de Medicamentos ANVISA (GET / POST)
	mux.HandleFunc("/api/pharma/products", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			sess, ok := a.getSession(r)
			if !ok {
				a.writeSessionExpired(w)
				return
			}
			if sess.Role != "tecnico" && sess.Role != "admin" && sess.Role != "enfermeiro" {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"error": "Cadastro de medicamentos restrito a Farmacêutico RT / Gerente / Administrador."})
				return
			}
			var payload struct {
				pharma.Product
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "JSON inválido", http.StatusBadRequest)
				return
			}
			if payload.Password != "" {
				if !a.verifySignature(sess.Username, payload.Password) {
					w.WriteHeader(http.StatusUnauthorized)
					json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital inválida: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar assinaturas digitais (21 CFR Part 11)."})
					return
				}
			}
			created, err := pharma.RegisterProduct(a.db, payload.Product)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			ip := getClientIP(r)
			a.logAudit(sess.Username, sess.Role, "CADASTRO_PRODUTO", fmt.Sprintf("Medicamento '%s' (DCB: %s, Reg. ANVISA: %s) cadastrado por %s", created.TradeName, created.DCB, created.AnvisaReg, sess.Name), ip)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(created)
			return
		}

		list, err := pharma.GetProducts(a.db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(list)
	})

	// 4. Lotes com Ordenamento FEFO
	mux.HandleFunc("/api/pharma/lots", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		chID := r.URL.Query().Get("chamber")
		st := r.URL.Query().Get("status")
		list, err := pharma.GetLots(a.db, chID, st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(list)
	})

	// 5. Laudo Pericial de Rastreabilidade Completa do Lote
	mux.HandleFunc("/api/pharma/lots/trace", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		lotNum := r.URL.Query().Get("lot")
		if lotNum == "" {
			http.Error(w, "Parâmetro 'lot' obrigatório", http.StatusBadRequest)
			return
		}
		rep, err := pharma.GetLotTrace(a.db, lotNum)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(rep)
	})

	// 6. Entrada de Estoque na Doca (Recebimento com Termômetro)
	mux.HandleFunc("/api/pharma/stock/entry", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "tecnico" && sess.Role != "admin" && sess.Role != "enfermeiro" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Acesso restrito ao Técnico, Gerente ou Administrador."})
			return
		}

		var payload struct {
			ProductID              int64   `json:"product_id"`
			LotNumber              string  `json:"lot_number"`
			ExpiryDate             string  `json:"expiry_date"`
			Quantity               int     `json:"quantity"`
			ChamberID              string  `json:"chamber_id"`
			ShelfLocation          string  `json:"shelf_location"`
			UnitPrice              float64 `json:"unit_price"`
			Supplier               string  `json:"supplier"`
			SupplierCNPJ           string  `json:"supplier_cnpj"`
			InvoiceNumber          string  `json:"invoice_number"`
			TransportCondition     string  `json:"transport_condition"`
			ArrivalTemp            float64 `json:"arrival_temp"`
			CargoIntegrity         string  `json:"cargo_integrity"`
			AmbientExposureMinutes int     `json:"ambient_exposure_minutes"`
			Decision               string  `json:"decision"`
			Password               string  `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}
		if payload.Password == "" || !a.verifySignature(sess.Username, payload.Password) {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital obrigatória: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar recebimentos (21 CFR Part 11)."})
			return
		}

		lot := pharma.Lot{
			ProductID:     payload.ProductID,
			LotNumber:     payload.LotNumber,
			ExpiryDate:    payload.ExpiryDate,
			Quantity:      payload.Quantity,
			ChamberID:     payload.ChamberID,
			ShelfLocation: payload.ShelfLocation,
			UnitPrice:     payload.UnitPrice,
		}
		entry := pharma.StockEntry{
			Supplier:               payload.Supplier,
			SupplierCNPJ:           payload.SupplierCNPJ,
			InvoiceNumber:          payload.InvoiceNumber,
			TransportCondition:     payload.TransportCondition,
			ArrivalTemp:            payload.ArrivalTemp,
			CargoIntegrity:         payload.CargoIntegrity,
			InvoiceMatch:           1,
			AmbientExposureMinutes: payload.AmbientExposureMinutes,
			ResponsibleName:        sess.Name,
			Decision:               payload.Decision,
		}

		createdLot, err := pharma.RegisterStockEntry(a.db, entry, lot)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "RECEBIMENTO_ESTOQUE", fmt.Sprintf("Recebimento do lote %s (NF %s) por %s. Temp: %.1f°C. Status: %s", createdLot.LotNumber, entry.InvoiceNumber, sess.Name, entry.ArrivalTemp, createdLot.Status), ip)

		if a.ledger != nil {
			ledgerMsg := fmt.Sprintf("ENTRY|LOT:%s|NF:%s|TEMP:%.1f|DEC:%s|BY:%s", createdLot.LotNumber, entry.InvoiceNumber, entry.ArrivalTemp, createdLot.Status, sess.Username)
			_, _ = a.ledger.AppendData("PHARMA-GATEWAY", uint64(time.Now().UnixNano()), []byte(ledgerMsg), 1)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"lot":    createdLot,
		})
	})

	// 7. Saída de Estoque (FEFO)
	mux.HandleFunc("/api/pharma/stock/exit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": "Autenticação requerida."})
			return
		}

		var payload struct {
			LotID         int64  `json:"lot_id"`
			Quantity      int    `json:"quantity"`
			Reason        string `json:"reason"`
			Recipient     string `json:"recipient"`
			InvoiceNumber string `json:"invoice_number"`
			Password      string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}
		if payload.Password == "" || !a.verifySignature(sess.Username, payload.Password) {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital obrigatória: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar dispensações (21 CFR Part 11)."})
			return
		}

		exit := pharma.StockExit{
			LotID:           payload.LotID,
			Quantity:        payload.Quantity,
			Reason:          payload.Reason,
			Recipient:       payload.Recipient,
			InvoiceNumber:   payload.InvoiceNumber,
			ResponsibleName: sess.Name,
		}
		if err := pharma.RegisterStockExit(a.db, exit); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "SAIDA_ESTOQUE", fmt.Sprintf("Saída de %d unidades do lote #%d. Motivo: %s. Destinatário: %s", exit.Quantity, exit.LotID, exit.Reason, exit.Recipient), ip)

		if a.ledger != nil {
			ledgerMsg := fmt.Sprintf("EXIT|LOT_ID:%d|QTY:%d|REASON:%s|TO:%s|BY:%s", exit.LotID, exit.Quantity, exit.Reason, exit.Recipient, sess.Username)
			_, _ = a.ledger.AppendData("PHARMA-GATEWAY", uint64(time.Now().UnixNano()), []byte(ledgerMsg), 1)
		}

		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})

	// 7.1 Transferência de Estoque com Trava Metrológica (ANVISA RDC 430/2020)
	mux.HandleFunc("/api/pharma/stock/transfer", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "tecnico" && sess.Role != "admin" && sess.Role != "enfermeiro" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Acesso não autorizado para transferência de medicamentos."})
			return
		}

		var payload struct {
			LotID           int64  `json:"lot_id"`
			SourceChamberID string `json:"source_chamber_id"`
			TargetChamberID string `json:"target_chamber_id"`
			ShelfLocation   string `json:"shelf_location"`
			Reason          string `json:"reason"`
			Justification   string `json:"justification"`
			Password        string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if payload.TargetChamberID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Câmara de destino é obrigatória."})
			return
		}
		if payload.Reason == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Motivo da transferência é obrigatório."})
			return
		}

		if payload.Password == "" || !a.verifySignature(sess.Username, payload.Password) {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital inválida: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar assinaturas (21 CFR Part 11)."})
			return
		}

		ip := getClientIP(r)
		fullReason := payload.Reason
		if payload.Justification != "" {
			fullReason += " - Parecer: " + payload.Justification
		}

		if payload.LotID > 0 {
			if err := pharma.TransferLot(a.db, payload.LotID, payload.TargetChamberID, payload.ShelfLocation, fullReason, sess.Name); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}

			a.logAudit(sess.Username, sess.Role, "TRANSFERENCIA_LOTE", fmt.Sprintf("Transferência do lote #%d para câmara %s. Motivo: %s", payload.LotID, payload.TargetChamberID, fullReason), ip)
			if a.ledger != nil {
				ledgerMsg := fmt.Sprintf("TRANSFER|LOT_ID:%d|TO:%s|REASON:%s|BY:%s", payload.LotID, payload.TargetChamberID, fullReason, sess.Username)
				_, _ = a.ledger.AppendData("PHARMA-GATEWAY", uint64(time.Now().UnixNano()), []byte(ledgerMsg), 1)
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "Lote transferido com sucesso e validado termicamente."})
			return
		}

		if payload.SourceChamberID != "" {
			count, err := pharma.TransferChamberLots(a.db, payload.SourceChamberID, payload.TargetChamberID, payload.ShelfLocation, fullReason, sess.Name)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}

			a.logAudit(sess.Username, sess.Role, "EVACUACAO_CAMARA", fmt.Sprintf("Evacuação de %d lotes da câmara %s para %s. Motivo: %s", count, payload.SourceChamberID, payload.TargetChamberID, fullReason), ip)
			if a.ledger != nil {
				ledgerMsg := fmt.Sprintf("EVACUATION|FROM:%s|TO:%s|COUNT:%d|REASON:%s|BY:%s", payload.SourceChamberID, payload.TargetChamberID, count, fullReason, sess.Username)
				_, _ = a.ledger.AppendData("PHARMA-GATEWAY", uint64(time.Now().UnixNano()), []byte(ledgerMsg), 1)
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": fmt.Sprintf("%d lotes transferidos com sucesso para a câmara %s.", count, payload.TargetChamberID), "transferred_count": count})
			return
		}

		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "Especifique o lot_id ou a source_chamber_id para transferência."})
	})

	// 7.2 Validação Prévia de Compatibilidade Térmica para Transferência
	mux.HandleFunc("/api/pharma/stock/transfer/validate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		targetChamberID := r.URL.Query().Get("target_chamber_id")
		lotIDStr := r.URL.Query().Get("lot_id")
		sourceChamberID := r.URL.Query().Get("source_chamber_id")

		if targetChamberID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "target_chamber_id é obrigatório"})
			return
		}

		var targetName string
		var cMin, cMax float64
		if err := a.db.QueryRow("SELECT name, temp_min, temp_max FROM chambers WHERE id = ? AND COALESCE(decommissioned, 0) = 0", targetChamberID).Scan(&targetName, &cMin, &cMax); err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": "Câmara de destino não encontrada."})
			return
		}

		if lotIDStr != "" {
			var pName string
			var pMin, pMax float64
			err := a.db.QueryRow(`
				SELECT p.trade_name, p.temp_min, p.temp_max
				FROM lots l
				JOIN products p ON p.id = l.product_id
				WHERE l.id = ?
			`, lotIDStr).Scan(&pName, &pMin, &pMax)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]any{"error": "Lote não encontrado."})
				return
			}

			compat, msg := pharma.CheckThermalCompatibility(pMin, pMax, cMin, cMax, pName, targetName)
			json.NewEncoder(w).Encode(pharma.TransferValidationResult{
				Compatible: compat,
				Message:    msg,
				ProdName:   pName,
				ProdMin:    pMin,
				ProdMax:    pMax,
				TargetName: targetName,
				TargetMin:  cMin,
				TargetMax:  cMax,
			})
			return
		}

		if sourceChamberID != "" {
			rows, err := a.db.Query(`
				SELECT p.trade_name, p.temp_min, p.temp_max
				FROM lots l
				JOIN products p ON p.id = l.product_id
				WHERE l.chamber_id = ? AND l.status NOT IN ('descartado', 'esgotado')
			`, sourceChamberID)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			defer rows.Close()

			allCompat := true
			firstIncompatMsg := ""
			count := 0
			for rows.Next() {
				count++
				var pn string
				var pmi, pma float64
				if err := rows.Scan(&pn, &pmi, &pma); err == nil {
					compat, msg := pharma.CheckThermalCompatibility(pmi, pma, cMin, cMax, pn, targetName)
					if !compat {
						allCompat = false
						if firstIncompatMsg == "" {
							firstIncompatMsg = msg
						}
					}
				}
			}

			if count == 0 {
				json.NewEncoder(w).Encode(pharma.TransferValidationResult{
					Compatible: true,
					Message:    "Nenhum lote ativo nesta câmara para transferir.",
					TargetName: targetName,
					TargetMin:  cMin,
					TargetMax:  cMax,
				})
				return
			}

			if !allCompat {
				json.NewEncoder(w).Encode(pharma.TransferValidationResult{
					Compatible: false,
					Message:    firstIncompatMsg,
					TargetName: targetName,
					TargetMin:  cMin,
					TargetMax:  cMax,
				})
				return
			}

			json.NewEncoder(w).Encode(pharma.TransferValidationResult{
				Compatible: true,
				Message:    fmt.Sprintf("Todos os %d lotes ativos são termicamente compatíveis com a câmara '%s'.", count, targetName),
				TargetName: targetName,
				TargetMin:  cMin,
				TargetMax:  cMax,
			})
			return
		}

		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "Informe lot_id ou source_chamber_id"})
	})

	// 8. Alarmes Operacionais
	mux.HandleFunc("/api/pharma/alarms", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		st := r.URL.Query().Get("status")
		list, err := pharma.GetAlarms(a.db, st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(list)
	})

	// 9. Reconhecimento de Alarme com Ação Tomada
	mux.HandleFunc("/api/pharma/alarms/acknowledge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var payload struct {
			AlarmID     int64  `json:"alarm_id"`
			ActionTaken string `json:"action_taken"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if err := pharma.AcknowledgeAlarm(a.db, payload.AlarmID, sess.Name, payload.ActionTaken); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "RECONHECE_ALARME", fmt.Sprintf("Alarme #%d reconhecido por %s. Ação: %s", payload.AlarmID, sess.Name, payload.ActionTaken), ip)
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})

	// 9b. Resolução de Alarme Operacional com Encerramento e Laudo Técnico
	mux.HandleFunc("/api/pharma/alarms/resolve", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var payload struct {
			AlarmID       int64  `json:"alarm_id"`
			Notes         string `json:"notes"`
			ElectronicSig string `json:"electronic_sig"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		sig := payload.ElectronicSig
		if sig == "" || !a.verifySignature(sess.Username, sig) {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital inválida: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar laudos (21 CFR Part 11)."})
			return
		}

		if err := pharma.ResolveAlarm(a.db, payload.AlarmID, sess.Username, sess.Name, payload.Notes, sig); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "RESOLVE_ALARME", fmt.Sprintf("Alarme #%d encerrado por %s. Laudo: %s. Assinatura: %s", payload.AlarmID, sess.Name, payload.Notes, sig), ip)

		if a.ledger != nil {
			ledgerMsg := fmt.Sprintf("ALARM_RESOLVE|ID:%d|BY:%s|NAME:%s|SIG:%s", payload.AlarmID, sess.Username, sess.Name, sig)
			_, _ = a.ledger.AppendData("PHARMA-GATEWAY", uint64(time.Now().UnixNano()), []byte(ledgerMsg), 1)
		}

		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "sig": sig})
	})

	// 10. Processos de Excursão Térmica (ANVISA RDC 430/2020 Art. 38)
	mux.HandleFunc("/api/pharma/excursions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		list, err := pharma.GetExcursions(a.db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(list)
	})

	// 11. Decisão e Parecer Técnico de Excursão com Assinatura Eletrônica
	mux.HandleFunc("/api/pharma/excursions/decide", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "tecnico" && sess.Role != "admin" && sess.Role != "enfermeiro" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Decisão pericial de excursão restrita ao Farmacêutico RT / Gerente / Administrador."})
			return
		}

		var payload struct {
			ExcursionID   int64  `json:"excursion_id"`
			Decision      string `json:"decision"` // "liberado" ou "descartado"
			Report        string `json:"report"`
			ElectronicSig string `json:"electronic_sig"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if payload.ElectronicSig == "" || !a.verifySignature(sess.Username, payload.ElectronicSig) {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital inválida: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar parecer de excursão (21 CFR Part 11)."})
			return
		}

		if err := pharma.DecideExcursion(a.db, payload.ExcursionID, payload.Decision, payload.Report, payload.ElectronicSig, sess.Name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "DECISAO_EXCURSAO", fmt.Sprintf("Decisão da excursão #%d: %s por %s. Parecer: %s. Assinatura: %s", payload.ExcursionID, payload.Decision, sess.Name, payload.Report, payload.ElectronicSig), ip)

		if a.ledger != nil {
			ledgerMsg := fmt.Sprintf("EXC_DECISION|ID:%d|DEC:%s|BY:%s|SIG:%s", payload.ExcursionID, payload.Decision, sess.Username, payload.ElectronicSig)
			_, _ = a.ledger.AppendData("PHARMA-GATEWAY", uint64(time.Now().UnixNano()), []byte(ledgerMsg), 1)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"sig":    payload.ElectronicSig,
		})
	})

	// 12. Simulação de Excursão para Demonstração Regulatória
	mux.HandleFunc("/api/pharma/excursions/simulate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "tecnico" && sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Simulação restrita a Gerente ou Administrador."})

			return
		}

		chID := r.URL.Query().Get("chamber")
		if chID == "" {
			chID = "CH-A"
		}

		excCode := fmt.Sprintf("EXC-SIM-%d", time.Now().Unix()%10000)
		exc := pharma.Excursion{
			Code:                     excCode,
			ChamberID:                chID,
			SensorID:                 "S01",
			StartTime:                time.Now().Add(-25 * time.Minute).Format("2006-01-02 15:04:05"),
			EndTime:                  time.Now().Format("2006-01-02 15:04:05"),
			PeakTemp:                 9.6,
			MinTemp:                  4.2,
			AccumulatedDegreeMinutes: 38.0,
			Cause:                    "Simulação controlada de queda de energia e transição para nobreak",
			TechnicalReport:          "Alerta sanitário emitido automaticamente. Todos os lotes foram colocados em análise conforme RDC 430/2020.",
		}

		excID, err := pharma.TriggerExcursion(a.db, exc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "SIMULACAO_EXCURSAO", fmt.Sprintf("Simulação da excursão %s disparada na câmara %s. Lotes colocados em análise.", excCode, chID), ip)

		json.NewEncoder(w).Encode(map[string]any{
			"status":       "ok",
			"excursion_id": excID,
			"code":         excCode,
			"message":      fmt.Sprintf("Excursão %s gerada. Lotes da câmara %s colocados em análise.", excCode, chID),
		})
	})
	// 13. Indicadores de Performance (KPIs) ANVISA RDC 430/2020
	mux.HandleFunc("/api/pharma/kpis", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		kpi, err := pharma.GetPharmaKPIs(a.db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(kpi)
	})

	// 14. Dossiê da Garantia da Qualidade, POPs e Qualificação DQ/IQ/OQ/PQ
	mux.HandleFunc("/api/pharma/quality/dossier", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dossier, err := pharma.GetQualityDossier(a.db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(dossier)
	})

	// 15. Gestão de POPs (Registro / Revisão pelo Farmacêutico RT)
	mux.HandleFunc("/api/pharma/quality/pops", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}

		sess, ok := a.getSession(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Autenticação requerida."})
			return
		}
		if sess.Role != "tecnico" && sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "Acesso negado: apenas Farmacêutico RT ou Admin podem revisar POPs."})
			return
		}

		var payload pharma.POPItem
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Payload JSON inválido."})
			return
		}

		saved, err := pharma.RegisterPOP(a.db, payload)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "REVISAO_POP", fmt.Sprintf("Revisão de POP '%s' (%s) aprovada por %s (Versão: %s, Próxima: %s)", saved.Code, saved.Title, sess.Name, saved.Version, saved.NextReview), ip)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(saved)
	})

	// 16. Registro de Treinamentos da Equipe
	mux.HandleFunc("/api/pharma/quality/trainings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}

		sess, ok := a.getSession(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Autenticação requerida."})
			return
		}
		if sess.Role != "tecnico" && sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "Acesso negado: apenas Farmacêutico RT ou Admin podem lançar treinamentos."})
			return
		}

		var payload pharma.TrainingRecord
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Payload JSON inválido."})
			return
		}

		saved, err := pharma.RegisterTraining(a.db, payload)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "REGISTRO_TREINAMENTO", fmt.Sprintf("Treinamento de %s registrado por %s (POPs: %s, Nota: %d%%, Status: %s)", saved.EmployeeName, sess.Name, saved.POPCode, saved.ScorePct, saved.Status), ip)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(saved)
	})

	// 17. Registro de Laudos de Qualificação Térmica (DQ/IQ/OQ/PQ)
	mux.HandleFunc("/api/pharma/quality/qualifications", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}

		sess, ok := a.getSession(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Autenticação requerida."})
			return
		}
		if sess.Role != "tecnico" && sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "Acesso negado: apenas Farmacêutico RT ou Admin podem cadastrar qualificações."})
			return
		}

		var payload pharma.QualificationItem
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Payload JSON inválido."})
			return
		}

		saved, err := pharma.RegisterChamberQualification(a.db, payload)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "REGISTRO_QUALIFICACAO", fmt.Sprintf("Laudo de Qualificação '%s' da câmara '%s' registrado por %s (Validade: %s)", saved.ReportNumber, saved.ChamberID, sess.Name, saved.ValidUntil), ip)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(saved)
	})

	// =========================================================================
	// FASE 6: EXPORTAÇÃO DE RELATÓRIOS REGULATÓRIOS E LAUDOS PERICIAIS
	// =========================================================================

	// 18. Dossiê Sanitário Oficial em PDF 1.4 com Assinatura Digital ECDSA
	mux.HandleFunc("/api/pharma/reports/dossier.pdf", func(w http.ResponseWriter, r *http.Request) {
		data, err := pharma.FetchDossierReportData(a.db, a.signer)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pdfBytes, err := pharma.GenerateDossierPDF(data)
		if err != nil {
			http.Error(w, "Falha ao gerar PDF: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "attachment; filename=Dossie_Sanitario_RDC430_ANVISA.pdf")
		w.Header().Set("X-Report-SHA256", data.ReportSHA256)
		w.Header().Set("X-Signature-ECDSA", data.SignatureHex)
		w.Write(pdfBytes)
	})

	// 19. Dossiê Sanitário Completo em CSV Sanitizado
	mux.HandleFunc("/api/pharma/reports/dossier.csv", func(w http.ResponseWriter, r *http.Request) {
		data, err := pharma.FetchDossierReportData(a.db, a.signer)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		csvBytes, err := pharma.GenerateDossierCSV(data)
		if err != nil {
			http.Error(w, "Falha ao gerar CSV: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=Dossie_Sanitario_RDC430_ANVISA.csv")
		w.Header().Set("X-Report-SHA256", data.ReportSHA256)
		w.Write(csvBytes)
	})

	// 20. Laudo Pericial de Impressão HTML / Print-to-PDF
	mux.HandleFunc("/api/pharma/reports/dossier.html", func(w http.ResponseWriter, r *http.Request) {
		data, err := pharma.FetchDossierReportData(a.db, a.signer)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		htmlStr := pharma.GenerateDossierHTML(data)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlStr))
	})

	// 21. Exportação do Ledger WORM em CSV para Auditoria Forense Externa
	mux.HandleFunc("/api/pharma/reports/ledger.csv", func(w http.ResponseWriter, r *http.Request) {
		csvBytes, err := pharma.GenerateLedgerCSV(a.db, a.signer)
		if err != nil {
			http.Error(w, "Falha ao exportar ledger: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=Ledger_WORM_Auditoria_ANVISA.csv")
		w.Write(csvBytes)
	})

	return mux
}

func (a *App) initMQTT(brokerURL string) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID("coldchain-backend")
	opts.SetCleanSession(false)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(1 * time.Second)

	if secret, err := os.ReadFile("/run/secrets/mqtt_password"); err == nil {
		opts.SetUsername("coldchain-app")
		opts.SetPassword(strings.TrimSpace(string(secret)))
	}

	opts.OnConnect = func(c mqtt.Client) {
		slog.Info("Conectado ao Mosquitto MQTT", "broker", brokerURL)
		c.Subscribe("coldchain/+/telemetry", 1, a.onTelemetry)
	}

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		slog.Warn("Aguardando conexão com o broker MQTT...", "erro", token.Error())
	}
	a.mqttClient = client
}

func (a *App) onTelemetry(client mqtt.Client, msg mqtt.Message) {
	var t Telemetry
	if err := json.Unmarshal(msg.Payload(), &t); err != nil {
		return
	}

	parts := strings.Split(msg.Topic(), "/")
	if len(parts) >= 3 {
		t.DeviceID = parts[1]
	}

	a.mu.Lock()
	a.devicesSeen[t.DeviceID] = time.Now()
	for ch := range a.subscribers {
		select {
		case ch <- t:
		default:
		}
	}
	a.mu.Unlock()

	a.batchMu.Lock()
	a.batchQueue = append(a.batchQueue, t)
	a.batchMu.Unlock()
}

func (a *App) batchCommitLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.flushBatch()
		}
	}
}

func (a *App) flushBatch() {
	a.batchMu.Lock()
	if len(a.batchQueue) == 0 {
		a.batchMu.Unlock()
		return
	}
	items := a.batchQueue
	a.batchQueue = nil
	a.batchMu.Unlock()

	tx, err := a.db.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare("INSERT OR IGNORE INTO readings (device_id, seq, ts, t, h, door, sync, fw) VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		_ = tx.Rollback()
		return
	}
	defer stmt.Close()

	deviceMaxSeq := make(map[string]int64)
	for _, it := range items {
		_, _ = stmt.Exec(it.DeviceID, it.Seq, it.TS, it.T, it.H, it.Door, it.Sync, it.FW)
		if it.Seq > deviceMaxSeq[it.DeviceID] {
			deviceMaxSeq[it.DeviceID] = it.Seq
		}
	}

	if err := tx.Commit(); err != nil {
		return
	}

	// Registro Criptográfico Imutável no Ledger (ANVISA RDC 430/2020)
	if a.ledger != nil {
		for _, it := range items {
			telemetryPayload, _ := json.Marshal(map[string]any{
				"device_id": it.DeviceID,
				"seq":       it.Seq,
				"ts":        it.TS,
				"t":         it.T,
				"h":         it.H,
				"door":      it.Door,
				"sync":      it.Sync,
				"fw":        it.FW,
			})
			_, err := a.ledger.AppendData(it.DeviceID, uint64(it.Seq), telemetryPayload, ledger.ClockTrusted)
			if err != nil {
				slog.Error("Erro ao gravar no ledger", "seq", it.Seq, "erro", err)
			}
			a.telemetryCount++
			if a.telemetryCount%50 == 0 {
				receipt := fmt.Sprintf("AUTONOMOUS_RFC3161_TSA_RECEIPT_%d", time.Now().Unix())
				_, _ = a.ledger.CreateAnchor(receipt)
				slog.Info("Âncora periódica registrada no ledger", "altura", a.telemetryCount)
			}
		}
	}

	if a.mqttClient != nil && a.mqttClient.IsConnected() {
		for dev, seq := range deviceMaxSeq {
			ackTopic := fmt.Sprintf("coldchain/%s/ack", dev)
			payload := fmt.Sprintf(`{"seq":%d}`, seq)
			a.mqttClient.Publish(ackTopic, 1, false, payload)
		}
	}
}

func (a *App) pharmaSimulatorLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	// Variação suave de temperatura para os sensores das Câmaras A e B
	offsets := map[string]float64{
		"S01": 4.8, "S02": 3.2, "S03": 4.1, "S04": 4.3, "S05": 3.9,
		"S06": 4.0, "S07": 4.4, "S08": 4.9, "S09": 3.7, "S10": 5.2,
		"S11": 4.7, "S12": 3.4, "S13": 4.2, "S14": 4.0, "S15": 4.3,
		"S16": 3.8, "S17": 4.5, "S18": 4.8,
		"S19": 4.2, "S20": 4.5, "S21": 3.9, "S22": 4.9,
		"S23": 21.8, "S24": 22.4, "S25": 21.2, "S26": 23.0,
		"S27": -72.4, "S28": -69.1,
	}

	step := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			step++
			nowStr := time.Now().Format("2006-01-02 15:04:05")

			rows, err := a.db.Query(`
				SELECT s.id, c.temp_min, c.temp_max, c.humidity_min, c.humidity_max 
				FROM sensors s
				JOIN chambers c ON s.chamber_id = c.id
				WHERE COALESCE(c.decommissioned, 0) = 0`)
			if err == nil {
				type sUpdate struct {
					id string
					t  float64
					h  float64
				}
				var updates []sUpdate
				for rows.Next() {
					var sID string
					var tMin, tMax, hMin, hMax float64
					if err := rows.Scan(&sID, &tMin, &tMax, &hMin, &hMax); err == nil {
						baseTemp, exists := offsets[sID]
						if !exists {
							baseTemp = (tMin + tMax) / 2.0
						}
						baseHum := (hMin + hMax) / 2.0
						if baseHum <= 0 {
							baseHum = 50.0
						}

						charVal := 1
						if len(sID) > 1 {
							charVal = int(sID[len(sID)-1])
						}
						factor := float64((step*7+charVal)%10-5) * 0.03
						tVal := baseTemp + factor
						hVal := baseHum + float64((step*3+charVal)%8-4)*0.8
						updates = append(updates, sUpdate{id: sID, t: tVal, h: hVal})
					}
				}
				rows.Close()

				for _, u := range updates {
					_, _ = a.db.Exec(`UPDATE sensors 
						SET current_temp = ?, current_humidity = ?, last_seen = ? 
						WHERE id = ?`, u.t, u.h, nowStr, u.id)
				}
			}
		}
	}
}

func (a *App) handleDecommissionSensor(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.getSession(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": "Autenticação requerida."})
		return
	}
	if sess.Role != "tecnico" && sess.Role != "admin" {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"error": "Acesso negado: apenas Responsável Técnico ou Admin podem excluir/descomissionar sensores."})
		return
	}

	var req struct {
		SensorID      string `json:"sensor_id"`
		Reason        string `json:"reason"`
		Justification string `json:"justification"`
		Password      string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.SensorID == "" {
		req.SensorID = r.URL.Query().Get("id")
	}

	if req.SensorID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "ID do sensor é obrigatório."})
		return
	}

	if req.Password == "" || !a.verifySignature(sess.Username, req.Password) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital obrigatória: forneça sua Chave Criptográfica Pessoal (CC-KEY-...). A senha de login não é permitida para firmar assinaturas digitais (21 CFR Part 11)."})
		return
	}

	if err := pharma.DecommissionSensor(a.db, req.SensorID, req.Reason, req.Justification, sess.Name); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	ip := getClientIP(r)
	a.logAudit(sess.Username, sess.Role, "DESCOMISSIONAMENTO_SENSOR", fmt.Sprintf("Sensor '%s' descomissionado por %s. Motivo: %s. Parecer: %s", req.SensorID, sess.Name, req.Reason, req.Justification), ip)

	if a.ledger != nil {
		ledgerData, _ := json.Marshal(map[string]any{
			"event":         "SENSOR_DECOMMISSION",
			"sensor_id":     req.SensorID,
			"reason":        req.Reason,
			"justification": req.Justification,
			"responsible":   sess.Name,
			"timestamp":     time.Now().Format(time.RFC3339),
		})
		_, _ = a.ledger.AppendData("PHARMA-GATEWAY", uint64(time.Now().UnixNano()), ledgerData, 1)
	}

	json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": fmt.Sprintf("Sensor '%s' descomissionado com sucesso com registro em trilha de auditoria e ledger.", req.SensorID),
	})
}
