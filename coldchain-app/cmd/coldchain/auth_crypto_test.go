package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestApp(t *testing.T) (*App, *sql.DB) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}

	schema := `
	CREATE TABLE users (
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
	CREATE TABLE sessions (
		token TEXT PRIMARY KEY,
		username TEXT NOT NULL,
		role TEXT NOT NULL,
		name TEXT NOT NULL,
		expires_at DATETIME NOT NULL
	);
	CREATE TABLE audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		username TEXT NOT NULL,
		role TEXT NOT NULL,
		action TEXT NOT NULL,
		details TEXT NOT NULL,
		ip_address TEXT NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	app := &App{
		db:          db,
		devicesSeen: make(map[string]time.Time),
	}
	return app, db
}

func TestFirstLoginCryptoKeyGeneration(t *testing.T) {
	app, db := setupTestApp(t)
	defer db.Close()

	// 1. Create a user as Admin would (with first_login = 1)
	salt := generateSalt()
	initialPwd := "TempPassword2026!"
	hash := hashPassword(initialPwd, salt)

	_, err := db.Exec(`
		INSERT INTO users (username, password_hash, salt, name, role, is_blocked, first_login, crypto_key_hash)
		VALUES ('mariana', ?, ?, 'Dra. Mariana Costa', 'tecnico', 0, 1, '')
	`, hash, salt)
	if err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	// 2. Perform First Login with initial password
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		json.NewDecoder(r.Body).Decode(&req)

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
		err := app.db.QueryRow(`
			SELECT id, password_hash, salt, name, role, is_blocked, COALESCE(first_login, 0), COALESCE(crypto_key_hash, '')
			FROM users WHERE username = ?
		`, req.Username).Scan(&id, &passwordHash, &salt, &name, &role, &isBlocked, &firstLogin, &cryptoKeyHash)

		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		computed := hashPassword(req.Password, salt)
		isPwdMatch := computed == passwordHash
		isKeyMatch := cryptoKeyHash != "" && computed == cryptoKeyHash

		if !isPwdMatch && !isKeyMatch {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var generatedCryptoKey string
		isFirstLogin := false
		if firstLogin == 1 {
			isFirstLogin = true
			generatedCryptoKey = generateCryptoKey()
			newKeyHash := hashPassword(generatedCryptoKey, salt)
			_, _ = app.db.Exec(`
				UPDATE users 
				SET crypto_key_hash = ?, first_login = 0 
				WHERE id = ?
			`, newKeyHash, id)
		}

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

	loginBody, _ := json.Marshal(map[string]string{
		"username": "mariana",
		"password": initialPwd,
	})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewBuffer(loginBody))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("first login failed with status %d", rec.Code)
	}

	var loginResp struct {
		Status     string `json:"status"`
		FirstLogin bool   `json:"first_login"`
		CryptoKey  string `json:"crypto_key"`
	}
	json.NewDecoder(rec.Body).Decode(&loginResp)

	if !loginResp.FirstLogin {
		t.Errorf("expected first_login to be true")
	}
	if loginResp.CryptoKey == "" || len(loginResp.CryptoKey) < 15 {
		t.Errorf("expected valid crypto_key, got: %s", loginResp.CryptoKey)
	}
	t.Logf("Generated Crypto Key: %s", loginResp.CryptoKey)

	// 3. Verify user in DB now has first_login = 0 and crypto_key_hash set
	var fl int
	var ckh string
	_ = db.QueryRow("SELECT first_login, crypto_key_hash FROM users WHERE username = 'mariana'").Scan(&fl, &ckh)
	if fl != 0 {
		t.Errorf("expected first_login = 0 after activation, got: %d", fl)
	}
	if ckh == "" {
		t.Errorf("expected crypto_key_hash to be populated")
	}

	// 4. Perform Second Login using the Crypto Key
	secondLoginBody, _ := json.Marshal(map[string]string{
		"username": "mariana",
		"password": loginResp.CryptoKey,
	})
	req2 := httptest.NewRequest("POST", "/api/auth/login", bytes.NewBuffer(secondLoginBody))
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("second login using crypto key failed with status %d", rec2.Code)
	}

	var loginResp2 struct {
		Status     string `json:"status"`
		FirstLogin bool   `json:"first_login"`
	}
	json.NewDecoder(rec2.Body).Decode(&loginResp2)
	if loginResp2.FirstLogin {
		t.Errorf("expected first_login to be false on second login")
	}

	// 5. Verify electronic signature check
	sigHash := hashPassword(loginResp.CryptoKey, salt)
	if sigHash != ckh {
		t.Errorf("expected signature hash to match stored crypto_key_hash")
	}
}
