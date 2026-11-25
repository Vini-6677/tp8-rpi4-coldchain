package usbkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	KeyFileVersion       = 1
	AlgorithmEd25519     = "Ed25519"
	KDFNameHKDFSHA256    = "HKDF-SHA256"
	CipherNameAES256GCM  = "AES-256-GCM"
	BindingHardware      = "HARDWARE_SERIAL"
	BindingVolumeUUID    = "VOLUME_UUID"
	PurposeLogin         = "LOGIN"
	PurposeSignAction    = "SIGN_ACTION"
	authDomainPrefix     = "COLDCHAIN-USB-V1"
	bindingDomainPrefix  = "COLDCHAIN-USB-BIND-V1"
	hkdfInfoString       = "coldchain-usb-key-kek-v1"
)

var (
	ErrCorruptedKeyFile      = errors.New("arquivo de chave corrompido ou adulterado")
	ErrUnsupportedKeyVersion = errors.New("versão ou algoritmo de chave não suportado")
	ErrDeviceBindingMismatch = errors.New("dispositivo USB não corresponde ao hardware vinculado (possível clonagem bloqueada)")
	ErrInvalidHardwareID     = errors.New("identificador físico do pen drive inválido")
)

// KeyFile representa o arquivo criptografado gravado em .coldchain/credential.cckey no pen drive.
type KeyFile struct {
	Version             int    `json:"version"`
	KeyID               string `json:"key_id"`
	Username            string `json:"username"`
	Algorithm           string `json:"algorithm"`
	BindingMode         string `json:"binding_mode"`
	DeviceBindingHash   string `json:"device_binding_hash"`
	KDF                 string `json:"kdf"`
	KDFSalt             string `json:"kdf_salt"`
	Cipher              string `json:"cipher"`
	CipherNonce         string `json:"cipher_nonce"`
	EncryptedPrivateKey string `json:"encrypted_private_key"`
	IssuedAt            string `json:"issued_at"`
	FileSHA256          string `json:"file_sha256"`
}

// canonicalKeyFilePayload é usado para cálculo determinístico do FileSHA256.
type canonicalKeyFilePayload struct {
	Version             int    `json:"version"`
	KeyID               string `json:"key_id"`
	Username            string `json:"username"`
	Algorithm           string `json:"algorithm"`
	BindingMode         string `json:"binding_mode"`
	DeviceBindingHash   string `json:"device_binding_hash"`
	KDF                 string `json:"kdf"`
	KDFSalt             string `json:"kdf_salt"`
	Cipher              string `json:"cipher"`
	CipherNonce         string `json:"cipher_nonce"`
	EncryptedPrivateKey string `json:"encrypted_private_key"`
	IssuedAt            string `json:"issued_at"`
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// hkdfSHA256 implementa RFC 5869 HKDF-Extract + HKDF-Expand com SHA-256 usando apenas stdlib.
func hkdfSHA256(ikm, salt, info []byte, length int) ([]byte, error) {
	if length <= 0 || length > 255*sha256.Size {
		return nil, fmt.Errorf("tamanho de derivação HKDF inválido: %d", length)
	}
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}
	// Extract: PRK = HMAC-SHA256(salt, IKM)
	extractMAC := hmac.New(sha256.New, salt)
	extractMAC.Write(ikm)
	prk := extractMAC.Sum(nil)
	defer zeroBytes(prk)

	// Expand
	out := make([]byte, 0, length)
	var prev []byte
	for counter := byte(1); len(out) < length; counter++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(prev)
		mac.Write(info)
		mac.Write([]byte{counter})
		prev = mac.Sum(nil)
		need := length - len(out)
		if need > len(prev) {
			need = len(prev)
		}
		out = append(out, prev[:need]...)
	}
	return out, nil
}

