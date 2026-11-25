package pharma

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func buildValidTestPDF(label string) []byte {
	return []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n% " + label + "\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
}

func setupCertTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("erro ao abrir sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := InitPharmaSchema(db); err != nil {
		t.Fatalf("InitPharmaSchema falhou: %v", err)
	}
	return db, filepath.Join(tmpDir, "certificates")
}

func TestValidatePDFUploadStrictRules(t *testing.T) {
	validPDF := buildValidTestPDF("CERT-RBC-01")
	res, err := ValidatePDFUpload("certificado_rbc.pdf", "application/pdf", validPDF)
	if err != nil {
		t.Fatalf("PDF válido deveria passar: %v", err)
	}
	if len(res.SHA256Hex) != 64 || res.DetectedMIME != "application/pdf" {
		t.Fatalf("resultado inesperado: %+v", res)
	}

	// 1. Extensão não-PDF rejeitada
	if _, err := ValidatePDFUpload("certificado.exe", "application/pdf", validPDF); err == nil {
		t.Fatalf("deveria rejeitar extensão .exe")
	}

	// 2. MIME declarado inválido rejeitado
	if _, err := ValidatePDFUpload("certificado.pdf", "text/html", validPDF); err == nil {
		t.Fatalf("deveria rejeitar MIME text/html")
	}

	// 3. Arquivo falso renomeado para .pdf sem cabeçalho %PDF-
	fakePDF := bytes.Repeat([]byte("<html>not a pdf</html>"), 10)
	if _, err := ValidatePDFUpload("falso.pdf", "application/pdf", fakePDF); err == nil {
		t.Fatalf("deveria rejeitar arquivo sem cabeçalho %%PDF-")
	}

	// 4. PDF truncado sem %%EOF
	truncated := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n" + strings.Repeat("A", 80))
	if _, err := ValidatePDFUpload("truncado.pdf", "application/pdf", truncated); err == nil {
		t.Fatalf("deveria rejeitar PDF truncado sem %%%%EOF")
	}

	// 5. Tamanho acima do limite (10 MB)
	oversized := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("0"), MaxCertificateSizeBytes+10)...)
	oversized = append(oversized, []byte("\n%%EOF")...)
	if _, err := ValidatePDFUpload("grande.pdf", "application/pdf", oversized); err == nil {
		t.Fatalf("deveria rejeitar arquivo maior que 10 MB")
	}
}

