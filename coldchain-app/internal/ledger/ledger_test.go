package ledger

import (
	"crypto/ecdsa"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) (*sql.DB, *FileSigner, func()) {
	tmpDir, err := os.MkdirTemp("", "ledger_test_*")
	if err != nil {
		t.Fatalf("falha ao criar tmpdir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test_ledger.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("falha ao abrir db: %v", err)
	}

	keyPath := filepath.Join(tmpDir, "server_key.pem")
	signer, err := NewFileSigner(keyPath)
	if err != nil {
		t.Fatalf("falha ao criar signer: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, signer, cleanup
}

func TestLedgerBootstrapAndChain(t *testing.T) {
	db, signer, cleanup := setupTestDB(t)
	defer cleanup()

	led, err := NewLedger(db, signer, EnvTest)
	if err != nil {
		t.Fatalf("erro ao criar ledger: %v", err)
	}

	// 1. Deve ter 2 registros iniciais: GENESIS (ID 1) e BOOT (ID 2)
	total, err := led.TotalRecords()
	if err != nil || total != 2 {
		t.Fatalf("esperado 2 registros no bootstrap, obtido %d (%v)", total, err)
	}

	// 2. Gravar 5 leituras de telemetria
	for i := 1; i <= 5; i++ {
		payload := []byte(fmt.Sprintf(`{"t":4.%d,"h":50.0,"door":0}`, i))
		rec, err := led.AppendData("esp32-01", uint64(i), payload, ClockTrusted)
		if err != nil {
			t.Fatalf("erro ao anexar dado #%d: %v", i, err)
		}
		if rec.ID != uint64(i+2) {
			t.Fatalf("esperado ID %d, obtido %d", i+2, rec.ID)
		}
	}

	// 3. Executar auditoria matemática pericial
	pubKey := signer.Public().(*ecdsa.PublicKey)
	report, err := VerifyChain(db, pubKey)
	if err != nil {
		t.Fatalf("erro na verificacao da cadeia: %v", err)
	}

	if !report.Valid {
		t.Fatalf("cadeia deveria ser valida, anomalias: %v", report.Anomalies)
	}
	if report.TotalRecords != 7 {
		t.Fatalf("esperado 7 registros, auditados %d", report.TotalRecords)
	}
	if report.CheckedHashes != 7 || report.CheckedSignatures != 7 {
		t.Fatalf("esperado 7 hashes e 7 assinaturas verificadas")
	}
}

func TestTamperDetectionOnPayload(t *testing.T) {
	db, signer, cleanup := setupTestDB(t)
	defer cleanup()

	led, err := NewLedger(db, signer, EnvTest)
	if err != nil {
		t.Fatalf("erro ao criar ledger: %v", err)
	}

	// Insere 3 registros
	for i := 1; i <= 3; i++ {
		_, err := led.AppendData("esp32-01", uint64(i), []byte(`{"t":4.2}`), ClockTrusted)
		if err != nil {
			t.Fatalf("erro: %v", err)
		}
	}

	// Desativa temporariamente triggers para simular atacante direto no arquivo SQLite
	_, _ = db.Exec("DROP TRIGGER trg_ledger_no_update;")
	// Altera maliciosamente a temperatura do bloco #3 de 4.2 para 99.9 (adulteração de 1 byte)
	res, err := db.Exec("UPDATE ledger SET payload = ? WHERE id = 3", []byte(`{"t":99.9}`))
	if err != nil {
		t.Fatalf("falha ao injetar alteracao: %v", err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected != 1 {
		t.Fatalf("esperado 1 linha adulterada")
	}

	// Audita a cadeia: DEVE detectar matematicamente a fraude!
	pubKey := signer.Public().(*ecdsa.PublicKey)
	report, err := VerifyChain(db, pubKey)
	if err != nil {
		t.Fatalf("erro na verificacao: %v", err)
	}

	if report.Valid {
		t.Fatalf("PERIGO: Verificador NÃO detectou payload adulterado!")
	}
	if len(report.Anomalies) == 0 {
		t.Fatalf("esperado pelo menos 1 anomalia reportada")
	}
	t.Logf("[SUCESSO] Fraude detectada com precisão: %s", report.Anomalies[0])
}

func TestTamperWORMTriggerEnforcement(t *testing.T) {
	db, signer, cleanup := setupTestDB(t)
	defer cleanup()

	led, err := NewLedger(db, signer, EnvTest)
	if err != nil {
		t.Fatalf("erro: %v", err)
	}

	_, _ = led.AppendData("esp32-01", 1, []byte(`{"t":4.2}`), ClockTrusted)

	// Tentativa de UPDATE pelo usuário SQL direto deve ser abortada pelo trigger
	_, err = db.Exec("UPDATE ledger SET device_id = 'hacker' WHERE id = 1")
	if err == nil {
		t.Fatalf("Trigger trg_ledger_no_update DEVERIA ter abortado a instrucao UPDATE")
	}
	t.Logf("[SUCESSO] UPDATE abortado pelo SQLite: %v", err)

	// Tentativa de DELETE deve ser abortada pelo trigger
	_, err = db.Exec("DELETE FROM ledger WHERE id = 1")
	if err == nil {
		t.Fatalf("Trigger trg_ledger_no_delete DEVERIA ter abortado a instrucao DELETE")
	}
	t.Logf("[SUCESSO] DELETE abortado pelo SQLite: %v", err)
}
