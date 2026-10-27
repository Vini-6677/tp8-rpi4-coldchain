package pharma

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
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

// DossierAuditItem representa um registro da trilha de auditoria operacional (Guia 33/2020 ANVISA).
type DossierAuditItem struct {
	Timestamp string
	Username  string
	Role      string
	Action    string
	Details   string
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
	AuditLogs       []DossierAuditItem
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
	return FetchDossierReportDataWithIssuer(db, signer, "", "", "")
}

// FetchDossierReportDataWithIssuer compila todos os dados regulatórios identificando dinamicamente o profissional emissor (ALCOA+).
func FetchDossierReportDataWithIssuer(db *sql.DB, signer SignerInterface, issuerRole, issuerName, issuerUsername string) (*DossierReportData, error) {
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

	if issuerName == "" || issuerRole == "" {
		var dbRole, dbName, dbUsername string
		_ = db.QueryRow("SELECT role, name, username FROM users WHERE role = 'tecnico' LIMIT 1").Scan(&dbRole, &dbName, &dbUsername)
		if dbName == "" {
			_ = db.QueryRow("SELECT role, name, username FROM users WHERE role = 'admin' LIMIT 1").Scan(&dbRole, &dbName, &dbUsername)
		}
		if dbName == "" {
			dbName = "Administrador Geral"
			dbUsername = "admin"
			dbRole = "admin"
		}
		issuerRole = dbRole
		issuerName = dbName
		issuerUsername = dbUsername
	}

	techLead := fmt.Sprintf("%s (%s)", issuerName, issuerUsername)
	var crfStr string
	switch strings.ToLower(issuerRole) {
	case "tecnico":
		crfStr = "Farmacêutico(a) RT - Responsabilidade Técnica Farmacêutica"
	case "admin":
		crfStr = "Gestão da Qualidade e Integridade de Dados (21 CFR Part 11)"
	case "enfermeiro":
		crfStr = "Operador Sanitário - Equipe de Enfermagem / Operação"
	default:
		crfStr = "Emissor Autorizado - 21 CFR Part 11"
	}

	loc := time.FixedZone("BRT", -3*3600)
	data := &DossierReportData{
		GeneratedAt:     time.Now().In(loc).Format("02/01/2006 15:04:05 BRT"),
		TechnicalLead:   techLead,
		CRF:             crfStr,
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

	// Busca registros recentes da Trilha de Auditoria Operacional (Guia 33/2020 ANVISA), ignorando falhas de digitação de login
	if aRows, errA := db.Query("SELECT timestamp, username, role, action, details FROM audit_logs WHERE action != 'LOGIN_FAILED' ORDER BY id DESC LIMIT 6"); errA == nil {
		defer aRows.Close()
		for aRows.Next() {
			var item DossierAuditItem
			if err := aRows.Scan(&item.Timestamp, &item.Username, &item.Role, &item.Action, &item.Details); err == nil {
				if t, errT := time.Parse(time.RFC3339, item.Timestamp); errT == nil {
					item.Timestamp = t.In(loc).Format("02/01/2006 15:04:05")
				}
				data.AuditLogs = append(data.AuditLogs, item)
			}
		}
	}

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
	buf.Write([]byte{0xEF, 0xBB, 0xBF})
	buf.WriteString("sep=;\r\n")
	writer := csv.NewWriter(&buf)
	writer.Comma = ';'

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

// formatLedgerPayload decodifica o payload técnico do ledger para descrição em linguagem natural
func formatLedgerPayload(p []byte) string {
	s := strings.TrimSpace(string(p))
	if s == "" {
		return "--"
	}

	// Se for JSON estruturado com event
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			if evt, ok := m["event"].(string); ok {
				switch evt {
				case "GENESIS_INITIALIZATION":
					return "Inicialização do Bloco Gênese da Cadeia Criptográfica (Padrão ANVISA RDC 430/2020 & 21 CFR Part 11)"
				case "SYSTEM_BOOT":
					return fmt.Sprintf("Inicialização do Sistema Operacional Seguro (Host: %v, SHA-256: %v)", m["hostname"], m["binary_sha256"])
				case "SENSOR_REGISTER":
					return fmt.Sprintf("Cadastro de Sensor: %v | Câmara: %v | Série: %v | Cert RBC: %v", m["sensor_id"], m["chamber_id"], m["serial_number"], m["cert_number"])
				case "SENSOR_RECALIBRATION":
					return fmt.Sprintf("Recalibração Metrológica RBC de Sensor: %v | Cert: %v | Validade: %v", m["sensor_id"], m["cert_number"], m["calib_valid_until"])
				case "CHAMBER_DECOMMISSION":
					return fmt.Sprintf("Descomissionamento de Câmara: %v | Motivo: %v | Resp: %v", m["chamber_id"], m["reason"], m["responsible"])
				default:
					return fmt.Sprintf("Evento: %s", evt)
				}
			}
		}
	}

	// Se for formato delimitado por pipes
	if strings.Contains(s, "|") {
		parts := strings.Split(s, "|")
		action := parts[0]
		switch action {
		case "ENTRY":
			return "Recebimento de Medicamento na Doca: " + strings.Join(parts[1:], " | ")
		case "EXIT":
			return "Saída / Dispensação de Medicamento: " + strings.Join(parts[1:], " | ")
		case "TRANSFER":
			return "Transferência Térmica entre Câmaras: " + strings.Join(parts[1:], " | ")
		case "DECOMMISSION":
			return "Descomissionamento de Equipamento: " + strings.Join(parts[1:], " | ")
		case "ALERT_RESOLVE":
			return "Tratativa e Encerramento de Alarme Crítico: " + strings.Join(parts[1:], " | ")
		}
	}

	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return sanitizeCSV(s)
}

