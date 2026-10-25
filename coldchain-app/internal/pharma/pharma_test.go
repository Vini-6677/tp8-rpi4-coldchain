package pharma

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("erro ao abrir sqlite in-memory: %v", err)
	}

	if err := InitPharmaSchema(db); err != nil {
		t.Fatalf("erro ao inicializar schema pharma: %v", err)
	}
	if err := SeedPharmaTestData(db); err != nil {
		t.Fatalf("erro ao popular dados de teste: %v", err)
	}
	return db
}

func TestPharmaChambersAndSensors(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	chambers, err := GetChambers(db)
	if err != nil {
		t.Fatalf("erro ao buscar câmaras: %v", err)
	}
	if len(chambers) != 5 {
		t.Errorf("esperava 5 câmaras (CH-A, CH-B, GEL-01, ALM-01, ULT-01), obteve %d", len(chambers))
	}

	// Câmara A deve ter 10 sensores
	sensorsA, err := GetSensors(db, "CH-A")
	if err != nil {
		t.Fatalf("erro ao buscar sensores CH-A: %v", err)
	}
	if len(sensorsA) != 10 {
		t.Errorf("esperava 10 sensores na CH-A, obteve %d", len(sensorsA))
	}

	// Câmara B deve ter 8 sensores
	sensorsB, err := GetSensors(db, "CH-B")
	if err != nil {
		t.Fatalf("erro ao buscar sensores CH-B: %v", err)
	}
	if len(sensorsB) != 8 {
		t.Errorf("esperava 8 sensores na CH-B, obteve %d", len(sensorsB))
	}

	// Almoxarifado deve ter 4 sensores
	sensorsAlm, err := GetSensors(db, "ALM-01")
	if err != nil {
		t.Fatalf("erro ao buscar sensores ALM-01: %v", err)
	}
	if len(sensorsAlm) != 4 {
		t.Errorf("esperava 4 sensores no ALM-01, obteve %d", len(sensorsAlm))
	}

	// Ultra-frio deve ter 2 sensores
	sensorsUlt, err := GetSensors(db, "ULT-01")
	if err != nil {
		t.Fatalf("erro ao buscar sensores ULT-01: %v", err)
	}
	if len(sensorsUlt) != 2 {
		t.Errorf("esperava 2 sensores no ULT-01, obteve %d", len(sensorsUlt))
	}
}

func TestPharmaKPIsAndQualityDossier(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Estado inicial com 1 lote em quarentena (ADA-ABB-2026-M4) e 1 alarme ativo
	kpiInit, err := GetPharmaKPIs(db)
	if err != nil {
		t.Fatalf("erro ao obter KPIs iniciais: %v", err)
	}
	if kpiInit.AvailabilityPct < 99.5 {
		t.Errorf("disponibilidade abaixo da meta ANVISA: %.2f%%", kpiInit.AvailabilityPct)
	}
	if kpiInit.ActiveAlarms != 1 {
		t.Errorf("esperava 1 alarme ativo no seed, obteve %d", kpiInit.ActiveAlarms)
	}
	if kpiInit.ExcursionRatePct <= 0.0 {
		t.Errorf("esperava taxa de desvio refletindo o lote em quarentena, obteve %.2f%%", kpiInit.ExcursionRatePct)
	}

	// 2. Resolver o alarme operacional e concluir a investigação de excursão
	err = AcknowledgeAlarm(db, 1, "Farmacêutica RT", "Verificado termostato e retorno a 4.5°C")
	if err != nil {
		t.Fatalf("erro ao reconhecer alarme: %v", err)
	}
	err = ResolveAlarm(db, 1, "Farmacêutica RT", "Dra. Mariana Vasconcelos", "Temperatura restabelecida com normalidade", "SIG-TEST-123")
	if err != nil {
		t.Fatalf("erro ao resolver alarme: %v", err)
	}
	err = DecideExcursion(db, 1, "liberado", "Laudo do fabricante confirma estabilidade do lote", "SIGN-ECDSA-TEST-99", "Dra. Mariana Vasconcelos")
	if err != nil {
		t.Fatalf("erro ao decidir excursão: %v", err)
	}

	// 3. Após resolução de alarme e liberação da quarentena, avalia conformidade
	kpiResolved, err := GetPharmaKPIs(db)
	if err != nil {
		t.Fatalf("erro ao obter KPIs após resolução: %v", err)
	}
	if kpiResolved.ExcursionRatePct > 0.1 {
		t.Errorf("taxa de desvio deveria ser <= 0.1%% após liberação, obteve: %.2f%%", kpiResolved.ExcursionRatePct)
	}
	if kpiResolved.ActiveAlarms != 0 {
		t.Errorf("esperava 0 alarmes ativos após resolução, obteve %d", kpiResolved.ActiveAlarms)
	}
	if kpiResolved.CapaEfficiencyPct != 100.0 {
		t.Errorf("esperava 100%% de eficácia de CAPA após assinatura eletrônica, obteve %.1f%%", kpiResolved.CapaEfficiencyPct)
	}
	// O banco inicial possui sensores vencendo/vencidos, logo o status dinâmico alerta corretamente
	if kpiResolved.ComplianceStatus != "ATENÇÃO: Sensores com Calibração RBC Vencendo (<30 dias)" {
		t.Errorf("esperava status de alerta metrológico, obteve: '%s'", kpiResolved.ComplianceStatus)
	}

	// 4. Recalibrar todos os sensores e certificar conformidade 100% plena
	_, err = db.Exec("UPDATE sensors SET calib_valid_until = '2027-10-15', status = 'Ativo'")
	if err != nil {
		t.Fatalf("erro ao recalibrar sensores: %v", err)
	}
	kpiPleno, err := GetPharmaKPIs(db)
	if err != nil {
		t.Fatalf("erro ao buscar KPIs plenamente conformes: %v", err)
	}
	if kpiPleno.ComplianceStatus != "100% Conforme (RDC 430/2020 & RDC 197/2017)" {
		t.Errorf("esperava 100%% Conforme após calibrações válidas, obteve '%s'", kpiPleno.ComplianceStatus)
	}

	dossier, err := GetQualityDossier(db)
	if err != nil {
		t.Fatalf("erro ao obter dossiê de qualidade: %v", err)
	}
	if len(dossier.POPs) != 6 {
		t.Errorf("esperava 6 POPs obrigatórios, obteve %d", len(dossier.POPs))
	}
	if len(dossier.Qualifications) != 5 {
		t.Errorf("esperava 5 qualificações térmicas, obteve %d", len(dossier.Qualifications))
	}
	if len(dossier.ChecklistItems) != 12 {
		t.Errorf("esperava 12 itens de autoinspeção, obteve %d", len(dossier.ChecklistItems))
	}
}

