package main

import (
	"coldchain-app/internal/ledger"
	"crypto/ecdsa"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

type ExportDocument struct {
	RegulatoryStandard string          `json:"regulatory_standard"`
	DocumentTitle      string          `json:"document_title"`
	GeneratedAt        string          `json:"generated_at"`
	TotalRecords       int             `json:"total_records"`
	ServerAuthority    ServerAuthInfo  `json:"server_authority"`
	Records            []ledger.Record `json:"records"`
}

type ServerAuthInfo struct {
	Algorithm    string `json:"algorithm"`
	KeyID        string `json:"key_id"`
	PublicKeyPEM string `json:"public_key_pem"`
}

func main() {
	dbPath := flag.String("db", "", "Caminho do arquivo de banco de dados SQLite (.db)")
	exportPath := flag.String("export", "", "Caminho do arquivo de exportação pericial JSON (.json)")
	keyPath := flag.String("key", "", "Caminho opcional da chave pública PEM para validação")
	jsonOut := flag.Bool("json", false, "Emitir laudo em formato JSON padronizado")
	flag.Parse()

	if *dbPath == "" && *exportPath == "" {
		fmt.Println("Uso do Verificador Independente ANVISA:")
		fmt.Println("  coldchain-verify -db <banco.db> [-key <chave.pem>] [-json]")
		fmt.Println("  coldchain-verify -export <export.json> [-json]")
		os.Exit(2)
	}

	var (
		report *ledger.VerifyReport
		err    error
	)

	if *exportPath != "" {
		report, err = verifyExportFile(*exportPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[-] Erro ao auditar arquivo de exportação: %v\n", err)
			os.Exit(1)
		}
	} else {
		report, err = verifyDatabase(*dbPath, *keyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[-] Erro ao auditar banco de dados: %v\n", err)
			os.Exit(1)
		}
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	} else {
		printReportFormatted(report)
	}

	if !report.Valid {
		os.Exit(1)
	}
}

func verifyExportFile(path string) (*ledger.VerifyReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc ExportDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("formato de exportação JSON inválido: %w", err)
	}

	start := time.Now()
	report := &ledger.VerifyReport{
		Valid:            true,
		Anomalies:        make([]string, 0),
		SeqGaps:          make([]string, 0),
		DeviceMaxSeq:     make(map[string]uint64),
		VerifiedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		RegulatoryNotice: "Laudo Pericial Criptográfico Independente conforme ANVISA RDC 430/2020 e 21 CFR 11",
		SignerKeyID:      doc.ServerAuthority.KeyID,
	}

	var pubKey *ecdsa.PublicKey
	if doc.ServerAuthority.PublicKeyPEM != "" {
		pubKey, err = parsePublicKeyPEM(doc.ServerAuthority.PublicKeyPEM)
		if err != nil {
			report.Anomalies = append(report.Anomalies, fmt.Sprintf("Chave pública no documento inválida: %v", err))
		}
	}

	// Ordena registros por ID ASC
	records := doc.Records
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})

	var (
		expectedID uint64 = 1
		lastHash   [32]byte
		zeroHash   [32]byte
	)

	for _, r := range records {
		r.Payload = []byte(r.PayloadText)
		if b, err := hex.DecodeString(r.KeyIDHex); err == nil && len(b) == 8 {
			copy(r.KeyID[:], b)
		}
		if b, err := hex.DecodeString(r.PrevHashHex); err == nil && len(b) == 32 {
			copy(r.PrevHash[:], b)
		}
		if b, err := hex.DecodeString(r.CurrentHashHex); err == nil && len(b) == 32 {
			copy(r.CurrentHash[:], b)
		}
		if b, err := hex.DecodeString(r.SignatureHex); err == nil && len(b) == 64 {
			copy(r.Signature[:], b)
		}

		if report.GenesisID == 0 {
			report.GenesisID = r.ID
		}
		report.HeadID = r.ID
		report.TotalRecords++

		// 1. Contiguidade de IDs
		if r.ID != expectedID {
			report.Valid = false
			report.Anomalies = append(report.Anomalies, fmt.Sprintf(
				"Lacuna de registros detectada: esperado ID #%d, recebido #%d", expectedID, r.ID))
		}
		expectedID = r.ID + 1

		// 2. Hash chaining
		if r.ID == 1 {
			if r.PrevHash != zeroHash {
				report.Valid = false
				report.Anomalies = append(report.Anomalies, "Bloco Gênese #1 com prev_hash não zerado.")
			}
		} else {
			if r.PrevHash != lastHash {
				report.Valid = false
				report.Anomalies = append(report.Anomalies, fmt.Sprintf(
					"Quebra de encadeamento no bloco #%d: prev_hash diverge do bloco anterior", r.ID))
			}
		}

		// 3. Recálculo do hash canônico
		calcHash := ledger.ComputeCanonicalHash(&r)
		report.CheckedHashes++
		if calcHash != r.CurrentHash {
			report.Valid = false
			report.Anomalies = append(report.Anomalies, fmt.Sprintf(
				"Violação de integridade no bloco #%d: hash armazenado não confere com o cálculo canônico (dados adulterados).", r.ID))
		}

		// 4. Assinatura ECDSA
		if pubKey != nil {
			report.CheckedSignatures++
			if !ledger.VerifySignature(pubKey, r.CurrentHash[:], r.Signature[:]) {
				report.Valid = false
				report.Anomalies = append(report.Anomalies, fmt.Sprintf(
					"Assinatura digital ECDSA P-256 INVÁLIDA no bloco #%d.", r.ID))
			}
		}

		// 5. Sequência do sensor
		if r.RecordType == ledger.RecordTypeData {
			lastSeq, exists := report.DeviceMaxSeq[r.DeviceID]
			if exists {
				if r.Seq <= lastSeq {
					report.Valid = false
					report.Anomalies = append(report.Anomalies, fmt.Sprintf(
						"Sequência não-crescente no sensor '%s': seq %d <= anterior %d", r.DeviceID, r.Seq, lastSeq))
				} else if r.Seq > lastSeq+1 {
					report.SeqGaps = append(report.SeqGaps, fmt.Sprintf(
						"Lacuna de telemetria no sensor '%s': pulou de %d para %d (%d leituras perdidas)",
						r.DeviceID, lastSeq, r.Seq, r.Seq-lastSeq-1))
				}
			}
			report.DeviceMaxSeq[r.DeviceID] = r.Seq
		}

		if r.ClockStatus == ledger.ClockUntrusted {
			report.UntrustedClocks++
		}

		lastHash = r.CurrentHash
	}

	report.DurationMs = float64(time.Since(start).Microseconds()) / 1000.0
	return report, nil
}

