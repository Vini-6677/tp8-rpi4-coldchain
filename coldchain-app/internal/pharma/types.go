package pharma

// Chamber representa uma câmara fria, geladeira ou freezer qualificado pela ANVISA.
type Chamber struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Type                 string   `json:"type"` // "camara_fria", "geladeira", "freezer"
	TempMin              float64  `json:"temp_min"`
	TempMax              float64  `json:"temp_max"`
	HumidityMin          float64  `json:"humidity_min"`
	HumidityMax          float64  `json:"humidity_max"`
	AlarmDelayMin        int      `json:"alarm_delay_min"`
	Status               string   `json:"status"` // "OK", "ALERTA", "EXCURSAO", "OFFLINE"
	QualStatus           string   `json:"qualification_status"`
	QualValidUntil       string   `json:"qualification_valid_until"`
	SensorCount          int      `json:"sensor_count"`
	AvgTemp              float64  `json:"avg_temp"`
	MinTemp24h           float64  `json:"min_temp_24h"`
	MaxTemp24h           float64  `json:"max_temp_24h"`
	OpenAlarmsCount      int      `json:"open_alarms_count"`
	QuarantinedLotsCount int      `json:"quarantined_lots_count"`
	LastReadingTime      string   `json:"last_reading_time"`
	CreatedAt            string   `json:"created_at"`
}

// Sensor representa um sensor metrológico físico vinculado a um ponto do mapeamento térmico.
type Sensor struct {
	ID              string  `json:"id"`
	ChamberID       string  `json:"chamber_id"`
	SerialNumber    string  `json:"serial_number"`
	ThermalPosition string  `json:"thermal_position"` // Ex: "Ponto Mais Quente (Superior Frontal)"
	CertNumber      string  `json:"cert_number"`
	CalibratedBy    string  `json:"calibrated_by"`
	CalibratedAt    string  `json:"calibrated_at"`
	CalibValidUntil string  `json:"calib_valid_until"`
	Status          string  `json:"status"` // "Ativo", "Vencendo", "Vencido"
	BatteryPct      int     `json:"battery_pct"`
	CurrentTemp     float64 `json:"current_temp"`
	CurrentHumidity float64 `json:"current_humidity"`
	LastSeen        string  `json:"last_seen"`
}

// Product representa o cadastro de medicamento no catálogo ANVISA.
type Product struct {
	ID             int64   `json:"id"`
	TradeName      string  `json:"trade_name"`
	DCB            string  `json:"dcb"`
	Presentation   string  `json:"presentation"`
	Manufacturer   string  `json:"manufacturer"`
	AnvisaReg      string  `json:"anvisa_reg"`
	TempMin        float64 `json:"temp_min"`
	TempMax        float64 `json:"temp_max"`
	HumidityMin    float64 `json:"humidity_min"`
	HumidityMax    float64 `json:"humidity_max"`
	Photosensitive int     `json:"photosensitive"` // 1 = Sim, 0 = Não
	CreatedAt      string  `json:"created_at"`
}

// Lot representa um lote físico armazenado com controle estrito de validade e FEFO/PVPS.
type Lot struct {
	ID               int64   `json:"id"`
	ProductID        int64   `json:"product_id"`
	ProductTradeName string  `json:"product_trade_name"`
	ProductDCB       string  `json:"product_dcb"`
	Manufacturer     string  `json:"manufacturer"`
	AnvisaReg        string  `json:"anvisa_reg"`
	LotNumber        string  `json:"lot_number"`
	ExpiryDate       string  `json:"expiry_date"` // YYYY-MM-DD
	Quantity         int     `json:"quantity"`
	ChamberID        string  `json:"chamber_id"`
	ChamberName      string  `json:"chamber_name"`
	ShelfLocation    string  `json:"shelf_location"`
	Status           string  `json:"status"` // "liberado", "quarentena", "reprovado", "recolhido", "devolvido", "vencido"
	UnitPrice        float64 `json:"unit_price"`
	DaysToExpiry     int     `json:"days_to_expiry"`
	FefoRank         int     `json:"fefo_rank"`
	CreatedAt        string  `json:"created_at"`
}