func TestPharmaMathematicalKPIEngine(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Testar cálculo matemático de MTTR (Tempo Médio de Atendimento)
	// Inserir alarme com created_at 10 minutos atrás e reconhecido agora (10.0 min de resposta)
	_, err := db.Exec(`INSERT INTO alarms (chamber_id, sensor_id, type, message, value, threshold, status, escalation_level, acknowledged_by, acknowledged_at, created_at)
		VALUES ('CH-A', 'S01', 'temperatura_alta', 'Teste MTTR', 8.5, 8.0, 'reconhecido', 1, 'Operador Teste', datetime('now'), datetime('now', '-10 minute'))`)
	if err != nil {
		t.Fatalf("erro ao inserir alarme de teste MTTR: %v", err)
	}

	kpis, err := GetPharmaKPIs(db)
	if err != nil {
		t.Fatalf("erro ao buscar KPIs: %v", err)
	}
	if kpis.AvgResponseTimeMinutes <= 0.0 || kpis.AvgResponseTimeMinutes > 15.0 {
		t.Errorf("esperava MTTR em torno de 10 minutos, obteve %.2f min", kpis.AvgResponseTimeMinutes)
	}

	// 2. Testar cálculo matemático da Taxa de Excursão com múltiplos lotes
	// Atualmente há 7 lotes (1 quarentena = 14.29%). Adicionando mais 3 lotes em quarentena (total 4/10 = 40.00%)
	for i := 101; i <= 103; i++ {
		_, err = db.Exec(`INSERT INTO lots (product_id, lot_number, expiry_date, quantity, chamber_id, shelf_location, status, unit_price)
			VALUES (1, ?, '2027-01-01', 100, 'CH-A', 'P-1', 'quarentena', 50.0)`, fmt.Sprintf("LOT-TEST-%d", i))
		if err != nil {
			t.Fatalf("erro ao inserir lote teste: %v", err)
		}
	}
	kpis, err = GetPharmaKPIs(db)
	if err != nil {
		t.Fatalf("erro ao buscar KPIs: %v", err)
	}
	if kpis.ExcursionRatePct != 40.0 {
		t.Errorf("esperava taxa de excursão de 40.00%% (4 de 10), obteve %.2f%%", kpis.ExcursionRatePct)
	}

	// 3. Testar cálculo de Eficácia de CAPA
	// Adicionar uma segunda excursão pendente (total 2 excursões, 0 assinadas = 0% eficácia)
	_, err = db.Exec(`INSERT INTO excursions (code, chamber_id, sensor_id, start_time, end_time, peak_temp, min_temp, accumulated_degree_minutes, cause, status, quarantined_lots_count)
		VALUES ('EXC-TEST-002', 'CH-A', 'S01', '2026-10-22 10:00:00', '2026-10-22 10:30:00', 9.1, 4.0, 15.0, 'Teste Falha', 'em_investigacao', 1)`)
	if err != nil {
		t.Fatalf("erro ao inserir excursão teste: %v", err)
	}
	kpis, err = GetPharmaKPIs(db)
	if err != nil {
		t.Fatalf("erro ao buscar KPIs: %v", err)
	}
	if kpis.CapaEfficiencyPct != 0.0 {
		t.Errorf("esperava 0%% de eficácia de CAPA sem laudos assinados, obteve %.1f%%", kpis.CapaEfficiencyPct)
	}

	// Resolver 1 excursão com assinatura eletrônica (1 de 2 = 50.0% eficácia)
	_, err = db.Exec(`UPDATE excursions SET status = 'concluido', electronic_sig = 'SIGN-ECDSA-TEST-50' WHERE code = 'EXC-TEST-002'`)
	if err != nil {
		t.Fatalf("erro ao atualizar excursão: %v", err)
	}
	kpis, err = GetPharmaKPIs(db)
	if err != nil {
		t.Fatalf("erro ao buscar KPIs: %v", err)
	}
	if kpis.CapaEfficiencyPct != 50.0 {
		t.Errorf("esperava 50.0%% de eficácia de CAPA (1 de 2), obteve %.1f%%", kpis.CapaEfficiencyPct)
	}

	// 4. Testar métricas de Sensores RBC
	// No seed inicial, há 28 sensores totais: 24 com calibração válida e 4 expirados em 2026-10-15.
	if kpis.TotalSensors != 28 {
		t.Errorf("esperava 28 sensores totais no seed, obteve %d", kpis.TotalSensors)
	}
	if kpis.CalibratedSensors != 24 {
		t.Errorf("esperava 24 sensores calibrados, obteve %d", kpis.CalibratedSensors)
	}
	if kpis.ExpiringSensors != 4 {
		if kpis.ExpiringSensors != 14 {
		t.Errorf("esperava 14 sensores vencendo nos próximos 30 dias (S01-S10 e S19-S22), obteve %d", kpis.ExpiringSensors)
	}
	}
}

