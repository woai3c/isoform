package pushgatewayauth

import (
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	HubKeyInfo   = "isoform-push-gateway hub v1"
	GrantKeyInfo = "isoform-push-gateway grant v1"
)

var errInvalidKey = errors.New("invalid push gateway key")

func DecodeHubPublicKey(encoded string) (*ecdh.PublicKey, []byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return nil, nil, errInvalidKey
	}
	key, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return nil, nil, errInvalidKey
	}
	return key, raw, nil
}

func DecodeGatewayPrivateKey(encoded string) (*ecdh.PrivateKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(raw) != 32 {
		return nil, errInvalidKey
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return nil, errInvalidKey
	}
	return key, nil
}

func LoadGatewayPrivateKey(path string) (*ecdh.PrivateKey, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("ISOFORM_PUSH_GATEWAY_KEY_FILE is required")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 256 {
		return nil, errors.New("push gateway key file must be a private regular file under 256 bytes")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("unable to read push gateway key file")
	}
	key, err := DecodeGatewayPrivateKey(string(data))
	if err != nil {
		return nil, errors.New("push gateway key file must contain a base64url X25519 private key")
	}
	return key, nil
}

func GenerateGatewayPrivateKeyFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("key file path is required")
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return errors.New("unable to generate push gateway key")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("unable to create push gateway key file")
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if err = file.Chmod(0600); err != nil {
		return errors.New("unable to secure push gateway key file")
	}
	encoded := base64.RawURLEncoding.EncodeToString(key.Bytes()) + "\n"
	if _, err = file.WriteString(encoded); err != nil {
		return errors.New("unable to write push gateway key file")
	}
	if err = file.Sync(); err != nil {
		return errors.New("unable to persist push gateway key file")
	}
	if err = file.Close(); err != nil {
		return errors.New("unable to close push gateway key file")
	}
	ok = true
	return nil
}

func PublicKeyString(key *ecdh.PrivateKey) string {
	if key == nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
}

func DeriveGrantKey(gatewayPrivate []byte) ([]byte, error) {
	if len(gatewayPrivate) != 32 {
		return nil, errInvalidKey
	}
	return hkdf.Key(sha256.New, gatewayPrivate, nil, GrantKeyInfo, 32)
}

func DeriveHubKey(sharedSecret, hubPublic, gatewayPublic []byte) ([]byte, error) {
	if len(sharedSecret) != 32 || len(hubPublic) != 32 || len(gatewayPublic) != 32 {
		return nil, errInvalidKey
	}
	salt := make([]byte, 0, 64)
	salt = append(salt, hubPublic...)
	salt = append(salt, gatewayPublic...)
	return hkdf.Key(sha256.New, sharedSecret, salt, HubKeyInfo, 32)
}

func DeriveHubKeyFromPrivate(hubPrivate *ecdh.PrivateKey, gatewayPublic []byte) ([]byte, []byte, error) {
	if hubPrivate == nil || len(gatewayPublic) != 32 {
		return nil, nil, errInvalidKey
	}
	gatewayKey, err := ecdh.X25519().NewPublicKey(gatewayPublic)
	if err != nil {
		return nil, nil, errInvalidKey
	}
	shared, err := hubPrivate.ECDH(gatewayKey)
	if err != nil {
		return nil, nil, errInvalidKey
	}
	hubPublic := hubPrivate.PublicKey().Bytes()
	key, err := DeriveHubKey(shared, hubPublic, gatewayPublic)
	if err != nil {
		return nil, nil, err
	}
	return key, hubPublic, nil
}

func DeriveHubKeyFromGateway(gatewayPrivate *ecdh.PrivateKey, hubPublic []byte) ([]byte, error) {
	if gatewayPrivate == nil || len(hubPublic) != 32 {
		return nil, errInvalidKey
	}
	hubKey, err := ecdh.X25519().NewPublicKey(hubPublic)
	if err != nil {
		return nil, errInvalidKey
	}
	shared, err := gatewayPrivate.ECDH(hubKey)
	if err != nil {
		return nil, errInvalidKey
	}
	return DeriveHubKey(shared, hubPublic, gatewayPrivate.PublicKey().Bytes())
}

func RequestSignature(key []byte, timestamp string, body []byte) string {
	digest := sha256.Sum256(body)
	message := "isoform-push-gateway v1\nPOST /v1/push\n" + timestamp + "\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func VerifyRequestSignature(key []byte, timestamp, signature string, body []byte, now time.Time) bool {
	if !timestampValid(timestamp, now) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || len(raw) != sha256.Size || base64.RawURLEncoding.EncodeToString(raw) != signature {
		return false
	}
	expected, err := base64.RawURLEncoding.DecodeString(RequestSignature(key, timestamp, body))
	return err == nil && hmac.Equal(raw, expected)
}

func VerifyRequestSignatureForHub(gatewayPrivate *ecdh.PrivateKey, hubPublic *ecdh.PublicKey, hubRaw []byte, timestamp, signature string, body []byte, now time.Time) bool {
	if gatewayPrivate == nil || hubPublic == nil || len(hubRaw) != 32 ||
		!timestampValid(timestamp, now) {
		return false
	}
	shared, err := gatewayPrivate.ECDH(hubPublic)
	if err != nil {
		return false
	}
	key, err := DeriveHubKey(shared, hubRaw, gatewayPrivate.PublicKey().Bytes())
	if err != nil {
		return false
	}
	return VerifyRequestSignature(key, timestamp, signature, body, now)
}

func timestampValid(timestamp string, now time.Time) bool {
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != timestamp {
		return false
	}
	nowSeconds := now.Unix()
	if seconds < nowSeconds-300 || seconds > nowSeconds+300 {
		return false
	}
	return true
}

func NewGrant(grantKey []byte, environment, token, hubID, hubPublic string, expires time.Time) (string, error) {
	_, hubRaw, err := DecodeHubPublicKey(hubPublic)
	if err != nil || len(grantKey) != 32 {
		return "", errInvalidKey
	}
	exp := expires.Unix()
	body := make([]byte, 9, 1+8+sha256.Size)
	body[0] = 1
	binary.BigEndian.PutUint64(body[1:], uint64(exp))
	message := fmt.Sprintf("isoform-push-grant v1\n%s\n%s\n%s\n%s\n%d", environment, token, hubID,
		base64.RawURLEncoding.EncodeToString(hubRaw), exp)
	mac := hmac.New(sha256.New, grantKey)
	_, _ = mac.Write([]byte(message))
	body = append(body, mac.Sum(nil)...)
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func VerifyGrant(grantKey []byte, grant, environment, token, hubID, hubPublic string, now time.Time) bool {
	if len(grantKey) != 32 {
		return false
	}
	_, hubRaw, err := DecodeHubPublicKey(hubPublic)
	if err != nil {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(grant)
	if err != nil || len(raw) != 1+8+sha256.Size || base64.RawURLEncoding.EncodeToString(raw) != grant || raw[0] != 1 {
		return false
	}
	exp := int64(binary.BigEndian.Uint64(raw[1:9]))
	if exp <= now.Unix() {
		return false
	}
	message := fmt.Sprintf("isoform-push-grant v1\n%s\n%s\n%s\n%s\n%d", environment, token, hubID,
		base64.RawURLEncoding.EncodeToString(hubRaw), exp)
	mac := hmac.New(sha256.New, grantKey)
	_, _ = mac.Write([]byte(message))
	return hmac.Equal(raw[9:], mac.Sum(nil))
}
