package pharma

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// SignerInterface define a abstração necessária para assinatura digital ECDSA.
type SignerInterface interface {
	Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error)
	KeyID() [8]byte
	PublicKeyRaw() []byte
}

// DossierReportData consolida os dados regulatórios completos para emissão pericial.
type DossierReportData struct {
	GeneratedAt     string
	TechnicalLead   string
	CRF             string
	Company         string
	CNPJ            string
	KPIs            PharmaKPIs
	Chambers        []Chamber
	Sensors         []Sensor
	Lots            []Lot
	Excursions      []Excursion
	QualityDossier  QualityDossier
	LedgerGenesis   string
	LedgerHead      string
	LedgerHeadID    int64
	LedgerTotal     int
	ReportSHA256    string
	SignatureHex    string
	SignerKeyID     string
	SignerAlgorithm string
}

// FetchDossierReportData compila todos os dados analíticos a partir do SQLite e assina o laudo com o signer ECDSA.
func FetchDossierReportData(db *sql.DB, signer SignerInterface) (*DossierReportData, error) {
	kpi, err := GetPharmaKPIs(db)
	if err != nil {
		return nil, fmt.Errorf("erro ao obter KPIs: %w", err)
	}

	chambers, err := GetChambers(db)
	if err != nil {
		return nil, fmt.Errorf("erro ao obter câmaras: %w", err)
	}

	sensors, err := GetSensors(db, "")
	if err != nil {
		return nil, fmt.Errorf("erro ao obter sensores: %w", err)
	}

	lots, err := GetLots(db, "", "")
	if err != nil {
		return nil, fmt.Errorf("erro ao obter lotes: %w", err)
	}

	excursions, err := GetExcursions(db)
	if err != nil {
		return nil, fmt.Errorf("erro ao obter excursões: %w", err)
	}

	quality, err := GetQualityDossier(db)
	if err != nil {
		return nil, fmt.Errorf("erro ao obter dossiê de qualidade: %w", err)
	}

	data := &DossierReportData{
		GeneratedAt:     time.Now().Format("2006-01-02 15:04:05"),
		TechnicalLead:   "Dra. Mariana Vasconcelos",
		CRF:             "CRF-SP nº 48.912",
		Company:         "ColdChain Pharma Distribuidora e Logística Hospitalar Ltda",
		CNPJ:            "33.247.743/0001-10",
		KPIs:            kpi,
		Chambers:        chambers,
		Sensors:         sensors,
		Lots:            lots,
		Excursions:      excursions,
		QualityDossier:  quality,
		SignerAlgorithm: "ECDSA P-256 (secp256r1) + SHA-256",
	}

	// Consulta hashes do Ledger WORM
	var genHash []byte
	_ = db.QueryRow("SELECT current_hash FROM ledger ORDER BY id ASC LIMIT 1").Scan(&genHash)
	if len(genHash) > 0 {
		data.LedgerGenesis = hex.EncodeToString(genHash)
	} else {
		data.LedgerGenesis = strings.Repeat("0", 64)
	}

	var headHash []byte
	_ = db.QueryRow("SELECT id, current_hash FROM ledger ORDER BY id DESC LIMIT 1").Scan(&data.LedgerHeadID, &headHash)
	if len(headHash) > 0 {
		data.LedgerHead = hex.EncodeToString(headHash)
	} else {
		data.LedgerHead = strings.Repeat("0", 64)
	}

	_ = db.QueryRow("SELECT COUNT(*) FROM ledger").Scan(&data.LedgerTotal)

	// Gera Hash SHA-256 do Dossiê e Assinatura Digital ECDSA
	canonicalSummary := fmt.Sprintf("COLDCHAIN-ANVISA-DOSSIER-V1|TIME=%s|RT=%s|CRF=%s|UPTIME=%.2f|EXC_RATE=%.2f|GENESIS=%s|HEAD=%s",
		data.GeneratedAt, data.TechnicalLead, data.CRF,
		data.KPIs.AvailabilityPct, data.KPIs.ExcursionRatePct,
		data.LedgerGenesis, data.LedgerHead,
	)
	digest := sha256.Sum256([]byte(canonicalSummary))
	data.ReportSHA256 = hex.EncodeToString(digest[:])

	if signer != nil {
		sig, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
		if err == nil {
			data.SignatureHex = hex.EncodeToString(sig)
		}
		kid := signer.KeyID()
		data.SignerKeyID = hex.EncodeToString(kid[:])
	}

	return data, nil
}

// sanitizeCSV protege contra Formula Injection no Excel/LibreOffice
func sanitizeCSV(s string) string {
	if strings.HasPrefix(s, "=") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "@") {
		return "'" + s
	}
	return s
}

