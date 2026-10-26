package pharma

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"math"
	"strings"
	"testing"
)

func TestCalculateMKT(t *testing.T) {
	// Caso de teste com temperaturas constantes de 5.0 °C
	temps := []float64{5.0, 5.0, 5.0, 5.0, 5.0}
	mkt := CalculateMKT(temps)
	if math.Abs(mkt-5.0) > 0.05 {
		t.Errorf("esperava MKT próximo de 5.0 °C, obteve %.2f", mkt)
	}

	// Caso de teste com excursão térmica: 5°C, 5°C, 10°C, 5°C
	// MKT deve ser superior à média aritmética simples por causa da ponderação não linear de Arrhenius
	tempsWithExcursion := []float64{5.0, 5.0, 10.0, 5.0}
	mktExc := CalculateMKT(tempsWithExcursion)
	avg := (5.0 + 5.0 + 10.0 + 5.0) / 4.0 // 6.25 °C
	if mktExc <= avg {
		t.Errorf("MKT (%.2f °C) deve ser maior que a média simples (%.2f °C) após excursão", mktExc, avg)
	}
}

func TestSensorHistoryReportsGeneration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("erro ao gerar chave ECDSA: %v", err)
	}
	signer := &mockSigner{privKey: priv}

	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS readings (
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
	)`)

	// 1. Fetch de dados do Laudo Metrológico de Sensores
	data, err := FetchSensorHistoryReportData(db, signer, "CH-A", 50, "tecnico", "Dra. Carolina", "carolina")
	if err != nil {
		t.Fatalf("erro ao buscar dados de sensores: %v", err)
	}

	if data.ChamberID == "" {
		t.Errorf("esperava câmara preenchida")
	}
	if data.MKT <= 0 {
		t.Errorf("esperava MKT calculado positivo, obteve %.2f", data.MKT)
	}
	if len(data.ReportSHA256) != 64 {
		t.Errorf("esperava hash SHA-256 de 64 caracteres")
	}
	if data.SignatureHex == "" {
		t.Errorf("esperava assinatura digital ECDSA preenchida")
	}

	// 2. Geração e validação do CSV de Sensores
	csvBytes, err := GenerateSensorHistoryCSV(data)
	if err != nil {
		t.Fatalf("erro ao gerar CSV: %v", err)
	}
	if !bytes.HasPrefix(csvBytes, []byte{0xEF, 0xBB, 0xBF}) {
		t.Errorf("CSV deve iniciar com UTF-8 BOM")
	}
	csvStr := string(csvBytes)
	if !strings.Contains(csvStr, "COLDCHAIN PHARMA RT - LAUDO DE MONITORAMENTO TERMICO DE SENSORES") {
		t.Errorf("cabeçalho oficial ausente no CSV")
	}
	if !strings.Contains(csvStr, "MKT - Temp Cinetica Media (C)") {
		t.Errorf("coluna MKT ausente no CSV")
	}
	if !strings.Contains(csvStr, "ANVISA RDC 430/2020") {
		t.Errorf("referência normativa ANVISA ausente no CSV")
	}

	// 3. Geração e validação do PDF de Sensores
	pdfBytes, err := GenerateSensorHistoryPDF(data)
	if err != nil {
		t.Fatalf("erro ao gerar PDF: %v", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-1.4")) {
		t.Errorf("PDF deve iniciar com %%PDF-1.4")
	}
	pdfStr := string(pdfBytes)
	if !strings.Contains(pdfStr, "COLDCHAIN PHARMA RT - LAUDO DE MONITORAMENTO TERMICO") {
		t.Errorf("título ausente no PDF")
	}
	if !strings.Contains(pdfStr, "MKT (EQUACAO ARRHENIUS)") {
		t.Errorf("bloco MKT ausente no PDF")
	}
	if !strings.Contains(pdfStr, "CHANCELA CRIPTOGRAFICA E DECLARACAO ALCOA+") {
		t.Errorf("chancela forense ausente no PDF")
	}
	if !strings.Contains(pdfStr, "Hash Forense SHA-256:") {
		t.Errorf("hash forense ausente no rodapé do PDF")
	}
	if !strings.Contains(pdfStr, data.ReportSHA256) {
		t.Errorf("SHA-256 do laudo ausente no rodapé das páginas")
	}
}
