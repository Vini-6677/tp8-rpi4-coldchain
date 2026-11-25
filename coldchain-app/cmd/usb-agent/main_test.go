package main

import (
	"coldchain-app/internal/usbkey"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testDriveProvider struct {
	mu     sync.RWMutex
	drives []usbkey.USBDrive
}

func (p *testDriveProvider) Set(drives []usbkey.USBDrive) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drives = append([]usbkey.USBDrive(nil), drives...)
}

func (p *testDriveProvider) ListDrives() ([]usbkey.USBDrive, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]usbkey.USBDrive, len(p.drives))
	for i, d := range p.drives {
		usbkey.InspectDriveKey(&d)
		out[i] = d
	}
	return out, nil
}

func TestIsAllowedOriginSecurity(t *testing.T) {
	allowed := []string{
		"",
		"http://localhost:5000",
		"http://127.0.0.1:5000",
		"http://coldchain-rpi4.local:5000",
		"http://192.168.1.55:5000",
		"http://10.0.0.12:5000",
		"http://172.16.4.2:5000",
	}
	for _, o := range allowed {
		if !isAllowedOrigin(o) {
			t.Errorf("origem local/LAN deveria ser permitida: %s", o)
		}
	}

	blocked := []string{
		"https://evil.com",
		"http://8.8.8.8:5000",
		"javascript:alert(1)",
	}
	for _, o := range blocked {
		if isAllowedOrigin(o) {
			t.Errorf("origem pública/maliciosa deveria ser bloqueada: %s", o)
		}
	}
}

func TestAgentWSProvisionSignHotPlugAndErrors(t *testing.T) {
	mount1 := t.TempDir()
	mountRO := t.TempDir()

	provider := &testDriveProvider{}
	provider.Set([]usbkey.USBDrive{
		{
			ID: "E:", MountPath: mount1, Label: "COLDCHAIN-USB",
			Vendor: "SanDisk", Product: "Ultra", HardwareSerial: "SN-TEST-00112233",
			SerialMasked: usbkey.MaskSerial("SN-TEST-00112233"),
			BindingMode: usbkey.BindingHardware, BindingID: "USB:SanDisk:Ultra:SN-TEST-00112233",
			TotalBytes: 16 << 30, FreeBytes: 15 << 30, Writable: true,
		},
		{
			ID: "F:", MountPath: mountRO, Label: "READONLY-USB",
			Vendor: "Generic", Product: "Flash", HardwareSerial: "SN-RO-445566",
			SerialMasked: usbkey.MaskSerial("SN-RO-445566"),
			BindingMode: usbkey.BindingHardware, BindingID: "USB:Generic:Flash:SN-RO-445566",
			TotalBytes: 8 << 30, FreeBytes: 8 << 30, Writable: false,
		},
	})

	agent := NewAgentServer(provider)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.StartMonitor(ctx, 80*time.Millisecond)

	srv := httptest.NewServer(agent.Handler())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	headers := http.Header{"Origin": []string{"http://coldchain-rpi4.local:5000"}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("falha ao conectar WS do agente: %v", err)
	}
	defer conn.Close()

	// 1. Snapshot inicial
	var snap map[string]any
	if err := conn.ReadJSON(&snap); err != nil || snap["type"] != "drives_snapshot" {
		t.Fatalf("esperava drives_snapshot inicial, obteve %+v (err=%v)", snap, err)
	}

	// 2. Tentativa em unidade somente leitura deve ser rejeitada
	_ = conn.WriteJSON(wsRequest{ReqID: "r1", Action: "provision_key", DriveID: "F:", Username: "tecnico1"})
	var r1 map[string]any
	_ = conn.ReadJSON(&r1)
	if r1["ok"] != false || r1["error_code"] != "DRIVE_READ_ONLY" {
		t.Fatalf("esperava DRIVE_READ_ONLY em unidade somente leitura, obteve %+v", r1)
	}

	// 3. Provisionamento real com fsync + read-back verification
	_ = conn.WriteJSON(wsRequest{ReqID: "r2", Action: "provision_key", DriveID: "E:", Username: "tecnico1"})
	var r2 map[string]any
	for {
		if err := conn.ReadJSON(&r2); err != nil {
			t.Fatalf("ReadJSON falhou: %v", err)
		}
		if r2["type"] == "rpc_response" && r2["req_id"] == "r2" {
			break
		}
	}
	if r2["ok"] != true || r2["verified_readback"] != true {
		t.Fatalf("provision_key falhou: %+v", r2)
	}
	if _, hasPriv := r2["private_key"]; hasPriv {
		t.Fatalf("chave privada jamais pode sair do agente")
	}
	pubKey := r2["public_key_b64"].(string)
	keyID := r2["key_id"].(string)
	bindHash := r2["device_binding_hash"].(string)

	// 4. Segunda tentativa sem overwrite=true deve proteger chave existente
	_ = conn.WriteJSON(wsRequest{ReqID: "r3", Action: "provision_key", DriveID: "E:", Username: "outro"})
	var r3 map[string]any
	for {
		_ = conn.ReadJSON(&r3)
		if r3["type"] == "rpc_response" && r3["req_id"] == "r3" {
			break
		}
	}
	if r3["ok"] != false || r3["error_code"] != "KEY_ALREADY_EXISTS" {
		t.Fatalf("esperava KEY_ALREADY_EXISTS sem overwrite, obteve %+v", r3)
	}

	// 5. Assinatura de desafio Ed25519
	_ = conn.WriteJSON(wsRequest{ReqID: "r4", Action: "sign_challenge", DriveID: "E:", ChallengeID: "chal-99", NonceB64: "bm9uY2U=", Purpose: "LOGIN"})
	var r4 map[string]any
	for {
		_ = conn.ReadJSON(&r4)
		if r4["type"] == "rpc_response" && r4["req_id"] == "r4" {
			break
		}
	}
	if r4["ok"] != true {
		t.Fatalf("sign_challenge falhou: %+v", r4)
	}
	expectedMsg := usbkey.BuildAuthChallengeMessage("chal-99", "bm9uY2U=", keyID, bindHash, "LOGIN")
	if !usbkey.VerifyChallengeSignature(pubKey, expectedMsg, r4["signature_b64"].(string)) {
		t.Fatalf("assinatura retornada pelo agente não passou na verificação Ed25519")
	}

	// 6. Hot-plug: inserção de um terceiro pen drive deve emitir evento drive_inserted em tempo real
	mount3 := t.TempDir()
	provider.Set([]usbkey.USBDrive{
		{ID: "E:", MountPath: mount1, Label: "COLDCHAIN-USB", BindingMode: usbkey.BindingHardware, BindingID: "USB:SanDisk:Ultra:SN-TEST-00112233", Writable: true},
		{ID: "G:", MountPath: mount3, Label: "NOVO-PEN", BindingMode: usbkey.BindingHardware, BindingID: "USB:Kingston:DT:SN-NEW-777", Writable: true},
	})

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	gotInserted := false
	for i := 0; i < 6; i++ {
		var ev map[string]any
		if err := conn.ReadJSON(&ev); err != nil {
			break
		}
		if ev["type"] == "drive_inserted" {
			dr := ev["drive"].(map[string]any)
			if dr["id"] == "G:" {
				gotInserted = true
				break
			}
		}
	}
	if !gotInserted {
		t.Fatalf("esperava evento hot-plug drive_inserted para unidade G:")
	}
}
