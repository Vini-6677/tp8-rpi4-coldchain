package usbkey

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	KeyRelDir   = ".coldchain"
	KeyFileName = "credential.cckey"

	KeyStatusValid            = "VALID_FILE"
	KeyStatusNoKey            = "NO_KEY"
	KeyStatusCorrupted        = "CORRUPTED"
	KeyStatusPermissionDenied = "PERMISSION_DENIED"
	KeyStatusBindingMismatch  = "BINDING_MISMATCH"
)

// KeyFilePath retorna o caminho completo esperado da credencial dentro de um ponto de montagem USB.
func KeyFilePath(mountRoot string) string {
	return filepath.Join(mountRoot, KeyRelDir, KeyFileName)
}

// WriteAndVerifyKeyFile grava o arquivo de chave de forma atômica no pen drive com fsync
// e executa leitura de confirmação (read-back + comparação SHA-256 e validação estrutural).
func WriteAndVerifyKeyFile(mountRoot string, payload []byte) (string, string, error) {
	if mountRoot == "" {
		return "", "", errors.New("caminho do pen drive não informado")
	}
	if len(payload) == 0 {
		return "", "", errors.New("conteúdo da chave vazio")
	}
	if _, err := ValidateKeyFileIntegrity(payload); err != nil {
		return "", "", fmt.Errorf("payload de chave inválido antes da escrita: %w", err)
	}

	dirPath := filepath.Join(mountRoot, KeyRelDir)
	if err := os.MkdirAll(dirPath, 0700); err != nil {
		if os.IsPermission(err) {
			return "", "", fmt.Errorf("permissão negada para criar diretório de chave no pen drive (%s): verifique se o dispositivo está protegido contra gravação", mountRoot)
		}
		return "", "", fmt.Errorf("falha ao preparar diretório no pen drive: %w", err)
	}

	randSuffix := make([]byte, 6)
	_, _ = rand.Read(randSuffix)
	tmpPath := filepath.Join(dirPath, fmt.Sprintf("%s.tmp.%s", KeyFileName, hex.EncodeToString(randSuffix)))
	finalPath := filepath.Join(dirPath, KeyFileName)

	// 1. Gravação em arquivo temporário + fsync obrigatório
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		if os.IsPermission(err) {
			return "", "", fmt.Errorf("permissão negada ao gravar no pen drive (%s): mídia somente leitura ou sem privilégio de escrita", mountRoot)
		}
		return "", "", fmt.Errorf("falha ao abrir arquivo temporário no pen drive: %w", err)
	}

	n, writeErr := f.Write(payload)
	if writeErr != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("falha durante escrita da chave no pen drive: %w", writeErr)
	}
	if n != len(payload) {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", "", errors.New("escrita incompleta no pen drive (possível falta de espaço livre)")
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("falha no fsync ao confirmar dados no pen drive: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("falha ao fechar arquivo gravado no pen drive: %w", err)
	}

	// 2. Rename atômico sobre o destino (no Windows, remove destino anterior se existir para atomicidade consistente)
	if _, statErr := os.Stat(finalPath); statErr == nil {
		_ = os.Chmod(finalPath, 0600)
		_ = os.Remove(finalPath)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("falha ao consolidar arquivo da chave no pen drive: %w", err)
	}

	// Tenta sincronizar o diretório (no-op em sistemas onde dir sync não se aplica)
	if d, dErr := os.Open(dirPath); dErr == nil {
		_ = d.Sync()
		_ = d.Close()
	}

	// 3. Leitura de confirmação (read-back) e verificação criptográfica de integridade
	readBack, err := os.ReadFile(finalPath)
	if err != nil {
		return "", "", fmt.Errorf("falha na leitura de confirmação pós-escrita no pen drive: %w", err)
	}
	expectedSum := sha256.Sum256(payload)
	actualSum := sha256.Sum256(readBack)
	if !bytes.Equal(expectedSum[:], actualSum[:]) || !bytes.Equal(payload, readBack) {
		_ = os.Remove(finalPath)
		return "", "", errors.New("falha de integridade na leitura pós-escrita (read-back SHA-256 divergente)")
	}
	if _, err := ValidateKeyFileIntegrity(readBack); err != nil {
		_ = os.Remove(finalPath)
		return "", "", fmt.Errorf("arquivo gravado falhou na validação estrutural pós-escrita: %w", err)
	}

	return finalPath, hex.EncodeToString(actualSum[:]), nil
}

// ReadKeyFileFromMount lê e valida a estrutura da chave em um pen drive montado.
func ReadKeyFileFromMount(mountRoot string) ([]byte, *KeyFile, string, error) {
	path := KeyFilePath(mountRoot)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, KeyStatusNoKey, nil
		}
		if os.IsPermission(err) {
			return nil, nil, KeyStatusPermissionDenied, fmt.Errorf("permissão negada para ler a chave no pen drive (%s)", mountRoot)
		}
		return nil, nil, KeyStatusCorrupted, fmt.Errorf("erro ao ler chave no pen drive (%s): %w", mountRoot, err)
	}
	kf, err := ValidateKeyFileIntegrity(raw)
	if err != nil {
		return raw, nil, KeyStatusCorrupted, err
	}
	return raw, kf, KeyStatusValid, nil
}
