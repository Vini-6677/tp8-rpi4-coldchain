package main

import (
	"coldchain-app/internal/usbkey"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const (
	agentVersion = "1.0.0"
	defaultAddr  = "127.0.0.1:5177"
)

// DriveProvider abstrai a enumeração do hardware USB (em produção usa usbkey.ListRemovableUSBDrives).
type DriveProvider interface {
	ListDrives() ([]usbkey.USBDrive, error)
}

type osDriveProvider struct{}

func (osDriveProvider) ListDrives() ([]usbkey.USBDrive, error) {
	return usbkey.ListRemovableUSBDrives()
}

type wsClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *wsClient) sendJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteJSON(v)
}

type AgentServer struct {
	provider   DriveProvider
	clientsMu  sync.RWMutex
	clients    map[*wsClient]struct{}
	drivesMu   sync.RWMutex
	lastDrives map[string]usbkey.USBDrive
	upgrader   websocket.Upgrader
}

func NewAgentServer(provider DriveProvider) *AgentServer {
	s := &AgentServer{
		provider:   provider,
		clients:    make(map[*wsClient]struct{}),
		lastDrives: make(map[string]usbkey.USBDrive),
	}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 8192,
		CheckOrigin: func(r *http.Request) bool {
			return isAllowedOrigin(r.Header.Get("Origin"))
		},
	}
	return s
}

// isAllowedOrigin garante que apenas origens locais (localhost, 127.0.0.1, *.local ou sub-redes privadas RFC1918)
// possam se conectar ao agente USB local, impedindo acesso de sites públicos da internet.
func isAllowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".local") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	return false
}

func setLocalCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin != "" && isAllowedOrigin(origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
}

func (s *AgentServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ws", s.handleWS)
	return mux
}

func (s *AgentServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	setLocalCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !isAllowedOrigin(origin) {
		http.Error(w, "Origem não autorizada", http.StatusForbidden)
		return
	}
	drives, err := s.provider.ListDrives()
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":        "ok",
		"agent_version": agentVersion,
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"drives_count":  len(drives),
		"drives":        drives,
		"scan_error":    errMsg,
	})
}

type wsRequest struct {
	ReqID        string `json:"req_id"`
	Action       string `json:"action"`
	DriveID      string `json:"drive_id"`
	Username     string `json:"username"`
	Overwrite    bool   `json:"overwrite"`
	ChallengeID  string `json:"challenge_id"`
	NonceB64     string `json:"nonce_b64"`
	Purpose      string `json:"purpose"`
}

func (s *AgentServer) handleWS(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && !isAllowedOrigin(origin) {
		http.Error(w, "Origem não autorizada", http.StatusForbidden)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("Falha no upgrade WebSocket do agente USB", "erro", err)
		return
	}
	client := &wsClient{conn: conn}
	s.clientsMu.Lock()
	s.clients[client] = struct{}{}
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, client)
		s.clientsMu.Unlock()
		_ = conn.Close()
	}()

	// Envia estado inicial dos pen drives conectados imediatamente
	drives, err := s.provider.ListDrives()
	if err == nil {
		s.updateCachedDrives(drives)
		_ = client.sendJSON(map[string]any{
			"type":          "drives_snapshot",
			"agent_version": agentVersion,
			"os":            runtime.GOOS,
			"drives":        drives,
		})
	} else {
		_ = client.sendJSON(map[string]any{
			"type":  "agent_error",
			"error": err.Error(),
		})
	}

	// Loop de comandos RPC do navegador
	for {
		var req wsRequest
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		resp := s.processRPC(req)
		if err := client.sendJSON(resp); err != nil {
			return
		}
	}
}

func (s *AgentServer) processRPC(req wsRequest) map[string]any {
	switch req.Action {
	case "list_drives":
		drives, err := s.provider.ListDrives()
		if err != nil {
			return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error": err.Error()}
		}
		s.updateCachedDrives(drives)
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": true, "drives": drives}

	case "provision_key":
		return s.rpcProvisionKey(req)

	case "sign_challenge":
		return s.rpcSignChallenge(req)

	case "inspect_drive":
		drives, err := s.provider.ListDrives()
		if err != nil {
			return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error": err.Error()}
		}
		for _, d := range drives {
			if d.ID == req.DriveID {
				return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": true, "drive": d}
			}
		}
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "DRIVE_NOT_FOUND", "error": "Pen drive selecionado não está mais conectado."}

	default:
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error": "Ação desconhecida: " + req.Action}
	}
}