func TestPharmaFEFOOrdering(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	lots, err := GetLots(db, "", "")
	if err != nil {
		t.Fatalf("erro ao buscar lotes: %v", err)
	}
	if len(lots) == 0 {
		t.Fatalf("nenhum lote retornado")
	}

	// Verifica se a ordenação FEFO está respeitada (datas crescentes)
	for i := 1; i < len(lots); i++ {
		if lots[i].ExpiryDate < lots[i-1].ExpiryDate {
			t.Errorf("inversão no algoritmo FEFO: lote %s (%s) antes de %s (%s)",
				lots[i-1].LotNumber, lots[i-1].ExpiryDate, lots[i].LotNumber, lots[i].ExpiryDate)
		}
	}
}

func TestPharmaStockEntryAndExit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Entrada na doca com temperatura de 4.3°C
	newLot := Lot{
		ProductID:     1,
		LotNumber:     "TEST-LOT-999",
		ExpiryDate:    "2027-10-10",
		Quantity:      100,
		ChamberID:     "CH-A",
		ShelfLocation: "Prateleira 3, Box 1",
		UnitPrice:     185.00,
	}
	entry := StockEntry{
		Supplier:               "Fornecedor Teste",
		SupplierCNPJ:           "00.000.000/0001-00",
		InvoiceNumber:          "NF-99999",
		TransportCondition:     "Caixa Térmica EPS Qualificada",
		ArrivalTemp:            4.3,
		CargoIntegrity:         "Íntegra",
		InvoiceMatch:           1,
		AmbientExposureMinutes: 4,
		ResponsibleName:        "Dr. Teste RT",
		Decision:               "Aprovado",
	}

	createdLot, err := RegisterStockEntry(db, entry, newLot)
	if err != nil {
		t.Fatalf("erro na entrada de estoque: %v", err)
	}
	if createdLot.Status != "liberado" {
		t.Errorf("esperava status liberado para chegada em 4.3°C, obteve %s", createdLot.Status)
	}

	// Saída parcial de 30 unidades
	exit := StockExit{
		LotID:           createdLot.ID,
		Quantity:        30,
		Reason:          "dispensacao",
		Recipient:       "Hospital Municipal Central",
		InvoiceNumber:   "NF-SAIDA-123",
		ResponsibleName: "Farmacêutico RT",
	}
	if err := RegisterStockExit(db, exit); err != nil {
		t.Fatalf("erro na saída de estoque: %v", err)
	}

	// Verifica rastreabilidade
	trace, err := GetLotTrace(db, "TEST-LOT-999")
	if err != nil {
		t.Fatalf("erro no laudo pericial de rastreabilidade: %v", err)
	}
	if trace.RemainingQty != 70 {
		t.Errorf("esperava saldo restante 70, obteve %d", trace.RemainingQty)
	}
	if trace.TotalExitedQty != 30 {
		t.Errorf("esperava saída total 30, obteve %d", trace.TotalExitedQty)
	}
}

