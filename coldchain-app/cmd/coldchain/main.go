package main

import (
	"coldchain-app/web"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
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

type App struct {
	db          *sql.DB
	mqttClient  mqtt.Client
	mu          sync.Mutex
	subscribers map[chan Telemetry]struct{}
	latestSeq   map[string]int64
	devicesSeen map[string]time.Time
	batchQueue  []Telemetry
	batchMu     sync.Mutex
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
	slog.Info("Iniciando Web-Server Cold Chain Gateway", "versao", "3.2.0")

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

	app := &App{
		db:          db,
		subscribers: make(map[chan Telemetry]struct{}),
		latestSeq:   make(map[string]int64),
		devicesSeen: make(map[string]time.Time),
	}

	// 2. Loop de commit de lotes em background (~1 segundo)
	ctx, cancel := context.WithCancel(context.Background())
	go app.batchCommitLoop(ctx)

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

func initDB(path string) (*sql.DB, error) {
	_ = os.MkdirAll("/data/coldchain", 0700)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

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
	`
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("erro no schema: %w", err)
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

		// Resposta pura para curl
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
			"version":  "3.2.0",
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
			"version":        "3.2.0",
			"readings_count": count,
			"devices_count":  devCount,
			"phrase":         mandatoryPhrase,
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
				// Sanitização contra CSV Formula Injection (=, +, -, @)
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

	return mux
}

func (a *App) initMQTT(brokerURL string) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID("coldchain-backend")
	opts.SetCleanSession(false) // Sessão persistente
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(1 * time.Second)

	// Lê credenciais de arquivo se existirem (Seção 7)
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

	// Extrai ID do tópico: coldchain/<id>/telemetry
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

	// Publica ACK ponta a ponta apenas após o commit no disco (Seção 8)
	if a.mqttClient != nil && a.mqttClient.IsConnected() {
		for dev, seq := range deviceMaxSeq {
			ackTopic := fmt.Sprintf("coldchain/%s/ack", dev)
			payload := fmt.Sprintf(`{"seq":%d}`, seq)
			a.mqttClient.Publish(ackTopic, 1, false, payload)
		}
	}
}