func (s *AgentServer) rpcProvisionKey(req wsRequest) map[string]any {
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if username == "" {
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "INVALID_USER", "error": "Login do usuário é obrigatório para gravar a chave no pen drive."}
	}
	drives, err := s.provider.ListDrives()
	if err != nil {
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "SCAN_FAILED", "error": err.Error()}
	}
	var target *usbkey.USBDrive
	for i := range drives {
		if drives[i].ID == req.DriveID {
			target = &drives[i]
			break
		}
	}
	if target == nil {
		return map[string]any{
			"type":       "rpc_response",
			"req_id":     req.ReqID,
			"ok":         false,
			"error_code": "DRIVE_NOT_FOUND",
			"error":      "O pen drive selecionado foi removido ou não é uma unidade USB removível válida (discos internos/SO são bloqueados).",
		}
	}
	if !target.Writable {
		return map[string]any{
			"type":       "rpc_response",
			"req_id":     req.ReqID,
			"ok":         false,
			"error_code": "DRIVE_READ_ONLY",
			"error":      fmt.Sprintf("O pen drive '%s' (%s) está montado como somente leitura ou protegido contra gravação.", target.Label, target.ID),
		}
	}
	if target.KeyStatus == usbkey.KeyStatusValid && !req.Overwrite {
		return map[string]any{
			"type":         "rpc_response",
			"req_id":       req.ReqID,
			"ok":           false,
			"error_code":   "KEY_ALREADY_EXISTS",
			"existing_user": target.KeyUsername,
			"error":        fmt.Sprintf("Este pen drive já possui uma chave gravada para o usuário '%s'. Marque 'Sobrescrever chave existente' para substituí-la.", target.KeyUsername),
		}
	}

	// Gera o par criptográfico Ed25519 localmente (a chave privada NUNCA sai do agente!)
	kf, rawJSON, pubKeyB64, err := usbkey.GenerateProvisionedKey(username, target.BindingID, target.BindingMode)
	if err != nil {
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "KEYGEN_FAILED", "error": err.Error()}
	}

	// Grava atomicamente com fsync + leitura de confirmação (read-back SHA-256)
	writtenPath, fileSHA256, err := usbkey.WriteAndVerifyKeyFile(target.MountPath, rawJSON)
	if err != nil {
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "WRITE_FAILED", "error": err.Error()}
	}

	// Atualiza snapshot após gravação
	if refreshed, rErr := s.provider.ListDrives(); rErr == nil {
		s.updateCachedDrives(refreshed)
		s.broadcast(map[string]any{
			"type":   "drives_snapshot",
			"drives": refreshed,
		})
	}

	slog.Info("Chave Ed25519 gravada e verificada no pen drive USB",
		"usuario", username, "drive", target.ID, "key_id", kf.KeyID, "binding", target.BindingMode, "path", writtenPath)

	return map[string]any{
		"type":               "rpc_response",
		"req_id":             req.ReqID,
		"ok":                 true,
		"key_id":             kf.KeyID,
		"username":           kf.Username,
		"public_key_b64":     pubKeyB64,
		"algorithm":          kf.Algorithm,
		"binding_mode":       kf.BindingMode,
		"device_binding_hash": kf.DeviceBindingHash,
		"usb_vendor_product": target.VendorProductLabel(),
		"usb_serial_masked":  target.SerialMasked,
		"drive_label":        target.Label,
		"drive_id":           target.ID,
		"file_sha256":        fileSHA256,
		"written_path":       writtenPath,
		"verified_readback":  true,
	}
}