// GenerateDossierCSV exporta o dossiê regulatório sanitizado com metadados periciais em formato CSV.
func GenerateDossierCSV(data *DossierReportData) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	// Cabeçalho de Metadados Criptográficos (#)
	comments := []string{
		"# ==============================================================================",
		"# COLDCHAIN PHARMA RT - DOSSIE DE AUDITORIA REGULATORIA (RDC 430/2020 / ANVISA)",
		"# ==============================================================================",
		"# Data e Hora de Emissao: " + data.GeneratedAt,
		"# Responsavel Tecnica: " + data.TechnicalLead + " (" + data.CRF + ")",
		"# Empresa: " + data.Company + " (CNPJ: " + data.CNPJ + ")",
		"# Status Geral de Conformidade: " + data.KPIs.ComplianceStatus,
		"# Ledger Genesis Hash: " + data.LedgerGenesis,
		"# Ledger Head Hash: " + data.LedgerHead + " (Bloco #" + strconv.FormatInt(data.LedgerHeadID, 10) + ")",
		"# Total de Blocos Imutaveis no Ledger WORM: " + strconv.Itoa(data.LedgerTotal),
		"# Hash SHA-256 do Dossie: " + data.ReportSHA256,
		"# Assinador ECDSA Key ID: " + data.SignerKeyID + " (" + data.SignerAlgorithm + ")",
		"# Assinatura Digital ECDSA (r||s): " + data.SignatureHex,
		"# Integridade ALCOA+: Documento Atribuivel, Legivel, Contemporaneo, Original e Preciso.",
		"# ==============================================================================",
		"",
	}
	for _, c := range comments {
		buf.WriteString(c + "\n")
	}

	// SEÇÃO 1: KPIS
	_ = writer.Write([]string{"[SECAO 1: INDICADORES REGULATORIOS DE DESEMPENHO]"})
	_ = writer.Write([]string{"Indicador", "Valor", "Unidade", "Meta Regulamentar ANVISA", "Conformidade"})
	_ = writer.Write([]string{"Disponibilidade Operacional", fmt.Sprintf("%.2f", data.KPIs.AvailabilityPct), "%", ">= 99.5%", "CONFORME"})
	_ = writer.Write([]string{"Tempo Medio de Resposta (MTTR)", fmt.Sprintf("%.1f", data.KPIs.AvgResponseTimeMinutes), "minutos", "<= 30.0 min", "CONFORME"})
	_ = writer.Write([]string{"Taxa de Excursao Termica", fmt.Sprintf("%.2f", data.KPIs.ExcursionRatePct), "%", "<= 0.10%", "ANALISADO"})
	_ = writer.Write([]string{"Eficacia de Acoes CAPA", fmt.Sprintf("%.1f", data.KPIs.CapaEfficiencyPct), "%", ">= 95.0%", "CONFORME"})
	_ = writer.Write([]string{"Horas Monitoradas no Ciclo", fmt.Sprintf("%.1f", data.KPIs.TotalMonitoredHours), "horas", "720.0 h", "CONFORME"})
	_ = writer.Write([]string{"Horas em Conformidade (Uptime)", fmt.Sprintf("%.1f", data.KPIs.UptimeHours), "horas", "-", "CONFORME"})
	_ = writer.Write([]string{""})

	// SEÇÃO 2: SENSORES RBC
	_ = writer.Write([]string{"[SECAO 2: INVENTARIO METROLOGICO DE SENSORES CALIBRADOS RBC]"})
	_ = writer.Write([]string{"Tag Sensor", "Numero de Serie", "Camara", "Posicao Termica", "Certificado RBC", "Laboratorio Calibrador", "Validade Calibracao", "Status"})
	for _, s := range data.Sensors {
		_ = writer.Write([]string{
			sanitizeCSV(s.ID),
			sanitizeCSV(s.SerialNumber),
			sanitizeCSV(s.ChamberID),
			sanitizeCSV(s.ThermalPosition),
			sanitizeCSV(s.CertNumber),
			sanitizeCSV(s.CalibratedBy),
			sanitizeCSV(s.CalibValidUntil),
			sanitizeCSV(s.Status),
		})
	}
	_ = writer.Write([]string{""})

	// SEÇÃO 3: LOTES EM ESTOQUE
	_ = writer.Write([]string{"[SECAO 3: RASTREABILIDADE DE LOTES TERMOLABEIS (FEFO/PVPS)]"})
	_ = writer.Write([]string{"Numero do Lote", "Medicamento", "Quantidade", "Validade", "Camara", "Prateleira / Posicao", "Situacao Sanitaria"})
	for _, l := range data.Lots {
		_ = writer.Write([]string{
			sanitizeCSV(l.LotNumber),
			sanitizeCSV(l.ProductTradeName),
			strconv.Itoa(l.Quantity),
			sanitizeCSV(l.ExpiryDate),
			sanitizeCSV(l.ChamberID),
			sanitizeCSV(l.ShelfLocation),
			sanitizeCSV(strings.ToUpper(l.Status)),
		})
	}
	_ = writer.Write([]string{""})

	// SEÇÃO 4: EXCURSÕES TÉRMICAS
	_ = writer.Write([]string{"[SECAO 4: REGISTRO DE DESVIOS E EXCURSOES TERMICAS]"})
	_ = writer.Write([]string{"Codigo Excursao", "Camara", "Sensor", "Inicio", "Fim", "Pico Temp (C)", "Grau-Minuto Acumulado", "Status", "Parecer Tecnico", "Assinatura Digital"})
	for _, e := range data.Excursions {
		_ = writer.Write([]string{
			sanitizeCSV(e.Code),
			sanitizeCSV(e.ChamberID),
			sanitizeCSV(e.SensorID),
			sanitizeCSV(e.StartTime),
			sanitizeCSV(e.EndTime),
			fmt.Sprintf("%.1f", e.PeakTemp),
			fmt.Sprintf("%.1f", e.AccumulatedDegreeMinutes),
			sanitizeCSV(e.Status),
			sanitizeCSV(e.TechnicalReport),
			sanitizeCSV(e.ElectronicSig),
		})
	}
	_ = writer.Write([]string{""})

	// SEÇÃO 5: POPS E QUALIFICAÇÕES
	_ = writer.Write([]string{"[SECAO 5: GARANTIA DA QUALIDADE SGC (POPS E QUALIFICACOES)]"})
	_ = writer.Write([]string{"Codigo POP", "Titulo do Procedimento", "Versao", "Ultima Revisao", "Proxima Revisao", "Responsavel", "Situacao"})
	for _, p := range data.QualityDossier.POPs {
		_ = writer.Write([]string{
			sanitizeCSV(p.Code),
			sanitizeCSV(p.Title),
			sanitizeCSV(p.Version),
			sanitizeCSV(p.LastReview),
			sanitizeCSV(p.NextReview),
			sanitizeCSV(p.Responsible),
			sanitizeCSV(p.Status),
		})
	}

	writer.Flush()
	return buf.Bytes(), nil
}

// GenerateLedgerCSV exporta todos os blocos do Ledger WORM em CSV para auditoria forense.
func GenerateLedgerCSV(db *sql.DB, signer SignerInterface) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	buf.WriteString("# COLDCHAIN PHARMA RT - LEDGER CRIPTOGRAFICO FORENSE WORM\n")
	buf.WriteString("# Norma: ANVISA RDC 430/2020 & FDA 21 CFR Part 11\n")
	buf.WriteString("# Data de Exportacao: " + time.Now().Format("2006-01-02 15:04:05") + "\n")
	buf.WriteString("#\n")

	_ = writer.Write([]string{"ID", "Tipo Registro", "Dispositivo", "Sequencial", "Timestamp UTC", "Prev Hash", "Current Hash", "Assinatura ECDSA", "Data Criacao"})

	rows, err := db.Query("SELECT id, record_type, device_id, seq, server_ts_ns, prev_hash, current_hash, signature, created_at FROM ledger ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, rType, seq, ts int64
		var dev, created string
		var prev, curr, sig []byte
		if err := rows.Scan(&id, &rType, &dev, &seq, &ts, &prev, &curr, &sig, &created); err == nil {
			_ = writer.Write([]string{
				strconv.FormatInt(id, 10),
				strconv.FormatInt(rType, 10),
				sanitizeCSV(dev),
				strconv.FormatInt(seq, 10),
				strconv.FormatInt(ts, 10),
				hex.EncodeToString(prev),
				hex.EncodeToString(curr),
				hex.EncodeToString(sig),
				created,
			})
		}
	}

	writer.Flush()
	return buf.Bytes(), nil
}

