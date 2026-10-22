package pharma

import (
	"database/sql"
	"testing"

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
	if len(chambers) != 3 {
		t.Errorf("esperava 3 câmaras, obteve %d", len(chambers))
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
