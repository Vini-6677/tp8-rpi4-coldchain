//go:build windows

package usbkey

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	driveRemovable            = 2
	ioctlStorageQueryProperty = 0x002D1400
	storageDeviceProperty     = 0
	propertyStandardQuery     = 0
	busTypeUSB                = 7
	fileReadOnlyVolume        = 0x00080000
)

var (
	kernel32DLL             = syscall.NewLazyDLL("kernel32.dll")
	procGetLogicalDrives    = kernel32DLL.NewProc("GetLogicalDrives")
	procGetDriveTypeW       = kernel32DLL.NewProc("GetDriveTypeW")
	procGetVolumeInfoW      = kernel32DLL.NewProc("GetVolumeInformationW")
	procGetDiskFreeSpaceExW = kernel32DLL.NewProc("GetDiskFreeSpaceExW")
)

type storagePropertyQuery struct {
	PropertyID uint32
	QueryType  uint32
	Additional [1]byte
}

// storageDeviceDescriptorHeader corresponde aos primeiros 40 bytes de STORAGE_DEVICE_DESCRIPTOR no Windows SDK.
type storageDeviceDescriptorHeader struct {
	Version               uint32
	Size                  uint32
	DeviceType            byte
	DeviceTypeModifier    byte
	RemovableMedia        byte
	CommandQueueing       byte
	VendorIDOffset        uint32
	ProductIDOffset       uint32
	ProductRevisionOffset uint32
	SerialNumberOffset    uint32
	BusType               uint32
	RawPropertiesLength   uint32
}