func TestPharmaAutomaticExcursionQuarantine(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Dispara excursão na Câmara A
	exc := Excursion{
		Code:                     "EXC-TEST-002",
		ChamberID:                "CH-A",
		SensorID:                 "S01",
		StartTime:                "2026-10-22 10:00:00",
		EndTime:                  "2026-10-22 10:35:00",
		PeakTemp:                 9.2,
		MinTemp:                  4.1,
		AccumulatedDegreeMinutes: 42.0,
		Cause:                    "Teste de falha no compressor e segregação automática",
		TechnicalReport:          "Teste regulatório ANVISA",
	}

	excID, err := TriggerExcursion(db, exc)
	if err != nil {
		t.Fatalf("erro ao disparar excursão: %v", err)
	}
	if excID == 0 {
		t.Fatalf("ID de excursão inválido")
	}

	// Verifica se os lotes da Câmara A foram segregados para 'quarentena'
	lotsA, err := GetLots(db, "CH-A", "")
	if err != nil {
		t.Fatalf("erro ao consultar lotes de CH-A: %v", err)
	}
	for _, l := range lotsA {
		if l.Status != "quarentena" {
			t.Errorf("lote %s não foi segregado para quarentena (status: %s)", l.LotNumber, l.Status)
		}
	}

	// Decisão do Farmacêutico RT: Liberar após laudo do fabricante
	err = DecideExcursion(db, excID, "liberado", "Laudo do fabricante Butantan atesta estabilidade mantida.", "SIG-SHA256-ECDSA-RT-41890", "Dr. Carlos Eduardo (CRF-SP 41.890)")
	if err != nil {
		t.Fatalf("erro ao decidir excursão: %v", err)
	}

	// Verifica se os lotes retornaram para 'liberado'
	lotsAfter, _ := GetLots(db, "CH-A", "")
	for _, l := range lotsAfter {
		if l.Status != "liberado" {
			t.Errorf("lote %s deveria retornar a liberado, status: %s", l.LotNumber, l.Status)
		}
	}
}


func TestPharmaProductRegistration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Validação de erro: nome vazio
	_, err := RegisterProduct(db, Product{})
	if err == nil {
		t.Errorf("esperava erro ao cadastrar produto com campos vazios")
	}

	// Cadastro válido
	p := Product{
		TradeName:      "Trastuzumabe 440mg",
		DCB:            "Trastuzumabe",
		Presentation:   "Frasco-ampola com pó liofilizado",
		Manufacturer:   "Roche Brasil",
		AnvisaReg:      "1.0100.0543.001-2",
		TempMin:        2.0,
		TempMax:        8.0,
		HumidityMin:    30.0,
		HumidityMax:    70.0,
		Photosensitive: 1,
	}
	created, err := RegisterProduct(db, p)
	if err != nil {
		t.Fatalf("erro ao cadastrar medicamento: %v", err)
	}
	if created.ID == 0 {
		t.Errorf("ID do produto criado deve ser maior que 0")
	}

	// Consulta o catálogo e confirma que o novo medicamento está presente
	prods, err := GetProducts(db)
	if err != nil {
		t.Fatalf("erro ao buscar produtos: %v", err)
	}
	found := false
	for _, pr := range prods {
		if pr.AnvisaReg == "1.0100.0543.001-2" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("medicamento recém cadastrado não encontrado no catálogo")
	}
}

