package pharma

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"io"
	"math/big"
	"strings"
	"testing"
)

type mockSigner struct {
	privKey *ecdsa.PrivateKey
}

func (m *mockSigner) Sign(r io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	rVal, sVal, err := ecdsa.Sign(r, m.privKey, digest)
	if err != nil {
		return nil, err
	}
	sig := make([]byte, 64)
	rBytes := rVal.Bytes()
	sBytes := sVal.Bytes()
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):64], sBytes)
	return sig, nil
}

func (m *mockSigner) KeyID() [8]byte {
	return [8]byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE}
}

func (m *mockSigner) PublicKeyRaw() []byte {
	return elliptic.Marshal(m.privKey.Curve, m.privKey.PublicKey.X, m.privKey.PublicKey.Y)
}

func TestDossierDataAndReportsGeneration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Cria tabela ledger para teste de exportação
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS ledger (
		id INTEGER PRIMARY KEY,
		record_type INTEGER NOT NULL,
		device_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		server_ts_ns INTEGER NOT NULL,
		prev_hash BLOB NOT NULL,
		current_hash BLOB NOT NULL,
		signature BLOB NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("erro ao criar tabela ledger no teste: %v", err)
	}

	_, err = db.Exec(`INSERT INTO ledger (id, record_type, device_id, seq, server_ts_ns, prev_hash, current_hash, signature)
		VALUES (1, 1, 'GW-01', 1, 1729600000000000000, X'0000', X'1111', X'2222')`)
	if err != nil {
		t.Fatalf("erro ao semear ledger no teste: %v", err)
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("erro ao gerar chave ECDSA de teste: %v", err)
	}
	signer := &mockSigner{privKey: priv}

	// 1. Compilação dos dados do dossiê
	data, err := FetchDossierReportData(db, signer)
	if err != nil {
		t.Fatalf("erro ao compilar dados do dossiê: %v", err)
	}

	if len(data.ReportSHA256) != 64 {
		t.Errorf("esperava hash SHA-256 de 64 caracteres, obteve %d", len(data.ReportSHA256))
	}
	if data.SignatureHex == "" {
		t.Errorf("esperava assinatura digital ECDSA preenchida")
	}
	if data.SignerKeyID != "deadbeefcafebabe" {
		t.Errorf("esperava key ID 'deadbeefcafebabe', obteve '%s'", data.SignerKeyID)
	}

	// Validar assinatura digital ECDSA matematicamente
	digestBytes, _ := hex.DecodeString(data.ReportSHA256)
	sigBytes, _ := hex.DecodeString(data.SignatureHex)
	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:])
	if !ecdsa.Verify(&priv.PublicKey, digestBytes, r, s) {
		t.Errorf("falha matemática na verificação da assinatura digital ECDSA do laudo")
	}

	// 2. Geração e validação de CSV
	csvBytes, err := GenerateDossierCSV(data)
	if err != nil {
		t.Fatalf("erro ao gerar CSV do dossiê: %v", err)
	}
	csvStr := string(csvBytes)
	if !strings.Contains(csvStr, "COLDCHAIN PHARMA RT - DOSSIE DE AUDITORIA REGULATORIA") {
		t.Errorf("cabeçalho CSV não encontrado")
	}
	if !strings.Contains(csvStr, data.ReportSHA256) {
		t.Errorf("hash SHA-256 ausente no CSV")
	}
	if !strings.Contains(csvStr, "[SECAO 1: INDICADORES REGULATORIOS DE DESEMPENHO]") {
		t.Errorf("Seção 1 ausente no CSV")
	}
	if !strings.Contains(csvStr, "[SECAO 2: INVENTARIO METROLOGICO DE SENSORES CALIBRADOS RBC]") {
		t.Errorf("Seção 2 ausente no CSV")
	}
	if !strings.Contains(csvStr, "S01") || !strings.Contains(csvStr, "CAL-RBC-2025/901") {
		t.Errorf("sensores RBC ausentes no CSV")
	}

	// 3. Geração e validação do PDF 1.4 binário
	pdfBytes, err := GenerateDossierPDF(data)
	if err != nil {
		t.Fatalf("erro ao gerar PDF do dossiê: %v", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-1.4")) {
		t.Errorf("PDF não inicia com cabeçalho padrão %%PDF-1.4")
	}
	trimmed := bytes.TrimSpace(pdfBytes)
	if !bytes.HasSuffix(trimmed, []byte("%%EOF")) {
		t.Errorf("PDF não finaliza com marcador padrão %%%%EOF")
	}
	pdfStr := string(pdfBytes)
	if !strings.Contains(pdfStr, "/Type /Catalog") {
		t.Errorf("objeto Catalog ausente no PDF")
	}
	if !strings.Contains(pdfStr, "/Type /Pages") || !strings.Contains(pdfStr, "/Count 2") {
		t.Errorf("páginas ausentes ou contagem != 2 no PDF")
	}
	if !strings.Contains(pdfStr, "xref") || !strings.Contains(pdfStr, "trailer") {
		t.Errorf("tabela xref ou trailer ausente no PDF")
	}
	if !strings.Contains(pdfStr, "COLDCHAIN PHARMA RT") {
		t.Errorf("texto principal ausente no PDF")
	}

	// 4. Geração e validação do HTML de impressão
	htmlStr := GenerateDossierHTML(data)
	if !strings.Contains(htmlStr, "<!DOCTYPE html>") {
		t.Errorf("doctype ausente no HTML")
	}
	if !strings.Contains(htmlStr, "ColdChain Pharma RT — Dossiê Sanitário de Boas Práticas") {
		t.Errorf("título ausente no HTML")
	}
	if !strings.Contains(htmlStr, data.ReportSHA256) {
		t.Errorf("hash SHA-256 ausente no HTML")
	}

	// 5. Geração de CSV do Ledger WORM
	ledgerCsv, err := GenerateLedgerCSV(db, signer)
	if err != nil {
		t.Fatalf("erro ao exportar Ledger em CSV: %v", err)
	}
	if !bytes.Contains(ledgerCsv, []byte("COLDCHAIN PHARMA RT - LEDGER CRIPTOGRAFICO FORENSE WORM")) {
		t.Errorf("cabeçalho de auditoria WORM ausente no CSV do ledger")
	}
}
