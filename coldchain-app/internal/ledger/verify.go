package ledger

import (
	"crypto/ecdsa"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// VerifyReport resume o laudo pericial da verificação matemática da cadeia
type VerifyReport struct {
	Valid             bool              `json:"valid"`
	TotalRecords      int               `json:"total_records"`
	CheckedHashes     int               `json:"checked_hashes"`
	CheckedSignatures int               `json:"checked_signatures"`
	GenesisID         uint64            `json:"genesis_id"`
	HeadID            uint64            `json:"head_id"`
	DurationMs        float64           `json:"duration_ms"`
	Anomalies         []string          `json:"anomalies"`
	UntrustedClocks   int               `json:"untrusted_clocks"`
	DeviceMaxSeq      map[string]uint64 `json:"device_max_seq"`
	SeqGaps           []string          `json:"seq_gaps"`
	SignerKeyID       string            `json:"signer_key_id"`
	VerifiedAt        string            `json:"verified_at"`
	RegulatoryNotice  string            `json:"regulatory_notice"`
}

// VerifyChain audita a integridade matemática, hash chaining e assinaturas ECDSA (Seção 9)
func VerifyChain(db *sql.DB, pubKey *ecdsa.PublicKey) (*VerifyReport, error) {
	start := time.Now()

	report := &VerifyReport{
		Valid:            true,
		Anomalies:        make([]string, 0),
		SeqGaps:          make([]string, 0),
		DeviceMaxSeq:     make(map[string]uint64),
		VerifiedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		RegulatoryNotice: "Laudo Pericial Criptográfico conforme ANVISA RDC 430/2020 e 21 CFR Part 11",
	}

	rows, err := db.Query(`
		SELECT id, record_type, env, clock_status, device_id, seq,
		       server_ts_ns, key_id, payload, prev_hash, current_hash, signature, created_at
		FROM ledger
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("falha ao consultar ledger para auditoria: %w", err)
	}
	defer rows.Close()

	var (
		expectedID uint64 = 1
		lastHash   [32]byte
		zeroHash   [32]byte
	)

	for rows.Next() {
		var (
			r     Record
			keyID []byte
			prev  []byte
			curr  []byte
			sig   []byte
		)
		err := rows.Scan(
			&r.ID,
			&r.RecordType,
			&r.Env,
			&r.ClockStatus,
			&r.DeviceID,
			&r.Seq,
			&r.ServerTSNs,
			&keyID,
			&r.Payload,
			&prev,
			&curr,
			&sig,
			&r.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("erro ao ler registro durante auditoria: %w", err)
		}

		copy(r.KeyID[:], keyID)
		copy(r.PrevHash[:], prev)
		copy(r.CurrentHash[:], curr)
		copy(r.Signature[:], sig)

		if report.SignerKeyID == "" {
			report.SignerKeyID = hex.EncodeToString(r.KeyID[:])
		}
		if report.GenesisID == 0 {
			report.GenesisID = r.ID
		}
		report.HeadID = r.ID
		report.TotalRecords++

		// 1. Checagem de contiguidade de ID (Seção 9.2 item 3)
		if r.ID != expectedID {
			report.Valid = false
			report.Anomalies = append(report.Anomalies, fmt.Sprintf(
				"Lacuna de registros detectada: esperado ID #%d, recebido #%d", expectedID, r.ID))
		}
		expectedID = r.ID + 1

		// 2. Checagem de encadeamento de hash (prev_hash == último current_hash)
		if r.ID == 1 {
			if r.PrevHash != zeroHash {
				report.Valid = false
				report.Anomalies = append(report.Anomalies, "Bloco GENESIS #1 não possui prev_hash zerado.")
			}
		} else {
			if r.PrevHash != lastHash {
				report.Valid = false
				report.Anomalies = append(report.Anomalies, fmt.Sprintf(
					"Quebra de encadeamento no bloco #%d: prev_hash (%s) diverge do hash anterior (%s)",
					r.ID, hex.EncodeToString(r.PrevHash[:8]), hex.EncodeToString(lastHash[:8])))
			}
		}

		// 3. Recálculo e verificação do hash canônico (Seção 9.2 item 1)
		calcHash := ComputeCanonicalHash(&r)
		report.CheckedHashes++
		if calcHash != r.CurrentHash {
			report.Valid = false
			report.Anomalies = append(report.Anomalies, fmt.Sprintf(
				"Violação de integridade no bloco #%d: hash armazenado (%s) não confere com o cálculo canônico (%s). DADOS FORAM ADULTERADOS!",
				r.ID, hex.EncodeToString(r.CurrentHash[:8]), hex.EncodeToString(calcHash[:8])))
		}

		// 4. Verificação da assinatura digital ECDSA P-256 (Seção 9.2 item 2)
		if pubKey != nil {
			report.CheckedSignatures++
			if !VerifySignature(pubKey, r.CurrentHash[:], r.Signature[:]) {
				report.Valid = false
				report.Anomalies = append(report.Anomalies, fmt.Sprintf(
					"Assinatura ECDSA P-256 INVÁLIDA no bloco #%d: autor não comprovado ou chave violada.", r.ID))
			}
		}

		// 5. Verificação de sequência por dispositivo (Seção 9.2 item 4)
		if r.RecordType == RecordTypeData {
			lastDevSeq, exists := report.DeviceMaxSeq[r.DeviceID]
			if exists {
				if r.Seq <= lastDevSeq {
					report.Valid = false
					report.Anomalies = append(report.Anomalies, fmt.Sprintf(
						"Sequência não-crescente no dispositivo '%s': seq recebido %d <= anterior %d",
						r.DeviceID, r.Seq, lastDevSeq))
				} else if r.Seq > lastDevSeq+1 {
					report.SeqGaps = append(report.SeqGaps, fmt.Sprintf(
						"Lacuna de telemetria no dispositivo '%s': pulou de seq %d para %d (%d perdidas)",
						r.DeviceID, lastDevSeq, r.Seq, r.Seq-lastDevSeq-1))
				}
			}
			report.DeviceMaxSeq[r.DeviceID] = r.Seq
		}

		// 6. Monitoramento de relógio não-confiável (Seção 10)
		if r.ClockStatus == ClockUntrusted {
			report.UntrustedClocks++
		}

		lastHash = r.CurrentHash
	}

	report.DurationMs = float64(time.Since(start).Microseconds()) / 1000.0
	return report, nil
}