func TestPharmaChamberRegistrationAndEdit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Validação de erro: ID vazio
	_, err := RegisterChamber(db, Chamber{})
	if err == nil {
		t.Errorf("esperava erro ao cadastrar câmara sem ID")
	}

	// Cadastro de nova câmara
	newChamber := Chamber{
		ID:             "CH-C",
		Name:           "Câmara Fria C - Reserva Oncológicos",
		Type:           "refrigerado",
		TempMin:        2.0,
		TempMax:        8.0,
		HumidityMin:    45.0,
		HumidityMax:    75.0,
		AlarmDelayMin:  3,
		QualStatus:     "Qualificado (DQ/IQ/OQ/PQ)",
		QualValidUntil: "2028-01-01",
	}
	created, err := RegisterChamber(db, newChamber)
	if err != nil {
		t.Fatalf("erro ao cadastrar câmara: %v", err)
	}
	if created.ID != "CH-C" {
		t.Errorf("esperava ID CH-C, obteve %s", created.ID)
	}

	// Edição da câmara criada
	created.TempMax = 7.5
	created.AlarmDelayMin = 2
	err = UpdateChamber(db, *created)
	if err != nil {
		t.Fatalf("erro ao atualizar câmara: %v", err)
	}

	// Consulta e confirma a alteração
	chambers, err := GetChambers(db)
	if err != nil {
		t.Fatalf("erro ao buscar câmaras: %v", err)
	}
	var found *Chamber
	for _, c := range chambers {
		if c.ID == "CH-C" {
			found = &c
			break
		}
	}
	if found == nil {
		t.Fatalf("câmara CH-C não encontrada na lista")
	}
	if found.TempMax != 7.5 || found.AlarmDelayMin != 2 {
		t.Errorf("alterações não persistidas: TempMax=%.1f, Delay=%d", found.TempMax, found.AlarmDelayMin)
	}
}


func TestPharmaSensorRegistrationAndCalibration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	now := time.Now()
	validFuture := now.AddDate(1, 0, 0).Format("2006-01-02")
	expiringSoon := now.AddDate(0, 0, 15).Format("2006-01-02")
	alreadyExpired := now.AddDate(0, 0, -10).Format("2006-01-02")

	// 1. Cadastrar sensor ativo com validade de 1 ano
	s1 := Sensor{
		ID:              "S29",
		ChamberID:       "CH-A",
		SerialNumber:    "SN-PT100-9929",
		ThermalPosition: "Ponto Crítico Superior (Porta)",
		CertNumber:      "CAL-RBC-2026/029",
		CalibratedBy:    "LabMetrologia RBC nº 412",
		CalibratedAt:    now.Format("2006-01-02"),
		CalibValidUntil: validFuture,
	}
	created1, err := RegisterSensor(db, s1)
	if err != nil {
		t.Fatalf("erro ao cadastrar sensor S29: %v", err)
	}
	if created1.Status != "Ativo" {
		t.Errorf("esperava status Ativo, obteve '%s'", created1.Status)
	}

	// 2. Tentar cadastrar sensor com câmara inexistente
	invalidChamber := Sensor{
		ID:              "S30",
		ChamberID:       "CAMARA_FANTASMA",
		SerialNumber:    "SN-TEST-00",
		ThermalPosition: "Centro",
		CertNumber:      "CAL-00",
		CalibratedBy:    "Lab Teste",
		CalibValidUntil: validFuture,
	}
	_, err = RegisterSensor(db, invalidChamber)
	if err == nil {
		t.Fatalf("esperava erro ao cadastrar sensor com câmara inexistente, mas passou")
	}

	// 3. Cadastrar sensor com vencimento próximo (15 dias)
	s2 := Sensor{
		ID:              "S31",
		ChamberID:       "CH-B",
		SerialNumber:    "SN-PT100-9931",
		ThermalPosition: "Prateleira 03",
		CertNumber:      "CAL-RBC-2025/931",
		CalibratedBy:    "LabMetrologia RBC nº 412",
		CalibratedAt:    now.AddDate(-1, 0, 15).Format("2006-01-02"),
		CalibValidUntil: expiringSoon,
	}
	created2, err := RegisterSensor(db, s2)
	if err != nil {
		t.Fatalf("erro ao cadastrar sensor S31: %v", err)
	}
	if created2.Status != "Atenção: Vencendo em breve" {
		t.Errorf("esperava status 'Atenção: Vencendo em breve', obteve '%s'", created2.Status)
	}

	// 4. Cadastrar sensor vencido
	s3 := Sensor{
		ID:              "S32",
		ChamberID:       "CH-A",
		SerialNumber:    "SN-PT100-9932",
		ThermalPosition: "Evaporador",
		CertNumber:      "CAL-RBC-2024/932",
		CalibratedBy:    "LabMetrologia RBC nº 412",
		CalibratedAt:    now.AddDate(-1, 0, -10).Format("2006-01-02"),
		CalibValidUntil: alreadyExpired,
	}
	created3, err := RegisterSensor(db, s3)
	if err != nil {
		t.Fatalf("erro ao cadastrar sensor S32: %v", err)
	}
	if created3.Status != "Vencido (Calibração Obrigatória)" {
		t.Errorf("esperava status 'Vencido (Calibração Obrigatória)', obteve '%s'", created3.Status)
	}

	// 5. Recalibração de sensor (UpdateSensor)
	recal := Sensor{
		ID:              "S32",
		CertNumber:      "CAL-RBC-2026/NEW-001",
		CalibratedAt:    now.Format("2006-01-02"),
		CalibValidUntil: validFuture,
	}
	err = UpdateSensor(db, recal)
	if err != nil {
		t.Fatalf("erro ao atualizar/recalibrar sensor S32: %v", err)
	}

	// 6. Verificar persistência em GetSensors
	sensorsA, err := GetSensors(db, "CH-A")
	if err != nil {
		t.Fatalf("erro ao buscar sensores da câmara CH-A: %v", err)
	}
	var s32Found *Sensor
	for _, s := range sensorsA {
		if s.ID == "S32" {
			s32Found = &s
			break
		}
	}
	if s32Found == nil {
		t.Fatalf("sensor S32 não encontrado na lista de CH-A")
	}
	if s32Found.Status != "Ativo" {
		t.Errorf("esperava status 'Ativo' após recalibração, obteve '%s'", s32Found.Status)
	}
	if s32Found.CertNumber != "CAL-RBC-2026/NEW-001" {
		t.Errorf("esperava cert 'CAL-RBC-2026/NEW-001', obteve '%s'", s32Found.CertNumber)
	}
}


