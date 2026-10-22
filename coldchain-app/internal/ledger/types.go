package ledger

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	LedgerVersionMagic = "LEDGER-V1"

	// Record Types (Seção 5.1)
	RecordTypeGenesis     uint8 = 0
	RecordTypeData        uint8 = 1
	RecordTypeBoot        uint8 = 2
	RecordTypeKeyRotation uint8 = 3
	RecordTypeAnchor      uint8 = 4
	RecordTypeCheckpoint  uint8 = 5
	RecordTypeClockEvent  uint8 = 6

	// Environments (Seção 2 & 5.2)
	EnvTest uint8 = 0
	EnvProd uint8 = 1

	// Clock Statuses (Seção 5.2 & 10)
	ClockTrusted   uint8 = 0
	ClockUntrusted uint8 = 1
)

func RecordTypeName(t uint8) string {
	switch t {
	case RecordTypeGenesis:
		return "GENESIS"
	case RecordTypeData:
		return "DATA"
	case RecordTypeBoot:
		return "BOOT"
	case RecordTypeKeyRotation:
		return "KEY_ROTATION"
	case RecordTypeAnchor:
		return "ANCHOR"
	case RecordTypeCheckpoint:
		return "CHECKPOINT"
	case RecordTypeClockEvent:
		return "CLOCK_EVENT"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", t)
	}
}

func EnvName(e uint8) string {
	switch e {
	case EnvTest:
		return "TEST"
	case EnvProd:
		return "PROD"
	default:
		return "UNKNOWN"
	}
}

func ClockStatusName(s uint8) string {
	switch s {
	case ClockTrusted:
		return "TRUSTED"
	case ClockUntrusted:
		return "UNTRUSTED"
	default:
		return "UNKNOWN"
	}
}

// Record representa um bloco na cadeia do ledger criptográfico
type Record struct {
	ID          uint64   `json:"id"`
	RecordType  uint8    `json:"record_type"`
	TypeName    string   `json:"type_name"`
	Env         uint8    `json:"env"`
	EnvName     string   `json:"env_name"`
	ClockStatus uint8    `json:"clock_status"`
	ClockName   string   `json:"clock_name"`
	DeviceID    string   `json:"device_id"`
	Seq         uint64   `json:"seq"`
	ServerTSNs  int64    `json:"server_ts_ns"`
	ServerTime  string   `json:"server_time"`
	KeyID       [8]byte  `json:"-"`
	KeyIDHex    string   `json:"key_id"`
	Payload     []byte   `json:"-"`
	PayloadText string   `json:"payload"`
	PrevHash    [32]byte `json:"-"`
	PrevHashHex string   `json:"prev_hash"`
	CurrentHash [32]byte `json:"-"`
	CurrentHashHex string `json:"current_hash"`
	Signature   [64]byte `json:"-"`
	SignatureHex string  `json:"signature"`
	CreatedAt   string   `json:"created_at"`
}

// PopulateFormattedFields preenche as strings hexadecimais e legíveis
func (r *Record) PopulateFormattedFields() {
	r.TypeName = RecordTypeName(r.RecordType)
	r.EnvName = EnvName(r.Env)
	r.ClockName = ClockStatusName(r.ClockStatus)
	r.KeyIDHex = hex.EncodeToString(r.KeyID[:])
	r.PrevHashHex = hex.EncodeToString(r.PrevHash[:])
	r.CurrentHashHex = hex.EncodeToString(r.CurrentHash[:])
	r.SignatureHex = hex.EncodeToString(r.Signature[:])
	r.PayloadText = string(r.Payload)
	if r.ServerTSNs > 0 {
		r.ServerTime = time.Unix(0, r.ServerTSNs).UTC().Format(time.RFC3339Nano)
	}
}

// ComputeCanonicalHash gera o digest SHA-256 exatamente como especificado na Seção 5.3
// current_hash = SHA-256(
//   "LEDGER-V1" ||
//   u8(record_type) || u8(env) || u8(clock_status) ||
//   u64(id) || u64(server_ts_ns) || u64(seq) ||
//   key_id[8] ||
//   u32(len(device_id)) || device_id ||
//   prev_hash[32] ||
//   u32(len(payload)) || payload
// )
func ComputeCanonicalHash(r *Record) [32]byte {
	h := sha256.New()
	h.Write([]byte(LedgerVersionMagic))
	h.Write([]byte{r.RecordType, r.Env, r.ClockStatus})

	_ = binary.Write(h, binary.BigEndian, r.ID)
	_ = binary.Write(h, binary.BigEndian, r.ServerTSNs)
	_ = binary.Write(h, binary.BigEndian, r.Seq)

	h.Write(r.KeyID[:])

	devIDBytes := []byte(r.DeviceID)
	_ = binary.Write(h, binary.BigEndian, uint32(len(devIDBytes)))
	h.Write(devIDBytes)

	h.Write(r.PrevHash[:])

	_ = binary.Write(h, binary.BigEndian, uint32(len(r.Payload)))
	h.Write(r.Payload)

	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}