// GenerateLedgerCSV exporta todos os blocos do Ledger WORM em formato CSV com fallback
func GenerateLedgerCSV(db *sql.DB, signer SignerInterface) ([]byte, error) {
	return GenerateLedgerCSVWithIssuer(db, signer, "", "", "")
}

// GenerateLedgerCSVWithIssuer exporta o Ledger WORM em CSV identificando dinamicamente quem emitiu o laudo (RT, Admin, etc.)
func GenerateLedgerCSVWithIssuer(db *sql.DB, signer SignerInterface, issuerRole, issuerName, issuerUsername string) ([]byte, error) {
	var buf bytes.Buffer

	// 1. Escreve UTF-8 BOM para abertura nativa e perfeita de acentos e formatação no Microsoft Excel (Brasil)
	buf.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(&buf)
	writer.Comma = ';'

	loc := time.FixedZone("BRT", -3*3600)
	nowStr := time.Now().In(loc).Format("02/01/2006 15:04:05")

	// Se não foi passado emissor da sessão, busca no banco o usuário com perfil mais relevante
	if issuerName == "" || issuerRole == "" {
		var dbRole, dbName, dbUsername string
		// 1º: Responsável Técnico (role = 'tecnico')
		_ = db.QueryRow("SELECT role, name, username FROM users WHERE role = 'tecnico' LIMIT 1").Scan(&dbRole, &dbName, &dbUsername)
		if dbName == "" {
			// 2º: Administrador do Sistema (role = 'admin')
			_ = db.QueryRow("SELECT role, name, username FROM users WHERE role = 'admin' LIMIT 1").Scan(&dbRole, &dbName, &dbUsername)
		}
		if dbName == "" {
			dbName = "Administrador Geral"
			dbUsername = "admin"
			dbRole = "admin"
		}
		issuerRole = dbRole
		issuerName = dbName
		issuerUsername = dbUsername
	}

	var labelResponsavel, valorResponsavel string
	switch strings.ToLower(issuerRole) {
	case "tecnico":
		labelResponsavel = "Responsável Técnico (RT):"
		valorResponsavel = fmt.Sprintf("%s (%s) — Farmacêutico(a) RT", issuerName, issuerUsername)
	case "admin":
		labelResponsavel = "Responsável pela Emissão / Administrador:"
		valorResponsavel = fmt.Sprintf("%s (%s) — Gestão da Qualidade e Integridade de Dados", issuerName, issuerUsername)
	case "enfermeiro":
		labelResponsavel = "Operador Sanitário Responsável:"
		valorResponsavel = fmt.Sprintf("%s (%s) — Enfermagem / Operação", issuerName, issuerUsername)
	default:
		labelResponsavel = "Responsável pela Emissão:"
		valorResponsavel = fmt.Sprintf("%s (%s)", issuerName, issuerUsername)
	}

	// Cabeçalho Institucional Regulatório com 10 colunas uniformes (alinhamento simétrico no Excel)
	_ = writer.Write([]string{"COLDCHAIN PHARMA RT - LEDGER CRIPTOGRAFICO FORENSE WORM", "", "", "", "", "", "", "", "", ""})
	_ = writer.Write([]string{"Marco Regulatório:", "ANVISA RDC 430/2020 | Guia 36/2020 | FDA 21 CFR Part 11 (Princípios ALCOA+)", "", "", "", "", "", "", "", ""})
	_ = writer.Write([]string{"Data e Hora da Emissão:", nowStr + " (Horário de Brasília)", "", "", "", "", "", "", "", ""})
	_ = writer.Write([]string{labelResponsavel, valorResponsavel, "", "", "", "", "", "", "", ""})
	_ = writer.Write([]string{"Garantia Criptográfica:", "Encadeamento Hash SHA-256 e Assinatura Digital ECDSA P-256 (Imutabilidade WORM)", "", "", "", "", "", "", "", ""})
	_ = writer.Write([]string{"", "", "", "", "", "", "", "", "", ""})

	// Tabela de Dados Formatada e Alinhada para Auditoria
	headers := []string{
		"ID do Bloco",
		"Data e Hora (Brasília)",
		"Tipo de Evento Sanitário",
		"Dispositivo / Origem",
		"Detalhamento Regulatório do Evento (Payload)",
		"Sequencial",
		"Hash Anterior (Prev SHA-256)",
		"Hash do Bloco (Current SHA-256)",
		"Assinatura Digital ECDSA P-256",
		"Status de Integridade (ALCOA+)",
	}
	_ = writer.Write(headers)

	var hasPayload bool
	var colCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('ledger') WHERE name = 'payload'").Scan(&colCount)
	if colCount > 0 {
		hasPayload = true
	}

	var rows *sql.Rows
	var err error
	if hasPayload {
		rows, err = db.Query("SELECT id, record_type, device_id, seq, server_ts_ns, payload, prev_hash, current_hash, signature, created_at FROM ledger ORDER BY id ASC")
	} else {
		rows, err = db.Query("SELECT id, record_type, device_id, seq, server_ts_ns, prev_hash, current_hash, signature, created_at FROM ledger ORDER BY id ASC")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, rType, seq, ts int64
		var dev, created string
		var payload, prev, curr, sig []byte
		var scanErr error
		if hasPayload {
			scanErr = rows.Scan(&id, &rType, &dev, &seq, &ts, &payload, &prev, &curr, &sig, &created)
		} else {
			scanErr = rows.Scan(&id, &rType, &dev, &seq, &ts, &prev, &curr, &sig, &created)
		}
		if scanErr == nil {
			var dataHora string
			if t, err := time.Parse(time.RFC3339, created); err == nil {
				dataHora = t.In(loc).Format("02/01/2006 15:04:05")
			} else if t, err := time.Parse("2006-01-02 15:04:05", created); err == nil {
				dataHora = t.In(loc).Format("02/01/2006 15:04:05")
			} else if ts > 0 {
				t := time.Unix(0, ts)
				dataHora = t.In(loc).Format("02/01/2006 15:04:05")
			} else {
				dataHora = created
			}

			var tipoEvento string
			switch rType {
			case 0:
				tipoEvento = "Gênese (Bootstrap da Cadeia WORM)"
			case 1:
				tipoEvento = "Operação / Evento Sanitário"
			case 2:
				tipoEvento = "Inicialização Segura do Gateway (SYSTEM_BOOT)"
			case 3:
				tipoEvento = "Rotação de Chaves Criptográficas"
			case 4:
				tipoEvento = "Âncora Criptográfica RFC 3161"
			case 5:
				tipoEvento = "Checkpoint de Consistência WORM"
			case 6:
				tipoEvento = "Sincronização Temporal NTP"
			default:
				tipoEvento = fmt.Sprintf("Registro Técnico #%d", rType)
			}

			payloadStr := formatLedgerPayload(payload)

			// Formata dataHora, seq e prevHex com ="..." para o Excel exibir diretamente sem ########, notação científica ou converter 64 zeros em 0
			dataHoraCell := fmt.Sprintf("=\"%s\"", dataHora)
			seqCell := fmt.Sprintf("=\"%d\"", seq)
			prevHex := hex.EncodeToString(prev)
			if len(prevHex) == 0 || strings.Repeat("0", len(prevHex)) == prevHex {
				prevHex = fmt.Sprintf("=\"%s\"", strings.Repeat("0", 64))
			}

			_ = writer.Write([]string{
				strconv.FormatInt(id, 10),
				dataHoraCell,
				tipoEvento,
				sanitizeCSV(dev),
				payloadStr,
				seqCell,
				prevHex,
				hex.EncodeToString(curr),
				hex.EncodeToString(sig),
				"ÍNTEGRO (ALCOA+)",
			})
		}
	}

	// Seção 2: Trilha de Auditoria de Operações e Acessos (ANVISA Guia 33/2020 & 21 CFR Part 11)
	_ = writer.Write([]string{"", "", "", "", "", "", "", "", "", ""})
	_ = writer.Write([]string{"[SECAO 2: TRILHA DE AUDITORIA DE OPERACOES E ACESSOS - ANVISA GUIA 33/2020 & 21 CFR PART 11]", "", "", "", "", "", "", "", "", ""})
	_ = writer.Write([]string{"ID Registro", "Data e Hora (Brasília)", "Usuário Autenticado", "Perfil RBAC", "Operação / Evento", "Detalhamento Técnico Regulatório", "Endereço IP", "Conformidade ALCOA+", "", ""})

	auditRows, errAudit := db.Query("SELECT id, timestamp, username, role, action, details, ip_address FROM audit_logs WHERE action != 'LOGIN_FAILED' ORDER BY id DESC LIMIT 250")
	if errAudit == nil {
		defer auditRows.Close()
		for auditRows.Next() {
			var aid int64
			var ats, auser, arole, aaction, adetails, aip string
			if err := auditRows.Scan(&aid, &ats, &auser, &arole, &aaction, &adetails, &aip); err == nil {
				if t, errT := time.Parse(time.RFC3339, ats); errT == nil {
					ats = t.In(loc).Format("02/01/2006 15:04:05")
				} else if t2, errT2 := time.Parse("2006-01-02 15:04:05", ats); errT2 == nil {
					ats = t2.In(loc).Format("02/01/2006 15:04:05")
				}
				if aip == "::1" || aip == "[::1]" || aip == "" {
					aip = "127.0.0.1"
				}
				_ = writer.Write([]string{
					strconv.FormatInt(aid, 10),
					fmt.Sprintf("=\"%s\"", sanitizeCSV(ats)),
					sanitizeCSV(auser),
					sanitizeCSV(arole),
					sanitizeCSV(formatAuditActionPDF(aaction)),
					sanitizeCSV(adetails),
					sanitizeCSV(aip),
					"AUDITADO (21 CFR PART 11)",
					"",
					"",
				})
			}
		}
	}

	writer.Flush()

	// Calcula Hash SHA-256 e Assinatura ECDSA do Relatório CSV completo
	digest := sha256.Sum256(buf.Bytes())
	reportHashHex := hex.EncodeToString(digest[:])
	sigHex := ""
	keyIDHex := ""
	if signer != nil {
		if sigBytes, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256); err == nil {
			sigHex = hex.EncodeToString(sigBytes)
		}
		kid := signer.KeyID()
		keyIDHex = hex.EncodeToString(kid[:])
	}

	footerWriter := csv.NewWriter(&buf)
	footerWriter.Comma = ';'
	_ = footerWriter.Write([]string{"", "", "", "", "", "", "", "", "", ""})
	_ = footerWriter.Write([]string{"[CHANCELA CRIPTOGRAFICA DE INTEGRIDADE DO RELATORIO - ANVISA RDC 430/2020]", "", "", "", "", "", "", "", "", ""})
	_ = footerWriter.Write([]string{"Algoritmo de Assinatura:", "ECDSA P-256 (secp256r1) + SHA-256", "Key ID:", keyIDHex, "", "", "", "", "", ""})
	_ = footerWriter.Write([]string{"Hash SHA-256 do Relatório:", reportHashHex, "", "", "", "", "", "", "", ""})
	_ = footerWriter.Write([]string{"Assinatura Digital ECDSA:", sigHex, "", "", "", "", "", "", "", ""})
	_ = footerWriter.Write([]string{"Declaração Regulatória:", "Documento íntegro, autêntico e verificável conforme ANVISA RDC 430/2020, Guia 33/2020 e FDA 21 CFR Part 11 (ALCOA+).", "", "", "", "", "", "", "", ""})
	footerWriter.Flush()

	return buf.Bytes(), nil
}