// ListRemovableUSBDrives detecta exclusivamente pen drives USB removíveis conectados no Windows,
// filtrando rigorosamente o disco do SO (%SystemDrive%), discos internos SATA/NVMe, unidades de rede e CD-ROM.
func ListRemovableUSBDrives() ([]USBDrive, error) {
	r1, _, callErr := procGetLogicalDrives.Call()
	mask := uint32(r1)
	if mask == 0 {
		return nil, fmt.Errorf("falha ao listar unidades lógicas do Windows: %w", callErr)
	}

	sysDrive := strings.ToUpper(strings.TrimSpace(os.Getenv("SystemDrive")))
	if sysDrive == "" {
		sysDrive = "C:"
	}

	drives := make([]USBDrive, 0)
	for i := 0; i < 26; i++ {
		if (mask & (1 << uint(i))) == 0 {
			continue
		}
		letter := fmt.Sprintf("%c:", 'A'+i)
		if strings.EqualFold(letter, sysDrive) {
			continue // Proteção absoluta contra gravação na unidade do sistema operacional
		}
		rootPath := letter + `\`
		rootUTF16, err := syscall.UTF16PtrFromString(rootPath)
		if err != nil {
			continue
		}

		// 1. Deve ser DRIVE_REMOVABLE (2)
		dt, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(rootUTF16)))
		if uint32(dt) != driveRemovable {
			continue
		}

		// 2. Consulta descritor de hardware via IOCTL_STORAGE_QUERY_PROPERTY (exige BusTypeUsb e RemovableMedia)
		vendor, product, hwSerial, busType, removable, ioctlOK := queryWindowsStorageDescriptor(letter)
		if ioctlOK {
			if busType != busTypeUSB || !removable {
				continue
			}
		} else {
			// Se o dispositivo não tem mídia inserida (ex.: leitor de cartão vazio) ou não respondeu, ignora
			if _, statErr := os.Stat(rootPath); statErr != nil {
				continue
			}
		}

		// 3. Informações do volume (rótulo, serial do volume, flag somente leitura)
		var volNameBuf [256]uint16
		var fsNameBuf [64]uint16
		var volSerial uint32
		var maxCompLen uint32
		var fsFlags uint32
		okVol, _, _ := procGetVolumeInfoW.Call(
			uintptr(unsafe.Pointer(rootUTF16)),
			uintptr(unsafe.Pointer(&volNameBuf[0])),
			uintptr(len(volNameBuf)),
			uintptr(unsafe.Pointer(&volSerial)),
			uintptr(unsafe.Pointer(&maxCompLen)),
			uintptr(unsafe.Pointer(&fsFlags)),
			uintptr(unsafe.Pointer(&fsNameBuf[0])),
			uintptr(len(fsNameBuf)),
		)
		if okVol == 0 {
			// Mídia ausente ou inacessível
			continue
		}

		var freeAvail, totalBytes, totalFree uint64
		_, _, _ = procGetDiskFreeSpaceExW.Call(
			uintptr(unsafe.Pointer(rootUTF16)),
			uintptr(unsafe.Pointer(&freeAvail)),
			uintptr(unsafe.Pointer(&totalBytes)),
			uintptr(unsafe.Pointer(&totalFree)),
		)

		volLabel := strings.TrimSpace(syscall.UTF16ToString(volNameBuf[:]))
		if volLabel == "" {
			volLabel = "Pen Drive (" + letter + ")"
		}
		volUUID := fmt.Sprintf("%08X", volSerial)

		bindingMode := BindingVolumeUUID
		bindingID := "VOL:" + volUUID
		serialDisplay := volUUID
		if IsValidHardwareSerial(hwSerial) {
			bindingMode = BindingHardware
			bindingID = fmt.Sprintf("USB:%s:%s:%s", strings.TrimSpace(vendor), strings.TrimSpace(product), strings.TrimSpace(hwSerial))
			serialDisplay = hwSerial
		}

		d := USBDrive{
			ID:             letter,
			MountPath:      rootPath,
			Label:          volLabel,
			Vendor:         vendor,
			Product:        product,
			HardwareSerial: hwSerial,
			SerialMasked:   MaskSerial(serialDisplay),
			VolumeUUID:     volUUID,
			BindingMode:    bindingMode,
			BindingID:      bindingID,
			TotalBytes:     totalBytes,
			FreeBytes:      freeAvail,
			Writable:       (fsFlags & fileReadOnlyVolume) == 0,
		}
		InspectDriveKey(&d)
		drives = append(drives, d)
	}
	return drives, nil
}

func queryWindowsStorageDescriptor(driveLetter string) (vendor, product, serial string, busType uint32, removable bool, ok bool) {
	devPath := `\\.\` + driveLetter
	devUTF16, err := syscall.UTF16PtrFromString(devPath)
	if err != nil {
		return "", "", "", 0, false, false
	}

	// Acesso 0 permite ler propriedades do dispositivo sem privilégios de Administrador
	handle, err := syscall.CreateFile(
		devUTF16,
		0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil,
		syscall.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil || handle == syscall.InvalidHandle {
		return "", "", "", 0, false, false
	}
	defer syscall.CloseHandle(handle)

	query := storagePropertyQuery{
		PropertyID: storageDeviceProperty,
		QueryType:  propertyStandardQuery,
	}
	outBuf := make([]byte, 1024)
	var bytesReturned uint32

	err = syscall.DeviceIoControl(
		handle,
		ioctlStorageQueryProperty,
		(*byte)(unsafe.Pointer(&query)),
		uint32(unsafe.Sizeof(query)),
		&outBuf[0],
		uint32(len(outBuf)),
		&bytesReturned,
		nil,
	)
	if err != nil || bytesReturned < uint32(unsafe.Sizeof(storageDeviceDescriptorHeader{})) {
		return "", "", "", 0, false, false
	}

	var hdr storageDeviceDescriptorHeader
	if err := binary.Read(bytes.NewReader(outBuf[:bytesReturned]), binary.LittleEndian, &hdr); err != nil {
		return "", "", "", 0, false, false
	}

	vendor = extractCString(outBuf[:bytesReturned], hdr.VendorIDOffset)
	product = extractCString(outBuf[:bytesReturned], hdr.ProductIDOffset)
	serial = extractCString(outBuf[:bytesReturned], hdr.SerialNumberOffset)
	return vendor, product, serial, hdr.BusType, hdr.RemovableMedia != 0, true
}

func extractCString(buf []byte, offset uint32) string {
	if offset == 0 || int(offset) >= len(buf) {
		return ""
	}
	sub := buf[offset:]
	end := bytes.IndexByte(sub, 0)
	if end >= 0 {
		sub = sub[:end]
	}
	return strings.TrimSpace(string(sub))
}
