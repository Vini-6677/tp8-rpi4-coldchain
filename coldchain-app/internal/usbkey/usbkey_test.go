package usbkey

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUSBKeyGenerateWriteVerifyAndChallengeSign(t *testing.T) {
	tmpMount := t.TempDir()
	hwID := "USB:SANDISK:ULTRA:4C530001231029108412"

	kf, rawJSON, pubKeyB64, err := GenerateProvisionedKey("tecnico.maria", hwID, BindingHardware)
	if err != nil {
		t.Fatalf("GenerateProvisionedKey falhou: %v", err)
	}
	if err := ValidatePublicKeyB64(pubKeyB64); err != nil {
		t.Fatalf("chave pública inválida: %v", err)
	}
	// A chave privada nunca pode aparecer em texto claro no JSON
	if strings.Contains(string(rawJSON), "PRIVATE KEY") {
		t.Fatalf("chave privada não deve aparecer em texto claro")
	}

	// Gravação atômica com fsync + read-back verification
	writtenPath, shaHex, err := WriteAndVerifyKeyFile(tmpMount, rawJSON)
	if err != nil {
		t.Fatalf("WriteAndVerifyKeyFile falhou: %v", err)
	}
	if len(shaHex) != 64 {
		t.Fatalf("esperado sha256 de 64 chars, obteve %d", len(shaHex))
	}
	if writtenPath != KeyFilePath(tmpMount) {
		t.Fatalf("caminho inesperado: %s", writtenPath)
	}

	// Leitura e inspeção do arquivo gravado
	_, loadedKF, status, err := ReadKeyFileFromMount(tmpMount)
	if err != nil || status != KeyStatusValid {
		t.Fatalf("ReadKeyFileFromMount falhou: status=%s err=%v", status, err)
	}
	if loadedKF.KeyID != kf.KeyID || loadedKF.Username != "tecnico.maria" {
		t.Fatalf("metadados divergentes após leitura: %+v", loadedKF)
	}

	// Fluxo Challenge-Response com o mesmo hardware (DEVE PASSAR)
	bindHash := ComputeDeviceBindingHash(hwID, loadedKF.KDFSalt)
	msg := BuildAuthChallengeMessage("chal-001", "bm9uY2UtMzItYnl0ZXM=", loadedKF.KeyID, bindHash, PurposeLogin)
	sigB64, signedBindHash, err := UnlockAndSign(loadedKF, hwID, msg)
	if err != nil {
		t.Fatalf("UnlockAndSign falhou com hardware legítimo: %v", err)
	}
	if signedBindHash != loadedKF.DeviceBindingHash {
		t.Fatalf("binding hash divergente")
	}
	if !VerifyChallengeSignature(pubKeyB64, msg, sigB64) {
		t.Fatalf("VerifyChallengeSignature rejeitou assinatura Ed25519 válida")
	}

	// Assinatura para mensagem alterada deve falhar
	if VerifyChallengeSignature(pubKeyB64, msg+"-tampered", sigB64) {
		t.Fatalf("VerifyChallengeSignature aceitou mensagem adulterada")
	}

	// Simulação de clonagem (arquivo copiado para outro pen drive com serial físico diferente)
	clonedHWID := "USB:KINGSTON:DATATRAVELER:9999888877776666"
	_, _, err = UnlockAndSign(loadedKF, clonedHWID, msg)
	if !errors.Is(err, ErrDeviceBindingMismatch) {
		t.Fatalf("esperava ErrDeviceBindingMismatch ao clonar chave para outro pen drive, obteve: %v", err)
	}

	// Inspeção de drive clonado deve marcar BINDING_MISMATCH
	clonedDrive := USBDrive{
		ID:        "F:",
		MountPath: tmpMount,
		BindingID: clonedHWID,
	}
	InspectDriveKey(&clonedDrive)
	if clonedDrive.KeyStatus != KeyStatusBindingMismatch || clonedDrive.KeyBindingMatch {
		t.Fatalf("esperava KeyStatusBindingMismatch no drive clonado, obteve status=%s match=%v", clonedDrive.KeyStatus, clonedDrive.KeyBindingMatch)
	}
}

func TestUSBKeyCorruptedAndMissingFiles(t *testing.T) {
	emptyDir := t.TempDir()
	_, _, status, err := ReadKeyFileFromMount(emptyDir)
	if err != nil || status != KeyStatusNoKey {
		t.Fatalf("esperava NO_KEY em pen drive vazio, obteve status=%s err=%v", status, err)
	}

	// Arquivo corrompido / adulterado
	hwID := "USB:TEST:KEY:ABCD12345678"
	_, rawJSON, _, err := GenerateProvisionedKey("enfermeiro.joao", hwID, BindingHardware)
	if err != nil {
		t.Fatalf("GenerateProvisionedKey falhou: %v", err)
	}
	if _, _, err := WriteAndVerifyKeyFile(emptyDir, rawJSON); err != nil {
		t.Fatalf("WriteAndVerifyKeyFile falhou: %v", err)
	}

	// Adultera o username no arquivo sem recomputar hash
	tampered := bytes.Replace(rawJSON, []byte("enfermeiro.joao"), []byte("admin          "), 1)
	if err := os.WriteFile(filepath.Join(emptyDir, KeyRelDir, KeyFileName), tampered, 0600); err != nil {
		t.Fatalf("falha ao escrever arquivo adulterado: %v", err)
	}
	_, _, status, err = ReadKeyFileFromMount(emptyDir)
	if status != KeyStatusCorrupted || !errors.Is(err, ErrCorruptedKeyFile) {
		t.Fatalf("esperava CORRUPTED/ErrCorruptedKeyFile para arquivo adulterado, obteve status=%s err=%v", status, err)
	}
}