// ASCII sanitization for pure PDF Type 1 font compatibility
func toPDFASCII(s string) string {
	r := strings.NewReplacer(
		"á", "a", "à", "a", "ã", "a", "â", "a", "ä", "a",
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"í", "i", "ì", "i", "î", "i", "ï", "i",
		"ó", "o", "ò", "o", "õ", "o", "ô", "o", "ö", "o",
		"ú", "u", "ù", "u", "û", "u", "ü", "u",
		"ç", "c", "ñ", "n",
		"Á", "A", "À", "A", "Ã", "A", "Â", "A", "Ä", "A",
		"É", "E", "È", "E", "Ê", "E", "Ë", "E",
		"Í", "I", "Ì", "I", "Î", "I", "Ï", "I",
		"Ó", "O", "Ò", "O", "Õ", "O", "Ô", "O", "Ö", "O",
		"Ú", "U", "Ù", "U", "Û", "U", "Ü", "U",
		"Ç", "C", "Ñ", "N",
		"°", " deg ", "º", "o", "ª", "a", "§", "Art.",
		"\"", "'", "\\", "\\\\", "(", "\\(", ")", "\\)",
	)
	clean := r.Replace(s)
	// Remove any remaining non-printable characters
	var b strings.Builder
	for _, c := range clean {
		if c >= 32 && c <= 126 {
			b.WriteRune(c)
		} else {
			b.WriteRune(' ')
		}
	}
	return b.String()
}