func TestCertificatesLifecycleVersioningAndTamperDetection(t *testing.T) {
	db, certDir := setupCertTestDB(t)

	// Cria câmara e sensor reais
	_, err := RegisterChamber(db, Chamber{
		ID: "CH-CERT-1", Name: "Câmara Vacinas", Type: "refrigerado",
		TempMin: 2.0, TempMax: 8.0, HumidityMin: 30, HumidityMax: 70,
		AlarmDelayMin: 5, QualStatus: "Qualificado", QualValidUntil: time.Now().AddDate(1, 0, 0).Format("2006-01-02"),
	})
	if err != nil {
		t.Fatalf("RegisterChamber falhou: %v", err)
	}
	_, err = RegisterSensor(db, Sensor{
		ID: "S-CERT-1", ChamberID: "CH-CERT-1", SerialNumber: "SN-9001",
		ThermalPosition: "Superior Porta", CertNumber: "CAL-RBC-2026/01",
		CalibratedBy: "INMETRO RBC 412", CalibratedAt: time.Now().Format("2006-01-02"),
		CalibValidUntil: time.Now().AddDate(0, 6, 0).Format("2006-01-02"),
	})
	if err != nil {
		t.Fatalf("RegisterSensor falhou: %v", err)
	}

	// Antes do upload, ambos aparecem como PENDENTE
	pending, err := GetPendingCertificateItems(db)
	if err != nil || len(pending) != 2 {
		t.Fatalf("esperava 2 itens pendentes (sensor + câmara), obteve %d (err=%v)", len(pending), err)
	}
	sensorsBefore, _ := GetSensors(db, "CH-CERT-1")
	if sensorsBefore[0].CertificateStatus != CertStatusPending {
		t.Fatalf("esperava status PENDENTE no sensor sem PDF, obteve %s", sensorsBefore[0].CertificateStatus)
	}

	// Upload v1 (válido > 30 dias)
	pdfV1 := buildValidTestPDF("CERT-V1")
	cert1, ver1, replaced, err := SaveOrReplaceCertificate(db, certDir, CertificateUploadInput{
		EntityType:     CertEntitySensor,
		EntityID:       "S-CERT-1",
		DocumentNumber: "CAL-RBC-2026/01",
		Issuer:         "INMETRO / Cgcre RBC 412",
		IssuedAt:       time.Now().AddDate(0, 0, -5).Format("2006-01-02"),
		ValidUntil:     time.Now().AddDate(1, 0, 0).Format("2006-01-02"),
		UploadedBy:     "tecnico",
		UploadedByName: "Dra. Ana",
		Filename:       "rbc_s1_v1.pdf",
		DeclaredMIME:   "application/pdf",
		FileBytes:      pdfV1,
	})
	if err != nil || replaced || cert1.Status != CertStatusValid || ver1.VersionNumber != 1 {
		t.Fatalf("upload v1 inesperado: cert=%+v ver=%+v replaced=%v err=%v", cert1, ver1, replaced, err)
	}

	// Sensor deve refletir automaticamente o status VALIDO
	sensorsAfter, _ := GetSensors(db, "CH-CERT-1")
	if sensorsAfter[0].CertificateStatus != CertStatusValid || sensorsAfter[0].CertificateID != cert1.ID {
		t.Fatalf("sensor não refletiu certificado válido: %+v", sensorsAfter[0])
	}

	// Substituição para v2 (vencendo em 10 dias -> PROXIMO_VENCIMENTO) preservando v1
	pdfV2 := buildValidTestPDF("CERT-V2-RENEWED")
	cert2, ver2, replaced2, err := SaveOrReplaceCertificate(db, certDir, CertificateUploadInput{
		CertificateID:  cert1.ID,
		DocumentNumber: "CAL-RBC-2026/02",
		Issuer:         "INMETRO / Cgcre RBC 412",
		IssuedAt:       time.Now().AddDate(0, 0, -1).Format("2006-01-02"),
		ValidUntil:     time.Now().AddDate(0, 0, 10).Format("2006-01-02"),
		ReplaceReason:  "Recalibração intermediária",
		UploadedBy:     "tecnico",
		UploadedByName: "Dra. Ana",
		Filename:       "rbc_s1_v2.pdf",
		DeclaredMIME:   "application/pdf",
		FileBytes:      pdfV2,
	})
	if err != nil || !replaced2 || ver2.VersionNumber != 2 || cert2.Status != CertStatusExpiring {
		t.Fatalf("substituição v2 inesperada: cert=%+v ver=%+v rep=%v err=%v", cert2, ver2, replaced2, err)
	}

	// Ambas as versões devem existir no histórico imutável e ser legíveis no disco
	versions, err := GetCertificateVersions(db, cert1.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("esperava 2 versões preservadas, obteve %d (err=%v)", len(versions), err)
	}
	if _, loadedV1, err := LoadCertificateFileVerified(db, certDir, cert1.ID, 1); err != nil || !bytes.Equal(loadedV1, pdfV1) {
		t.Fatalf("versão 1 deveria continuar intacta no disco: err=%v", err)
	}

	// Trigger SQLite deve proibir DELETE em certificate_versions e certificates
	if _, err := db.Exec("DELETE FROM certificate_versions WHERE id = ?", ver1.ID); err == nil {
		t.Fatalf("DELETE em certificate_versions deveria ser bloqueado pelo trigger imutável")
	}
	if _, err := db.Exec("DELETE FROM certificates WHERE id = ?", cert1.ID); err == nil {
		t.Fatalf("DELETE em certificates deveria ser bloqueado pelo trigger imutável")
	}

	// Adulteração em disco deve ser detectada pela verificação SHA-256
	v2Path := filepath.Join(certDir, filepath.FromSlash(ver2.StorageRelPath))
	if err := os.WriteFile(v2Path, buildValidTestPDF("ADULTERADO"), 0600); err != nil {
		t.Fatalf("falha ao simular adulteração: %v", err)
	}
	if _, _, err := LoadCertificateFileVerified(db, certDir, cert1.ID, 2); err == nil || !strings.Contains(err.Error(), "VIOLAÇÃO DE INTEGRIDADE") {
		t.Fatalf("esperava erro de violação de integridade SHA-256, obteve: %v", err)
	}
}
