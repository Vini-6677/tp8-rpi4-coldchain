package pharma

import (
	"database/sql"
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

	kpi, err := GetPharmaKPIs(db)
	if err != nil {
		t.Fatalf("erro ao obter KPIs: %v", err)
	}
	if kpi.AvailabilityPct < 99.5 {
		t.Errorf("disponibilidade abaixo da meta ANVISA: %.2f%%", kpi.AvailabilityPct)
	}
	if kpi.AvgResponseTimeMinutes > 30.0 {
		t.Errorf("tempo de resposta acima da meta ANVISA: %.2f min", kpi.AvgResponseTimeMinutes)
	}
	if kpi.ExcursionRatePct > 0.1 {
		t.Errorf("taxa de desvio acima da meta ANVISA: %.2f%%", kpi.ExcursionRatePct)
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