// StockEntry representa o recebimento na doca com conferência física e térmica.
type StockEntry struct {
	ID                     int64   `json:"id"`
	LotID                  int64   `json:"lot_id"`
	LotNumber              string  `json:"lot_number"`
	ProductTradeName       string  `json:"product_trade_name"`
	Supplier               string  `json:"supplier"`
	SupplierCNPJ           string  `json:"supplier_cnpj"`
	InvoiceNumber          string  `json:"invoice_number"`
	ReceivedAt             string  `json:"received_at"`
	TransportCondition     string  `json:"transport_condition"`
	ArrivalTemp            float64 `json:"arrival_temp"`
	CargoIntegrity         string  `json:"cargo_integrity"`
	InvoiceMatch           int     `json:"invoice_match"`
	AmbientExposureMinutes int     `json:"ambient_exposure_minutes"`
	ResponsibleName        string  `json:"responsible_name"`
	Decision               string  `json:"decision"` // "Aprovado", "Quarentena", "Reprovado"
}

// StockExit representa uma saída de medicamento com conferência de lote e FEFO.
type StockExit struct {
	ID               int64   `json:"id"`
	LotID            int64   `json:"lot_id"`
	LotNumber        string  `json:"lot_number"`
	ProductTradeName string  `json:"product_trade_name"`
	Quantity         int     `json:"quantity"`
	Reason           string  `json:"reason"` // "dispensacao", "transferencia", "devolucao", "perda", "descarte", "recolhimento"
	Recipient        string  `json:"recipient"`
	InvoiceNumber    string  `json:"invoice_number"`
	ResponsibleName  string  `json:"responsible_name"`
	ExitAt           string  `json:"exit_at"`
}

// Excursion representa uma ocorrência de temperatura fora da faixa com processo investigativo ANVISA.
type Excursion struct {
	ID                       int64   `json:"id"`
	Code                     string  `json:"code"` // Ex: "EXC-2026-001"
	ChamberID                string  `json:"chamber_id"`
	ChamberName              string  `json:"chamber_name"`
	SensorID                 string  `json:"sensor_id"`
	StartTime                string  `json:"start_time"`
	EndTime                  string  `json:"end_time"`
	PeakTemp                 float64 `json:"peak_temp"`
	MinTemp                  float64 `json:"min_temp"`
	AccumulatedDegreeMinutes float64 `json:"accumulated_degree_minutes"`
	Cause                    string  `json:"cause"`
	Status                   string  `json:"status"` // "em_investigacao", "fabricante_consultado", "liberado", "descartado"
	QuarantinedLotsCount     int     `json:"quarantined_lots_count"`
	TechnicalReport          string  `json:"technical_report"`
	ManufacturerNotified     int     `json:"manufacturer_notified"`
	ManufacturerDecision     string  `json:"manufacturer_decision"`
	FinalDecision            string  `json:"final_decision"`
	ElectronicSig            string  `json:"electronic_sig"`
	DecidedBy                string  `json:"decided_by"`
	DecidedAt                string  `json:"decided_at"`
}

// Alarm representa um alarme disparado no sistema com fluxo de reconhecimento formal.
type Alarm struct {
	ID              int64   `json:"id"`
	ChamberID       string  `json:"chamber_id"`
	ChamberName     string  `json:"chamber_name"`
	SensorID        string  `json:"sensor_id"`
	Type            string  `json:"type"` // "temperatura_alta", "temperatura_baixa", "sensor_offline", "bateria_baixa", "falha_energia", "porta_aberta"
	Message         string  `json:"message"`
	Value           float64 `json:"value"`
	Threshold       float64 `json:"threshold"`
	Status          string  `json:"status"` // "ativo", "reconhecido", "resolvido"
	EscalationLevel int     `json:"escalation_level"`
	AcknowledgedBy  string  `json:"acknowledged_by"`
	AcknowledgedAt  string  `json:"acknowledged_at"`
	ActionTaken     string  `json:"action_taken"`
	CreatedAt       string  `json:"created_at"`
	ResolvedAt      string  `json:"resolved_at"`
}

// LotHistory registra toda movimentação, excursão ou mudança de status sofrida pelo lote.
type LotHistory struct {
	ID        int64  `json:"id"`
	LotID     int64  `json:"lot_id"`
	EventType string `json:"event_type"`
	Details   string `json:"details"`
	ChamberID string `json:"chamber_id"`
	CreatedAt string `json:"created_at"`
}

