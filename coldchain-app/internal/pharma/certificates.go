package pharma

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	MaxCertificateSizeBytes = 10 * 1024 * 1024 // 10 MB
	MinCertificateSizeBytes = 64

	CertEntitySensor   = "SENSOR"
	CertEntityChamber  = "CHAMBER"
	CertEntityProduct  = "PRODUCT"
	CertEntityFacility = "FACILITY"

	CertDocCalibration   = "CALIBRACAO_RBC"
	CertDocQualification = "QUALIFICACAO_TERMICA"
	CertDocAnvisaReg     = "REGISTRO_ANVISA"
	CertDocSanitaryLic   = "LICENCA_SANITARIA"

	CertStatusValid    = "VALIDO"
	CertStatusExpiring = "PROXIMO_VENCIMENTO"
	CertStatusExpired  = "VENCIDO"
	CertStatusPending  = "PENDENTE"
)

var safeFilenameRegex = regexp.MustCompile(`[^a-zA-Z0-9._\- ()]+`)

// Certificate representa um certificado regulatório gerenciado com versionamento imutável.
type Certificate struct {
	ID               string `json:"id"`
	EntityType       string `json:"entity_type"`
	EntityID         string `json:"entity_id"`
	EntityLabel      string `json:"entity_label"`
	DocType          string `json:"doc_type"`
	DocumentNumber   string `json:"document_number"`
	Issuer           string `json:"issuer"`
	IssuedAt         string `json:"issued_at"`
	ValidUntil       string `json:"valid_until"`
	Notes            string `json:"notes"`
	CurrentVersion   int    `json:"current_version"`
	CurrentVersionID int64  `json:"current_version_id"`
	OriginalFilename string `json:"original_filename"`
	FileSizeBytes    int64  `json:"file_size_bytes"`
	SHA256Hash       string `json:"sha256_hash"`
	UploadedBy       string `json:"uploaded_by"`
	UploadedByName   string `json:"uploaded_by_name"`
	UploadedAt       string `json:"uploaded_at"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	Status           string `json:"status"`        // VALIDO, PROXIMO_VENCIMENTO, VENCIDO, PENDENTE
	StatusLabel      string `json:"status_label"`  // Texto amigável
	DaysRemaining    int    `json:"days_remaining"`
	VersionsCount    int    `json:"versions_count"`
}

// CertificateVersion representa uma versão imutável preservada na trilha de auditoria.
type CertificateVersion struct {
	ID               int64  `json:"id"`
	CertificateID    string `json:"certificate_id"`
	VersionNumber    int    `json:"version_number"`
	OriginalFilename string `json:"original_filename"`
	StorageRelPath   string `json:"storage_rel_path"`
	MimeType         string `json:"mime_type"`
	FileSizeBytes    int64  `json:"file_size_bytes"`
	SHA256Hash       string `json:"sha256_hash"`
	DocumentNumber   string `json:"document_number"`
	Issuer           string `json:"issuer"`
	IssuedAt         string `json:"issued_at"`
	ValidUntil       string `json:"valid_until"`
	UploadedBy       string `json:"uploaded_by"`
	UploadedByName   string `json:"uploaded_by_name"`
	UploadedAt       string `json:"uploaded_at"`
	ReplaceReason    string `json:"replace_reason"`
	IsCurrent        bool   `json:"is_current"`
	LedgerEventID    int64  `json:"ledger_event_id"`
}

// PendingCertificateItem representa uma entidade ativa do sistema que ainda não possui PDF anexado.
type PendingCertificateItem struct {
	EntityType        string `json:"entity_type"`
	EntityID          string `json:"entity_id"`
	EntityLabel       string `json:"entity_label"`
	RecommendedDocType string `json:"recommended_doc_type"`
	SuggestedNumber   string `json:"suggested_number"`
	SuggestedIssuer   string `json:"suggested_issuer"`
	SuggestedIssuedAt string `json:"suggested_issued_at"`
	SuggestedValidUntil string `json:"suggested_valid_until"`
	Status            string `json:"status"`
}

// CertificateSummary agrega a situação documental em tempo real para o painel e KPIs.
type CertificateSummary struct {
	Total       int `json:"total"`
	Valid       int `json:"valid"`
	Expiring    int `json:"expiring"`
	Expired     int `json:"expired"`
	Pending     int `json:"pending"`
}

// CertificateUploadInput reúne os metadados e bytes já lidos para criação ou substituição de certificado.
type CertificateUploadInput struct {
	CertificateID  string // Preenchido em substituições diretas
	EntityType     string
	EntityID       string
	EntityLabel    string
	DocType        string
	DocumentNumber string
	Issuer         string
	IssuedAt       string
	ValidUntil     string
	Notes          string
	ReplaceReason  string
	UploadedBy     string
	UploadedByName string
	Filename       string
	DeclaredMIME   string
	FileBytes      []byte
	LedgerEventID  int64
}

// ValidatedPDF contém o resultado da inspeção rigorosa de um PDF enviado.
type ValidatedPDF struct {
	SanitizedFilename string
	DetectedMIME      string
	SizeBytes         int64
	SHA256Hex         string
}

// InitCertificatesSchema cria as tabelas e triggers imutáveis de certificados regulatórios.
func InitCertificatesSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS certificates (
		id TEXT PRIMARY KEY,
		entity_type TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		entity_label TEXT NOT NULL,
		doc_type TEXT NOT NULL,
		document_number TEXT NOT NULL,
		issuer TEXT NOT NULL,
		issued_at TEXT NOT NULL,
		valid_until TEXT NOT NULL,
		notes TEXT DEFAULT '',
		current_version INTEGER NOT NULL DEFAULT 1,
		current_version_id INTEGER NOT NULL DEFAULT 0,
		created_by TEXT NOT NULL,
		created_by_name TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_certificates_entity ON certificates (entity_type, entity_id);

	CREATE TABLE IF NOT EXISTS certificate_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		certificate_id TEXT NOT NULL REFERENCES certificates(id),
		version_number INTEGER NOT NULL,
		original_filename TEXT NOT NULL,
		storage_rel_path TEXT NOT NULL,
		mime_type TEXT NOT NULL,
		file_size_bytes INTEGER NOT NULL,
		sha256_hash TEXT NOT NULL,
		document_number TEXT NOT NULL,
		issuer TEXT NOT NULL,
		issued_at TEXT NOT NULL,
		valid_until TEXT NOT NULL,
		uploaded_by TEXT NOT NULL,
		uploaded_by_name TEXT NOT NULL,
		uploaded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		replace_reason TEXT DEFAULT '',
		is_current INTEGER NOT NULL DEFAULT 1,
		ledger_event_id INTEGER DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_cert_versions_cert ON certificate_versions (certificate_id, version_number);

	CREATE TRIGGER IF NOT EXISTS trg_certificate_versions_no_delete
	BEFORE DELETE ON certificate_versions
	BEGIN
		SELECT RAISE(ABORT, 'Segurança: Versões de certificados regulatórios são imutáveis e não podem ser excluídas.');
	END;

	CREATE TRIGGER IF NOT EXISTS trg_certificates_no_delete
	BEFORE DELETE ON certificates
	BEGIN
		SELECT RAISE(ABORT, 'Segurança: Registros de certificados regulatórios não podem ser excluídos silenciosamente.');
	END;
	`
	_, err := db.Exec(schema)
	return err
}