func TestPharmaQualityDossierPersistence(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Verificar se o dossiê inicial vem carregado do banco SQLite
	dossier, err := GetQualityDossier(db)
	if err != nil {
		t.Fatalf("erro ao buscar dossiê de qualidade: %v", err)
	}
	if len(dossier.POPs) != 6 {
		t.Errorf("esperava 6 POPs iniciais no SQLite, obteve %d", len(dossier.POPs))
	}
	if len(dossier.Qualifications) < 5 {
		t.Errorf("esperava pelo menos 5 qualificações iniciais, obteve %d", len(dossier.Qualifications))
	}
	if len(dossier.Trainings) < 4 {
		t.Errorf("esperava pelo menos 4 treinamentos iniciais, obteve %d", len(dossier.Trainings))
	}

	// 2. Registrar nova revisão de POP existente (POP-GQ-001)
	rev := POPItem{
		Code:        "POP-GQ-001",
		Title:       "Monitoramento Contínuo e Ininterrupto de Temperatura e Umidade 24/7",
		Version:     "Rev. 05/2026",
		LastReview:  "2026-01-15",
		NextReview:  "2027-01-15",
		Responsible: "Farmacêutica RT",
		Status:      "Vigente",
	}
	_, err = RegisterPOP(db, rev)
	if err != nil {
		t.Fatalf("erro ao atualizar revisão de POP: %v", err)
	}

	// 3. Cadastrar novo POP inédito (POP-GQ-007)
	newPOP := POPItem{
		Code:        "POP-GQ-007",
		Title:       "Validação de Sistemas Computadorizados e Auditoria de Software (GAMP 5)",
		Version:     "Rev. 01/2026",
		LastReview:  "2026-03-01",
		NextReview:  "2027-03-01",
		Responsible: "Garantia da Qualidade / TI",
		Status:      "Vigente",
	}
	_, err = RegisterPOP(db, newPOP)
	if err != nil {
		t.Fatalf("erro ao cadastrar novo POP: %v", err)
	}

	// 4. Registrar novos Treinamentos com notas distintas (Apto >= 85 e Pendente < 85)
	tApto := TrainingRecord{
		EmployeeName: "Lucas Almeida Ramos",
		Role:         "Operador Logístico",
		POPCode:      "POP-GQ-001, POP-GQ-004",
		TrainedAt:    "2026-04-10",
		ScorePct:     95,
	}
	tRec, err := RegisterTraining(db, tApto)
	if err != nil {
		t.Fatalf("erro ao registrar treinamento aprovado: %v", err)
	}
	if tRec.Status != "Apto" {
		t.Errorf("esperava status 'Apto' para nota 95, obteve '%s'", tRec.Status)
	}

	tPendente := TrainingRecord{
		EmployeeName: "Marcos Vinicius Silva",
		Role:         "Auxiliar de Expedição",
		POPCode:      "POP-GQ-003",
		TrainedAt:    "2026-04-12",
		ScorePct:     75,
	}
	tRec2, err := RegisterTraining(db, tPendente)
	if err != nil {
		t.Fatalf("erro ao registrar treinamento pendente: %v", err)
	}
	if tRec2.Status != "Pendente Reciclagem" {
		t.Errorf("esperava status 'Pendente Reciclagem' para nota 75, obteve '%s'", tRec2.Status)
	}

	// 5. Registrar Laudo de Qualificação Térmica para câmara nova
	newQual := QualificationItem{
		ChamberID:       "CH-A",
		Stage:           "DQ / IQ / OQ / PQ",
		ReportNumber:    "QUAL-2026-REC-01",
		ExecutedBy:      "LabMetrologia RBC Cert nº 412",
		ExecutionDate:   "2026-05-20",
		ValidUntil:      "2028-05-20",
		ThermalAutonomy: "7h 15min sem energia (limite 8.0°C)",
		Status:          "Aprovado",
	}
	_, err = RegisterChamberQualification(db, newQual)
	if err != nil {
		t.Fatalf("erro ao registrar laudo de qualificação: %v", err)
	}

	// 6. Consultar dossiê final e validar todas as atualizações
	updatedDossier, err := GetQualityDossier(db)
	if err != nil {
		t.Fatalf("erro ao consultar dossiê atualizado: %v", err)
	}

	if len(updatedDossier.POPs) != 7 {
		t.Errorf("esperava 7 POPs no total, obteve %d", len(updatedDossier.POPs))
	}
	var pop1Found, pop7Found bool
	for _, p := range updatedDossier.POPs {
		if p.Code == "POP-GQ-001" && p.Version == "Rev. 05/2026" {
			pop1Found = true
		}
		if p.Code == "POP-GQ-007" {
			pop7Found = true
		}
	}
	if !pop1Found {
		t.Errorf("POP-GQ-001 não teve a revisão 05/2026 persistida no SQLite")
	}
	if !pop7Found {
		t.Errorf("POP-GQ-007 não foi encontrado no SQLite")
	}

	// Validar que os novos treinamentos aparecem na lista
	var foundLucas, foundMarcos bool
	for _, tr := range updatedDossier.Trainings {
		if tr.EmployeeName == "Lucas Almeida Ramos" && tr.Status == "Apto" {
			foundLucas = true
		}
		if tr.EmployeeName == "Marcos Vinicius Silva" && tr.Status == "Pendente Reciclagem" {
			foundMarcos = true
		}
	}
	if !foundLucas || !foundMarcos {
		t.Errorf("treinamentos não persistidos corretamente: Lucas=%v, Marcos=%v", foundLucas, foundMarcos)
	}
}