// LotTraceReport reúne a linha do tempo pericial completa de um lote para fiscalização.
type LotTraceReport struct {
	Lot            Lot          `json:"lot"`
	Product        Product      `json:"product"`
	StockEntry     *StockEntry  `json:"stock_entry,omitempty"`
	History        []LotHistory `json:"history"`
	Excursions     []Excursion  `json:"excursions"`
	Exits          []StockExit  `json:"exits"`
	TotalExitedQty int          `json:"total_exited_qty"`
	RemainingQty   int          `json:"remaining_qty"`
	IntegrityALCOA string       `json:"integrity_alcoa"`
}


// PharmaKPIs reúne os indicadores de desempenho regulatórios auditados pela ANVISA (RDC 430/2020 / HageLab 2025).
type PharmaKPIs struct {
	AvailabilityPct        float64 `json:"availability_pct"`          // Meta: >= 99.5%
	AvgResponseTimeMinutes float64 `json:"avg_response_time_minutes"`  // Meta: <= 30 min
	ExcursionRatePct       float64 `json:"excursion_rate_pct"`         // Meta: <= 0.1%
	CapaEfficiencyPct      float64 `json:"capa_efficiency_pct"`        // Meta: >= 95.0%
	TotalMonitoredHours    float64 `json:"total_monitored_hours"`
	UptimeHours            float64 `json:"uptime_hours"`
	TotalAlarms            int     `json:"total_alarms"`
	AcknowledgedAlarms     int     `json:"acknowledged_alarms"`
	ActiveAlarms           int     `json:"active_alarms"`
	TotalSensors           int     `json:"total_sensors"`
	CalibratedSensors      int     `json:"calibrated_sensors"`
	ExpiringSensors        int     `json:"expiring_sensors"`
	RetentionYears         int     `json:"retention_years"`            // 5 anos (RDC 430 Art. 60)
	LastAuditDate          string  `json:"last_audit_date"`
	ComplianceStatus       string  `json:"compliance_status"`         // "100% Conforme"
}

// POPItem representa um Procedimento Operacional Padrão da Garantia da Qualidade.
type POPItem struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	LastReview  string `json:"last_review"`
	NextReview  string `json:"next_review"`
	Responsible string `json:"responsible"`
	Status      string `json:"status"` // "Vigente", "Em Revisão"
}

// QualificationItem representa uma fase de qualificação de câmara (DQ/IQ/OQ/PQ) e autonomia térmica.
type QualificationItem struct {
	ChamberID       string `json:"chamber_id"`
	ChamberName     string `json:"chamber_name"`
	Stage           string `json:"stage"` // "DQ", "IQ", "OQ", "PQ"
	ReportNumber    string `json:"report_number"`
	ExecutedBy      string `json:"executed_by"`
	ExecutionDate   string `json:"execution_date"`
	ValidUntil      string `json:"valid_until"`
	ThermalAutonomy string `json:"thermal_autonomy"` // Ex: "6h 30min sem energia (limite 8°C)"
	Status          string `json:"status"`           // "Aprovado", "Pendente"
}

// TrainingRecord representa o treinamento de um colaborador nas normas e POPs.
type TrainingRecord struct {
	EmployeeName string `json:"employee_name"`
	Role         string `json:"role"`
	POPCode      string `json:"pop_code"`
	TrainedAt    string `json:"trained_at"`
	ScorePct     int    `json:"score_pct"`
	Status       string `json:"status"` // "Apto", "Pendente Reciclagem"
}

// ChecklistItem representa um item de conformidade da autoinspeção sanitária RDC 430.
type ChecklistItem struct {
	Item        string `json:"item"`
	Requirement string `json:"requirement"`
	Evidence    string `json:"evidence"`
	Compliant   bool   `json:"compliant"`
}

// QualityDossier engloba todo o dossiê da garantia da qualidade pronto para inspeção sanitária.
type QualityDossier struct {
	POPs           []POPItem           `json:"pops"`
	Qualifications []QualificationItem `json:"qualifications"`
	Trainings      []TrainingRecord    `json:"trainings"`
	ChecklistItems []ChecklistItem     `json:"checklist_items"`
	GeneratedAt    string              `json:"generated_at"`
	TechnicalLead  string              `json:"technical_lead"`
	CRF            string              `json:"crf"`
}