// ValidatePDFUpload valida rigorosamente extensão .pdf, MIME type, cabeçalho %PDF-, estrutura %%EOF e limite de tamanho.
func ValidatePDFUpload(filename, declaredMIME string, data []byte) (*ValidatedPDF, error) {
	base := filepath.Base(strings.TrimSpace(filename))
	if base == "" || base == "." || base == "/" || base == `\` {
		return nil, errors.New("nome do arquivo PDF inválido")
	}
	if !strings.EqualFold(filepath.Ext(base), ".pdf") {
		return nil, fmt.Errorf("extensão inválida '%s': apenas arquivos .pdf emitidos pelo órgão regulador são aceitos", filepath.Ext(base))
	}

	// Verifica limite mínimo e máximo de tamanho
	size := int64(len(data))
	if size < MinCertificateSizeBytes {
		return nil, errors.New("arquivo PDF vazio ou corrompido (tamanho abaixo do mínimo estrutural)")
	}
	if size > MaxCertificateSizeBytes {
		return nil, fmt.Errorf("arquivo excede o limite máximo permitido de %d MB (recebido %.2f MB)", MaxCertificateSizeBytes/(1024*1024), float64(size)/(1024*1024))
	}

	// Verifica MIME declarado no cabeçalho multipart (rejeita outros tipos explícitos como HTML/JS/EXE/PNG)
	cleanDeclared := strings.ToLower(strings.TrimSpace(strings.Split(declaredMIME, ";")[0]))
	if cleanDeclared != "" && cleanDeclared != "application/pdf" {
		return nil, fmt.Errorf("MIME type rejeitado ('%s'): esperado estritamente 'application/pdf'", cleanDeclared)
	}

	// Verifica assinatura de cabeçalho mágica %PDF-1.x ou %PDF-2.x
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, errors.New("assinatura binária inválida: o arquivo não inicia com o cabeçalho padrão '%PDF-'")
	}
	if len(data) < 8 || (data[5] != '1' && data[5] != '2') || data[6] != '.' || data[7] < '0' || data[7] > '9' {
		return nil, errors.New("cabeçalho de versão PDF inválido (esperado %PDF-1.x ou %PDF-2.x)")
	}

	// Detecção real de MIME pelos bytes (http.DetectContentType verifica assinatura binária)
	headLen := len(data)
	if headLen > 512 {
		headLen = 512
	}
	detectedMIME := http.DetectContentType(data[:headLen])
	if detectedMIME != "application/pdf" {
		return nil, fmt.Errorf("detecção binária de MIME falhou ('%s'): o conteúdo não corresponde a um PDF válido", detectedMIME)
	}

	// Verifica marcador de fim de arquivo PDF (%%EOF) nos últimos 2048 bytes
	tailStart := len(data) - 2048
	if tailStart < 0 {
		tailStart = 0
	}
	if !bytes.Contains(data[tailStart:], []byte("%%EOF")) {
		return nil, errors.New("arquivo PDF incompleto ou truncado: marcador estrutural '%%EOF' não encontrado")
	}

	// Sanitiza nome preservando extensão .pdf
	sanitized := safeFilenameRegex.ReplaceAllString(base, "_")
	if !strings.EqualFold(filepath.Ext(sanitized), ".pdf") {
		sanitized += ".pdf"
	}
	if len(sanitized) > 160 {
		sanitized = sanitized[:156] + ".pdf"
	}

	sum := sha256.Sum256(data)
	return &ValidatedPDF{
		SanitizedFilename: sanitized,
		DetectedMIME:      detectedMIME,
		SizeBytes:         size,
		SHA256Hex:         hex.EncodeToString(sum[:]),
	}, nil
}

// ComputeCertificateStatus calcula o status real do certificado com base na data de validade.
func ComputeCertificateStatus(validUntil string, now time.Time) (status string, label string, daysRemaining int) {
	v := strings.TrimSpace(validUntil)
	if v == "" {
		return CertStatusPending, "Pendente", 0
	}
	parsed, err := time.Parse("2006-01-02", v)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return CertStatusExpired, "Data Inválida", -1
		}
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	expiryDay := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, now.Location())
	days := int(math.Round(expiryDay.Sub(today).Hours() / 24.0))

	if days < 0 {
		return CertStatusExpired, "Vencido", days
	}
	if days <= 30 {
		return CertStatusExpiring, "Próximo do Vencimento", days
	}
	return CertStatusValid, "Válido", days
}

func defaultDocTypeForEntity(entityType string) string {
	switch entityType {
	case CertEntitySensor:
		return CertDocCalibration
	case CertEntityChamber:
		return CertDocQualification
	case CertEntityProduct:
		return CertDocAnvisaReg
	default:
		return CertDocSanitaryLic
	}
}

func normalizeEntityType(t string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case CertEntitySensor:
		return CertEntitySensor, nil
	case CertEntityChamber:
		return CertEntityChamber, nil
	case CertEntityProduct:
		return CertEntityProduct, nil
	case CertEntityFacility:
		return CertEntityFacility, nil
	default:
		return "", fmt.Errorf("tipo de entidade inválido '%s' (permitidos: SENSOR, CHAMBER, PRODUCT, FACILITY)", t)
	}
}

func validateCertDates(issuedAt, validUntil string) error {
	if strings.TrimSpace(issuedAt) == "" || strings.TrimSpace(validUntil) == "" {
		return errors.New("data de emissão e data de validade são obrigatórias")
	}
	tIssued, err := time.Parse("2006-01-02", strings.TrimSpace(issuedAt))
	if err != nil {
		return fmt.Errorf("data de emissão inválida (use AAAA-MM-DD): %w", err)
	}
	tValid, err := time.Parse("2006-01-02", strings.TrimSpace(validUntil))
	if err != nil {
		return fmt.Errorf("data de validade inválida (use AAAA-MM-DD): %w", err)
	}
	if tValid.Before(tIssued) {
		return errors.New("a data de validade do certificado não pode ser anterior à data de emissão")
	}
	return nil
}

// resolveEntityLabel verifica se a entidade vinculada existe no banco e retorna um rótulo legível.
func resolveEntityLabel(db *sql.DB, entityType, entityID, providedLabel string) (string, error) {
	entityID = strings.TrimSpace(entityID)
	if entityID == "" {
		return "", errors.New("identificador do item/entidade vinculado é obrigatório")
	}
	switch entityType {
	case CertEntitySensor:
		var chamberID, pos string
		err := db.QueryRow("SELECT chamber_id, thermal_position FROM sensors WHERE id = ?", entityID).Scan(&chamberID, &pos)
		if err != nil {
			return "", fmt.Errorf("sensor '%s' não encontrado no parque metrológico", entityID)
		}
		return fmt.Sprintf("Sensor %s (%s — %s)", entityID, chamberID, pos), nil
	case CertEntityChamber:
		var name string
		err := db.QueryRow("SELECT name FROM chambers WHERE id = ? AND COALESCE(decommissioned, 0) = 0", entityID).Scan(&name)
		if err != nil {
			return "", fmt.Errorf("câmara '%s' não encontrada ou descomissionada", entityID)
		}
		return fmt.Sprintf("%s (%s)", name, entityID), nil
	case CertEntityProduct:
		prodID, err := strconv.ParseInt(entityID, 10, 64)
		if err != nil {
			return "", fmt.Errorf("ID de medicamento inválido '%s'", entityID)
		}
		var tradeName, reg string
		if err := db.QueryRow("SELECT trade_name, anvisa_reg FROM products WHERE id = ?", prodID).Scan(&tradeName, &reg); err != nil {
			return "", fmt.Errorf("medicamento #%d não encontrado no catálogo ANVISA", prodID)
		}
		return fmt.Sprintf("%s (ANVISA: %s)", tradeName, reg), nil
	case CertEntityFacility:
		lbl := strings.TrimSpace(providedLabel)
		if lbl == "" {
			lbl = entityID
		}
		return lbl, nil
	default:
		return providedLabel, nil
	}
}

// saveCertificatePDFAtomic salva o PDF em disco de forma atômica (tmp -> fsync -> rename) e verifica read-back.
func saveCertificatePDFAtomic(storageDir, certID string, version int, sha256Hex string, data []byte) (string, error) {
	if storageDir == "" {
		return "", errors.New("diretório de armazenamento de certificados não configurado")
	}
	relDir := certID
	fullDir := filepath.Join(storageDir, relDir)
	if err := os.MkdirAll(fullDir, 0700); err != nil {
		return "", fmt.Errorf("falha ao criar diretório de certificados: %w", err)
	}
	prefix := sha256Hex
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	fileName := fmt.Sprintf("v%d_%s.pdf", version, prefix)
	relPath := filepath.ToSlash(filepath.Join(relDir, fileName))
	finalPath := filepath.Join(fullDir, fileName)

	rBytes := make([]byte, 6)
	_, _ = rand.Read(rBytes)
	tmpPath := finalPath + ".tmp." + hex.EncodeToString(rBytes)

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", fmt.Errorf("falha ao criar arquivo temporário do PDF: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("falha ao gravar PDF em disco: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("falha ao sincronizar PDF em disco (fsync): %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("falha ao consolidar PDF em disco: %w", err)
	}

	// Leitura de confirmação de integridade SHA-256
	rb, err := os.ReadFile(finalPath)
	if err != nil {
		return "", fmt.Errorf("falha na verificação pós-gravação do PDF: %w", err)
	}
	actualSum := sha256.Sum256(rb)
	if hex.EncodeToString(actualSum[:]) != sha256Hex {
		_ = os.Remove(finalPath)
		return "", errors.New("divergência SHA-256 detectada na confirmação de gravação do PDF")
	}
	return relPath, nil
}

// generateCertID gera um identificador único legível para o certificado.
func generateCertID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("CERT-%s-%s", time.Now().Format("20060102"), strings.ToUpper(hex.EncodeToString(b)))
}

// syncEntityFromCertificate propaga os metadados do certificado ativo para a entidade vinculada.
func syncEntityFromCertificate(tx *sql.Tx, entityType, entityID, docNumber, issuer, issuedAt, validUntil string) {
	now := time.Now()
	st, _, _ := ComputeCertificateStatus(validUntil, now)
	switch entityType {
	case CertEntitySensor:
		sensorStatus := "Ativo"
		if st == CertStatusExpired {
			sensorStatus = "Vencido (Calibração Obrigatória)"
		} else if st == CertStatusExpiring {
			sensorStatus = "Atenção: Vencendo em breve"
		}
		_, _ = tx.Exec(`
			UPDATE sensors
			SET cert_number = ?, calibrated_by = ?, calibrated_at = ?, calib_valid_until = ?, status = ?
			WHERE id = ?`,
			docNumber, issuer, issuedAt, validUntil, sensorStatus, entityID)
	case CertEntityChamber:
		qualStatus := "Qualificado (DQ/IQ/OQ/PQ)"
		if st == CertStatusExpired {
			qualStatus = "Qualificação Vencida"
		} else if st == CertStatusExpiring {
			qualStatus = "Qualificação Vencendo (<30d)"
		}
		_, _ = tx.Exec(`
			UPDATE chambers
			SET qualification_status = ?, qualification_valid_until = ?
			WHERE id = ? AND COALESCE(decommissioned, 0) = 0`,
			qualStatus, validUntil, entityID)

		var existingQualID int64
		err := tx.QueryRow(`SELECT id FROM chamber_qualifications WHERE chamber_id = ? ORDER BY id DESC LIMIT 1`, entityID).Scan(&existingQualID)
		if err == nil && existingQualID > 0 {
			_, _ = tx.Exec(`
				UPDATE chamber_qualifications
				SET report_number = ?, executed_by = ?, execution_date = ?, valid_until = ?, status = ?
				WHERE id = ?`,
				docNumber, issuer, issuedAt, validUntil, qualStatus, existingQualID)
		} else {
			_, _ = tx.Exec(`
				INSERT INTO chamber_qualifications (chamber_id, stage, report_number, executed_by, execution_date, valid_until, thermal_autonomy, status)
				VALUES (?, 'DQ / IQ / OQ / PQ', ?, ?, ?, ?, 'Validada conforme Laudo PDF', ?)`,
				entityID, docNumber, issuer, issuedAt, validUntil, qualStatus)
		}
	case CertEntityProduct:
		if prodID, err := strconv.ParseInt(entityID, 10, 64); err == nil && strings.TrimSpace(docNumber) != "" {
			_, _ = tx.Exec(`UPDATE products SET anvisa_reg = ? WHERE id = ?`, docNumber, prodID)
		}
	}
}

// SaveOrReplaceCertificate cria um novo certificado ou registra uma nova versão auditável
// quando já existe um certificado para a mesma entidade/tipo (ou quando CertificateID é informado).
func SaveOrReplaceCertificate(db *sql.DB, storageDir string, in CertificateUploadInput) (*Certificate, *CertificateVersion, bool, error) {
	validated, err := ValidatePDFUpload(in.Filename, in.DeclaredMIME, in.FileBytes)
	if err != nil {
		return nil, nil, false, err
	}

	if err := validateCertDates(in.IssuedAt, in.ValidUntil); err != nil {
		return nil, nil, false, err
	}

	in.Issuer = strings.TrimSpace(in.Issuer)
	in.DocumentNumber = strings.TrimSpace(in.DocumentNumber)
	if in.Issuer == "" {
		return nil, nil, false, errors.New("órgão regulador / emissor do certificado é obrigatório")
	}
	if in.DocumentNumber == "" {
		return nil, nil, false, errors.New("número do certificado / registro é obrigatório")
	}
	if in.UploadedBy == "" {
		in.UploadedBy = "sistema"
	}
	if in.UploadedByName == "" {
		in.UploadedByName = in.UploadedBy
	}

	var existingID string
	var existingVersion int
	isReplacement := false

	if strings.TrimSpace(in.CertificateID) != "" {
		err := db.QueryRow(`SELECT id, entity_type, entity_id, entity_label, doc_type, current_version FROM certificates WHERE id = ?`, strings.TrimSpace(in.CertificateID)).
			Scan(&existingID, &in.EntityType, &in.EntityID, &in.EntityLabel, &in.DocType, &existingVersion)
		if err != nil {
			return nil, nil, false, fmt.Errorf("certificado '%s' não encontrado para substituição", in.CertificateID)
		}
		isReplacement = true
		if strings.TrimSpace(in.ReplaceReason) == "" {
			return nil, nil, false, errors.New("justificativa / motivo da substituição é obrigatória para trilha de auditoria")
		}
	} else {
		normType, err := normalizeEntityType(in.EntityType)
		if err != nil {
			return nil, nil, false, err
		}
		in.EntityType = normType
		if strings.TrimSpace(in.DocType) == "" {
			in.DocType = defaultDocTypeForEntity(in.EntityType)
		}
		resolvedLabel, err := resolveEntityLabel(db, in.EntityType, in.EntityID, in.EntityLabel)
		if err != nil {
			return nil, nil, false, err
		}
		in.EntityLabel = resolvedLabel

		// Se já existe um certificado para o mesmo sensor/câmara/produto e doc_type, versiona automaticamente (sem sobrescrever/apagar!)
		if in.EntityType != CertEntityFacility {
			err := db.QueryRow(`SELECT id, current_version FROM certificates WHERE entity_type = ? AND entity_id = ? AND doc_type = ? LIMIT 1`,
				in.EntityType, in.EntityID, in.DocType).Scan(&existingID, &existingVersion)
			if err == nil && existingID != "" {
				isReplacement = true
				if strings.TrimSpace(in.ReplaceReason) == "" {
					in.ReplaceReason = "Atualização / renovação de certificado regulatório"
				}
			}
		}
	}

	certID := existingID
	nextVersion := 1
	if isReplacement {
		nextVersion = existingVersion + 1
	} else {
		certID = generateCertID()
	}

	// 1. Grava o PDF em disco atomicamente antes de alterar o banco
	relPath, err := saveCertificatePDFAtomic(storageDir, certID, nextVersion, validated.SHA256Hex, in.FileBytes)
	if err != nil {
		return nil, nil, false, err
	}

	// 2. Persiste em transação SQLite
	tx, err := db.Begin()
	if err != nil {
		return nil, nil, false, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	nowStr := time.Now().UTC().Format("2006-01-02 15:04:05")
	if !isReplacement {
		_, err = tx.Exec(`
			INSERT INTO certificates (
				id, entity_type, entity_id, entity_label, doc_type, document_number, issuer,
				issued_at, valid_until, notes, current_version, current_version_id,
				created_by, created_by_name, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0, ?, ?, ?, ?)`,
			certID, in.EntityType, in.EntityID, in.EntityLabel, in.DocType, in.DocumentNumber, in.Issuer,
			in.IssuedAt, in.ValidUntil, strings.TrimSpace(in.Notes), in.UploadedBy, in.UploadedByName, nowStr, nowStr)
		if err != nil {
			return nil, nil, false, fmt.Errorf("erro ao criar registro de certificado: %w", err)
		}
	} else {
		_, err = tx.Exec(`UPDATE certificate_versions SET is_current = 0 WHERE certificate_id = ?`, certID)
		if err != nil {
			return nil, nil, false, fmt.Errorf("erro ao arquivar versão anterior do certificado: %w", err)
		}
	}

	res, err := tx.Exec(`
		INSERT INTO certificate_versions (
			certificate_id, version_number, original_filename, storage_rel_path, mime_type,
			file_size_bytes, sha256_hash, document_number, issuer, issued_at, valid_until,
			uploaded_by, uploaded_by_name, uploaded_at, replace_reason, is_current, ledger_event_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		certID, nextVersion, validated.SanitizedFilename, relPath, validated.DetectedMIME,
		validated.SizeBytes, validated.SHA256Hex, in.DocumentNumber, in.Issuer, in.IssuedAt, in.ValidUntil,
		in.UploadedBy, in.UploadedByName, nowStr, strings.TrimSpace(in.ReplaceReason), in.LedgerEventID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("erro ao salvar versão do certificado: %w", err)
	}
	verID, _ := res.LastInsertId()

	_, err = tx.Exec(`
		UPDATE certificates
		SET document_number = ?, issuer = ?, issued_at = ?, valid_until = ?,
		    notes = CASE WHEN ? != '' THEN ? ELSE notes END,
		    current_version = ?, current_version_id = ?, updated_at = ?
		WHERE id = ?`,
		in.DocumentNumber, in.Issuer, in.IssuedAt, in.ValidUntil,
		strings.TrimSpace(in.Notes), strings.TrimSpace(in.Notes),
		nextVersion, verID, nowStr, certID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("erro ao atualizar ponteiro de versão ativa do certificado: %w", err)
	}

	syncEntityFromCertificate(tx, in.EntityType, in.EntityID, in.DocumentNumber, in.Issuer, in.IssuedAt, in.ValidUntil)

	if err = tx.Commit(); err != nil {
		return nil, nil, false, fmt.Errorf("erro ao confirmar transação de certificado: %w", err)
	}

	cert, getErr := GetCertificateByID(db, certID)
	if getErr != nil {
		return nil, nil, isReplacement, getErr
	}
	ver := &CertificateVersion{
		ID:               verID,
		CertificateID:    certID,
		VersionNumber:    nextVersion,
		OriginalFilename: validated.SanitizedFilename,
		StorageRelPath:   relPath,
		MimeType:         validated.DetectedMIME,
		FileSizeBytes:    validated.SizeBytes,
		SHA256Hash:       validated.SHA256Hex,
		DocumentNumber:   in.DocumentNumber,
		Issuer:           in.Issuer,
		IssuedAt:         in.IssuedAt,
		ValidUntil:       in.ValidUntil,
		UploadedBy:       in.UploadedBy,
		UploadedByName:   in.UploadedByName,
		UploadedAt:       nowStr,
		ReplaceReason:    strings.TrimSpace(in.ReplaceReason),
		IsCurrent:        true,
		LedgerEventID:    in.LedgerEventID,
	}
	return cert, ver, isReplacement, nil
}

