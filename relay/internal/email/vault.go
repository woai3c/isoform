package email

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

var errSealed = errors.New("sealed value does not open")

// Vault keeps addresses as a keyed hash for lookup and as AES-256-GCM
// ciphertext bound to their row, so a database copy without the key reveals
// no address.
type Vault struct {
	aead       cipher.AEAD
	addressKey []byte
	codeKey    []byte
}

// NewVault derives the vault's keys from the 32-byte email key.
func NewVault(key []byte) (*Vault, error) {
	if len(key) != KeySize {
		return nil, errors.New("the email key must be 32 bytes")
	}
	block, err := aes.NewCipher(derive(key, "yansu-email-seal-v1"))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead, addressKey: derive(key, "yansu-email-address-v1"), codeKey: derive(key, "yansu-email-code-v1")}, nil
}

func derive(key []byte, label string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(label))
	return mac.Sum(nil)
}

func keyed(key []byte, parts ...string) string {
	mac := hmac.New(sha256.New, key)
	for _, part := range parts {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// HashAddress is the lookup key of a normalized address.
func (v *Vault) HashAddress(address string) string { return keyed(v.addressKey, address) }

// HashCode binds a verification code to the hub and the address it went to.
func (v *Vault) HashCode(hubID, addressHash, code string) string {
	return keyed(v.codeKey, hubID, addressHash, code)
}

// Seal encrypts plaintext bound to label (the row that holds it).
func (v *Vault) Seal(plaintext, label string) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(v.aead.Seal(nonce, nonce, []byte(plaintext), []byte(label))), nil
}

// Open reverses Seal for the same label.
func (v *Vault) Open(sealed, label string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil || len(raw) < v.aead.NonceSize()+v.aead.Overhead() {
		return "", errSealed
	}
	plaintext, err := v.aead.Open(nil, raw[:v.aead.NonceSize()], raw[v.aead.NonceSize():], []byte(label))
	if err != nil {
		return "", errSealed
	}
	return string(plaintext), nil
}

func addressLabel(hubID string) string { return "address|" + hubID }
