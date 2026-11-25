package usbkey

import (
	"crypto/subtle"
	"strings"
)

// USBDrive descreve uma unidade USB removível validada (nunca discos internos/SO).
type USBDrive struct {
	ID              string `json:"id"`
	MountPath       string `json:"mount_path"`
	Label           string `json:"label"`
	Vendor          string `json:"vendor"`
	Product         string `json:"product"`
	HardwareSerial  string `json:"-"`
	SerialMasked    string `json:"serial_masked"`
	VolumeUUID      string `json:"-"`
	BindingMode     string `json:"binding_mode"`
	BindingID       string `json:"-"`
	TotalBytes      uint64 `json:"total_bytes"`
	FreeBytes       uint64 `json:"free_bytes"`
	Writable        bool   `json:"writable"`
	KeyStatus       string `json:"key_status"`
	KeyID           string `json:"key_id,omitempty"`
	KeyUsername     string `json:"key_username,omitempty"`
	KeyIssuedAt     string `json:"key_issued_at,omitempty"`
	KeyBindingMatch bool   `json:"key_binding_match"`
	KeyError        string `json:"key_error,omitempty"`
}

// VendorProductLabel formata fabricante e modelo para exibição e auditoria.
func (d USBDrive) VendorProductLabel() string {
	v := strings.TrimSpace(d.Vendor)
	p := strings.TrimSpace(d.Product)
	switch {
	case v != "" && p != "":
		return v + " " + p
	case p != "":
		return p
	case v != "":
		return v
	default:
		return "USB Mass Storage"
	}
}

// InspectDriveKey inspeciona o arquivo .coldchain/credential.cckey no pen drive e preenche os metadados.
func InspectDriveKey(d *USBDrive) {
	if d == nil {
		return
	}
	_, kf, status, err := ReadKeyFileFromMount(d.MountPath)
	d.KeyStatus = status
	if err != nil {
		d.KeyError = err.Error()
		return
	}
	if status != KeyStatusValid || kf == nil {
		return
	}
	d.KeyID = kf.KeyID
	d.KeyUsername = kf.Username
	d.KeyIssuedAt = kf.IssuedAt

	// Verifica vínculo de hardware sem expor a chave privada
	expectedHash := ComputeDeviceBindingHash(d.BindingID, kf.KDFSalt)
	if subtle.ConstantTimeCompare([]byte(expectedHash), []byte(strings.ToLower(kf.DeviceBindingHash))) == 1 {
		d.KeyBindingMatch = true
	} else {
		d.KeyBindingMatch = false
		d.KeyStatus = KeyStatusBindingMismatch
		d.KeyError = ErrDeviceBindingMismatch.Error()
	}
}

// IsValidHardwareSerial verifica se um serial reportado pelo controlador USB é realmente utilizável (não genérico/vazio/zeros).
func IsValidHardwareSerial(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return false
	}
	lower := strings.ToLower(s)
	if lower == "0000" || lower == "00000000" || lower == "000000000000" || lower == "none" || lower == "n/a" || lower == "unknown" {
		return false
	}
	allZero := true
	for _, r := range s {
		if r != '0' && r != '-' && r != '_' {
			allZero = false
			break
		}
	}
	return !allZero
}