func (s *AgentServer) rpcSignChallenge(req wsRequest) map[string]any {
	if strings.TrimSpace(req.ChallengeID) == "" || strings.TrimSpace(req.NonceB64) == "" {
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "INVALID_CHALLENGE", "error": "Desafio criptográfico ausente."}
	}
	drives, err := s.provider.ListDrives()
	if err != nil {
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "SCAN_FAILED", "error": err.Error()}
	}
	var target *usbkey.USBDrive
	for i := range drives {
		if drives[i].ID == req.DriveID {
			target = &drives[i]
			break
		}
	}
	if target == nil {
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "DRIVE_NOT_FOUND", "error": "Pen drive foi desconectado durante a autenticação."}
	}

	_, kf, status, err := usbkey.ReadKeyFileFromMount(target.MountPath)
	if status == usbkey.KeyStatusNoKey {
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "NO_KEY", "error": "Pen drive sem arquivo de chave ColdChain."}
	}
	if status == usbkey.KeyStatusPermissionDenied {
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "PERMISSION_DENIED", "error": err.Error()}
	}
	if err != nil || kf == nil {
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "CORRUPTED_KEY", "error": "Arquivo de chave no pen drive está corrompido ou adulterado."}
	}

	purpose := req.Purpose
	if purpose == "" {
		purpose = usbkey.PurposeLogin
	}
	currentBindHash := usbkey.ComputeDeviceBindingHash(target.BindingID, kf.KDFSalt)
	msg := usbkey.BuildAuthChallengeMessage(req.ChallengeID, req.NonceB64, kf.KeyID, currentBindHash, purpose)

	sigB64, bindHash, err := usbkey.UnlockAndSign(kf, target.BindingID, msg)
	if err != nil {
		if errors.Is(err, usbkey.ErrDeviceBindingMismatch) {
			return map[string]any{
				"type":                "rpc_response",
				"req_id":              req.ReqID,
				"ok":                  false,
				"error_code":          "CLONE_DETECTED",
				"device_binding_hash": bindHash,
				"error":               "Falha de vínculo de hardware: este arquivo de chave pertence a outro pen drive físico (cópia/clonagem bloqueada).",
			}
		}
		return map[string]any{"type": "rpc_response", "req_id": req.ReqID, "ok": false, "error_code": "SIGN_FAILED", "error": err.Error()}
	}

	return map[string]any{
		"type":                "rpc_response",
		"req_id":              req.ReqID,
		"ok":                  true,
		"key_id":              kf.KeyID,
		"username":            kf.Username,
		"signature_b64":       sigB64,
		"device_binding_hash": bindHash,
		"binding_mode":        kf.BindingMode,
	}
}

func (s *AgentServer) updateCachedDrives(drives []usbkey.USBDrive) {
	s.drivesMu.Lock()
	defer s.drivesMu.Unlock()
	next := make(map[string]usbkey.USBDrive, len(drives))
	for _, d := range drives {
		next[d.ID] = d
	}
	s.lastDrives = next
}

func (s *AgentServer) broadcast(msg any) {
	s.clientsMu.RLock()
	clients := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clientsMu.RUnlock()

	for _, c := range clients {
		_ = c.sendJSON(msg)
	}
}

// StartMonitor monitora continuamente a inserção e remoção de pen drives USB em tempo real.
func (s *AgentServer) StartMonitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 700 * time.Millisecond
	}
	// Carga inicial
	if initial, err := s.provider.ListDrives(); err == nil {
		s.updateCachedDrives(initial)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			drives, err := s.provider.ListDrives()
			if err != nil {
				continue
			}

			s.drivesMu.Lock()
			prev := s.lastDrives
			next := make(map[string]usbkey.USBDrive, len(drives))
			inserted := make([]usbkey.USBDrive, 0)
			changed := false

			for _, d := range drives {
				next[d.ID] = d
				old, existed := prev[d.ID]
				if !existed {
					inserted = append(inserted, d)
					changed = true
				} else if old.KeyStatus != d.KeyStatus || old.KeyID != d.KeyID || old.Writable != d.Writable || old.Label != d.Label {
					changed = true
				}
			}

			removed := make([]string, 0)
			for id := range prev {
				if _, stillHere := next[id]; !stillHere {
					removed = append(removed, id)
					changed = true
				}
			}
			s.lastDrives = next
			s.drivesMu.Unlock()

			if !changed {
				continue
			}

			for _, d := range inserted {
				slog.Info("Pen drive USB detectado", "drive", d.ID, "label", d.Label, "key_status", d.KeyStatus, "user", d.KeyUsername)
				s.broadcast(map[string]any{
					"type":   "drive_inserted",
					"drive":  d,
					"drives": drives,
				})
			}
			for _, id := range removed {
				slog.Info("Pen drive USB removido", "drive", id)
				s.broadcast(map[string]any{
					"type":     "drive_removed",
					"drive_id": id,
					"drives":   drives,
				})
			}
			if len(inserted) == 0 && len(removed) == 0 {
				s.broadcast(map[string]any{
					"type":   "drives_snapshot",
					"drives": drives,
				})
			}
		}
	}
}

func main() {
	addr := flag.String("addr", defaultAddr, "Endereço local de escuta (estritamente loopback 127.0.0.1)")
	flag.Parse()

	// Garante que o agente só aceite loopback por segurança
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		fmt.Fprintf(os.Stderr, "ERRO DE SEGURANÇA: coldchain-usb-agent deve escutar exclusivamente em loopback (127.0.0.1). Endereço rejeitado: %s\n", *addr)
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	slog.Info("Iniciando ColdChain USB Hardware Key Agent", "versao", agentVersion, "os", runtime.GOOS, "addr", *addr)

	agent := NewAgentServer(osDriveProvider{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.StartMonitor(ctx, 700*time.Millisecond)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           agent.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Erro no servidor do agente USB", "erro", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("Encerrando ColdChain USB Hardware Key Agent...")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}
