//go:build linux

package usbkey

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ListRemovableUSBDrives detecta exclusivamente pen drives USB removíveis montados no Linux,
// filtrando rigorosamente discos internos (mmcblk*, nvme*, loop*, dm-*) e partições do SO (/, /boot, /data, /var).
func ListRemovableUSBDrives() ([]USBDrive, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("falha ao ler /proc/mounts: %w", err)
	}
	defer f.Close()

	blockedMounts := map[string]bool{
		"/": true, "/boot": true, "/boot/efi": true, "/data": true,
		"/var": true, "/usr": true, "/etc": true, "/home": true, "/tmp": true, "/opt": true,
	}

	seen := map[string]bool{}
	drives := make([]USBDrive, 0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		devPath := fields[0]
		mountPath := unescapeMountPath(fields[1])
		opts := fields[3]

		if !strings.HasPrefix(devPath, "/dev/sd") {
			continue // Exclui mmcblk, nvme, loop, dm, tmpfs, overlay, etc.
		}
		if blockedMounts[mountPath] || seen[mountPath] {
			continue
		}

		baseDev := filepath.Base(devPath) // ex: sdb1 ou sdb
		parentDisk := strings.TrimRight(baseDev, "0123456789")
		if parentDisk == "" {
			continue
		}

		// 1. Verifica flag removable == 1 em sysfs
		remBytes, err := os.ReadFile(filepath.Join("/sys/block", parentDisk, "removable"))
		if err != nil || strings.TrimSpace(string(remBytes)) != "1" {
			continue
		}

		// 2. Confirma que o barramento físico no sysfs é USB
		realSysPath, err := filepath.EvalSymlinks(filepath.Join("/sys/block", parentDisk))
		if err != nil || !strings.Contains(realSysPath, "/usb") {
			continue
		}

		seen[mountPath] = true
		vendor, product, hwSerial := readLinuxUSBIdentity(realSysPath)
		volUUID := findLinuxDiskSymlinkName("/dev/disk/by-uuid", devPath)
		label := findLinuxDiskSymlinkName("/dev/disk/by-label", devPath)
		if label == "" {
			label = filepath.Base(mountPath)
		}

		var totalBytes, freeBytes uint64
		var st syscall.Statfs_t
		if err := syscall.Statfs(mountPath, &st); err == nil {
			totalBytes = st.Blocks * uint64(st.Bsize)
			freeBytes = st.Bavail * uint64(st.Bsize)
		}

		writable := true
		for _, opt := range strings.Split(opts, ",") {
			if opt == "ro" {
				writable = false
				break
			}
		}

		bindingMode := BindingVolumeUUID
		bindingID := "VOL:" + volUUID
		serialDisplay := volUUID
		if IsValidHardwareSerial(hwSerial) {
			bindingMode = BindingHardware
			bindingID = fmt.Sprintf("USB:%s:%s:%s", vendor, product, hwSerial)
			serialDisplay = hwSerial
		} else if volUUID == "" {
			bindingID = "DEV:" + parentDisk + ":" + label
			serialDisplay = parentDisk
		}

		d := USBDrive{
			ID:             devPath,
			MountPath:      mountPath,
			Label:          label,
			Vendor:         vendor,
			Product:        product,
			HardwareSerial: hwSerial,
			SerialMasked:   MaskSerial(serialDisplay),
			VolumeUUID:     volUUID,
			BindingMode:    bindingMode,
			BindingID:      bindingID,
			TotalBytes:     totalBytes,
			FreeBytes:      freeBytes,
			Writable:       writable,
		}
		InspectDriveKey(&d)
		drives = append(drives, d)
	}

	return drives, nil
}

func readLinuxUSBIdentity(sysPath string) (vendor, product, serial string) {
	cur := sysPath
	for i := 0; i < 10 && cur != "/" && cur != "."; i++ {
		if b, err := os.ReadFile(filepath.Join(cur, "serial")); err == nil {
			serial = strings.TrimSpace(string(b))
			if vb, err := os.ReadFile(filepath.Join(cur, "manufacturer")); err == nil {
				vendor = strings.TrimSpace(string(vb))
			} else if vb, err := os.ReadFile(filepath.Join(cur, "idVendor")); err == nil {
				vendor = strings.TrimSpace(string(vb))
			}
			if pb, err := os.ReadFile(filepath.Join(cur, "product")); err == nil {
				product = strings.TrimSpace(string(pb))
			} else if pb, err := os.ReadFile(filepath.Join(cur, "idProduct")); err == nil {
				product = strings.TrimSpace(string(pb))
			}
			return vendor, product, serial
		}
		cur = filepath.Dir(cur)
	}
	return vendor, product, serial
}

func findLinuxDiskSymlinkName(dir, targetDev string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		resolved, err := filepath.EvalSymlinks(full)
		if err == nil && resolved == targetDev {
			return e.Name()
		}
	}
	return ""
}

func unescapeMountPath(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}
