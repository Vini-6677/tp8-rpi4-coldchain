package main

import (
	"bytes"
	"coldchain-app/internal/ledger"
	"coldchain-app/internal/usbkey"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func buildValidPDFBytes(tag string) []byte {
	return []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n% " + tag + "\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
}

func setupIntegrationApp(t *testing.T) (*App, http.Handler) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := initDB(filepath.Join(tmpDir, "integration.db"))
	if err != nil {
		t.Fatalf("initDB falhou: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	keyPath := filepath.Join(tmpDir, "ledger.pem")
	signer, err := ledger.NewFileSigner(keyPath)
	if err != nil {
		t.Fatalf("NewFileSigner falhou: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(keyPath, 0600) })
	led, err := ledger.NewLedger(db, signer, ledger.EnvTest)
	if err != nil {
		t.Fatalf("NewLedger falhou: %v", err)
	}

	app := &App{
		db:           db,
		certDir:      filepath.Join(tmpDir, "certificates"),
		ledger:       led,
		signer:       signer,
		subscribers:  make(map[chan Telemetry]struct{}),
		latestSeq:    make(map[string]int64),
		devicesSeen:  make(map[string]time.Time),
		usbSigTokens: make(map[string]usbSigTokenEntry),
	}
	return app, app.routes()
}

func loginAdmin(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login admin falhou (%d): %s", w.Code, w.Body.String())
	}
	var resp struct {
		CryptoKey string `json:"crypto_key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("nenhum cookie de sessão retornado")
	}
	return cookies[0], resp.CryptoKey
}

func addPDFPart(t *testing.T, mw *multipart.Writer, fieldName, filename, contentType string, content []byte) {
	t.Helper()
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="`+fieldName+`"; filename="`+filename+`"`)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	pw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("CreatePart falhou: %v", err)
	}
	_, _ = pw.Write(content)
}

func TestCertificatesIntegratedWithSensorAndChamberCreation(t *testing.T) {
	_, handler := setupIntegrationApp(t)
	cookie, cryptoKey := loginAdmin(t, handler)

	// 1. Cria câmara já anexando o laudo de qualificação PDF
	var chBuf bytes.Buffer
	chW := multipart.NewWriter(&chBuf)
	_ = chW.WriteField("id", "CH-INT-1")
	_ = chW.WriteField("name", "Câmara Integrada 1")
	_ = chW.WriteField("type", "refrigerado")
	_ = chW.WriteField("temp_min", "2.0")
	_ = chW.WriteField("temp_max", "8.0")
	_ = chW.WriteField("humidity_min", "30")
	_ = chW.WriteField("humidity_max", "75")
	_ = chW.WriteField("alarm_delay_min", "5")
	_ = chW.WriteField("qualification_status", "Qualificado (DQ/IQ/OQ/PQ)")
	_ = chW.WriteField("qualification_valid_until", time.Now().AddDate(1, 0, 0).Format("2006-01-02"))
	_ = chW.WriteField("cert_document_number", "QUAL-2026/001")
	_ = chW.WriteField("cert_issuer", "Engenharia Clínica ANVISA")
	_ = chW.WriteField("password", cryptoKey)
	addPDFPart(t, chW, "cert_file", "qualificacao_ch1.pdf", "application/pdf", buildValidPDFBytes("QUAL-CH1"))
	_ = chW.Close()

	reqCh := httptest.NewRequest(http.MethodPost, "/api/pharma/chambers", &chBuf)
	reqCh.Header.Set("Content-Type", chW.FormDataContentType())
	reqCh.AddCookie(cookie)
	wCh := httptest.NewRecorder()
	handler.ServeHTTP(wCh, reqCh)
	if wCh.Code != http.StatusCreated {
		t.Fatalf("esperava 201 ao criar câmara com PDF, obteve %d: %s", wCh.Code, wCh.Body.String())
	}
	var chResp map[string]any
	_ = json.Unmarshal(wCh.Body.Bytes(), &chResp)
	if chResp["certificate_status"] != "VALIDO" || chResp["certificate_id"] == "" {
		t.Fatalf("câmara criada com PDF deveria retornar certificate_status=VALIDO: %+v", chResp)
	}

	// 2. Tentativa de criar sensor com PDF falso deve falhar ANTES de criar o sensor
	var badBuf bytes.Buffer
	badW := multipart.NewWriter(&badBuf)
	_ = badW.WriteField("id", "S-FAKE")
	_ = badW.WriteField("chamber_id", "CH-INT-1")
	_ = badW.WriteField("serial_number", "SN-FAKE")
	_ = badW.WriteField("thermal_position", "Topo")
	_ = badW.WriteField("cert_number", "CAL-FAKE")
	_ = badW.WriteField("calibrated_by", "Lab")
	_ = badW.WriteField("calibrated_at", time.Now().Format("2006-01-02"))
	_ = badW.WriteField("calib_valid_until", time.Now().AddDate(1, 0, 0).Format("2006-01-02"))
	_ = badW.WriteField("password", cryptoKey)
	addPDFPart(t, badW, "cert_file", "falso.pdf", "application/pdf", []byte("ISTO NAO E UM PDF VALIDO 12345678901234567890123456789012345678901234567890"))
	_ = badW.Close()

	reqBad := httptest.NewRequest(http.MethodPost, "/api/pharma/sensors", &badBuf)
	reqBad.Header.Set("Content-Type", badW.FormDataContentType())
	reqBad.AddCookie(cookie)
	wBad := httptest.NewRecorder()
	handler.ServeHTTP(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("esperava 400 para PDF falso no cadastro de sensor, obteve %d: %s", wBad.Code, wBad.Body.String())
	}

	// 3. Cria sensor com PDF RBC válido anexado no mesmo formulário
	var sBuf bytes.Buffer
	sW := multipart.NewWriter(&sBuf)
	_ = sW.WriteField("id", "S-INT-1")
	_ = sW.WriteField("chamber_id", "CH-INT-1")
	_ = sW.WriteField("serial_number", "SN-PT100-777")
	_ = sW.WriteField("thermal_position", "Ponto Quente Porta")
	_ = sW.WriteField("cert_number", "CAL-RBC-2026/777")
	_ = sW.WriteField("calibrated_by", "INMETRO RBC 412")
	_ = sW.WriteField("calibrated_at", time.Now().Format("2006-01-02"))
	_ = sW.WriteField("calib_valid_until", time.Now().AddDate(1, 0, 0).Format("2006-01-02"))
	_ = sW.WriteField("password", cryptoKey)
	addPDFPart(t, sW, "cert_file", "cert_s_int_1.pdf", "application/pdf", buildValidPDFBytes("SENSOR-V1"))
	_ = sW.Close()

	reqS := httptest.NewRequest(http.MethodPost, "/api/pharma/sensors", &sBuf)
	reqS.Header.Set("Content-Type", sW.FormDataContentType())
	reqS.AddCookie(cookie)
	wS := httptest.NewRecorder()
	handler.ServeHTTP(wS, reqS)
	if wS.Code != http.StatusCreated {
		t.Fatalf("esperava 201 ao criar sensor com PDF, obteve %d: %s", wS.Code, wS.Body.String())
	}
	var sResp map[string]any
	_ = json.Unmarshal(wS.Body.Bytes(), &sResp)
	certID, _ := sResp["certificate_id"].(string)
	if certID == "" || sResp["certificate_status"] != "VALIDO" {
		t.Fatalf("sensor criado deveria retornar certificate_id e VALIDO: %+v", sResp)
	}

	// 4. Visualização inline no iframe deve retornar X-Frame-Options: SAMEORIGIN e Content-Type: application/pdf
	reqView := httptest.NewRequest(http.MethodGet, "/api/pharma/certificates/file?id="+certID+"&mode=inline", nil)
	reqView.AddCookie(cookie)
	wView := httptest.NewRecorder()
	handler.ServeHTTP(wView, reqView)
	if wView.Code != http.StatusOK || wView.Header().Get("Content-Type") != "application/pdf" || wView.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("visualização inline inválida: code=%d ct=%s xfo=%s", wView.Code, wView.Header().Get("Content-Type"), wView.Header().Get("X-Frame-Options"))
	}

	// 5. Substituição do certificado cria versão v2 preservando v1
	var repBuf bytes.Buffer
	repW := multipart.NewWriter(&repBuf)
	_ = repW.WriteField("certificate_id", certID)
	_ = repW.WriteField("document_number", "CAL-RBC-2027/777")
	_ = repW.WriteField("issuer", "INMETRO RBC 412")
	_ = repW.WriteField("issued_at", time.Now().Format("2006-01-02"))
	_ = repW.WriteField("valid_until", time.Now().AddDate(1, 0, 0).Format("2006-01-02"))
	_ = repW.WriteField("replace_reason", "Renovação anual RBC")
	_ = repW.WriteField("password", cryptoKey)
	addPDFPart(t, repW, "file", "cert_s_int_1_v2.pdf", "application/pdf", buildValidPDFBytes("SENSOR-V2"))
	_ = repW.Close()

	reqRep := httptest.NewRequest(http.MethodPost, "/api/pharma/certificates/replace", &repBuf)
	reqRep.Header.Set("Content-Type", repW.FormDataContentType())
	reqRep.AddCookie(cookie)
	wRep := httptest.NewRecorder()
	handler.ServeHTTP(wRep, reqRep)
	if wRep.Code != http.StatusOK {
		t.Fatalf("substituição falhou (%d): %s", wRep.Code, wRep.Body.String())
	}

	// Verifica que existem 2 versões na trilha de auditoria
	reqVer := httptest.NewRequest(http.MethodGet, "/api/pharma/certificates/versions?id="+certID, nil)
	reqVer.AddCookie(cookie)
	wVer := httptest.NewRecorder()
	handler.ServeHTTP(wVer, reqVer)
	var verResp struct {
		Versions []map[string]any `json:"versions"`
	}
	_ = json.Unmarshal(wVer.Body.Bytes(), &verResp)
	if len(verResp.Versions) != 2 {
		t.Fatalf("esperava 2 versões preservadas após substituição, obteve %d", len(verResp.Versions))
	}
}

func TestUSBKeyProvisionLoginCloneProtectionSignAndRevocation(t *testing.T) {
	app, handler := setupIntegrationApp(t)
	adminCookie, _ := loginAdmin(t, handler)

	// 1. Admin cria usuário técnico
	createBody, _ := json.Marshal(map[string]string{
		"name": "Dra. Paula USB", "username": "paula", "role": "tecnico", "password": "TempPassword123!",
	})
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/admin/users/create", bytes.NewReader(createBody))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.AddCookie(adminCookie)
	wCreate := httptest.NewRecorder()
	handler.ServeHTTP(wCreate, reqCreate)
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("falha ao criar usuário paula: %d %s", wCreate.Code, wCreate.Body.String())
	}

	// 2. Agente gera par Ed25519 localmente vinculado ao pen drive e grava com fsync + read-back
	usbDir := t.TempDir()
	hwID := "USB:SANDISK:EXTREME:9988776655443322"
	kf, rawJSON, pubKeyB64, err := usbkey.GenerateProvisionedKey("paula", hwID, usbkey.BindingHardware)
	if err != nil {
		t.Fatalf("GenerateProvisionedKey falhou: %v", err)
	}
	_, fileSHA, err := usbkey.WriteAndVerifyKeyFile(usbDir, rawJSON)
	if err != nil {
		t.Fatalf("WriteAndVerifyKeyFile falhou: %v", err)
	}

	// 3. Registra SOMENTE a chave pública no servidor
	regBody, _ := json.Marshal(map[string]any{
		"username":            "paula",
		"key_id":              kf.KeyID,
		"public_key_b64":      pubKeyB64,
		"algorithm":           kf.Algorithm,
		"binding_mode":        kf.BindingMode,
		"device_binding_hash": kf.DeviceBindingHash,
		"usb_vendor_product":  "SanDisk Extreme",
		"usb_serial_masked":   usbkey.MaskSerial("9988776655443322"),
		"drive_label":         "CHAVE-PAULA",
		"file_sha256":         fileSHA,
	})
	reqReg := httptest.NewRequest(http.MethodPost, "/api/admin/users/usb-keys/register", bytes.NewReader(regBody))
	reqReg.Header.Set("Content-Type", "application/json")
	reqReg.AddCookie(adminCookie)
	wReg := httptest.NewRecorder()
	handler.ServeHTTP(wReg, reqReg)
	if wReg.Code != http.StatusCreated {
		t.Fatalf("falha ao registrar chave pública USB: %d %s", wReg.Code, wReg.Body.String())
	}

	// Confirma que nenhuma chave privada foi salva no banco
	var dbPub string
	_ = app.db.QueryRow("SELECT public_key_b64 FROM user_usb_keys WHERE id = ?", kf.KeyID).Scan(&dbPub)
	if dbPub != pubKeyB64 || strings.Contains(dbPub, "PRIVATE") {
		t.Fatalf("chave pública armazenada incorretamente")
	}

	// 4. Login Plug-and-Play (Challenge -> Sign -> Login)
	chalReqBody, _ := json.Marshal(map[string]string{"key_id": kf.KeyID, "purpose": "LOGIN"})
	reqChal := httptest.NewRequest(http.MethodPost, "/api/auth/usb/challenge", bytes.NewReader(chalReqBody))
	wChal := httptest.NewRecorder()
	handler.ServeHTTP(wChal, reqChal)
	if wChal.Code != http.StatusOK {
		t.Fatalf("challenge falhou: %d %s", wChal.Code, wChal.Body.String())
	}
	var chalResp struct {
		ChallengeID string `json:"challenge_id"`
		NonceB64    string `json:"nonce_b64"`
	}
	_ = json.Unmarshal(wChal.Body.Bytes(), &chalResp)

	// Lê chave do pen drive e assina
	_, loadedKF, _, err := usbkey.ReadKeyFileFromMount(usbDir)
	if err != nil {
		t.Fatalf("ReadKeyFileFromMount falhou: %v", err)
	}
	msg := usbkey.BuildAuthChallengeMessage(chalResp.ChallengeID, chalResp.NonceB64, loadedKF.KeyID, loadedKF.DeviceBindingHash, usbkey.PurposeLogin)
	sigB64, bindHash, err := usbkey.UnlockAndSign(loadedKF, hwID, msg)
	if err != nil {
		t.Fatalf("UnlockAndSign falhou: %v", err)
	}

	loginPayload, _ := json.Marshal(map[string]string{
		"challenge_id":        chalResp.ChallengeID,
		"key_id":              loadedKF.KeyID,
		"signature_b64":       sigB64,
		"device_binding_hash": bindHash,
	})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/auth/usb/login", bytes.NewReader(loginPayload))
	wLogin := httptest.NewRecorder()
	handler.ServeHTTP(wLogin, reqLogin)
	if wLogin.Code != http.StatusOK {
		t.Fatalf("login USB falhou: %d %s", wLogin.Code, wLogin.Body.String())
	}
	usbCookie := wLogin.Result().Cookies()[0]

	// 5. Proteção contra replay do mesmo desafio
	reqReplay := httptest.NewRequest(http.MethodPost, "/api/auth/usb/login", bytes.NewReader(loginPayload))
	wReplay := httptest.NewRecorder()
	handler.ServeHTTP(wReplay, reqReplay)
	if wReplay.Code != http.StatusUnauthorized {
		t.Fatalf("replay do desafio deveria falhar com 401, obteve %d", wReplay.Code)
	}

	// 6. Assinatura 21 CFR Part 11 com Pen Drive (SIGN_ACTION -> token único)
	sigChalBody, _ := json.Marshal(map[string]string{"key_id": kf.KeyID, "purpose": "SIGN_ACTION"})
	reqSC := httptest.NewRequest(http.MethodPost, "/api/auth/usb/challenge", bytes.NewReader(sigChalBody))
	reqSC.AddCookie(usbCookie)
	wSC := httptest.NewRecorder()
	handler.ServeHTTP(wSC, reqSC)
	if wSC.Code != http.StatusOK {
		t.Fatalf("SIGN_ACTION challenge falhou: %d %s", wSC.Code, wSC.Body.String())
	}
	var scResp struct {
		ChallengeID string `json:"challenge_id"`
		NonceB64    string `json:"nonce_b64"`
	}
	_ = json.Unmarshal(wSC.Body.Bytes(), &scResp)
	scMsg := usbkey.BuildAuthChallengeMessage(scResp.ChallengeID, scResp.NonceB64, loadedKF.KeyID, loadedKF.DeviceBindingHash, usbkey.PurposeSignAction)
	scSig, scBind, err := usbkey.UnlockAndSign(loadedKF, hwID, scMsg)
	if err != nil {
		t.Fatalf("UnlockAndSign SIGN_ACTION falhou: %v", err)
	}
	tokReqBody, _ := json.Marshal(map[string]string{
		"challenge_id": scResp.ChallengeID, "key_id": loadedKF.KeyID, "signature_b64": scSig, "device_binding_hash": scBind,
	})
	reqTok := httptest.NewRequest(http.MethodPost, "/api/auth/usb/sign-token", bytes.NewReader(tokReqBody))
	reqTok.AddCookie(usbCookie)
	wTok := httptest.NewRecorder()
	handler.ServeHTTP(wTok, reqTok)
	if wTok.Code != http.StatusOK {
		t.Fatalf("sign-token falhou: %d %s", wTok.Code, wTok.Body.String())
	}
	var tokResp struct {
		SigToken string `json:"sig_token"`
	}
	_ = json.Unmarshal(wTok.Body.Bytes(), &tokResp)
	if !app.verifySignature("paula", tokResp.SigToken) {
		t.Fatalf("verifySignature deveria aceitar token USB válido")
	}
	if app.verifySignature("paula", tokResp.SigToken) {
		t.Fatalf("verifySignature não deveria aceitar replay do token USB já consumido")
	}

	// 7. Revogação da chave USB derruba sessão ativa e bloqueia futuros logins
	revBody, _ := json.Marshal(map[string]string{"key_id": kf.KeyID, "reason": "Pen drive extraviado"})
	reqRev := httptest.NewRequest(http.MethodPost, "/api/admin/users/usb-keys/revoke", bytes.NewReader(revBody))
	reqRev.AddCookie(adminCookie)
	wRev := httptest.NewRecorder()
	handler.ServeHTTP(wRev, reqRev)
	if wRev.Code != http.StatusOK {
		t.Fatalf("revogação falhou: %d %s", wRev.Code, wRev.Body.String())
	}

	// Sessão criada pelo pen drive revogado deve cair imediatamente
	reqMe := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	reqMe.AddCookie(usbCookie)
	wMe := httptest.NewRecorder()
	handler.ServeHTTP(wMe, reqMe)
	var meResp struct {
		Authenticated bool `json:"authenticated"`
	}
	_ = json.Unmarshal(wMe.Body.Bytes(), &meResp)
	if meResp.Authenticated {
		t.Fatalf("sessão da chave USB revogada deveria ter sido encerrada imediatamente")
	}

	// Novo desafio com chave revogada deve retornar 403 KEY_REVOKED
	reqChal2 := httptest.NewRequest(http.MethodPost, "/api/auth/usb/challenge", bytes.NewReader(chalReqBody))
	wChal2 := httptest.NewRecorder()
	handler.ServeHTTP(wChal2, reqChal2)
	if wChal2.Code != http.StatusForbidden {
		t.Fatalf("chave revogada deveria retornar 403, obteve %d: %s", wChal2.Code, wChal2.Body.String())
	}

	// 8. Coexistência 100% preservada: mesmo após revogar o pen drive, o login tradicional (senha -> gera CC-KEY)
	// e a validação clássica 21 CFR Part 11 continuam funcionando normalmente (rejeitando senha e aceitando CC-KEY)!
	tradLoginBody, _ := json.Marshal(map[string]string{"username": "paula", "password": "TempPassword123!"})
	reqTrad := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(tradLoginBody))
	reqTrad.Header.Set("Content-Type", "application/json")
	wTrad := httptest.NewRecorder()
	handler.ServeHTTP(wTrad, reqTrad)
	if wTrad.Code != http.StatusOK {
		t.Fatalf("login tradicional deveria continuar funcionando após revogação USB: %d %s", wTrad.Code, wTrad.Body.String())
	}
	var tradResp struct {
		FirstLogin bool   `json:"first_login"`
		CryptoKey  string `json:"crypto_key"`
	}
	_ = json.Unmarshal(wTrad.Body.Bytes(), &tradResp)
	if !tradResp.FirstLogin || !strings.HasPrefix(tradResp.CryptoKey, "CC-KEY-") {
		t.Fatalf("primeiro login tradicional deveria gerar CC-KEY pessoal: %+v", tradResp)
	}
	// Assinatura clássica com senha de login continua estritamente bloqueada
	if app.verifySignature("paula", "TempPassword123!") {
		t.Fatalf("verifySignature jamais deve aceitar senha de login")
	}
	// Assinatura clássica com CC-KEY pessoal continua válida
	if !app.verifySignature("paula", tradResp.CryptoKey) {
		t.Fatalf("verifySignature deveria continuar aceitando CC-KEY pessoal")
	}
	// Login usando a própria CC-KEY pessoal continua funcionando
	ccLoginBody, _ := json.Marshal(map[string]string{"username": "paula", "password": tradResp.CryptoKey})
	reqCC := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(ccLoginBody))
	reqCC.Header.Set("Content-Type", "application/json")
	wCC := httptest.NewRecorder()
	handler.ServeHTTP(wCC, reqCC)
	if wCC.Code != http.StatusOK {
		t.Fatalf("login clássico com CC-KEY deveria continuar funcionando: %d %s", wCC.Code, wCC.Body.String())
	}
}