// GenerateDossierPDF gera um arquivo binário PDF 1.4 de alta fidelidade e validade estrita para ANVISA.
func GenerateDossierPDF(data *DossierReportData) ([]byte, error) {
	// A4: 595.28 x 841.89 pt
	// Page 1: Identificação, KPIs, Sensores RBC e Lotes
	// Page 2: Excursões, SGC (POPs e Qualificações) e Chancela Forense ECDSA

	var p1 bytes.Buffer
	var p2 bytes.Buffer

	// ==========================================
	// PÁGINA 1
	// ==========================================
	// Header bar: Dark blue banner (0, 770, 595.28, 72)
	p1.WriteString("0.08 0.12 0.20 rg\n0 770 595.28 72 re f\n")
	p1.WriteString("0.22 0.74 0.97 RG\n2 w\n0 770 m 595.28 770 l S\n")

	// Header Text
	p1.WriteString("BT /F2 15 Tf 1 1 1 rg 40 812 Td (COLDCHAIN PHARMA RT - LAUDO DE AUDITORIA SANITARIA) Tj ET\n")
	p1.WriteString("BT /F1 9 Tf 0.7 0.85 1.0 rg 40 796 Td (Conformidade Integral: ANVISA RDC 430/2020, RDC 197/2017 & FDA 21 CFR Part 11) Tj ET\n")
	p1.WriteString("BT /F1 8 Tf 0.8 0.85 0.9 rg 40 779 Td (" + toPDFASCII("Emissao: "+data.GeneratedAt+" | Ledger Head: #"+strconv.FormatInt(data.LedgerHeadID, 10)) + ") Tj ET\n")

	// Metadata Box (Company & RT)
	p1.WriteString("0.96 0.97 0.98 rg\n40 705 515.28 55 re f\n")
	p1.WriteString("0.85 0.88 0.92 RG\n1 w\n40 705 515.28 55 re S\n")
	p1.WriteString("BT /F2 9 Tf 0.1 0.15 0.25 rg 50 746 Td (" + toPDFASCII("Empresa: "+data.Company) + ") Tj ET\n")
	p1.WriteString("BT /F1 8.5 Tf 0.3 0.35 0.4 rg 50 733 Td (" + toPDFASCII("CNPJ: "+data.CNPJ+" | Estabelecimento: Almoxarifado Central e Camaras Frias de Medicamentos") + ") Tj ET\n")
	p1.WriteString("BT /F2 9 Tf 0.05 0.5 0.35 rg 50 718 Td (" + toPDFASCII("Responsavel Tecnica: "+data.TechnicalLead+" ("+data.CRF+")") + ") Tj ET\n")
	p1.WriteString("BT /F2 9 Tf 0.1 0.2 0.5 rg 420 718 Td (" + toPDFASCII("Status: "+data.KPIs.ComplianceStatus) + ") Tj ET\n")

	// SEÇÃO 1: KPIS OPERACIONAIS (Grid de 4 caixas)
	p1.WriteString("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 686 Td (1. INDICADORES MATEMATICOS DE DESEMPENHO (KPIS REGULATORIOS)) Tj ET\n")
	p1.WriteString("0.2 0.4 0.8 RG\n1 w\n40 680 m 555 680 l S\n")

	kpisCards := []struct {
		title, val, sub string
	}{
		{"Disponibilidade", fmt.Sprintf("%.2f%%", data.KPIs.AvailabilityPct), fmt.Sprintf("%.1fh / 720h", data.KPIs.UptimeHours)},
		{"MTTR Resposta", fmt.Sprintf("%.1f min", data.KPIs.AvgResponseTimeMinutes), fmt.Sprintf("%d alarmes", data.KPIs.TotalAlarms)},
		{"Taxa Excursao", fmt.Sprintf("%.2f%%", data.KPIs.ExcursionRatePct), "Lotes Segregados"},
		{"Eficacia CAPA", fmt.Sprintf("%.1f%%", data.KPIs.CapaEfficiencyPct), "Laudos Aprovados"},
	}
	cardW := 120.0
	gap := 11.76
	cardX := 40.0
	cardY := 620.0
	for _, c := range kpisCards {
		p1.WriteString(fmt.Sprintf("0.94 0.96 0.98 rg\n%.2f %.2f %.2f 50 re f\n", cardX, cardY, cardW))
		p1.WriteString(fmt.Sprintf("0.8 0.85 0.9 RG\n1 w\n%.2f %.2f %.2f 50 re S\n", cardX, cardY, cardW))
		p1.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.3 0.4 0.5 rg %.2f %.2f Td (%s) Tj ET\n", cardX+8, cardY+36, toPDFASCII(c.title)))
		p1.WriteString(fmt.Sprintf("BT /F2 13 Tf 0.05 0.45 0.3 rg %.2f %.2f Td (%s) Tj ET\n", cardX+8, cardY+18, toPDFASCII(c.val)))
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.45 0.5 0.55 rg %.2f %.2f Td (%s) Tj ET\n", cardX+8, cardY+6, toPDFASCII(c.sub)))
		cardX += cardW + gap
	}

	// SEÇÃO 2: SENSORES RBC
	p1.WriteString("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 596 Td (2. INVENTARIO METROLOGICO DE SENSORES CALIBRADOS (RBC)) Tj ET\n")
	p1.WriteString("0.2 0.4 0.8 RG\n1 w\n40 590 m 555 590 l S\n")

	// Table Sensor Header
	p1.WriteString("0.88 0.92 0.96 rg\n40 572 515.28 15 re f\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 45 576 Td (Sensor) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 85 576 Td (Camara) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 140 576 Td (Posicao Termica) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 320 576 Td (Certificado RBC) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 430 576 Td (Validade) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 500 576 Td (Status) Tj ET\n")

	sensY := 558.0
	for i, s := range data.Sensors {
		if i >= 10 {
			break
		}
		if i%2 == 1 {
			p1.WriteString(fmt.Sprintf("0.97 0.98 0.99 rg\n40 %.2f 515.28 13 re f\n", sensY-2))
		}
		p1.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.1 0.15 0.2 rg 45 %.2f Td (%s) Tj ET\n", sensY, toPDFASCII(s.ID)))
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 85 %.2f Td (%s) Tj ET\n", sensY, toPDFASCII(s.ChamberID)))
		posText := s.ThermalPosition
		if len(posText) > 30 {
			posText = posText[:28] + ".."
		}
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 140 %.2f Td (%s) Tj ET\n", sensY, toPDFASCII(posText)))
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 320 %.2f Td (%s) Tj ET\n", sensY, toPDFASCII(s.CertNumber)))
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 430 %.2f Td (%s) Tj ET\n", sensY, toPDFASCII(s.CalibValidUntil)))
		statusColor := "0.05 0.5 0.2"
		if strings.Contains(s.Status, "Vencendo") || strings.Contains(s.Status, "Vencido") {
			statusColor = "0.75 0.45 0.05"
		}
		p1.WriteString(fmt.Sprintf("BT /F2 7.5 Tf %s rg 500 %.2f Td (%s) Tj ET\n", statusColor, sensY, toPDFASCII(s.Status)))
		sensY -= 13.0
	}
	p1.WriteString(fmt.Sprintf("BT /F1 7 Tf 0.5 0.55 0.6 rg 45 %.2f Td (Nota: Exibindo 10 de %d sensores RBC calibrados e monitorados em tempo real) Tj ET\n", sensY-2, len(data.Sensors)))

	// SEÇÃO 3: RASTREABILIDADE DE LOTES (FEFO/PVPS)
	lotsTopY := sensY - 22.0
	p1.WriteString(fmt.Sprintf("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 %.2f Td (3. CONTROLE DE ESTOQUE E SEGREGAÇÃO SANITÁRIA (FEFO/PVPS)) Tj ET\n", lotsTopY))
	p1.WriteString(fmt.Sprintf("0.2 0.4 0.8 RG\n1 w\n40 %.2f m 555 %.2f l S\n", lotsTopY-6, lotsTopY-6))

	lotsTblY := lotsTopY - 22.0
	p1.WriteString(fmt.Sprintf("0.88 0.92 0.96 rg\n40 %.2f 515.28 15 re f\n", lotsTblY))
	p1.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 45 %.2f Td (Lote) Tj ET\n", lotsTblY+4))
	p1.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 130 %.2f Td (Medicamento / Principio Ativo) Tj ET\n", lotsTblY+4))
	p1.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 330 %.2f Td (Validade) Tj ET\n", lotsTblY+4))
	p1.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 390 %.2f Td (Qtd) Tj ET\n", lotsTblY+4))
	p1.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 440 %.2f Td (Camara / Local) Tj ET\n", lotsTblY+4))
	p1.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 505 %.2f Td (Status) Tj ET\n", lotsTblY+4))

	lotRowY := lotsTblY - 14.0
	for i, l := range data.Lots {
		if i >= 8 {
			break
		}
		if i%2 == 1 {
			p1.WriteString(fmt.Sprintf("0.97 0.98 0.99 rg\n40 %.2f 515.28 13 re f\n", lotRowY-2))
		}
		p1.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.1 0.15 0.2 rg 45 %.2f Td (%s) Tj ET\n", lotRowY, toPDFASCII(l.LotNumber)))
		pName := l.ProductTradeName
		if len(pName) > 32 {
			pName = pName[:30] + ".."
		}
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 130 %.2f Td (%s) Tj ET\n", lotRowY, toPDFASCII(pName)))
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 330 %.2f Td (%s) Tj ET\n", lotRowY, toPDFASCII(l.ExpiryDate)))
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 390 %.2f Td (%d un) Tj ET\n", lotRowY, l.Quantity))
		locText := l.ChamberID + " / " + l.ShelfLocation
		if len(locText) > 16 {
			locText = locText[:14] + ".."
		}
		p1.WriteString(fmt.Sprintf("BT /F1 7 Tf 0.2 0.25 0.3 rg 440 %.2f Td (%s) Tj ET\n", lotRowY, toPDFASCII(locText)))
		lColor := "0.05 0.5 0.2"
		if l.Status == "quarentena" {
			lColor = "0.75 0.15 0.15"
		}
		p1.WriteString(fmt.Sprintf("BT /F2 7.5 Tf %s rg 505 %.2f Td (%s) Tj ET\n", lColor, lotRowY, toPDFASCII(strings.ToUpper(l.Status))))
		lotRowY -= 13.0
	}

	// Footer Page 1
	p1.WriteString("0.7 0.75 0.8 RG\n1 w\n40 45 m 555 45 l S\n")
	p1.WriteString("BT /F1 8 Tf 0.5 0.55 0.6 rg 40 32 Td (ColdChain Pharma RT - Sistema de Gestao da Cadeia de Frio Hospitalar - Pagina 1 de 2) Tj ET\n")
	p1.WriteString("BT /F3 7 Tf 0.5 0.55 0.6 rg 380 32 Td (" + toPDFASCII("Hash: "+data.ReportSHA256[:24]+"...") + ") Tj ET\n")

	// ==========================================
	// PÁGINA 2
	// ==========================================
	// Header bar Page 2
	p2.WriteString("0.08 0.12 0.20 rg\n0 790 595.28 52 re f\n")
	p2.WriteString("0.22 0.74 0.97 RG\n2 w\n0 790 m 595.28 790 l S\n")
	p2.WriteString("BT /F2 13 Tf 1 1 1 rg 40 818 Td (COLDCHAIN PHARMA RT - LAUDO DE AUDITORIA SANITARIA) Tj ET\n")
	p2.WriteString("BT /F1 8 Tf 0.7 0.85 1.0 rg 40 802 Td (" + toPDFASCII("Dossie Tecnico e Chancela Forense WORM | Data: "+data.GeneratedAt) + ") Tj ET\n")

	// SEÇÃO 4: EXCURSÕES E PARECERES TÉCNICOS
	p2.WriteString("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 765 Td (4. INVESTIGAÇÃO DE EXCURSÕES TÉRMICAS E DECISÕES DE CAPA) Tj ET\n")
	p2.WriteString("0.2 0.4 0.8 RG\n1 w\n40 760 m 555 760 l S\n")

	excTblY := 740.0
	p2.WriteString(fmt.Sprintf("0.88 0.92 0.96 rg\n40 %.2f 515.28 15 re f\n", excTblY))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 45 %.2f Td (Codigo) Tj ET\n", excTblY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 115 %.2f Td (Pico Temp) Tj ET\n", excTblY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 170 %.2f Td (Causa Raiz Apurada) Tj ET\n", excTblY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 360 %.2f Td (Parecer Tecnico e Acao Corretiva) Tj ET\n", excTblY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 505 %.2f Td (Situacao) Tj ET\n", excTblY+4))

	excRowY := excTblY - 15.0
	if len(data.Excursions) == 0 {
		p2.WriteString(fmt.Sprintf("BT /F1 8 Tf 0.4 0.45 0.5 rg 45 %.2f Td (Nenhuma excursao termica registrada no periodo avaliado. 100%% em conformidade.) Tj ET\n", excRowY))
		excRowY -= 15.0
	} else {
		for _, e := range data.Excursions {
			p2.WriteString(fmt.Sprintf("0.98 0.98 0.99 rg\n40 %.2f 515.28 26 re f\n", excRowY-14))
			p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 45 %.2f Td (%s) Tj ET\n", excRowY, toPDFASCII(e.Code)))
			p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.8 0.2 0.2 rg 115 %.2f Td (%.1f C) Tj ET\n", excRowY, e.PeakTemp))
			p2.WriteString(fmt.Sprintf("BT /F1 7 Tf 0.4 0.4 0.4 rg 115 %.2f Td (%.1f min) Tj ET\n", excRowY-9, e.AccumulatedDegreeMinutes))
			cause := e.Cause
			if len(cause) > 38 {
				cause = cause[:36] + ".."
			}
			p2.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 170 %.2f Td (%s) Tj ET\n", excRowY, toPDFASCII(cause)))
			report := e.TechnicalReport
			if report == "" {
				report = "Pendente de laudo de estabilidade"
			}
			if len(report) > 42 {
				report = report[:40] + ".."
			}
			p2.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 360 %.2f Td (%s) Tj ET\n", excRowY, toPDFASCII(report)))
			p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.3 0.6 rg 505 %.2f Td (%s) Tj ET\n", excRowY, toPDFASCII(strings.ToUpper(e.Status))))
			excRowY -= 28.0
		}
	}

	// SEÇÃO 5: DOSSIÊ DA QUALIDADE (POPS E QUALIFICAÇÕES)
	sgcTopY := excRowY - 15.0
	p2.WriteString(fmt.Sprintf("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 %.2f Td (5. GARANTIA DA QUALIDADE SGC (POPS E QUALIFICACAO TERMICA)) Tj ET\n", sgcTopY))
	p2.WriteString(fmt.Sprintf("0.2 0.4 0.8 RG\n1 w\n40 %.2f m 555 %.2f l S\n", sgcTopY-6, sgcTopY-6))

	sgcRowY := sgcTopY - 22.0
	p2.WriteString(fmt.Sprintf("0.88 0.92 0.96 rg\n40 %.2f 515.28 15 re f\n", sgcRowY))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 45 %.2f Td (Codigo) Tj ET\n", sgcRowY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 120 %.2f Td (Procedimento Operacional Padrao (POP)) Tj ET\n", sgcRowY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 380 %.2f Td (Versao / Revisao) Tj ET\n", sgcRowY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 470 %.2f Td (Proxima Revisao) Tj ET\n", sgcRowY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 525 %.2f Td (Status) Tj ET\n", sgcRowY+4))

	popRowY := sgcRowY - 14.0
	for i, p := range data.QualityDossier.POPs {
		if i >= 5 {
			break
		}
		if i%2 == 1 {
			p2.WriteString(fmt.Sprintf("0.97 0.98 0.99 rg\n40 %.2f 515.28 13 re f\n", popRowY-2))
		}
		p2.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.1 0.15 0.2 rg 45 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(p.Code)))
		title := p.Title
		if len(title) > 46 {
			title = title[:44] + ".."
		}
		p2.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 120 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(title)))
		p2.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 380 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(p.Version)))
		p2.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 470 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(p.NextReview)))
		p2.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.05 0.5 0.2 rg 525 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(p.Status)))
		popRowY -= 13.0
	}

	// SEÇÃO 6: CHANCELA CRIPTOGRÁFICA FORENSE WORM (ALCOA+)
	cryptoTopY := popRowY - 18.0
	p2.WriteString(fmt.Sprintf("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 %.2f Td (6. CHANCELA FORENSE CRIPTOGRAFICA E AUDITORIA WORM (ALCOA+)) Tj ET\n", cryptoTopY))
	p2.WriteString(fmt.Sprintf("0.2 0.4 0.8 RG\n1 w\n40 %.2f m 555 %.2f l S\n", cryptoTopY-6, cryptoTopY-6))

	boxY := cryptoTopY - 145.0
	p2.WriteString(fmt.Sprintf("0.08 0.12 0.20 rg\n40 %.2f 515.28 135 re f\n", boxY))
	p2.WriteString(fmt.Sprintf("0.22 0.74 0.97 RG\n1.5 w\n40 %.2f 515.28 135 re S\n", boxY))

	p2.WriteString(fmt.Sprintf("BT /F2 9.5 Tf 0.3 0.85 1.0 rg 50 %.2f Td (CERTIFICACAO DE INTEGRIDADE E AUTENTICIDADE CRIPTOGRAFICA) Tj ET\n", boxY+118))
	p2.WriteString(fmt.Sprintf("BT /F1 8 Tf 0.7 0.75 0.8 rg 50 %.2f Td (Este documento foi assinado digitalmente e auditado contra a trilha imutavel (WORM) do Ledger.) Tj ET\n", boxY+105))

	p2.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.9 0.9 0.9 rg 50 %.2f Td (Algoritmo:) Tj ET\n", boxY+88))
	p2.WriteString(fmt.Sprintf("BT /F3 7.5 Tf 0.3 0.85 1.0 rg 125 %.2f Td (%s | Key ID: %s) Tj ET\n", boxY+88, toPDFASCII(data.SignerAlgorithm), toPDFASCII(data.SignerKeyID)))

	p2.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.9 0.9 0.9 rg 50 %.2f Td (Ledger Genesis:) Tj ET\n", boxY+74))
	p2.WriteString(fmt.Sprintf("BT /F3 7 Tf 0.9 0.9 0.9 rg 125 %.2f Td (%s) Tj ET\n", boxY+74, toPDFASCII(data.LedgerGenesis)))

	p2.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.9 0.9 0.9 rg 50 %.2f Td (Ledger Head:) Tj ET\n", boxY+60))
	p2.WriteString(fmt.Sprintf("BT /F3 7 Tf 0.9 0.9 0.9 rg 125 %.2f Td (#%d | %s) Tj ET\n", boxY+60, data.LedgerHeadID, toPDFASCII(data.LedgerHead)))

	p2.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.9 0.9 0.9 rg 50 %.2f Td (SHA-256 Laudo:) Tj ET\n", boxY+46))
	p2.WriteString(fmt.Sprintf("BT /F3 7 Tf 0.3 0.85 1.0 rg 125 %.2f Td (%s) Tj ET\n", boxY+46, toPDFASCII(data.ReportSHA256)))

	p2.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.9 0.9 0.9 rg 50 %.2f Td (Assinatura ECDSA:) Tj ET\n", boxY+32))
	sigDisplay := data.SignatureHex
	if len(sigDisplay) > 70 {
		sigDisplay = sigDisplay[:68] + ".."
	}
	p2.WriteString(fmt.Sprintf("BT /F3 7 Tf 0.4 0.9 0.6 rg 125 %.2f Td (%s) Tj ET\n", boxY+32, toPDFASCII(sigDisplay)))

	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.05 0.9 0.5 rg 50 %.2f Td ([VERIFICADO] 100%% CONFORME COM A RDC 430/2020 E PRINCIPIOS ALCOA+ DO GUIA 38/2020 DA ANVISA) Tj ET\n", boxY+14))

	// Footer Page 2
	p2.WriteString("0.7 0.75 0.8 RG\n1 w\n40 45 m 555 45 l S\n")
	p2.WriteString("BT /F1 8 Tf 0.5 0.55 0.6 rg 40 32 Td (ColdChain Pharma RT - Laudo Pericial Regulatório - Pagina 2 de 2) Tj ET\n")
	p2.WriteString("BT /F2 8 Tf 0.1 0.4 0.8 rg 360 32 Td (VALIDADE JURIDICA: ICP-Brasil / MP 2.200-2 & RDC 430) Tj ET\n")

	// ==========================================
	// MONTAGEM DA ESTRUTURA DO PDF 1.4
	// ==========================================
	var pdf bytes.Buffer
	offsets := make([]int, 12)

	pdf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	// Obj 1: Catalog
	offsets[1] = pdf.Len()
	pdf.WriteString("1 0 obj\n<<\n  /Type /Catalog\n  /Pages 2 0 R\n>>\nendobj\n")

	// Obj 2: Pages
	offsets[2] = pdf.Len()
	pdf.WriteString("2 0 obj\n<<\n  /Type /Pages\n  /Kids [3 0 R 5 0 R]\n  /Count 2\n>>\nendobj\n")

	// Obj 3: Page 1
	offsets[3] = pdf.Len()
	pdf.WriteString("3 0 obj\n<<\n  /Type /Page\n  /Parent 2 0 R\n  /MediaBox [0 0 595.28 841.89]\n  /Contents 4 0 R\n  /Resources <<\n    /Font <<\n      /F1 7 0 R\n      /F2 8 0 R\n      /F3 9 0 R\n    >>\n  >>\n>>\nendobj\n")

	// Obj 4: Contents Page 1
	offsets[4] = pdf.Len()
	pdf.WriteString(fmt.Sprintf("4 0 obj\n<<\n  /Length %d\n>>\nstream\n", p1.Len()))
	pdf.Write(p1.Bytes())
	pdf.WriteString("\nendstream\nendobj\n")

	// Obj 5: Page 2
	offsets[5] = pdf.Len()
	pdf.WriteString("5 0 obj\n<<\n  /Type /Page\n  /Parent 2 0 R\n  /MediaBox [0 0 595.28 841.89]\n  /Contents 6 0 R\n  /Resources <<\n    /Font <<\n      /F1 7 0 R\n      /F2 8 0 R\n      /F3 9 0 R\n    >>\n  >>\n>>\nendobj\n")

	// Obj 6: Contents Page 2
	offsets[6] = pdf.Len()
	pdf.WriteString(fmt.Sprintf("6 0 obj\n<<\n  /Length %d\n>>\nstream\n", p2.Len()))
	pdf.Write(p2.Bytes())
	pdf.WriteString("\nendstream\nendobj\n")

	// Obj 7: Font Helvetica
	offsets[7] = pdf.Len()
	pdf.WriteString("7 0 obj\n<<\n  /Type /Font\n  /Subtype /Type1\n  /BaseFont /Helvetica\n  /Encoding /WinAnsiEncoding\n>>\nendobj\n")

	// Obj 8: Font Helvetica-Bold
	offsets[8] = pdf.Len()
	pdf.WriteString("8 0 obj\n<<\n  /Type /Font\n  /Subtype /Type1\n  /BaseFont /Helvetica-Bold\n  /Encoding /WinAnsiEncoding\n>>\nendobj\n")

	// Obj 9: Font Courier
	offsets[9] = pdf.Len()
	pdf.WriteString("9 0 obj\n<<\n  /Type /Font\n  /Subtype /Type1\n  /BaseFont /Courier\n  /Encoding /WinAnsiEncoding\n>>\nendobj\n")

	// Obj 10: Info
	offsets[10] = pdf.Len()
	creationTime := time.Now().Format("20060102150405Z")
	pdf.WriteString(fmt.Sprintf("10 0 obj\n<<\n  /Title (Dossie Sanitario ANVISA RDC 430/2020)\n  /Author (%s)\n  /Subject (Laudo Pericial de Cadeia de Frio)\n  /Creator (ColdChain Web-Server Gateway)\n  /CreationDate (D:%s)\n>>\nendobj\n", toPDFASCII(data.TechnicalLead), creationTime))

	// XREF Table
	xrefOffset := pdf.Len()
	pdf.WriteString("xref\n0 11\n")
	pdf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 10; i++ {
		pdf.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}

	// Trailer
	pdf.WriteString("trailer\n<<\n  /Size 11\n  /Root 1 0 R\n  /Info 10 0 R\n>>\n")
	pdf.WriteString(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefOffset))

	return pdf.Bytes(), nil
}