// UpdateCertificateVersionLedgerID associa o ID do bloco WORM gerado após o commit.
func UpdateCertificateVersionLedgerID(db *sql.DB, versionID int64, ledgerEventID uint64) {
	if versionID <= 0 || ledgerEventID == 0 {
		return
	}
	_, _ = db.Exec(`UPDATE certificate_versions SET ledger_event_id = ? WHERE id = ?`, int64(ledgerEventID), versionID)
}

// GetCertificateByID retorna os metadados completos e o status dinâmico de um certificado.
func GetCertificateByID(db *sql.DB, certID string) (*Certificate, error) {
	var c Certificate
	err := db.QueryRow(`
		SELECT c.id, c.entity_type, c.entity_id, c.entity_label, c.doc_type,
		       c.document_number, c.issuer, c.issued_at, c.valid_until, COALESCE(c.notes, ''),
		       c.current_version, c.current_version_id, c.created_at, c.updated_at,
		       COALESCE(v.original_filename, ''), COALESCE(v.file_size_bytes, 0), COALESCE(v.sha256_hash, ''),
		       COALESCE(v.uploaded_by, c.created_by), COALESCE(v.uploaded_by_name, c.created_by_name),
		       COALESCE(v.uploaded_at, c.created_at),
		       (SELECT COUNT(*) FROM certificate_versions cv WHERE cv.certificate_id = c.id)
		FROM certificates c
		LEFT JOIN certificate_versions v ON v.id = c.current_version_id
		WHERE c.id = ?`, certID).Scan(
		&c.ID, &c.EntityType, &c.EntityID, &c.EntityLabel, &c.DocType,
		&c.DocumentNumber, &c.Issuer, &c.IssuedAt, &c.ValidUntil, &c.Notes,
		&c.CurrentVersion, &c.CurrentVersionID, &c.CreatedAt, &c.UpdatedAt,
		&c.OriginalFilename, &c.FileSizeBytes, &c.SHA256Hash,
		&c.UploadedBy, &c.UploadedByName, &c.UploadedAt, &c.VersionsCount,
	)
	if err != nil {
		return nil, err
	}
	c.Status, c.StatusLabel, c.DaysRemaining = ComputeCertificateStatus(c.ValidUntil, time.Now())
	return &c, nil
}

