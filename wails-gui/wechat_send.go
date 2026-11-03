package main

import (
	"crypto/aes"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// sendWeChatText sends a plain-text message to a WeChat user.
// Markdown is stripped because WeChat does not render it.
func sendWeChatText(api *wechatAPIClient, toUserID, contextToken, text string) error {
	plain := markdownToPlainText(text)

	req := &wechatSendMessageRequest{
		Msg: wechatOutgoingMessage{
			ToUserID:     toUserID,
			FromUserID:   "",
			ClientID:     uuid.New().String(),
			MessageType:  wechatMessageTypeSystem, // 2 = bot message
			MessageState: 2,
			ContextToken: contextToken,
			ItemList: []wechatSendItem{
				{
					Type:     wechatItemTypeText,
					TextItem: &wechatSendTextItem{Text: plain},
				},
			},
		},
		BaseInfo: defaultBaseInfo,
	}
	return api.SendMessage(req)
}

// sendWeChatFile sends a file to a WeChat user via the current iLink CDN flow.
func sendWeChatFile(api *wechatAPIClient, toUserID, contextToken, filename string, data []byte) error {
	return sendWeChatFileCDN(api, toUserID, contextToken, filename, data)
}

// sendWeChatFileCDN sends a file via:
// getuploadurl(filekey/media_type/rawsize/rawfilemd5/filesize/aeskey)
// → AES-128-ECB encrypt → CDN upload → sendmessage(file_item + media ref).
func sendWeChatFileCDN(api *wechatAPIClient, toUserID, contextToken, filename string, data []byte) error {
	fileKey, err := generateWeChatFileKey()
	if err != nil {
		return fmt.Errorf("generate filekey: %w", err)
	}
	aesKeyBytes, aesKeyHex, err := generateWeChatAESKey()
	if err != nil {
		return fmt.Errorf("generate aes key: %w", err)
	}
	fileMD5 := md5.Sum(data)

	encrypted, err := encryptAESECB(data, aesKeyBytes)
	if err != nil {
		return fmt.Errorf("encrypt file: %w", err)
	}

	uploadResp, err := api.GetUploadURL(&wechatGetUploadURLRequest{
		FileKey:     fileKey,
		MediaType:   wechatUploadMediaTypeFile,
		ToUserID:    toUserID,
		RawSize:     int64(len(data)),
		RawFileMD5:  hex.EncodeToString(fileMD5[:]),
		FileSize:    int64(len(encrypted)),
		NoNeedThumb: true,
		AESKey:      aesKeyHex,
	})
	if err != nil {
		return fmt.Errorf("getuploadurl: %w", err)
	}
	uploadURL := buildWeChatCDNUploadURL(uploadResp, fileKey)
	if uploadURL == "" {
		return fmt.Errorf("getuploadurl returned no upload target")
	}
	encryptQueryParam, err := api.UploadToCDN(uploadURL, encrypted)
	if err != nil {
		return fmt.Errorf("CDN upload: %w", err)
	}

	log.Printf("[wechat] file uploaded to CDN: name=%s size=%d encrypted=%d", filename, len(data), len(encrypted))
	aesKeyForMessage := base64.StdEncoding.EncodeToString([]byte(aesKeyHex))

	req := &wechatSendMessageRequest{
		Msg: wechatOutgoingMessage{
			ToUserID:     toUserID,
			FromUserID:   "",
			ClientID:     uuid.New().String(),
			MessageType:  wechatMessageTypeSystem,
			MessageState: 2,
			ContextToken: contextToken,
			ItemList: []wechatSendItem{
				{
					Type: wechatItemTypeFile,
					FileItem: &wechatSendFileItem{
						FileName: filename,
						MD5:      hex.EncodeToString(fileMD5[:]),
						Len:      fmt.Sprintf("%d", len(data)),
						Media: &wechatSendMedia{
							EncryptQueryParam: encryptQueryParam,
							AESKey:            aesKeyForMessage,
							EncryptType:       1,
						},
					},
				},
			},
		},
		BaseInfo: defaultBaseInfo,
	}
	return api.SendMessage(req)
}

func generateWeChatFileKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func generateWeChatAESKey() ([]byte, string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, "", err
	}
	return buf, hex.EncodeToString(buf), nil
}

func buildWeChatCDNUploadURL(resp *wechatGetUploadURLResponse, fileKey string) string {
	if resp == nil {
		return ""
	}
	if strings.TrimSpace(resp.UploadFullURL) != "" {
		return resp.UploadFullURL
	}
	if strings.TrimSpace(resp.UploadParam) == "" || strings.TrimSpace(fileKey) == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/upload?encrypted_query_param=%s&filekey=%s",
		wechatDefaultCDNBaseURL,
		url.QueryEscape(resp.UploadParam),
		url.QueryEscape(fileKey),
	)
}

// --- AES-128-ECB encryption ---

// decodeAESKey decodes the aes_key value used by inbound CDN media refs.
// The server may send either base64(raw 16 bytes) or base64(hex string).
func decodeAESKey(b64Key string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64Key)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	if len(raw) == 16 {
		return raw, nil
	}
	if len(raw) == 32 {
		hexStr := string(raw)
		decoded, hexErr := hexDecode(hexStr)
		if hexErr == nil && len(decoded) == 16 {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("unexpected AES key length %d (expected 16 raw bytes)", len(raw))
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd-length hex string")
	}
	b := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		hi := hexVal(s[i])
		lo := hexVal(s[i+1])
		if hi < 0 || lo < 0 {
			return nil, fmt.Errorf("invalid hex char at position %d", i)
		}
		b[i/2] = byte(hi<<4 | lo)
	}
	return b, nil
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c - 'a' + 10)
	case c >= 'A' && c <= 'F':
		return int(c - 'A' + 10)
	default:
		return -1
	}
}

// encryptAESECB encrypts data using AES-128-ECB with PKCS7 padding.
func encryptAESECB(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(ciphertext[i:i+aes.BlockSize], padded[i:i+aes.BlockSize])
	}
	return ciphertext, nil
}

// pkcs7Pad pads data to a multiple of blockSize using PKCS#7.
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	pad := make([]byte, padding)
	for i := range pad {
		pad[i] = byte(padding)
	}
	return append(data, pad...)
}

// markdownToPlainText converts markdown-formatted text to plain text
// suitable for WeChat display. Preserves readability while removing
// markdown syntax that WeChat cannot render.
func markdownToPlainText(md string) string {
	s := md

	// Remove code block fences but keep content.
	s = regexp.MustCompile("(?m)^```[a-zA-Z]*\\s*$").ReplaceAllString(s, "")

	// Convert headers to plain text with emphasis.
	s = regexp.MustCompile("(?m)^#{1,6}\\s+(.+)$").ReplaceAllString(s, "$1")

	// Remove bold/italic markers.
	s = regexp.MustCompile(`\*{1,3}([^*]+)\*{1,3}`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`_{1,3}([^_]+)_{1,3}`).ReplaceAllString(s, "$1")

	// Remove inline code backticks.
	s = regexp.MustCompile("`([^`]+)`").ReplaceAllString(s, "$1")

	// Convert links [text](url) to text (url).
	s = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllString(s, "$1 ($2)")

	// Remove image syntax ![alt](url).
	s = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`).ReplaceAllString(s, "$1")

	// Clean up excessive blank lines.
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}