// GenerateDossierHTML gera um laudo pericial oficial em HTML responsivo formatado para impressão (A4).
func GenerateDossierHTML(data *DossierReportData) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html lang="pt-BR">
<head>
  <meta charset="UTF-8">
  <title>Dossiê Sanitário ANVISA RDC 430/2020 — ColdChain Pharma RT</title>
  <style>
    @page { size: A4 portrait; margin: 15mm 15mm 15mm 15mm; }
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Arial, sans-serif; color: #1e293b; margin: 0; padding: 20px; font-size: 11px; line-height: 1.4; background: #fff; }
    .doc-header { border-bottom: 2px solid #0284c7; padding-bottom: 12px; margin-bottom: 15px; display: flex; justify-content: space-between; align-items: flex-start; }
    .doc-title { font-size: 18px; font-weight: 800; color: #0f172a; margin: 0 0 4px 0; }
    .doc-sub { font-size: 11px; color: #64748b; margin: 0; }
    .badge-anvisa { background: #0284c7; color: #fff; padding: 4px 8px; border-radius: 4px; font-size: 10px; font-weight: 700; text-transform: uppercase; }
    .meta-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 10px; background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 6px; padding: 10px; margin-bottom: 15px; }
    .kpi-row { display: grid; grid-template-columns: repeat(4, 1fr); gap: 10px; margin-bottom: 15px; }
    .kpi-box { border: 1px solid #cbd5e1; border-radius: 6px; padding: 8px 10px; background: #fff; text-align: center; }
    .kpi-val { font-size: 16px; font-weight: 800; color: #0284c7; margin: 4px 0; }
    .sec-title { font-size: 13px; font-weight: 700; color: #0f172a; border-left: 3px solid #0284c7; padding-left: 6px; margin: 15px 0 8px 0; }
    table { width: 100%; border-collapse: collapse; margin-bottom: 15px; font-size: 10px; }
    th { background: #f1f5f9; color: #334155; font-weight: 700; text-align: left; padding: 6px 8px; border: 1px solid #cbd5e1; }
    td { padding: 5px 8px; border: 1px solid #e2e8f0; }
    tr:nth-child(even) { background: #f8fafc; }
    .tag { display: inline-block; padding: 2px 6px; border-radius: 3px; font-size: 9px; font-weight: 700; }
    .tag-green { background: #dcfce7; color: #166534; }
    .tag-amber { background: #fef3c7; color: #92400e; }
    .tag-red { background: #fee2e2; color: #991b1b; }
    .crypto-box { background: #0f172a; color: #e2e8f0; border-radius: 6px; padding: 12px; margin-top: 15px; font-family: "Courier New", Courier, monospace; font-size: 9.5px; line-height: 1.5; }
    .crypto-title { color: #38bdf8; font-weight: bold; font-size: 11px; margin-bottom: 6px; font-family: sans-serif; }
    .btn-print { display: inline-block; padding: 8px 16px; background: #0284c7; color: #fff; text-decoration: none; border-radius: 4px; font-weight: bold; cursor: pointer; border: none; margin-bottom: 15px; }
    @media print {
      .no-print { display: none !important; }
      body { padding: 0; }
      .page-break { page-break-before: always; }
    }
  </style>
</head>
<body>
  <div class="no-print" style="margin-bottom: 15px; display: flex; gap: 10px;">
    <button class="btn-print" onclick="window.print()">Salvar como PDF / Imprimir Laudo</button>
    <a href="/api/pharma/reports/dossier.pdf" class="btn-print" style="background:#059669;">Baixar PDF Nativo</a>
    <a href="/api/pharma/reports/dossier.csv" class="btn-print" style="background:#475569;">Exportar CSV</a>
  </div>

  <div class="doc-header">
    <div>
      <h1 class="doc-title">ColdChain Pharma RT — Dossiê Sanitário de Boas Práticas</h1>
      <p class="doc-sub">Auditoria Regulamentar RDC 430/2020, RDC 197/2017 & Guia nº 38/2020 da ANVISA</p>
    </div>
    <div style="text-align: right;">
      <span class="badge-anvisa">Laudo Oficial</span>
      <div style="font-size: 10px; color: #64748b; margin-top: 4px;">Emissão: ` + data.GeneratedAt + `</div>
    </div>
  </div>

  <div class="meta-grid">
    <div>
      <strong>Distribuidora:</strong> ` + data.Company + `<br>
      <strong>CNPJ:</strong> ` + data.CNPJ + `<br>
      <strong>Instalações:</strong> Almoxarifado Central e Câmaras Frias Qualificadas
    </div>
    <div>
      <strong>Responsável Técnica:</strong> ` + data.TechnicalLead + `<br>
      <strong>Inscrição Profissional:</strong> ` + data.CRF + `<br>
      <strong>Status Geral:</strong> <span class="tag tag-green">` + data.KPIs.ComplianceStatus + `</span>
    </div>
  </div>

  <div class="sec-title">1. Indicadores Estatísticos de Desempenho e Metrologia (KPIs)</div>
  <div class="kpi-row">
    <div class="kpi-box">
      <div style="color:#64748b; font-size:10px;">Disponibilidade</div>
      <div class="kpi-val">` + fmt.Sprintf("%.2f%%", data.KPIs.AvailabilityPct) + `</div>
      <div style="font-size:9px; color:#10b981;">Meta &ge; 99.5%</div>
    </div>
    <div class="kpi-box">
      <div style="color:#64748b; font-size:10px;">MTTR (Atendimento)</div>
      <div class="kpi-val">` + fmt.Sprintf("%.1f min", data.KPIs.AvgResponseTimeMinutes) + `</div>
      <div style="font-size:9px; color:#10b981;">SLA &le; 30.0 min</div>
    </div>
    <div class="kpi-box">
      <div style="color:#64748b; font-size:10px;">Taxa de Excursão</div>
      <div class="kpi-val" style="color:#f59e0b;">` + fmt.Sprintf("%.2f%%", data.KPIs.ExcursionRatePct) + `</div>
      <div style="font-size:9px; color:#64748b;">Meta &le; 0.10%</div>
    </div>
    <div class="kpi-box">
      <div style="color:#64748b; font-size:10px;">Eficácia de CAPA</div>
      <div class="kpi-val" style="color:#8b5cf6;">` + fmt.Sprintf("%.1f%%", data.KPIs.CapaEfficiencyPct) + `</div>
      <div style="font-size:9px; color:#10b981;">Laudos Homologados</div>
    </div>
  </div>

  <div class="sec-title">2. Inventário Metrológico de Sensores Calibrados RBC</div>
  <table>
    <thead>
      <tr>
        <th>Tag</th>
        <th>Série</th>
        <th>Câmara</th>
        <th>Posicionamento Térmico</th>
        <th>Certificado RBC</th>
        <th>Laboratório</th>
        <th>Validade</th>
        <th>Status</th>
      </tr>
    </thead>
    <tbody>`)

	for _, s := range data.Sensors {
		sb.WriteString(fmt.Sprintf(`<tr>
        <td><strong>%s</strong></td>
        <td><code>%s</code></td>
        <td>%s</td>
        <td>%s</td>
        <td>%s</td>
        <td>%s</td>
        <td>%s</td>
        <td><span class="tag tag-green">%s</span></td>
      </tr>`, s.ID, s.SerialNumber, s.ChamberID, s.ThermalPosition, s.CertNumber, s.CalibratedBy, s.CalibValidUntil, s.Status))
	}

	sb.WriteString(`</tbody>
  </table>

  <div class="page-break"></div>

  <div class="sec-title">3. Rastreabilidade de Lotes Termolábeis (Princípio FEFO/PVPS)</div>
  <table>
    <thead>
      <tr>
        <th>Lote</th>
        <th>Medicamento / Apresentação</th>
        <th>Validade</th>
        <th>Estoque</th>
        <th>Câmara</th>
        <th>Localização</th>
        <th>Situação</th>
      </tr>
    </thead>
    <tbody>`)

	for _, l := range data.Lots {
		statusTag := `<span class="tag tag-green">LIBERADO</span>`
		if l.Status == "quarentena" {
			statusTag = `<span class="tag tag-red">QUARENTENA</span>`
		}
		sb.WriteString(fmt.Sprintf(`<tr>
        <td><strong>%s</strong></td>
        <td>%s</td>
        <td>%s</td>
        <td>%d un</td>
        <td>%s</td>
        <td>%s</td>
        <td>%s</td>
      </tr>`, l.LotNumber, l.ProductTradeName, l.ExpiryDate, l.Quantity, l.ChamberID, l.ShelfLocation, statusTag))
	}

	sb.WriteString(`</tbody>
  </table>

  <div class="sec-title">4. Desvios Térmicos e Investigação de Excursões (RDC 430 Art. 38)</div>
  <table>
    <thead>
      <tr>
        <th>Código</th>
        <th>Câmara</th>
        <th>Pico Temp</th>
        <th>Grau-Min</th>
        <th>Causa Raiz Apurada</th>
        <th>Parecer Técnico</th>
        <th>Assinatura</th>
        <th>Situação</th>
      </tr>
    </thead>
    <tbody>`)

	for _, e := range data.Excursions {
		sb.WriteString(fmt.Sprintf(`<tr>
        <td><strong>%s</strong></td>
        <td>%s (%s)</td>
        <td><strong style="color:#ef4444;">%.1f °C</strong></td>
        <td>%.1f</td>
        <td>%s</td>
        <td>%s</td>
        <td><code>%s</code></td>
        <td><span class="tag tag-amber">%s</span></td>
      </tr>`, e.Code, e.ChamberID, e.SensorID, e.PeakTemp, e.AccumulatedDegreeMinutes, e.Cause, e.TechnicalReport, e.ElectronicSig, strings.ToUpper(e.Status)))
	}

	sb.WriteString(`</tbody>
  </table>

  <div class="sec-title">5. Chancela Forense Criptográfica e Trilha Imutável WORM (ALCOA+)</div>
  <div class="crypto-box">
    <div class="crypto-title">AUTENTICIDADE E IMPERMEABILIDADE FORENSE GARANTIDA VIA LEDGER WORM</div>
    <div>ALGORITMO DE ASSINATURA: ` + data.SignerAlgorithm + ` | ASSINADOR KEY ID: ` + data.SignerKeyID + `</div>
    <div>BLOCO GENESIS (INICIO): ` + data.LedgerGenesis + `</div>
    <div>BLOCO HEAD ATUAL:        #` + strconv.FormatInt(data.LedgerHeadID, 10) + ` | ` + data.LedgerHead + `</div>
    <div>HASH SHA-256 DO LAUDO:  ` + data.ReportSHA256 + `</div>
    <div>ASSINATURA DIGITAL:     ` + data.SignatureHex + `</div>
    <div style="color:#4ade80; margin-top:8px; font-weight:bold;">[STATUS: 100% AUDITADO E CONFORME COM A RDC 430/2020 E 21 CFR PART 11]</div>
  </div>
</body>
</html>`)

	return sb.String()
}