// NormalizeHardwareID padroniza a string de identificação do pen drive para derivação determinística.
func NormalizeHardwareID(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// ComputeDeviceBindingHash calcula o hash SHA-256 de vínculo entre o hardware USB e o salt da chave.
func ComputeDeviceBindingHash(hardwareID, kdfSaltB64 string) string {
	norm := NormalizeHardwareID(hardwareID)
	h := sha256.New()
	h.Write([]byte(bindingDomainPrefix))
	h.Write([]byte("|"))
	h.Write([]byte(norm))
	h.Write([]byte("|"))
	h.Write([]byte(strings.TrimSpace(kdfSaltB64)))
	return hex.EncodeToString(h.Sum(nil))
}

func buildKeyAAD(version int, keyID, username, bindingHash string) []byte {
	return []byte(fmt.Sprintf("v%d|%s|%s|%s", version, keyID, username, bindingHash))
}

func computeKeyFileHash(kf *KeyFile) (string, error) {
	canon := canonicalKeyFilePayload{
		Version:             kf.Version,
		KeyID:               kf.KeyID,
		Username:            kf.Username,
		Algorithm:           kf.Algorithm,
		BindingMode:         kf.BindingMode,
		DeviceBindingHash:   kf.DeviceBindingHash,
		KDF:                 kf.KDF,
		KDFSalt:             kf.KDFSalt,
		Cipher:              kf.Cipher,
		CipherNonce:         kf.CipherNonce,
		EncryptedPrivateKey: kf.EncryptedPrivateKey,
		IssuedAt:            kf.IssuedAt,
	}
	raw, err := json.Marshal(canon)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// GenerateProvisionedKey gera um par Ed25519, cifra a chave privada vinculada ao hardware USB
// e retorna o envelope pronto para gravar no pen drive + a chave pública base64 para o servidor.
func GenerateProvisionedKey(username, hardwareID, bindingMode string) (*KeyFile, []byte, string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return nil, nil, "", errors.New("usuário obrigatório para geração de chave USB")
	}
	normHW := NormalizeHardwareID(hardwareID)
	if len(normHW) < 4 {
		return nil, nil, "", ErrInvalidHardwareID
	}
	if bindingMode != BindingHardware && bindingMode != BindingVolumeUUID {
		bindingMode = BindingHardware
	}

	// Gera KeyID único (128 bits)
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, nil, "", fmt.Errorf("falha ao gerar key_id: %w", err)
	}
	keyID := "ccusb_" + hex.EncodeToString(idBytes)

	// Gera par Ed25519
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, "", fmt.Errorf("falha ao gerar par Ed25519: %w", err)
	}
	seed := append([]byte(nil), privKey.Seed()...)
	defer zeroBytes(seed)
	defer zeroBytes(privKey)

	// Gera salt HKDF (32B) e nonce AES-GCM (12B)
	kdfSalt := make([]byte, 32)
	if _, err := rand.Read(kdfSalt); err != nil {
		return nil, nil, "", fmt.Errorf("falha ao gerar salt: %w", err)
	}
	kdfSaltB64 := base64.StdEncoding.EncodeToString(kdfSalt)

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, "", fmt.Errorf("falha ao gerar nonce: %w", err)
	}

	bindingHash := ComputeDeviceBindingHash(normHW, kdfSaltB64)
	kek, err := hkdfSHA256([]byte(normHW), kdfSalt, []byte(hkdfInfoString), 32)
	if err != nil {
		return nil, nil, "", err
	}
	defer zeroBytes(kek)

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, nil, "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, "", err
	}

	aad := buildKeyAAD(KeyFileVersion, keyID, username, bindingHash)
	ciphertext := gcm.Seal(nil, nonce, seed, aad)

	kf := &KeyFile{
		Version:             KeyFileVersion,
		KeyID:               keyID,
		Username:            username,
		Algorithm:           AlgorithmEd25519,
		BindingMode:         bindingMode,
		DeviceBindingHash:   bindingHash,
		KDF:                 KDFNameHKDFSHA256,
		KDFSalt:             kdfSaltB64,
		Cipher:              CipherNameAES256GCM,
		CipherNonce:         base64.StdEncoding.EncodeToString(nonce),
		EncryptedPrivateKey: base64.StdEncoding.EncodeToString(ciphertext),
		IssuedAt:            time.Now().UTC().Format(time.RFC3339),
	}

	fileHash, err := computeKeyFileHash(kf)
	if err != nil {
		return nil, nil, "", err
	}
	kf.FileSHA256 = fileHash

	rawJSON, err := json.MarshalIndent(kf, "", "  ")
	if err != nil {
		return nil, nil, "", err
	}
	rawJSON = append(rawJSON, '\n')

	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)
	return kf, rawJSON, pubKeyB64, nil
}

// ValidateKeyFileIntegrity valida estrutura JSON, algoritmo e SHA-256 interno do arquivo do pen drive.
func ValidateKeyFileIntegrity(raw []byte) (*KeyFile, error) {
	if len(raw) == 0 || len(raw) > 64*1024 {
		return nil, ErrCorruptedKeyFile
	}
	var kf KeyFile
	if err := json.Unmarshal(raw, &kf); err != nil {
		return nil, fmt.Errorf("%w: JSON inválido", ErrCorruptedKeyFile)
	}
	if kf.Version != KeyFileVersion || kf.Algorithm != AlgorithmEd25519 || kf.KDF != KDFNameHKDFSHA256 || kf.Cipher != CipherNameAES256GCM {
		return nil, ErrUnsupportedKeyVersion
	}
	if kf.KeyID == "" || kf.Username == "" || kf.DeviceBindingHash == "" || kf.KDFSalt == "" || kf.CipherNonce == "" || kf.EncryptedPrivateKey == "" || kf.FileSHA256 == "" {
		return nil, fmt.Errorf("%w: campos obrigatórios ausentes", ErrCorruptedKeyFile)
	}

	expectedHash, err := computeKeyFileHash(&kf)
	if err != nil {
		return nil, ErrCorruptedKeyFile
	}
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(kf.FileSHA256)), []byte(expectedHash)) != 1 {
		return nil, fmt.Errorf("%w: hash interno divergente", ErrCorruptedKeyFile)
	}
	return &kf, nil
}