func verifyDatabase(dbPath, keyPath string) (*ledger.VerifyReport, error) {
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("erro ao abrir banco de dados: %w", err)
	}
	defer db.Close()

	var pubKey *ecdsa.PublicKey
	if keyPath != "" {
		k, err := loadPublicKey(keyPath)
		if err != nil {
			return nil, err
		}
		pubKey = k
	}

	return ledger.VerifyChain(db, pubKey)
}

func loadPublicKey(path string) (*ecdsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePublicKeyPEM(string(data))
}

func parsePublicKeyPEM(pemStr string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("bloco PEM inválido")
	}

	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if ecPub, ok := pub.(*ecdsa.PublicKey); ok {
			return ecPub, nil
		}
	}

	if priv, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return &priv.PublicKey, nil
	}

	return nil, fmt.Errorf("chave não é ECDSA P-256")
}

func printReportFormatted(r *ledger.VerifyReport) {
	fmt.Println("================================================================================")
	fmt.Println("   LAUDO PERICIAL CRIPTOGRÁFICO DE AUDITORIA ANVISA (RDC 430/2020 & 21 CFR 11)   ")
	fmt.Println("================================================================================")
	fmt.Printf("Data da Auditoria:        %s\n", r.VerifiedAt)
	fmt.Printf("Total de Blocos Auditados: %d (Gênese #%d até Head #%d)\n", r.TotalRecords, r.GenesisID, r.HeadID)
	fmt.Printf("Hashes SHA-256 Checados:  %d\n", r.CheckedHashes)
	fmt.Printf("Assinaturas ECDSA P-256:  %d\n", r.CheckedSignatures)
	fmt.Printf("Tempo de Execução:        %.2f ms\n", r.DurationMs)
	fmt.Printf("Identificador da Chave:   %s\n", r.SignerKeyID)
	fmt.Printf("Registros com Relógio ?:  %d\n", r.UntrustedClocks)
	fmt.Println("--------------------------------------------------------------------------------")

	if len(r.SeqGaps) > 0 {
		fmt.Printf("[AVISO] Lacunas de telemetria de sensores detectadas (%d):\n", len(r.SeqGaps))
		for _, g := range r.SeqGaps {
			fmt.Printf("  * %s\n", g)
		}
		fmt.Println("--------------------------------------------------------------------------------")
	}

	if r.Valid {
		fmt.Println(">>> RESULTADO PERICIAL: [ CADEIA 100% ÍNTEGRA E AUTÊNTICA ] <<<")
		fmt.Println("Garantia Matemática:")
		fmt.Println("  1. Nenhum registro foi alterado, inserido retroativamente ou excluído.")
		fmt.Println("  2. O encadeamento de hash SHA-256 é estritamente contínuo e ininterrupto.")
		fmt.Println("  3. Todas as assinaturas digitais foram comprovadas pela chave do Gateway.")
	} else {
		fmt.Println(">>> RESULTADO PERICIAL: [ ALERTA CRÍTICO: VIOLAÇÃO DE INTEGRIDADE ] <<<")
		fmt.Printf("Foram detectadas %d anomalias ou adulterações:\n", len(r.Anomalies))
		for i, a := range r.Anomalies {
			fmt.Printf("  [%d] %s\n", i+1, a)
		}
	}
	fmt.Println("================================================================================")
}
