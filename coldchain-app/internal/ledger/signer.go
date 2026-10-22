package ledger

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sync"
)

// Signer define a abstração unificada do assinador criptográfico (Seção 6.1)
// Agora: FileSigner (chave ECDSA P-256 em arquivo na flash)
// Futuro: ATECCSigner (chip criptográfico de hardware ATECC608)
type Signer interface {
	Public() crypto.PublicKey
	Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error)
	KeyID() [8]byte
	PublicKeyPEM() (string, error)
	PublicKeyRaw() []byte
}

// FileSigner implementa Signer armazenando a chave privada P-256 em flash (Seção 6.2)
type FileSigner struct {
	mu      sync.RWMutex
	privKey *ecdsa.PrivateKey
	pubKey  *ecdsa.PublicKey
	keyID   [8]byte
	path    string
}

// NewFileSigner carrega uma chave existente ou gera uma nova de forma atômica
func NewFileSigner(keyPath string) (*FileSigner, error) {
	if keyPath == "" {
		keyPath = "/data/coldchain/server_ledger_key.pem"
	}

	fs := &FileSigner{path: keyPath}
	if err := fs.loadOrGenerate(); err != nil {
		return nil, fmt.Errorf("falha ao inicializar FileSigner: %w", err)
	}
	return fs, nil
}

func (fs *FileSigner) loadOrGenerate() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// 1. Tenta carregar se o arquivo já existir
	if data, err := os.ReadFile(fs.path); err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return errors.New("arquivo de chave PEM inválido ou corrompido")
		}
		priv, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			// Tenta como PKCS8
			k, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err2 != nil {
				return fmt.Errorf("erro ao decodificar chave privada EC: %v / %v", err, err2)
			}
			var ok bool
			priv, ok = k.(*ecdsa.PrivateKey)
			if !ok {
				return errors.New("chave não é ECDSA P-256")
			}
		}
		fs.privKey = priv
		fs.pubKey = &priv.PublicKey
		fs.computeKeyID()
		return nil
	}

	// 2. Provisionamento único: Gera nova chave ECDSA P-256
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("falha ao gerar chave P-256: %w", err)
	}

	dir := filepath.Dir(fs.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("falha ao criar diretório da chave: %w", err)
	}

	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("falha ao serializar chave: %w", err)
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: der,
	})

	// Escrita atômica (Seção 6.2): arquivo temporário -> fsync -> rename
	tmpPath := fs.path + ".tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0400)
	if err != nil {
		return fmt.Errorf("falha ao criar arquivo temporário da chave: %w", err)
	}
	if _, err := tmpFile.Write(pemBytes); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, fs.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("falha ao renomear chave atômica: %w", err)
	}

	_ = os.Chmod(fs.path, 0400)

	fs.privKey = priv
	fs.pubKey = &priv.PublicKey
	fs.computeKeyID()
	return nil
}

func (fs *FileSigner) computeKeyID() {
	der, _ := x509.MarshalPKIXPublicKey(fs.pubKey)
	hash := sha256.Sum256(der)
	copy(fs.keyID[:], hash[:8])
}

// Public retorna a chave pública
func (fs *FileSigner) Public() crypto.PublicKey {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.pubKey
}

// KeyID retorna os 8 primeiros bytes do SHA-256 da chave pública
func (fs *FileSigner) KeyID() [8]byte {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.keyID
}

// Sign assina o digest SHA-256 e produz EXATAMENTE 64 bytes raw (r || s), compatível com ATECC608 (Seção 5.4)
func (fs *FileSigner) Sign(_ io.Reader, digest []byte, _ crypto.SignerOpts) ([]byte, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	if fs.privKey == nil {
		return nil, errors.New("chave privada não carregada")
	}

	r, s, err := ecdsa.Sign(rand.Reader, fs.privKey, digest)
	if err != nil {
		return nil, fmt.Errorf("erro ao gerar assinatura ECDSA: %w", err)
	}

	return EncodeRawSignature(r, s), nil
}

// PublicKeyPEM retorna a chave pública em formato PEM
func (fs *FileSigner) PublicKeyPEM() (string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	der, err := x509.MarshalPKIXPublicKey(fs.pubKey)
	if err != nil {
		return "", err
	}
	block := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: der,
	}
	return string(pem.EncodeToMemory(block)), nil
}

// PublicKeyRaw retorna os bytes comprimidos da chave pública (33 bytes)
func (fs *FileSigner) PublicKeyRaw() []byte {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return elliptic.MarshalCompressed(elliptic.P256(), fs.pubKey.X, fs.pubKey.Y)
}

// EncodeRawSignature codifica r e s como 64 bytes raw (32 bytes r + 32 bytes s em Big-Endian)
func EncodeRawSignature(r, s *big.Int) []byte {
	var raw [64]byte
	rBytes := r.Bytes()
	sBytes := s.Bytes()

	// r preenchido com zeros à esquerda
	copy(raw[32-len(rBytes):32], rBytes)
	// s preenchido com zeros à esquerda
	copy(raw[64-len(sBytes):64], sBytes)

	return raw[:]
}

// DecodeRawSignature decodifica 64 bytes raw em inteiros r e s
func DecodeRawSignature(sig []byte) (*big.Int, *big.Int, error) {
	if len(sig) != 64 {
		return nil, nil, fmt.Errorf("assinatura raw deve ter exatamente 64 bytes (recebido %d)", len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return r, s, nil
}

// VerifySignature valida a assinatura raw r||s contra o digest usando a chave pública ECDSA P-256
func VerifySignature(pubKey *ecdsa.PublicKey, digest []byte, sigRaw []byte) bool {
	if pubKey == nil || len(digest) != 32 || len(sigRaw) != 64 {
		return false
	}
	r, s, err := DecodeRawSignature(sigRaw)
	if err != nil {
		return false
	}
	return ecdsa.Verify(pubKey, digest, r, s)
}
