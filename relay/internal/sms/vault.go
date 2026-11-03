package sms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// KeySize is the length of the relay's SMS keys.
const KeySize = 32

// errSealed reports a ciphertext that does not open under this key and label.
var errSealed = errors.New("sealed value does not open")

// VaultKeys are the secrets a vault works with. Seal seals every new value.
// Previous, set only during a rotation, still opens values sealed under the
// key Seal replaced. Hash keys the lookup hashes (numbers, which opt-outs are
// stored by, codes and message ids); it is long-lived, because a hash cannot
// be recomputed without the number, and defaults to Seal for a relay that
// never set it.
type VaultKeys struct {
	Seal     []byte
	Previous []byte
	Hash     []byte
}

// Vault holds the keys derived from the relay's SMS keys. Numbers are stored
// as a keyed hash for lookup and as AES-256-GCM ciphertext bound to their row,
// so a database copy without the keys reveals neither the numbers nor the
// replies. A ciphertext names the key that sealed it, so a rotation opens the
// values sealed before it and Reseal moves them to the new key.
type Vault struct {
	current   string
	aeads     map[string]cipher.AEAD
	order     []string
	numberKey []byte
	codeKey   []byte
	idKey     []byte
}

// NewVault derives the vault's keys. Each key must be 32 bytes.
func NewVault(keys VaultKeys) (*Vault, error) {
	if len(keys.Seal) != KeySize || (keys.Previous != nil && len(keys.Previous) != KeySize) || (keys.Hash != nil && len(keys.Hash) != KeySize) {
		return nil, errors.New("the SMS keys must be 32 bytes")
	}
	hashKey := keys.Hash
	if hashKey == nil {
		hashKey = keys.Seal
	}
	v := &Vault{
		aeads:     map[string]cipher.AEAD{},
		numberKey: derive(hashKey, "yansu-sms-number-v1"),
		codeKey:   derive(hashKey, "yansu-sms-code-v1"),
		idKey:     derive(hashKey, "yansu-sms-message-v1"),
	}
	for _, key := range [][]byte{keys.Seal, keys.Previous} {
		if key == nil {
			continue
		}
		block, err := aes.NewCipher(derive(key, "yansu-sms-seal-v1"))
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		id := KeyID(key)
		if _, seen := v.aeads[id]; seen {
			return nil, errors.New("the previous SMS key is the current one")
		}
		v.aeads[id] = aead
		v.order = append(v.order, id)
	}
	v.current = v.order[0]
	return v, nil
}

// KeyID names a sealing key without revealing it: the first four bytes of an
// HMAC under the key, in hex.
func KeyID(key []byte) string {
	return hex.EncodeToString(derive(key, "yansu-sms-key-id-v1")[:4])
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

// HashNumber is the lookup key for an E.164 number.
func (v *Vault) HashNumber(number string) string { return keyed(v.numberKey, number) }

// HashCode binds a verification code to the hub and the number it was sent to.
func (v *Vault) HashCode(hubID, numberHash, code string) string {
	return keyed(v.codeKey, hubID, numberHash, code)
}

// HashMessageID is the replay key for a provider message id.
func (v *Vault) HashMessageID(id string) string { return keyed(v.idKey, id) }

// Seal encrypts plaintext under the current key, bound to label (what the
// value is and which row holds it), as the key id, a dot, and unpadded
// base64url of nonce followed by ciphertext.
func (v *Vault) Seal(plaintext, label string) (string, error) {
	aead := v.aeads[v.current]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), []byte(label))
	return v.current + "." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Open reverses Seal for the same label, with the key the value names. A
// value sealed before key ids existed names none and is tried with each key.
func (v *Vault) Open(sealed, label string) (string, error) {
	ids := v.order
	if id, body, ok := strings.Cut(sealed, "."); ok {
		if _, known := v.aeads[id]; !known {
			return "", errSealed
		}
		ids, sealed = []string{id}, body
	}
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return "", errSealed
	}
	for _, id := range ids {
		aead := v.aeads[id]
		if len(raw) < aead.NonceSize()+aead.Overhead() {
			return "", errSealed
		}
		nonce, body := raw[:aead.NonceSize()], raw[aead.NonceSize():]
		if plaintext, err := aead.Open(nil, nonce, body, []byte(label)); err == nil {
			return string(plaintext), nil
		}
	}
	return "", errSealed
}

// Current reports whether a value is sealed under the current key.
func (v *Vault) Current(sealed string) bool {
	id, _, ok := strings.Cut(sealed, ".")
	return ok && id == v.current
}

// numberLabel and replyLabel bind ciphertexts to the row that stores them.
func numberLabel(hubID string) string    { return "number|" + hubID }
func replyLabel(hubID, id string) string { return "reply|" + hubID + "|" + id }