// ListCertificates lista todos os certificados com filtros opcionais por tipo de entidade, ID da entidade ou status.
func ListCertificates(db *sql.DB, entityType, entityID, statusFilter string) ([]Certificate, error) {
	query := `
		SELECT c.id, c.entity_type, c.entity_id, c.entity_label, c.doc_type,
		       c.document_number, c.issuer, c.issued_at, c.valid_until, COALESCE(c.notes, ''),
		       c.current_version, c.current_version_id, c.created_at, c.updated_at,
		       COALESCE(v.original_filename, ''), COALESCE(v.file_size_bytes, 0), COALESCE(v.sha256_hash, ''),
		       COALESCE(v.uploaded_by, c.created_by), COALESCE(v.uploaded_by_name, c.created_by_name),
		       COALESCE(v.uploaded_at, c.created_at),
		       (SELECT COUNT(*) FROM certificate_versions cv WHERE cv.certificate_id = c.id)
		FROM certificates c
		LEFT JOIN certificate_versions v ON v.id = c.current_version_id
		WHERE 1=1`
	var args []any
	if strings.TrimSpace(entityType) != "" {
		query += " AND c.entity_type = ?"
		args = append(args, strings.ToUpper(strings.TrimSpace(entityType)))
	}
	if strings.TrimSpace(entityID) != "" {
		query += " AND c.entity_id = ?"
		args = append(args, strings.TrimSpace(entityID))
	}
	query += " ORDER BY c.updated_at DESC, c.id DESC"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now()
	statusUpper := strings.ToUpper(strings.TrimSpace(statusFilter))
	list := make([]Certificate, 0)
	for rows.Next() {
		var c Certificate
		if err := rows.Scan(
			&c.ID, &c.EntityType, &c.EntityID, &c.EntityLabel, &c.DocType,
			&c.DocumentNumber, &c.Issuer, &c.IssuedAt, &c.ValidUntil, &c.Notes,
			&c.CurrentVersion, &c.CurrentVersionID, &c.CreatedAt, &c.UpdatedAt,
			&c.OriginalFilename, &c.FileSizeBytes, &c.SHA256Hash,
			&c.UploadedBy, &c.UploadedByName, &c.UploadedAt, &c.VersionsCount,
		); err != nil {
			return nil, err
		}
		c.Status, c.StatusLabel, c.DaysRemaining = ComputeCertificateStatus(c.ValidUntil, now)
		if statusUpper != "" && statusUpper != "ALL" && c.Status != statusUpper {
			continue
		}
		list = append(list, c)
	}
	return list, nil
}