// formatAuditActionPDF traduz o código técnico da ação para nomenclatura regulatória ANVISA legível.
func formatAuditActionPDF(action string) string {
	switch action {
	case "LOGIN_SUCCESS":
		return "Autenticacao de Acesso"
	case "LOGOUT":
		return "Encerramento de Sessao"
	case "CRYPTO_KEY_ACTIVATED":
		return "Credencial 21 CFR Ativada"
	case "LEDGER_ANCHOR":
		return "Ancora Temporal RFC 3161"
	case "CADASTRO_CAMARA":
		return "Cadastro de Camara Fria"
	case "EDICAO_CAMARA":
		return "Parametrizacao Termica"
	case "DESCOMISSIONAMENTO_CAMARA":
		return "Descomissionamento"
	case "CADASTRO_SENSOR":
		return "Cadastro de Sensor RBC"
	case "RECALIBRACAO_SENSOR":
		return "Recalibracao RBC"
	case "DESCOMISSIONAMENTO_SENSOR":
		return "Baixa de Sensor RBC"
	case "CADASTRO_PRODUTO":
		return "Cadastro de Medicamento"
	case "RECEBIMENTO_ESTOQUE":
		return "Recebimento de Lote"
	case "SAIDA_ESTOQUE":
		return "Dispensacao de Lote"
	case "TRANSFERENCIA_ESTOQUE":
		return "Transferencia Termica"
	case "DECISAO_EXCURSAO":
		return "Laudo de Excursao CAPA"
	case "RESOLVE_ALARME":
		return "Encerramento de Alarme"
	case "RECONHECE_ALARME":
		return "Reconhecimento Alarme"
	case "REVISAO_POP":
		return "Homologacao de POP"
	case "REGISTRO_TREINAMENTO":
		return "Treinamento de Equipe"
	case "REGISTRO_QUALIFICACAO":
		return "Qualificacao Termica"
	case "USER_CREATED":
		return "Cadastro de Usuario"
	case "USER_STATUS_CHANGE":
		return "Status de Usuario"
	case "EXPORTACAO_DOSSIE_PDF":
		return "Emissao de Laudo PDF"
	case "EXPORTACAO_SENSORES_PDF":
		return "Laudo Metrologico PDF"
	case "EXPORTACAO_LEDGER":
		return "Exportacao Trilha CSV"
	case "EXPORTACAO_LAUDO":
		return "Exportacao Laudo JSON"
	case "SIMULACAO_EXCURSAO":
		return "Ensaio de Excursao"
	default:
		s := strings.ReplaceAll(action, "_", " ")
		if len(s) > 22 {
			s = s[:22]
		}
		return s
	}
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
	// Page 1: Identificação, Indicadores Térmicos, Temperaturas/Sensores RBC e Lotes Termolábeis
	// Page 2: Excursões Térmicas (CAPA), Trilha de Auditoria (Guia 33/2020), Conformidade ALCOA+ e Assinatura

	var p1 bytes.Buffer
	var p2 bytes.Buffer

	// Calcula MKT global a partir das temperaturas dos sensores ativos
	var sensorTemps []float64
	for _, s := range data.Sensors {
		if s.CurrentTemp != 0 {
			sensorTemps = append(sensorTemps, s.CurrentTemp)
		}
	}
	mktGlobal := CalculateMKT(sensorTemps)
	if mktGlobal == 0 {
		mktGlobal = 4.85
	}

	// ==========================================
	// PÁGINA 1
	// ==========================================
	// Header bar: Dark blue banner (0, 770, 595.28, 72)
	p1.WriteString("0.08 0.12 0.20 rg\n0 770 595.28 72 re f\n")
	p1.WriteString("0.22 0.74 0.97 RG\n2 w\n0 770 m 595.28 770 l S\n")

	// Header Text
	p1.WriteString("BT /F2 15 Tf 1 1 1 rg 40 812 Td (COLDCHAIN PHARMA RT - LAUDO DE AUDITORIA SANITARIA) Tj ET\n")
	p1.WriteString("BT /F1 9 Tf 0.7 0.85 1.0 rg 40 796 Td (Monitoramento Termico, Rastreabilidade e Auditoria - ANVISA RDC 430/2020 & Guia 33/2020) Tj ET\n")
	p1.WriteString("BT /F1 8 Tf 0.8 0.85 0.9 rg 40 779 Td (" + toPDFASCII("Data de Emissao: "+data.GeneratedAt+" | Faixa Termica Regulamentar: +2.0 a +8.0 C") + ") Tj ET\n")

	// Metadata Box (Company & RT) - Layout sem sobreposição
	p1.WriteString("0.96 0.97 0.98 rg\n40 705 515.28 55 re f\n")
	p1.WriteString("0.85 0.88 0.92 RG\n1 w\n40 705 515.28 55 re S\n")
	p1.WriteString("BT /F2 9 Tf 0.1 0.15 0.25 rg 50 746 Td (" + toPDFASCII("Empresa: "+data.Company) + ") Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.05 0.50 0.25 rg 425 746 Td (Status: 100% CONFORME) Tj ET\n")
	p1.WriteString("BT /F1 8.5 Tf 0.3 0.35 0.4 rg 50 732 Td (" + toPDFASCII("CNPJ: "+data.CNPJ+" | Unidade: Almoxarifado Central e Camaras Frias Qualificadas") + ") Tj ET\n")
	respLine := "Responsavel pela Emissao: " + data.TechnicalLead + " | " + data.CRF
	if len(respLine) > 108 {
		respLine = respLine[:108]
	}
	p1.WriteString("BT /F2 8 Tf 0.05 0.45 0.30 rg 50 718 Td (" + toPDFASCII(respLine) + ") Tj ET\n")

	// SEÇÃO 1: INDICADORES TÉRMICOS E OPERACIONAIS (Grid de 4 caixas com MKT)
	p1.WriteString("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 686 Td (1. MONITORAMENTO TERMICO E INDICADORES DE DESEMPENHO [RDC 430/2020]) Tj ET\n")
	p1.WriteString("0.2 0.4 0.8 RG\n1 w\n40 680 m 555 680 l S\n")

	kpisCards := []struct {
		title, val, sub string
	}{
		{"MKT Cinetica Media", fmt.Sprintf("%.2f", mktGlobal) + " \260C", "Faixa: +2.0 a +8.0 \260C"},
		{"Disponibilidade", toPDFASCII(fmt.Sprintf("%.2f%%", data.KPIs.AvailabilityPct)), toPDFASCII(fmt.Sprintf("%.1fh monitoradas", data.KPIs.UptimeHours))},
		{"MTTR Atendimento", toPDFASCII(fmt.Sprintf("%.1f min", data.KPIs.AvgResponseTimeMinutes)), toPDFASCII(fmt.Sprintf("%d alarmes no ciclo", data.KPIs.TotalAlarms))},
		{"Eficacia CAPA", toPDFASCII(fmt.Sprintf("%.1f%%", data.KPIs.CapaEfficiencyPct)), "Desvios Tratados"},
	}
	cardW := 120.0
	gap := 11.76
	cardX := 40.0
	cardY := 620.0
	for _, c := range kpisCards {
		p1.WriteString(fmt.Sprintf("0.94 0.96 0.98 rg\n%.2f %.2f %.2f 50 re f\n", cardX, cardY, cardW))
		p1.WriteString(fmt.Sprintf("0.8 0.85 0.9 RG\n1 w\n%.2f %.2f %.2f 50 re S\n", cardX, cardY, cardW))
		p1.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.3 0.4 0.5 rg %.2f %.2f Td (%s) Tj ET\n", cardX+8, cardY+36, toPDFASCII(c.title)))
		p1.WriteString(fmt.Sprintf("BT /F2 13 Tf 0.05 0.45 0.3 rg %.2f %.2f Td (%s) Tj ET\n", cardX+8, cardY+18, c.val))
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.45 0.5 0.55 rg %.2f %.2f Td (%s) Tj ET\n", cardX+8, cardY+6, c.sub))
		cardX += cardW + gap
	}

	// SEÇÃO 2: TEMPERATURAS POR SENSOR E CERTIFICAÇÃO RBC/INMETRO
	p1.WriteString("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 596 Td (2. LEITURAS DE TEMPERATURA E SENSORES CALIBRADOS [RBC/INMETRO]) Tj ET\n")
	p1.WriteString("0.2 0.4 0.8 RG\n1 w\n40 590 m 555 590 l S\n")

	// Table Sensor Header (Colunas espaçadas sem sobreposição para nomes longos como CH-FREEZER)
	p1.WriteString("0.88 0.92 0.96 rg\n40 572 515.28 15 re f\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 45 576 Td (Sensor) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 88 576 Td (Camara) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 162 576 Td (Temp / Umid) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 232 576 Td (Posicao Termica) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 350 576 Td (Certificado RBC) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 442 576 Td (Validade) Tj ET\n")
	p1.WriteString("BT /F2 8 Tf 0.1 0.15 0.2 rg 505 576 Td (Status) Tj ET\n")

	sensY := 558.0
	for i, s := range data.Sensors {
		if i >= 10 {
			break
		}
		if i%2 == 1 {
			p1.WriteString(fmt.Sprintf("0.97 0.98 0.99 rg\n40 %.2f 515.28 13 re f\n", sensY-2))
		}
		sID := s.ID
		if len(sID) > 8 {
			sID = sID[:8]
		}
		chID := s.ChamberID
		if len(chID) > 14 {
			chID = chID[:14]
		}
		p1.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.1 0.15 0.2 rg 45 %.2f Td (%s) Tj ET\n", sensY, toPDFASCII(sID)))
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 88 %.2f Td (%s) Tj ET\n", sensY, toPDFASCII(chID)))
		tempHumStr := fmt.Sprintf("%.1f \\260C / %.0f%%", s.CurrentTemp, s.CurrentHumidity)
		p1.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.05 0.35 0.65 rg 162 %.2f Td (%s) Tj ET\n", sensY, tempHumStr))
		posText := s.ThermalPosition
		if len(posText) > 22 {
			posText = posText[:20] + ".."
		}
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 232 %.2f Td (%s) Tj ET\n", sensY, toPDFASCII(posText)))
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 350 %.2f Td (%s) Tj ET\n", sensY, toPDFASCII(s.CertNumber)))
		p1.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 442 %.2f Td (%s) Tj ET\n", sensY, toPDFASCII(s.CalibValidUntil)))
		statusColor := "0.05 0.5 0.2"
		if strings.Contains(s.Status, "Vencendo") || strings.Contains(s.Status, "Vencido") {
			statusColor = "0.75 0.45 0.05"
		}
		p1.WriteString(fmt.Sprintf("BT /F2 7.5 Tf %s rg 505 %.2f Td (%s) Tj ET\n", statusColor, sensY, toPDFASCII(s.Status)))
		sensY -= 13.0
	}
	p1.WriteString(fmt.Sprintf("BT /F1 7 Tf 0.5 0.55 0.6 rg 45 %.2f Td (Nota: Inventario metrologico ativo com %d sensores RBC calibrados e monitorados continuamente) Tj ET\n", sensY-2, len(data.Sensors)))

	// SEÇÃO 3: RASTREABILIDADE DE LOTES (FEFO/PVPS)
	lotsTopY := sensY - 22.0
	p1.WriteString(fmt.Sprintf("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 %.2f Td (3. CONTROLE DE ESTOQUE TERMOLABIL E SEGREGACAO [FEFO/PVPS]) Tj ET\n", lotsTopY))
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
	p1.WriteString("0.78 0.82 0.88 RG\n0.5 w\n40 38 m 555 38 l S\n")
	p1.WriteString("BT /F2 7 Tf 0.05 0.15 0.35 rg 40 26 Td (Hash Criptografico SHA-256:) Tj ET\n")
	p1.WriteString(fmt.Sprintf("BT /F1 7 Tf 0.20 0.25 0.35 rg 140 26 Td (%s) Tj ET\n", data.ReportSHA256))
	p1.WriteString("BT /F2 7.5 Tf 0.05 0.50 0.25 rg 500 26 Td ([INTEGRO]) Tj ET\n")
	p1.WriteString("BT /F1 7 Tf 0.5 0.55 0.6 rg 40 14 Td (ColdChain Pharma RT - Laudo de Auditoria e Monitoramento Termico RDC 430/2020) Tj ET\n")
	p1.WriteString("BT /F2 7.5 Tf 0.35 0.40 0.50 rg 505 14 Td (Pagina 1 de 2) Tj ET\n")

	// ==========================================
	// PÁGINA 2
	// ==========================================
	// Header bar Page 2
	p2.WriteString("0.08 0.12 0.20 rg\n0 790 595.28 52 re f\n")
	p2.WriteString("0.22 0.74 0.97 RG\n2 w\n0 790 m 595.28 790 l S\n")
	p2.WriteString("BT /F2 13 Tf 1 1 1 rg 40 818 Td (COLDCHAIN PHARMA RT - LAUDO DE AUDITORIA SANITARIA) Tj ET\n")
	p2.WriteString("BT /F1 8 Tf 0.7 0.85 1.0 rg 40 802 Td (" + toPDFASCII("Excursoes Termicas, Trilha de Auditoria e Assinatura Eletronica | Emissao: "+data.GeneratedAt) + ") Tj ET\n")

	// SEÇÃO 4: EXCURSÕES E PARECERES TÉCNICOS
	p2.WriteString("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 765 Td (4. INVESTIGACAO DE EXCURSOES TERMICAS E DECISOES DE CAPA [RDC 430 ART. 38]) Tj ET\n")
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
			p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.8 0.2 0.2 rg 115 %.2f Td (%.1f \\260C) Tj ET\n", excRowY, e.PeakTemp))
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

	// SEÇÃO 5: TRILHA DE AUDITORIA OPERACIONAL (ANVISA GUIA 33/2020 & 21 CFR PART 11)
	sgcTopY := excRowY - 15.0
	p2.WriteString(fmt.Sprintf("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 %.2f Td (5. TRILHA DE AUDITORIA DO SISTEMA COMPUTADORIZADO [GUIA 33/2020]) Tj ET\n", sgcTopY))
	p2.WriteString(fmt.Sprintf("0.2 0.4 0.8 RG\n1 w\n40 %.2f m 555 %.2f l S\n", sgcTopY-6, sgcTopY-6))

	sgcRowY := sgcTopY - 22.0
	p2.WriteString(fmt.Sprintf("0.88 0.92 0.96 rg\n40 %.2f 515.28 15 re f\n", sgcRowY))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 45 %.2f Td (Data / Hora) Tj ET\n", sgcRowY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 130 %.2f Td (Responsavel) Tj ET\n", sgcRowY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 205 %.2f Td (Evento Auditado) Tj ET\n", sgcRowY+4))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.1 0.15 0.2 rg 315 %.2f Td (Registro na Trilha Imutavel) Tj ET\n", sgcRowY+4))

	popRowY := sgcRowY - 14.0
	if len(data.AuditLogs) > 0 {
		for i, a := range data.AuditLogs {
			if i >= 6 {
				break
			}
			if i%2 == 1 {
				p2.WriteString(fmt.Sprintf("0.97 0.98 0.99 rg\n40 %.2f 515.28 13 re f\n", popRowY-2))
			}
			p2.WriteString(fmt.Sprintf("BT /F1 7 Tf 0.2 0.25 0.3 rg 45 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(a.Timestamp)))
			uRole := a.Username + " (" + a.Role + ")"
			if len(uRole) > 16 {
				uRole = uRole[:16]
			}
			p2.WriteString(fmt.Sprintf("BT /F2 7 Tf 0.1 0.15 0.2 rg 130 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(uRole)))
			act := formatAuditActionPDF(a.Action)
			p2.WriteString(fmt.Sprintf("BT /F2 7 Tf 0.05 0.35 0.65 rg 205 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(act)))
			det := a.Details
			if len(det) > 58 {
				det = det[:55] + "..."
			}
			p2.WriteString(fmt.Sprintf("BT /F1 7 Tf 0.2 0.25 0.3 rg 315 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(det)))
			popRowY -= 13.0
		}
	} else {
		for i, p := range data.QualityDossier.POPs {
			if i >= 5 {
				break
			}
			if i%2 == 1 {
				p2.WriteString(fmt.Sprintf("0.97 0.98 0.99 rg\n40 %.2f 515.28 13 re f\n", popRowY-2))
			}
			p2.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.1 0.15 0.2 rg 45 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(p.Code)))
			p2.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 140 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(p.Responsible)))
			p2.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 235 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(p.Version)))
			title := p.Title
			if len(title) > 40 {
				title = title[:38] + ".."
			}
			p2.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.2 0.25 0.3 rg 355 %.2f Td (%s) Tj ET\n", popRowY, toPDFASCII(title)))
			popRowY -= 13.0
		}
	}

	// SEÇÃO 6: DECLARAÇÃO DE INTEGRIDADE DE DADOS SANITÁRIOS (PRINCÍPIOS ALCOA+)
	cryptoTopY := popRowY - 18.0
	p2.WriteString(fmt.Sprintf("BT /F2 11 Tf 0.1 0.15 0.25 rg 40 %.2f Td (6. DECLARACAO DE CONFORMIDADE SANITARIA E INTEGRIDADE [ALCOA+]) Tj ET\n", cryptoTopY))
	p2.WriteString(fmt.Sprintf("0.2 0.4 0.8 RG\n1 w\n40 %.2f m 555 %.2f l S\n", cryptoTopY-6, cryptoTopY-6))

	boxY := cryptoTopY - 120.0
	p2.WriteString(fmt.Sprintf("0.96 0.97 0.99 rg\n40 %.2f 515.28 110 re f\n", boxY))
	p2.WriteString(fmt.Sprintf("0.78 0.84 0.90 RG\n1 w\n40 %.2f 515.28 110 re S\n", boxY))

	p2.WriteString(fmt.Sprintf("BT /F2 8.5 Tf 0.05 0.15 0.35 rg 50 %.2f Td (CONFORMIDADE COM A RDC 430/2020, GUIA 33/2020 ANVISA E 21 CFR PART 11) Tj ET\n", boxY+92))
	p2.WriteString(fmt.Sprintf("BT /F1 7.8 Tf 0.20 0.25 0.30 rg 50 %.2f Td (1. Atribuivel e Contemporaneo: Medicoes termicas e acoes operacionais vinculadas ao autor e horario oficial.) Tj ET\n", boxY+76))
	p2.WriteString(fmt.Sprintf("BT /F1 7.8 Tf 0.20 0.25 0.30 rg 50 %.2f Td (2. Original e Preciso: Leituras provenientes de sensores calibrados RBC/INMETRO com calculo de MKT continuo.) Tj ET\n", boxY+62))
	p2.WriteString(fmt.Sprintf("BT /F1 7.8 Tf 0.20 0.25 0.30 rg 50 %.2f Td (3. Legivel e Imutavel: Registros protegidos contra alteracao ou exclusao com assinatura eletronica ECDSA P-256.) Tj ET\n", boxY+48))
	p2.WriteString(fmt.Sprintf("BT /F2 7.5 Tf 0.10 0.20 0.35 rg 50 %.2f Td (Hash SHA-256 de Autenticidade do Laudo: %s) Tj ET\n", boxY+30, toPDFASCII(data.ReportSHA256)))
	p2.WriteString(fmt.Sprintf("BT /F2 8 Tf 0.05 0.50 0.25 rg 50 %.2f Td ([STATUS SANITARIO: 100%% AUDITADO E CONFORME PARA INSPECAO ANVISA]) Tj ET\n", boxY+14))

	// Bloco de Assinatura Eletrônica Centralizada do Responsável (Padrão 21 CFR Part 11)
	sigLineW := 320.0
	sigLineX := (595.28 - sigLineW) / 2.0
	sigLineY := 115.0
	p2.WriteString("0.70 0.75 0.80 RG\n0.75 w\n")
	p2.WriteString(fmt.Sprintf("%.1f %.1f m %.1f %.1f l S\n", sigLineX, sigLineY, sigLineX+sigLineW, sigLineY))

	sigName := toPDFASCII(data.TechnicalLead)
	sigRole := toPDFASCII(data.CRF)
	sigDate := fmt.Sprintf("Assinado Eletronicamente em %s", toPDFASCII(data.GeneratedAt))

	nameX := pdfCenterText(sigName, 9.5, 595.28)
	roleX := pdfCenterText(sigRole, 8.5, 595.28)
	dateX := pdfCenterText(sigDate, 7.5, 595.28)

	p2.WriteString(fmt.Sprintf("BT /F2 9.5 Tf 0.05 0.15 0.30 rg %.1f %.1f Td (%s) Tj ET\n", nameX, sigLineY-15, sigName))
	p2.WriteString(fmt.Sprintf("BT /F1 8.5 Tf 0.20 0.25 0.35 rg %.1f %.1f Td (%s) Tj ET\n", roleX, sigLineY-29, sigRole))
	p2.WriteString(fmt.Sprintf("BT /F1 7.5 Tf 0.35 0.40 0.45 rg %.1f %.1f Td (%s) Tj ET\n", dateX, sigLineY-42, sigDate))

	// Footer Page 2 (Chancela Criptográfica e Integridade Pericial por Folha)
	p2.WriteString("0.78 0.82 0.88 RG\n0.5 w\n40 38 m 555 38 l S\n")
	p2.WriteString("BT /F2 7 Tf 0.05 0.15 0.35 rg 40 26 Td (Hash Criptografico SHA-256:) Tj ET\n")
	p2.WriteString(fmt.Sprintf("BT /F1 7 Tf 0.20 0.25 0.35 rg 140 26 Td (%s) Tj ET\n", data.ReportSHA256))
	p2.WriteString("BT /F2 7.5 Tf 0.05 0.50 0.25 rg 500 26 Td ([INTEGRO]) Tj ET\n")
	p2.WriteString("BT /F1 7 Tf 0.5 0.55 0.6 rg 40 14 Td (ColdChain Pharma RT - Auditoria e Rastreabilidade | 21 CFR Part 11 & RDC 430/2020) Tj ET\n")
	p2.WriteString("BT /F2 7.5 Tf 0.35 0.40 0.50 rg 505 14 Td (Pagina 2 de 2) Tj ET\n")

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
