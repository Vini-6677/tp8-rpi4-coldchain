package main

import (
	"coldchain-app/internal/usbkey"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// USBKeyRecord representa uma chave pública USB vinculada a um usuário no SQLite (NUNCA armazena chave privada).
type USBKeyRecord struct {
	ID                string `json:"id"`
	UserID            int64  `json:"user_id"`
	Username          string `json:"username"`
	PublicKeyB64      string `json:"public_key_b64"`
	Algorithm         string `json:"algorithm"`
	BindingMode       string `json:"binding_mode"`
	DeviceBindingHash string `json:"device_binding_hash"`
	USBVendorProduct  string `json:"usb_vendor_product"`
	USBSerialMasked   string `json:"usb_serial_masked"`
	DriveLabel        string `json:"drive_label"`
	FileSHA256        string `json:"file_sha256"`
	Status            string `json:"status"`
	CreatedBy         string `json:"created_by"`
	CreatedAt         string `json:"created_at"`
	LastUsedAt        string `json:"last_used_at"`
	RevokedBy         string `json:"revoked_by"`
	RevokedAt         string `json:"revoked_at"`
	RevokedReason     string `json:"revoked_reason"`
}

type usbSigTokenEntry struct {
	Username  string
	KeyID     string
	ExpiresAt time.Time
}

func initUSBKeySchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS user_usb_keys (
		id TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		username TEXT NOT NULL,
		public_key_b64 TEXT NOT NULL,
		algorithm TEXT NOT NULL DEFAULT 'Ed25519',
		binding_mode TEXT NOT NULL DEFAULT 'HARDWARE_SERIAL',
		device_binding_hash TEXT NOT NULL,
		usb_vendor_product TEXT DEFAULT '',
		usb_serial_masked TEXT DEFAULT '',
		drive_label TEXT DEFAULT '',
		file_sha256 TEXT DEFAULT '',
		status TEXT NOT NULL DEFAULT 'ACTIVE',
		created_by TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_used_at DATETIME,
		revoked_by TEXT DEFAULT '',
		revoked_at DATETIME,
		revoked_reason TEXT DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_user_usb_keys_username ON user_usb_keys (username, status);

	CREATE TABLE IF NOT EXISTS auth_challenges (
		id TEXT PRIMARY KEY,
		key_id TEXT NOT NULL,
		username TEXT NOT NULL,
		purpose TEXT NOT NULL DEFAULT 'LOGIN',
		nonce_b64 TEXT NOT NULL,
		expires_at DATETIME NOT NULL,
		used_at DATETIME,
		client_ip TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_auth_challenges_expires ON auth_challenges (expires_at);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	_, _ = db.Exec("ALTER TABLE sessions ADD COLUMN usb_key_id TEXT DEFAULT ''")
	return nil
}

func (a *App) issueUSBSigToken(username, keyID string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := "CC-USB-SIG-" + hex.EncodeToString(b)

	a.usbSigMu.Lock()
	defer a.usbSigMu.Unlock()
	if a.usbSigTokens == nil {
		a.usbSigTokens = make(map[string]usbSigTokenEntry)
	}
	now := time.Now()
	for k, v := range a.usbSigTokens {
		if now.After(v.ExpiresAt) {
			delete(a.usbSigTokens, k)
		}
	}
	a.usbSigTokens[tok] = usbSigTokenEntry{
		Username:  username,
		KeyID:     keyID,
		ExpiresAt: now.Add(90 * time.Second),
	}
	return tok
}

func (a *App) consumeUSBSigToken(username, token string) bool {
	if !strings.HasPrefix(token, "CC-USB-SIG-") {
		return false
	}
	a.usbSigMu.Lock()
	defer a.usbSigMu.Unlock()
	entry, ok := a.usbSigTokens[token]
	if !ok {
		return false
	}
	delete(a.usbSigTokens, token)
	if time.Now().After(entry.ExpiresAt) {
		return false
	}
	return strings.EqualFold(entry.Username, username)
}

func (a *App) listUSBKeysByUsername(username string) []USBKeyRecord {
	query := `
		SELECT id, user_id, username, public_key_b64, algorithm, binding_mode, device_binding_hash,
		       COALESCE(usb_vendor_product, ''), COALESCE(usb_serial_masked, ''), COALESCE(drive_label, ''),
		       COALESCE(file_sha256, ''), status, created_by, created_at,
		       COALESCE(last_used_at, ''), COALESCE(revoked_by, ''), COALESCE(revoked_at, ''), COALESCE(revoked_reason, '')
		FROM user_usb_keys`
	var args []any
	if strings.TrimSpace(username) != "" {
		query += " WHERE username = ?"
		args = append(args, strings.ToLower(strings.TrimSpace(username)))
	}
	query += " ORDER BY created_at DESC"

	rows, err := a.db.Query(query, args...)
	if err != nil {
		return []USBKeyRecord{}
	}
	defer rows.Close()

	list := make([]USBKeyRecord, 0)
	for rows.Next() {
		var rec USBKeyRecord
		if err := rows.Scan(
			&rec.ID, &rec.UserID, &rec.Username, &rec.PublicKeyB64, &rec.Algorithm, &rec.BindingMode, &rec.DeviceBindingHash,
			&rec.USBVendorProduct, &rec.USBSerialMasked, &rec.DriveLabel, &rec.FileSHA256, &rec.Status,
			&rec.CreatedBy, &rec.CreatedAt, &rec.LastUsedAt, &rec.RevokedBy, &rec.RevokedAt, &rec.RevokedReason,
		); err == nil {
			list = append(list, rec)
		}
	}
	return list
}

func (a *App) registerUSBAuthRoutes(mux *http.ServeMux) {
	// 1. Admin: Registrar chave pública provisionada no pen drive (NUNCA recebe chave privada)
	mux.HandleFunc("/api/admin/users/usb-keys/register", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Apenas o Administrador pode provisionar chaves criptográficas em pen drive."})
			return
		}

		var req struct {
			Username          string `json:"username"`
			KeyID             string `json:"key_id"`
			PublicKeyB64      string `json:"public_key_b64"`
			Algorithm         string `json:"algorithm"`
			BindingMode       string `json:"binding_mode"`
			DeviceBindingHash string `json:"device_binding_hash"`
			USBVendorProduct  string `json:"usb_vendor_product"`
			USBSerialMasked   string `json:"usb_serial_masked"`
			DriveLabel        string `json:"drive_label"`
			FileSHA256        string `json:"file_sha256"`
			RevokePrevious    bool   `json:"revoke_previous"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Payload JSON inválido."})
			return
		}

		req.Username = strings.ToLower(strings.TrimSpace(req.Username))
		req.KeyID = strings.TrimSpace(req.KeyID)
		req.DeviceBindingHash = strings.ToLower(strings.TrimSpace(req.DeviceBindingHash))
		if req.Algorithm == "" {
			req.Algorithm = usbkey.AlgorithmEd25519
		}
		if req.BindingMode == "" {
			req.BindingMode = usbkey.BindingHardware
		}

		if req.Username == "" || req.KeyID == "" || req.PublicKeyB64 == "" || len(req.DeviceBindingHash) != 64 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Dados obrigatórios da chave pública USB ausentes ou inválidos."})
			return
		}
		if req.Algorithm != usbkey.AlgorithmEd25519 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Algoritmo não suportado. Esperado: Ed25519."})
			return
		}
		if err := usbkey.ValidatePublicKeyB64(req.PublicKeyB64); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		var userID int64
		if err := a.db.QueryRow("SELECT id FROM users WHERE username = ?", req.Username).Scan(&userID); err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": fmt.Sprintf("Usuário '%s' não encontrado.", req.Username)})
			return
		}

		if req.RevokePrevious {
			_, _ = a.db.Exec(`
				UPDATE user_usb_keys
				SET status = 'REVOKED', revoked_by = ?, revoked_at = CURRENT_TIMESTAMP, revoked_reason = 'Substituída por nova chave USB provisionada'
				WHERE username = ? AND status = 'ACTIVE'`,
				sess.Username, req.Username)
		}

		_, err := a.db.Exec(`
			INSERT INTO user_usb_keys (
				id, user_id, username, public_key_b64, algorithm, binding_mode, device_binding_hash,
				usb_vendor_product, usb_serial_masked, drive_label, file_sha256, status, created_by
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'ACTIVE', ?)`,
			req.KeyID, userID, req.Username, req.PublicKeyB64, req.Algorithm, req.BindingMode, req.DeviceBindingHash,
			req.USBVendorProduct, req.USBSerialMasked, req.DriveLabel, req.FileSHA256, sess.Username)
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": fmt.Sprintf("Falha ao registrar chave USB (key_id duplicado ou erro de banco): %v", err)})
			return
		}

		ip := getClientIP(r)
		a.logAudit(sess.Username, sess.Role, "USB_KEY_PROVISIONED",
			fmt.Sprintf("Chave USB Ed25519 '%s' vinculada ao usuário '%s' (Pen Drive: %s / %s, Vínculo: %s, Serial: %s)",
				req.KeyID, req.Username, req.DriveLabel, req.USBVendorProduct, req.BindingMode, req.USBSerialMasked), ip)

		if a.ledger != nil {
			ledgerPayload, _ := json.Marshal(map[string]any{
				"event":               "USB_KEY_PROVISIONED",
				"key_id":              req.KeyID,
				"username":            req.Username,
				"algorithm":           req.Algorithm,
				"binding_mode":        req.BindingMode,
				"device_binding_hash": req.DeviceBindingHash,
				"by":                  sess.Username,
				"timestamp":           time.Now().UTC().Format(time.RFC3339),
			})
			_, _ = a.ledger.AppendData("PHARMA-USB-AUTH", uint64(time.Now().UnixNano()), ledgerPayload, 1)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"key_id":  req.KeyID,
			"message": fmt.Sprintf("Chave pública USB registrada com sucesso para '%s'.", req.Username),
		})
	})

	// 2. Admin: Listar chaves USB cadastradas
	mux.HandleFunc("/api/admin/users/usb-keys", func(w http.ResponseWriter, r *http.Request) {
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
		list := a.listUSBKeysByUsername(r.URL.Query().Get("username"))
		json.NewEncoder(w).Encode(list)
	})

	// 3. Admin: Revogar chave USB (perda, roubo ou substituição) e derrubar sessões ativas daquela chave
	mux.HandleFunc("/api/admin/users/usb-keys/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Apenas o Administrador pode revogar chaves USB."})
			return
		}

		var req struct {
			KeyID  string `json:"key_id"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Dados inválidos."})
			return
		}
		req.KeyID = strings.TrimSpace(req.KeyID)
		req.Reason = strings.TrimSpace(req.Reason)
		if req.KeyID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "key_id é obrigatório."})
			return
		}
		if req.Reason == "" {
			req.Reason = "Revogação administrativa (perda/roubo/substituição)"
		}

		var ownerUser, currentStatus string
		if err := a.db.QueryRow("SELECT username, status FROM user_usb_keys WHERE id = ?", req.KeyID).Scan(&ownerUser, &currentStatus); err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": "Chave USB não encontrada."})
			return
		}
		if currentStatus == "REVOKED" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Esta chave USB já está revogada."})
			return
		}

		_, err := a.db.Exec(`
			UPDATE user_usb_keys
			SET status = 'REVOKED', revoked_by = ?, revoked_at = CURRENT_TIMESTAMP, revoked_reason = ?
			WHERE id = ?`,
			sess.Username, req.Reason, req.KeyID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "Erro ao revogar chave USB."})
			return
		}

		// Encerra imediatamente qualquer sessão aberta por este pen drive
		resSess, _ := a.db.Exec("DELETE FROM sessions WHERE usb_key_id = ?", req.KeyID)
		killedSessions, _ := resSess.RowsAffected()

		a.logAudit(sess.Username, sess.Role, "USB_KEY_REVOKED",
			fmt.Sprintf("Chave USB '%s' do usuário '%s' REVOGADA por %s. Motivo: %s (%d sessão(ões) encerrada(s))",
				req.KeyID, ownerUser, sess.Name, req.Reason, killedSessions), getClientIP(r))

		if a.ledger != nil {
			ledgerPayload, _ := json.Marshal(map[string]any{
				"event":           "USB_KEY_REVOKED",
				"key_id":          req.KeyID,
				"username":        ownerUser,
				"reason":          req.Reason,
				"killed_sessions": killedSessions,
				"by":              sess.Username,
				"timestamp":       time.Now().UTC().Format(time.RFC3339),
			})
			_, _ = a.ledger.AppendData("PHARMA-USB-AUTH", uint64(time.Now().UnixNano()), ledgerPayload, 1)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"status":          "ok",
			"killed_sessions": killedSessions,
			"message":         fmt.Sprintf("Chave USB '%s' revogada imediatamente.", req.KeyID),
		})
	})

	// 4. Auth USB: Emitir desafio criptográfico único (Challenge-Response)
	mux.HandleFunc("/api/auth/usb/challenge", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			KeyID   string `json:"key_id"`
			Purpose string `json:"purpose"` // "LOGIN" ou "SIGN_ACTION"
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Requisição inválida."})
			return
		}
		req.KeyID = strings.TrimSpace(req.KeyID)
		purpose := strings.ToUpper(strings.TrimSpace(req.Purpose))
		if purpose == "" {
			purpose = usbkey.PurposeLogin
		}
		if purpose != usbkey.PurposeLogin && purpose != usbkey.PurposeSignAction {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Finalidade de desafio inválida."})
			return
		}
		if req.KeyID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "key_id não informado."})
			return
		}

		ip := getClientIP(r)
		var (
			username      string
			keyStatus     string
			revokedReason string
			isBlocked     int
		)
		err := a.db.QueryRow(`
			SELECT k.username, k.status, COALESCE(k.revoked_reason, ''), u.is_blocked
			FROM user_usb_keys k
			JOIN users u ON u.id = k.user_id
			WHERE k.id = ?`, req.KeyID).Scan(&username, &keyStatus, &revokedReason, &isBlocked)
		if err != nil {
			a.logAudit("desconhecido", "desconhecido", "USB_LOGIN_UNKNOWN_KEY", fmt.Sprintf("Tentativa de autenticação com chave USB não cadastrada (%s)", req.KeyID), ip)
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{
				"error_code": "KEY_NOT_REGISTERED",
				"error":      "Chave criptográfica do pen drive não está cadastrada neste servidor.",
			})
			return
		}

		if keyStatus != "ACTIVE" {
			a.logAudit(username, "usuario", "USB_LOGIN_REVOKED_KEY", fmt.Sprintf("Tentativa de uso de chave USB REVOGADA (%s). Motivo original: %s", req.KeyID, revokedReason), ip)
			w.WriteHeader(http.StatusForbidden)
			msg := "Esta chave USB foi REVOGADA pelo administrador e não pode mais ser utilizada."
			if revokedReason != "" {
				msg += " Motivo: " + revokedReason
			}
			json.NewEncoder(w).Encode(map[string]any{
				"error_code": "KEY_REVOKED",
				"error":      msg,
			})
			return
		}

		if isBlocked == 1 {
			a.logAudit(username, "usuario", "LOGIN_BLOCKED", "Tentativa de login USB em conta bloqueada", ip)
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{
				"error_code": "USER_BLOCKED",
				"error":      "Esta conta de usuário foi bloqueada pelo administrador.",
			})
			return
		}

		// Se for assinatura 21 CFR Part 11 de ação autenticada, garante que o pen drive pertence ao usuário logado
		if purpose == usbkey.PurposeSignAction {
			sess, ok := a.getSession(r)
			if !ok {
				a.writeSessionExpired(w)
				return
			}
			if !strings.EqualFold(sess.Username, username) {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{
					"error_code": "USB_OWNER_MISMATCH",
					"error":      fmt.Sprintf("O pen drive conectado pertence ao usuário '%s', mas a sessão ativa é de '%s' (21 CFR Part 11 exige assinatura pessoal).", username, sess.Username),
				})
				return
			}
		}

		// Limpa desafios expirados antigos
		_, _ = a.db.Exec("DELETE FROM auth_challenges WHERE expires_at < datetime('now', '-5 minutes')")

		idRaw := make([]byte, 12)
		nonceRaw := make([]byte, 32)
		_, _ = rand.Read(idRaw)
		_, _ = rand.Read(nonceRaw)
		challengeID := "chal_" + hex.EncodeToString(idRaw)
		nonceB64 := base64.StdEncoding.EncodeToString(nonceRaw)
		expiresAt := time.Now().UTC().Add(30 * time.Second)

		_, err = a.db.Exec(`
			INSERT INTO auth_challenges (id, key_id, username, purpose, nonce_b64, expires_at, client_ip)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			challengeID, req.KeyID, username, purpose, nonceB64, expiresAt.Format(time.RFC3339), ip)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "Erro ao emitir desafio criptográfico."})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"challenge_id":   challengeID,
			"nonce_b64":      nonceB64,
			"key_id":         req.KeyID,
			"username":       username,
			"purpose":        purpose,
			"expires_in_sec": 30,
		})
	})

	// 5. Auth USB: Validar assinatura Ed25519 do desafio e autenticar automaticamente (Plug-and-Play)
	mux.HandleFunc("/api/auth/usb/login", func(w http.ResponseWriter, r *http.Request) {
		a.handleUSBChallengeVerify(w, r, usbkey.PurposeLogin)
	})

	// 6. Auth USB: Validar assinatura Ed25519 do desafio e emitir token único de assinatura 21 CFR Part 11
	mux.HandleFunc("/api/auth/usb/sign-token", func(w http.ResponseWriter, r *http.Request) {
		a.handleUSBChallengeVerify(w, r, usbkey.PurposeSignAction)
	})
}

func (a *App) handleUSBChallengeVerify(w http.ResponseWriter, r *http.Request, expectedPurpose string) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	var activeSess *Session
	if expectedPurpose == usbkey.PurposeSignAction {
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		activeSess = sess
	}

	var req struct {
		ChallengeID       string `json:"challenge_id"`
		KeyID             string `json:"key_id"`
		SignatureB64      string `json:"signature_b64"`
		DeviceBindingHash string `json:"device_binding_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "Requisição inválida."})
		return
	}

	req.ChallengeID = strings.TrimSpace(req.ChallengeID)
	req.KeyID = strings.TrimSpace(req.KeyID)
	req.DeviceBindingHash = strings.ToLower(strings.TrimSpace(req.DeviceBindingHash))
	ip := getClientIP(r)

	var (
		chalKeyID     string
		chalUsername  string
		chalPurpose   string
		nonceB64      string
		expiresAtStr  string
		usedAt        sql.NullString
	)
	err := a.db.QueryRow(`
		SELECT key_id, username, purpose, nonce_b64, expires_at, used_at
		FROM auth_challenges WHERE id = ?`, req.ChallengeID).
		Scan(&chalKeyID, &chalUsername, &chalPurpose, &nonceB64, &expiresAtStr, &usedAt)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error_code": "INVALID_CHALLENGE", "error": "Desafio criptográfico inválido ou expirado."})
		return
	}
	if usedAt.Valid && usedAt.String != "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error_code": "CHALLENGE_REUSED", "error": "Desafio criptográfico já consumido (proteção contra replay)."})
		return
	}
	// Consome imediatamente o desafio para prevenir ataques de replay
	_, _ = a.db.Exec("UPDATE auth_challenges SET used_at = CURRENT_TIMESTAMP WHERE id = ?", req.ChallengeID)

	expiresAt, parseErr := time.Parse(time.RFC3339, expiresAtStr)
	if parseErr != nil {
		expiresAt, parseErr = time.Parse("2006-01-02 15:04:05", expiresAtStr)
	}
	if parseErr != nil || time.Now().UTC().After(expiresAt.UTC()) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error_code": "CHALLENGE_EXPIRED", "error": "Tempo limite do desafio criptográfico expirado. Reconecte o pen drive."})
		return
	}
	if chalKeyID != req.KeyID || chalPurpose != expectedPurpose {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error_code": "CHALLENGE_MISMATCH", "error": "Parâmetros do desafio não conferem com a chave apresentada."})
		return
	}

	var (
		userID          int64
		username        string
		name            string
		role            string
		isBlocked       int
		pubKeyB64       string
		regBindingHash  string
		keyStatus       string
		revokedReason   string
	)
	err = a.db.QueryRow(`
		SELECT u.id, u.username, u.name, u.role, u.is_blocked,
		       k.public_key_b64, k.device_binding_hash, k.status, COALESCE(k.revoked_reason, '')
		FROM user_usb_keys k
		JOIN users u ON u.id = k.user_id
		WHERE k.id = ?`, req.KeyID).
		Scan(&userID, &username, &name, &role, &isBlocked, &pubKeyB64, &regBindingHash, &keyStatus, &revokedReason)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error_code": "KEY_NOT_REGISTERED", "error": "Chave USB não encontrada."})
		return
	}
	if keyStatus != "ACTIVE" {
		a.logAudit(username, role, "USB_LOGIN_REVOKED_KEY", fmt.Sprintf("Bloqueada tentativa com chave USB revogada (%s)", req.KeyID), ip)
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"error_code": "KEY_REVOKED", "error": "Chave USB revogada: " + revokedReason})
		return
	}
	if isBlocked == 1 {
		a.logAudit(username, role, "LOGIN_BLOCKED", "Tentativa de acesso USB em conta bloqueada", ip)
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"error_code": "USER_BLOCKED", "error": "Esta conta de usuário foi bloqueada pelo administrador."})
		return
	}
	if activeSess != nil && !strings.EqualFold(activeSess.Username, username) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"error_code": "USB_OWNER_MISMATCH", "error": "Pen drive pertence a outro usuário."})
		return
	}

	// Camada 2 anti-clonagem: compara o hash de vínculo de hardware apresentado com o registrado
	if subtle.ConstantTimeCompare([]byte(req.DeviceBindingHash), []byte(strings.ToLower(regBindingHash))) != 1 {
		a.logAudit(username, role, "USB_AUTH_CLONE_SUSPECTED",
			fmt.Sprintf("ALERTA DE SEGURANÇA: Chave USB '%s' apresentada em dispositivo com serial físico divergente (possível clonagem bloqueada)", req.KeyID), ip)
		if a.ledger != nil {
			lp, _ := json.Marshal(map[string]any{
				"event": "USB_AUTH_CLONE_SUSPECTED", "key_id": req.KeyID, "username": username, "ip": ip, "timestamp": time.Now().UTC().Format(time.RFC3339),
			})
			_, _ = a.ledger.AppendData("PHARMA-USB-AUTH", uint64(time.Now().UnixNano()), lp, 1)
		}
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{
			"error_code": "CLONE_DETECTED",
			"error":      "Dispositivo USB não corresponde ao hardware físico original vinculado à chave (possível clonagem bloqueada).",
		})
		return
	}

	// Verifica assinatura criptográfica Ed25519 usando exclusivamente a chave pública armazenada
	msg := usbkey.BuildAuthChallengeMessage(req.ChallengeID, nonceB64, req.KeyID, regBindingHash, expectedPurpose)
	if !usbkey.VerifyChallengeSignature(pubKeyB64, msg, req.SignatureB64) {
		a.logAudit(username, role, "USB_LOGIN_INVALID_SIG", fmt.Sprintf("Assinatura Ed25519 inválida para chave USB '%s'", req.KeyID), ip)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error_code": "INVALID_SIGNATURE",
			"error":      "Assinatura criptográfica da chave USB inválida.",
		})
		return
	}

	_, _ = a.db.Exec("UPDATE user_usb_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?", req.KeyID)

	if expectedPurpose == usbkey.PurposeSignAction {
		sigToken := a.issueUSBSigToken(username, req.KeyID)
		a.logAudit(username, role, "USB_SIGN_AUTHORIZED", fmt.Sprintf("Assinatura eletrônica 21 CFR Part 11 autorizada via pen drive USB '%s'", req.KeyID), ip)
		json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"sig_token": sigToken,
			"key_id":    req.KeyID,
		})
		return
	}

	// Emite sessão autenticada vinculada ao usb_key_id (permite derrubada imediata em caso de revogação)
	token := generateToken()
	expires := time.Now().Add(24 * time.Hour)
	_, err = a.db.Exec(`
		INSERT INTO sessions (token, username, role, name, expires_at, usb_key_id)
		VALUES (?, ?, ?, ?, ?, ?)`,
		token, username, role, name, expires.Format("2006-01-02 15:04:05"), req.KeyID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": "Erro ao criar sessão USB."})
		return
	}

	a.logAudit(username, role, "LOGIN_USB_SUCCESS", fmt.Sprintf("Autenticação automática plug-and-play via pen drive USB '%s' (Ed25519)", req.KeyID), ip)

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})

	json.NewEncoder(w).Encode(map[string]any{
		"status":      "ok",
		"auth_method": "usb_key",
		"key_id":      req.KeyID,
		"user": map[string]any{
			"username": username,
			"name":     name,
			"role":     role,
		},
	})
}