// GetCertificateVersions retorna o histórico completo e imutável de versões de um certificado.
func GetCertificateVersions(db *sql.DB, certID string) ([]CertificateVersion, error) {
	rows, err := db.Query(`
		SELECT id, certificate_id, version_number, original_filename, storage_rel_path,
		       mime_type, file_size_bytes, sha256_hash, document_number, issuer,
		       issued_at, valid_until, uploaded_by, uploaded_by_name, uploaded_at,
		       COALESCE(replace_reason, ''), is_current, COALESCE(ledger_event_id, 0)
		FROM certificate_versions
		WHERE certificate_id = ?
		ORDER BY version_number DESC`, certID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := make([]CertificateVersion, 0)
	for rows.Next() {
		var v CertificateVersion
		var isCur int
		if err := rows.Scan(
			&v.ID, &v.CertificateID, &v.VersionNumber, &v.OriginalFilename, &v.StorageRelPath,
			&v.MimeType, &v.FileSizeBytes, &v.SHA256Hash, &v.DocumentNumber, &v.Issuer,
			&v.IssuedAt, &v.ValidUntil, &v.UploadedBy, &v.UploadedByName, &v.UploadedAt,
			&v.ReplaceReason, &isCur, &v.LedgerEventID,
		); err != nil {
			return nil, err
		}
		v.IsCurrent = (isCur == 1)
		versions = append(versions, v)
	}
	return versions, nil
}

// LoadCertificateFileVerified lê os bytes do PDF em disco e verifica o SHA-256 contra o registrado no SQLite.
func LoadCertificateFileVerified(db *sql.DB, storageDir, certID string, versionNumber int) (*CertificateVersion, []byte, error) {
	var v CertificateVersion
	var isCur int
	var err error
	if versionNumber > 0 {
		err = db.QueryRow(`
			SELECT id, certificate_id, version_number, original_filename, storage_rel_path,
			       mime_type, file_size_bytes, sha256_hash, document_number, issuer,
			       issued_at, valid_until, uploaded_by, uploaded_by_name, uploaded_at,
			       COALESCE(replace_reason, ''), is_current, COALESCE(ledger_event_id, 0)
			FROM certificate_versions
			WHERE certificate_id = ? AND version_number = ?`, certID, versionNumber).Scan(
			&v.ID, &v.CertificateID, &v.VersionNumber, &v.OriginalFilename, &v.StorageRelPath,
			&v.MimeType, &v.FileSizeBytes, &v.SHA256Hash, &v.DocumentNumber, &v.Issuer,
			&v.IssuedAt, &v.ValidUntil, &v.UploadedBy, &v.UploadedByName, &v.UploadedAt,
			&v.ReplaceReason, &isCur, &v.LedgerEventID)
	} else {
		err = db.QueryRow(`
			SELECT id, certificate_id, version_number, original_filename, storage_rel_path,
			       mime_type, file_size_bytes, sha256_hash, document_number, issuer,
			       issued_at, valid_until, uploaded_by, uploaded_by_name, uploaded_at,
			       COALESCE(replace_reason, ''), is_current, COALESCE(ledger_event_id, 0)
			FROM certificate_versions
			WHERE certificate_id = ? AND is_current = 1
			ORDER BY version_number DESC LIMIT 1`, certID).Scan(
			&v.ID, &v.CertificateID, &v.VersionNumber, &v.OriginalFilename, &v.StorageRelPath,
			&v.MimeType, &v.FileSizeBytes, &v.SHA256Hash, &v.DocumentNumber, &v.Issuer,
			&v.IssuedAt, &v.ValidUntil, &v.UploadedBy, &v.UploadedByName, &v.UploadedAt,
			&v.ReplaceReason, &isCur, &v.LedgerEventID)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("versão do certificado não encontrada: %w", err)
	}
	v.IsCurrent = (isCur == 1)

	cleanRel := filepath.Clean(filepath.FromSlash(v.StorageRelPath))
	if strings.HasPrefix(cleanRel, "..") || filepath.IsAbs(cleanRel) {
		return nil, nil, errors.New("caminho de armazenamento inválido")
	}
	fullPath := filepath.Join(storageDir, cleanRel)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, nil, fmt.Errorf("arquivo PDF não encontrado no armazenamento persistente: %w", err)
	}
	sum := sha256.Sum256(data)
	actualHex := hex.EncodeToString(sum[:])
	if actualHex != strings.ToLower(v.SHA256Hash) {
		return nil, nil, fmt.Errorf("VIOLAÇÃO DE INTEGRIDADE: SHA-256 do PDF em disco (%s) diverge do registro auditado (%s)", actualHex[:16], v.SHA256Hash[:16])
	}
	return &v, data, nil
}