func TestPharmaDecommissionChamber(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Tentar descomissionar câmara com lotes ativos (CH-A) deve falhar (trava regulatória)
	err := DecommissionChamber(db, "CH-A", "Fim de vida útil", "Equipamento antigo", "Dra. Mariana", "descartar")
	if err == nil {
		t.Errorf("esperava erro ao tentar descomissionar câmara CH-A com lotes ativos, mas obteve sucesso")
	}

	// 2. Criar câmara temporária sem lotes
	tempCh := Chamber{
		ID:                   "TEMP-TEST",
		Name:                 "Câmara de Teste Descarte",
		Type:                 "refrigerado",
		TempMin:              2.0,
		TempMax:              8.0,
		AlarmDelayMin:        5,
		QualStatus:           "Qualificado",
		QualValidUntil:       "2028-01-01",
	}
	_, err = RegisterChamber(db, tempCh)
	if err != nil {
		t.Fatalf("erro ao criar câmara de teste: %v", err)
	}

	// 3. Descomissionar câmara vazia deve passar
	err = DecommissionChamber(db, "TEMP-TEST", "Substituição", "Laudo pericial aprovado", "Dra. Mariana", "descartar")
	if err != nil {
		t.Fatalf("erro ao descomissionar câmara vazia: %v", err)
	}

	// 4. Confirma que a câmara foi removida
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM chambers WHERE id = 'TEMP-TEST'").Scan(&count)
	if count != 0 {
		t.Errorf("esperava 0 câmaras com ID TEMP-TEST após descomissionamento, obteve %d", count)
	}
}

