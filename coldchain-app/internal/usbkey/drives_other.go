//go:build !windows && !linux

package usbkey

import "errors"

// ListRemovableUSBDrives retorna erro explícito em sistemas operacionais sem implementação nativa.
func ListRemovableUSBDrives() ([]USBDrive, error) {
	return nil, errors.New("detecção automática de pen drives USB disponível apenas em Windows e Linux")
}