// GetPendingCertificateItems lista sensores, câmaras e medicamentos ativos que ainda não possuem PDF anexado.
func GetPendingCertificateItems(db *sql.DB) ([]PendingCertificateItem, error) {
	items := make([]PendingCertificateItem, 0)

	// 1. Sensores sem PDF de calibração RBC
	sRows, err := db.Query(`
		SELECT s.id, s.chamber_id, s.thermal_position, s.cert_number, s.calibrated_by, s.calibrated_at, s.calib_valid_until
		FROM sensors s
		LEFT JOIN certificates c ON c.entity_type = 'SENSOR' AND c.entity_id = s.id
		WHERE c.id IS NULL
		ORDER BY s.id ASC`)
	if err == nil {
		defer sRows.Close()
		for sRows.Next() {
			var id, chID, pos, certNum, lab, cat, cval string
			if err := sRows.Scan(&id, &chID, &pos, &certNum, &lab, &cat, &cval); err == nil {
				items = append(items, PendingCertificateItem{
					EntityType:          CertEntitySensor,
					EntityID:            id,
					EntityLabel:         fmt.Sprintf("Sensor %s (%s — %s)", id, chID, pos),
					RecommendedDocType:  CertDocCalibration,
					SuggestedNumber:     certNum,
					SuggestedIssuer:     lab,
					SuggestedIssuedAt:   cat,
					SuggestedValidUntil: cval,
					Status:              CertStatusPending,
				})
			}
		}
	}

	// 2. Câmaras ativas sem PDF de qualificação térmica
	cRows, err := db.Query(`
		SELECT ch.id, ch.name, COALESCE(ch.qualification_valid_until, '')
		FROM chambers ch
		LEFT JOIN certificates c ON c.entity_type = 'CHAMBER' AND c.entity_id = ch.id
		WHERE COALESCE(ch.decommissioned, 0) = 0 AND c.id IS NULL
		ORDER BY ch.id ASC`)
	if err == nil {
		defer cRows.Close()
		for cRows.Next() {
			var id, name, qval string
			if err := cRows.Scan(&id, &name, &qval); err == nil {
				items = append(items, PendingCertificateItem{
					EntityType:          CertEntityChamber,
					EntityID:            id,
					EntityLabel:         fmt.Sprintf("%s (%s)", name, id),
					RecommendedDocType:  CertDocQualification,
					SuggestedNumber:     "QUAL-DQ/IQ/OQ/PQ-" + id,
					SuggestedIssuer:     "Engenharia Clínica / Validação Térmica",
					SuggestedIssuedAt:   time.Now().Format("2006-01-02"),
					SuggestedValidUntil: qval,
					Status:              CertStatusPending,
				})
			}
		}
	}

	// 3. Medicamentos no catálogo sem PDF de registro ANVISA
	pRows, err := db.Query(`
		SELECT p.id, p.trade_name, p.anvisa_reg, p.manufacturer
		FROM products p
		LEFT JOIN certificates c ON c.entity_type = 'PRODUCT' AND c.entity_id = CAST(p.id AS TEXT)
		WHERE c.id IS NULL
		ORDER BY p.trade_name ASC`)
	if err == nil {
		defer pRows.Close()
		for pRows.Next() {
			var id int64
			var name, reg, mfg string
			if err := pRows.Scan(&id, &name, &reg, &mfg); err == nil {
				items = append(items, PendingCertificateItem{
					EntityType:          CertEntityProduct,
					EntityID:            strconv.FormatInt(id, 10),
					EntityLabel:         fmt.Sprintf("%s (Reg. %s)", name, reg),
					RecommendedDocType:  CertDocAnvisaReg,
					SuggestedNumber:     reg,
					SuggestedIssuer:     "ANVISA — Agência Nacional de Vigilância Sanitária",
					SuggestedIssuedAt:   time.Now().Format("2006-01-02"),
					SuggestedValidUntil: time.Now().AddDate(5, 0, 0).Format("2006-01-02"),
					Status:              CertStatusPending,
				})
			}
		}
	}

	return items, nil
}

// GetCertificateSummary calcula os totais reais de certificados válidos, vencendo, vencidos e pendentes.
func GetCertificateSummary(db *sql.DB) (CertificateSummary, error) {
	certs, err := ListCertificates(db, "", "", "")
	if err != nil {
		return CertificateSummary{}, err
	}
	pending, err := GetPendingCertificateItems(db)
	if err != nil {
		return CertificateSummary{}, err
	}
	sum := CertificateSummary{
		Total:   len(certs),
		Pending: len(pending),
	}
	for _, c := range certs {
		switch c.Status {
		case CertStatusValid:
			sum.Valid++
		case CertStatusExpiring:
			sum.Expiring++
		case CertStatusExpired:
			sum.Expired++
		}
	}
	return sum, nil
}

// GetEntityCertificatesMap retorna mapa "ENTITY_TYPE:ENTITY_ID" -> Certificate mais recente.
func GetEntityCertificatesMap(db *sql.DB) map[string]Certificate {
	result := make(map[string]Certificate)
	list, err := ListCertificates(db, "", "", "")
	if err != nil {
		return result
	}
	for _, c := range list {
		key := c.EntityType + ":" + c.EntityID
		if _, exists := result[key]; !exists {
			result[key] = c
		}
	}
	return result
}