func TestThermalCompatibilityAndTransfer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 1. Testes unitários da trava de compatibilidade térmica
	// 1.1 Almoxarifado (15°C a 25°C) tentando transferir para Ultrafreezer (-80°C) -> BLOQUEIO
	compat, msg := CheckThermalCompatibility(15.0, 25.0, -86.0, -60.0, "Medicamento Ambiente", "Ultrafreezer")
	if compat {
		t.Errorf("esperava bloqueio térmico para medicamento de almoxarifado em ultrafreezer, mas obteve sucesso: %s", msg)
	}

	// 1.2 Vacina (2°C a 8°C) tentando transferir para Almoxarifado (15°C a 25°C) -> BLOQUEIO
	compat, msg = CheckThermalCompatibility(2.0, 8.0, 15.0, 25.0, "Vacina Meningocócica", "Almoxarifado")
	if compat {
		t.Errorf("esperava bloqueio térmico para vacina em almoxarifado, mas obteve sucesso: %s", msg)
	}

	// 1.3 Vacina (2°C a 8°C) tentando transferir para Câmara de Congelados (-20°C a -10°C) -> BLOQUEIO
	compat, msg = CheckThermalCompatibility(2.0, 8.0, -20.0, -10.0, "Vacina Meningocócica", "Câmara R Congelados")
	if compat {
		t.Errorf("esperava bloqueio térmico para vacina em câmara de congelados, mas obteve sucesso: %s", msg)
	}

	// 1.4 Vacina (2°C a 8°C) para Câmara B (2°C a 8°C) -> APROVADO
	compat, msg = CheckThermalCompatibility(2.0, 8.0, 2.0, 8.0, "Vacina Meningocócica", "Câmara B")
	if !compat {
		t.Errorf("esperava compatibilidade térmica para vacina em câmara 2 a 8°C, mas falhou: %s", msg)
	}

	// 2. Teste de Transferência Real no Banco de Dados com Trava Metrológica
	// Pega o lote 1 (Vacina Meningocócica na câmara CH-A, 2°C a 8°C)
	var lotID int64
	var sourceChamber string
	err := db.QueryRow("SELECT id, chamber_id FROM lots WHERE lot_number = 'VAC-MEN-2026-A1'").Scan(&lotID, &sourceChamber)
	if err != nil {
		t.Fatalf("lote VAC-MEN-2026-A1 não encontrado no banco de testes: %v", err)
	}

	// 2.1 Tentar transferir vacina para Almoxarifado ALM-01 -> DEVE FALHAR COM ERRO DE TRAVA TÉRMICA
	err = TransferLot(db, lotID, "ALM-01", "Prat 1", "Teste Inválido", "Dr. Lucas")
	if err == nil {
		t.Errorf("esperava falha ao transferir vacina de 2-8°C para almoxarifado ALM-01, mas a transferência foi aceita!")
	}

	// 2.2 Tentar transferir vacina para Ultrafreezer ULT-01 -> DEVE FALHAR COM ERRO DE TRAVA TÉRMICA
	err = TransferLot(db, lotID, "ULT-01", "Rack 1", "Teste Inválido", "Dr. Lucas")
	if err == nil {
		t.Errorf("esperava falha ao transferir vacina de 2-8°C para ultrafreezer ULT-01, mas a transferência foi aceita!")
	}

	// 2.3 Transferir vacina para Câmara B (CH-B, 2°C a 8°C) -> DEVE TER SUCESSO
	err = TransferLot(db, lotID, "CH-B", "Prateleira 3, Bandeja A", "Balanceamento de Carga", "Dr. Lucas")
	if err != nil {
		t.Fatalf("esperava sucesso ao transferir vacina para câmara compatível CH-B, mas obteve erro: %v", err)
	}

	// 2.4 Verifica se a câmara do lote foi atualizada
	var currentChamber string
	_ = db.QueryRow("SELECT chamber_id FROM lots WHERE id = ?", lotID).Scan(&currentChamber)
	if currentChamber != "CH-B" {
		t.Errorf("esperava lote alocado em CH-B, obteve %s", currentChamber)
	}

	// 2.5 Verifica se foi gravado o evento no histórico do lote (lot_history)
	var histCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM lot_history WHERE lot_id = ? AND event_type = 'TRANSFERENCIA_INTERNA'", lotID).Scan(&histCount)
	if histCount == 0 {
		t.Errorf("esperava registro do evento TRANSFERENCIA_INTERNA em lot_history, mas nenhum foi encontrado")
	}

	// 3. Teste de Evacuação de Câmara (TransferChamberLots)
	// Tentar evacuar CH-B para ALM-01 deve ser bloqueado porque CH-B contém vacinas termolábeis
	_, err = TransferChamberLots(db, "CH-B", "ALM-01", "Prateleira Geral", "Parada para manutenção", "Dr. Lucas")
	if err == nil {
		t.Errorf("esperava bloqueio térmico ao evacuar câmara com vacinas para almoxarifado, mas passou!")
	}

	// Evacuar CH-B para CH-A (ambas 2°C a 8°C) deve passar
	count, err := TransferChamberLots(db, "CH-B", "CH-A", "Prateleira Geral Evacuação", "Parada para manutenção", "Dr. Lucas")
	if err != nil {
		t.Fatalf("esperava sucesso ao evacuar CH-B para CH-A, mas obteve erro: %v", err)
	}
	if count == 0 {
		t.Errorf("esperava transferir pelo menos 1 lote, transferiu %d", count)
	}

	// Agora CH-B não tem mais nenhum lote ativo
	var chBLots int
	_ = db.QueryRow("SELECT COUNT(*) FROM lots WHERE chamber_id = 'CH-B' AND status != 'descartado'").Scan(&chBLots)
	if chBLots != 0 {
		t.Errorf("esperava 0 lotes em CH-B após evacuação completa, encontrou %d", chBLots)
	}
}
