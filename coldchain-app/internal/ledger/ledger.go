package ledger

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Ledger gerencia a cadeia criptográfica de blocos imutáveis
type Ledger struct {
	db         *sql.DB
	signer     Signer
	env        uint8
	mu         sync.Mutex
	latestID   uint64
	latestHash [32]byte
}

// NewLedger inicializa a tabela, triggers WORM e garante os blocos GENESIS e BOOT
func NewLedger(db *sql.DB, signer Signer, env uint8) (*Ledger, error) {
	if db == nil || signer == nil {
		return nil, errors.New("db e signer são obrigatórios")
	}

	l := &Ledger{
		db:     db,
		signer: signer,
		env:    env,
	}

	if err := l.initSchema(); err != nil {
		return nil, fmt.Errorf("erro ao criar schema do ledger: %w", err)
	}

	if err := l.bootstrapChain(); err != nil {
		return nil, fmt.Errorf("erro no bootstrap da cadeia: %w", err)
	}

	return l, nil
}

func (l *Ledger) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS ledger (
		id INTEGER PRIMARY KEY,
		record_type INTEGER NOT NULL,
		env INTEGER NOT NULL,
		clock_status INTEGER NOT NULL,
		device_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		server_ts_ns INTEGER NOT NULL,
		key_id BLOB NOT NULL,
		payload BLOB NOT NULL,
		prev_hash BLOB NOT NULL,
		current_hash BLOB NOT NULL,
		signature BLOB NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_device_seq 
	ON ledger (device_id, seq) WHERE record_type = 1;

	CREATE TRIGGER IF NOT EXISTS trg_ledger_no_update
	BEFORE UPDATE ON ledger
	BEGIN
		SELECT RAISE(ABORT, 'Segurança ANVISA: O Ledger Criptográfico é estritamente imutável (WORM).');
	END;

	CREATE TRIGGER IF NOT EXISTS trg_ledger_no_delete
	BEFORE DELETE ON ledger
	BEGIN
		SELECT RAISE(ABORT, 'Segurança ANVISA: O Ledger Criptográfico é estritamente append-only.');
	END;
	`
	_, err := l.db.Exec(schema)
	return err
}

func (l *Ledger) bootstrapChain() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var (
		count      int
		latestID   int64
		latestHash []byte
	)

	err := l.db.QueryRow("SELECT COUNT(*) FROM ledger").Scan(&count)
	if err != nil {
		return err
	}

	if count > 0 {
		var lastType int
		err = l.db.QueryRow("SELECT id, record_type, current_hash FROM ledger ORDER BY id DESC LIMIT 1").Scan(&latestID, &lastType, &latestHash)
		if err != nil {
			return err
		}
		l.latestID = uint64(latestID)
		copy(l.latestHash[:], latestHash)
		if uint8(lastType) == RecordTypeBoot {
			return nil
		}
		return l.appendBootRecordLocked()
	}

	// 1. Criar bloco GENESIS (ID = 1, prev_hash = zeros)
	kid := l.signer.KeyID()
	pubPEM, _ := l.signer.PublicKeyPEM()
	genesisPayload, _ := json.Marshal(map[string]any{
		"event":         "GENESIS_INITIALIZATION",
		"version":       LedgerVersionMagic,
		"env":           EnvName(l.env),
		"standard":      "ANVISA RDC 430/2020 & 21 CFR Part 11",
		"public_key":    pubPEM,
		"key_id":        hex.EncodeToString(kid[:]),
		"genesis_time":  time.Now().UTC().Format(time.RFC3339Nano),
		"author":        "ColdChain Medical Gateway",
	})

	var zeroHash [32]byte
	recGenesis := &Record{
		ID:          1,
		RecordType:  RecordTypeGenesis,
		Env:         l.env,
		ClockStatus: ClockTrusted,
		DeviceID:    "GATEWAY-CORE",
		Seq:         0,
		ServerTSNs:  time.Now().UTC().UnixNano(),
		KeyID:       l.signer.KeyID(),
		Payload:     genesisPayload,
		PrevHash:    zeroHash,
	}
	recGenesis.CurrentHash = ComputeCanonicalHash(recGenesis)

	sig, err := l.signer.Sign(nil, recGenesis.CurrentHash[:], nil)
	if err != nil {
		return fmt.Errorf("erro ao assinar GENESIS: %w", err)
	}
	copy(recGenesis.Signature[:], sig)

	if err := l.insertRecord(recGenesis); err != nil {
		return fmt.Errorf("erro ao gravar GENESIS: %w", err)
	}

	l.latestID = 1
	l.latestHash = recGenesis.CurrentHash

	// 2. Criar bloco BOOT subsequente
	return l.appendBootRecordLocked()
}

func (l *Ledger) appendBootRecordLocked() error {
	binHash := getExecutableHash()
	bootPayload, _ := json.Marshal(map[string]any{
		"event":        "SYSTEM_BOOT",
		"binary_sha256": binHash,
		"boot_time":    time.Now().UTC().Format(time.RFC3339Nano),
		"hostname":     os.Getenv("HOSTNAME"),
		"env":          EnvName(l.env),
	})

	rec := &Record{
		ID:          l.latestID + 1,
		RecordType:  RecordTypeBoot,
		Env:         l.env,
		ClockStatus: ClockTrusted,
		DeviceID:    "GATEWAY-CORE",
		Seq:         0,
		ServerTSNs:  time.Now().UTC().UnixNano(),
		KeyID:       l.signer.KeyID(),
		Payload:     bootPayload,
		PrevHash:    l.latestHash,
	}
	rec.CurrentHash = ComputeCanonicalHash(rec)

	sig, err := l.signer.Sign(nil, rec.CurrentHash[:], nil)
	if err != nil {
		return fmt.Errorf("erro ao assinar BOOT: %w", err)
	}
	copy(rec.Signature[:], sig)

	if err := l.insertRecord(rec); err != nil {
		return fmt.Errorf("erro ao gravar BOOT: %w", err)
	}

	l.latestID = rec.ID
	l.latestHash = rec.CurrentHash
	return nil
}

func getExecutableHash() string {
	exePath, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	f, err := os.Open(exePath)
	if err != nil {
		return "unreadable"
	}
	defer f.Close()

	h := sha256.New()
	_, _ = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

func (l *Ledger) insertRecord(r *Record) error {
	query := `
	INSERT INTO ledger (
		id, record_type, env, clock_status, device_id, seq,
		server_ts_ns, key_id, payload, prev_hash, current_hash, signature
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := l.db.Exec(query,
		r.ID,
		r.RecordType,
		r.Env,
		r.ClockStatus,
		r.DeviceID,
		r.Seq,
		r.ServerTSNs,
		r.KeyID[:],
		r.Payload,
		r.PrevHash[:],
		r.CurrentHash[:],
		r.Signature[:],
	)
	return err
}

// AppendData grava um registro do tipo DATA (telemetria de sensor)
func (l *Ledger) AppendData(deviceID string, seq uint64, payload []byte, clockStatus uint8) (*Record, error) {
	return l.Append(RecordTypeData, deviceID, seq, payload, clockStatus)
}

// Append grava qualquer tipo de bloco de forma thread-safe e serializada (Seção 4)
func (l *Ledger) Append(recordType uint8, deviceID string, seq uint64, payload []byte, clockStatus uint8) (*Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec := &Record{
		ID:          l.latestID + 1,
		RecordType:  recordType,
		Env:         l.env,
		ClockStatus: clockStatus,
		DeviceID:    deviceID,
		Seq:         seq,
		ServerTSNs:  time.Now().UTC().UnixNano(),
		KeyID:       l.signer.KeyID(),
		Payload:     payload,
		PrevHash:    l.latestHash,
	}

	rec.CurrentHash = ComputeCanonicalHash(rec)

	sig, err := l.signer.Sign(nil, rec.CurrentHash[:], nil)
	if err != nil {
		return nil, fmt.Errorf("falha ao assinar bloco #%d: %w", rec.ID, err)
	}
	copy(rec.Signature[:], sig)

	if err := l.insertRecord(rec); err != nil {
		return nil, fmt.Errorf("falha ao persistir bloco #%d: %w", rec.ID, err)
	}

	l.latestID = rec.ID
	l.latestHash = rec.CurrentHash

	rec.PopulateFormattedFields()
	return rec, nil
}

// CreateAnchor grava uma âncora criptográfica com recibo de autenticação externa (Seção 8)
func (l *Ledger) CreateAnchor(receipt string) (*Record, error) {
	l.mu.Lock()
	height := l.latestID
	headHash := l.latestHash
	l.mu.Unlock()

	anchorData, _ := json.Marshal(map[string]any{
		"anchor_event":    "RFC3161_EXTERNAL_TSA_ANCHOR",
		"height":          height,
		"head_hash":       hex.EncodeToString(headHash[:]),
		"external_receipt": receipt,
		"timestamp":       time.Now().UTC().Format(time.RFC3339Nano),
	})

	return l.Append(RecordTypeAnchor, "GATEWAY-CORE", height, anchorData, ClockTrusted)
}

// TotalRecords retorna a contagem de blocos na cadeia
func (l *Ledger) TotalRecords() (int, error) {
	var count int
	err := l.db.QueryRow("SELECT COUNT(*) FROM ledger").Scan(&count)
	return count, err
}

// GetRecords retorna registros paginados ordenados por id DESC
func (l *Ledger) GetRecords(offset, limit int) ([]Record, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var total int
	err := l.db.QueryRow("SELECT COUNT(*) FROM ledger").Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := l.db.Query(`
		SELECT id, record_type, env, clock_status, device_id, seq,
		       server_ts_ns, key_id, payload, prev_hash, current_hash, signature, created_at
		FROM ledger
		ORDER BY id DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var (
			r       Record
			keyID   []byte
			prev    []byte
			curr    []byte
			sig     []byte
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
			return nil, 0, err
		}
		copy(r.KeyID[:], keyID)
		copy(r.PrevHash[:], prev)
		copy(r.CurrentHash[:], curr)
		copy(r.Signature[:], sig)

		r.PopulateFormattedFields()
		records = append(records, r)
	}

	return records, total, nil
}