// BuildAuthChallengeMessage constrói a mensagem canônica assinada no protocolo Challenge-Response.
func BuildAuthChallengeMessage(challengeID, nonceB64, keyID, deviceBindingHash, purpose string) string {
	if purpose == "" {
		purpose = PurposeLogin
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s",
		authDomainPrefix,
		strings.ToUpper(strings.TrimSpace(purpose)),
		strings.TrimSpace(challengeID),
		strings.TrimSpace(nonceB64),
		strings.TrimSpace(keyID),
		strings.ToLower(strings.TrimSpace(deviceBindingHash)),
	)
}

// UnlockAndSign desbloqueia a chave privada Ed25519 usando o identificador físico atual do pen drive,
// assina o desafio e limpa imediatamente a chave da memória.
func UnlockAndSign(kf *KeyFile, currentHardwareID, challengeMessage string) (string, string, error) {
	if kf == nil {
		return "", "", ErrCorruptedKeyFile
	}
	normHW := NormalizeHardwareID(currentHardwareID)
	if len(normHW) < 4 {
		return "", "", ErrInvalidHardwareID
	}

	currentBindHash := ComputeDeviceBindingHash(normHW, kf.KDFSalt)
	if subtle.ConstantTimeCompare([]byte(currentBindHash), []byte(strings.ToLower(kf.DeviceBindingHash))) != 1 {
		return "", currentBindHash, ErrDeviceBindingMismatch
	}

	kdfSalt, err := base64.StdEncoding.DecodeString(kf.KDFSalt)
	if err != nil {
		return "", currentBindHash, ErrCorruptedKeyFile
	}
	nonce, err := base64.StdEncoding.DecodeString(kf.CipherNonce)
	if err != nil || len(nonce) != 12 {
		return "", currentBindHash, ErrCorruptedKeyFile
	}
	ciphertext, err := base64.StdEncoding.DecodeString(kf.EncryptedPrivateKey)
	if err != nil {
		return "", currentBindHash, ErrCorruptedKeyFile
	}

	kek, err := hkdfSHA256([]byte(normHW), kdfSalt, []byte(hkdfInfoString), 32)
	if err != nil {
		return "", currentBindHash, err
	}
	defer zeroBytes(kek)

	block, err := aes.NewCipher(kek)
	if err != nil {
		return "", currentBindHash, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", currentBindHash, err
	}

	aad := buildKeyAAD(kf.Version, kf.KeyID, kf.Username, kf.DeviceBindingHash)
	seed, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", currentBindHash, ErrDeviceBindingMismatch
	}
	defer zeroBytes(seed)

	if len(seed) != ed25519.SeedSize {
		return "", currentBindHash, ErrCorruptedKeyFile
	}

	privKey := ed25519.NewKeyFromSeed(seed)
	defer zeroBytes(privKey)

	sig := ed25519.Sign(privKey, []byte(challengeMessage))
	return base64.StdEncoding.EncodeToString(sig), currentBindHash, nil
}

// VerifyChallengeSignature valida uma assinatura Ed25519 no servidor utilizando apenas a chave pública.
func VerifyChallengeSignature(pubKeyB64, challengeMessage, sigB64 string) bool {
	pubKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubKeyB64))
	if err != nil || len(pubKey) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pubKey), []byte(challengeMessage), sig)
}

// ValidatePublicKeyB64 confirma que a string base64 recebida no registro corresponde a uma chave pública Ed25519 válida (32 bytes).
func ValidatePublicKeyB64(pubKeyB64 string) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubKeyB64))
	if err != nil {
		return errors.New("chave pública em base64 inválido")
	}
	if len(raw) != ed25519.PublicKeySize {
		return fmt.Errorf("tamanho inválido para chave pública Ed25519: esperado %d bytes, recebido %d", ed25519.PublicKeySize, len(raw))
	}
	return nil
}

// MaskSerial oculta parte central do serial do pen drive para exibição segura em UI e auditoria.
func MaskSerial(serial string) string {
	s := strings.TrimSpace(serial)
	if s == "" {
		return "N/D"
	}
	if len(s) <= 6 {
		return s[:1] + "***" + s[len(s)-1:]
	}
	return s[:4] + "••••" + s[len(s)-4:]
}
