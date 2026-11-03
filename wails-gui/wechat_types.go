package main

// iLink Bot API types for WeChat integration.
// Reference: https://github.com/qufei1993/cc-weixin

// --- QR Code Login ---

// wechatQRCodeResponse is the response from GET /ilink/bot/get_bot_qrcode.
type wechatQRCodeResponse struct {
	Ret              int    `json:"ret"`
	QRCode           string `json:"qrcode"`             // QR code identifier for status polling
	QRCodeImgContent string `json:"qrcode_img_content"` // URL encoded in the QR code image
}

// wechatQRCodeStatusResponse is the response from GET /ilink/bot/get_qrcode_status.
type wechatQRCodeStatusResponse struct {
	Errcode    int    `json:"errcode"`
	Errmsg     string `json:"errmsg"`
	Status     string `json:"status"` // "wait", "scaned", "confirmed", "expired"
	BotToken   string `json:"bot_token,omitempty"`
	IlinkBotID string `json:"ilink_bot_id,omitempty"`
	BaseURL    string `json:"baseurl,omitempty"`
	IlinkUID   string `json:"ilink_user_id,omitempty"`
}

// --- Base Info (required in every POST body) ---

// wechatBaseInfo is required in every POST request body.
type wechatBaseInfo struct {
	ChannelVersion string `json:"channel_version"`
}

var defaultBaseInfo = wechatBaseInfo{ChannelVersion: "0.1.0"}

// --- Messages ---

// wechatGetUpdatesRequest is the request body for POST /ilink/bot/getupdates.
type wechatGetUpdatesRequest struct {
	GetUpdatesBuf string         `json:"get_updates_buf,omitempty"` // cursor for pagination
	BaseInfo      wechatBaseInfo `json:"base_info"`
}

// wechatGetUpdatesResponse is the response from POST /ilink/bot/getupdates.
type wechatGetUpdatesResponse struct {
	Errcode       int             `json:"errcode"`
	Errmsg        string          `json:"errmsg"`
	GetUpdatesBuf string          `json:"get_updates_buf"` // next cursor
	SyncBuf       string          `json:"sync_buf"`        // sync cursor
	Msgs          []wechatMessage `json:"msgs"`
}

// wechatMessage represents a single message from getupdates.
type wechatMessage struct {
	Seq          int64               `json:"seq"`
	MessageID    int64               `json:"message_id"`
	MessageType  int                 `json:"message_type"` // 1=USER, 2=SYSTEM, etc.
	MessageState int                 `json:"message_state"`
	FromUserID   string              `json:"from_user_id"`
	ToUserID     string              `json:"to_user_id"`
	ClientID     string              `json:"client_id"`
	CreateTimeMS int64               `json:"create_time_ms"`
	ItemList     []wechatMessageItem `json:"item_list"`
	// ContextToken is needed for replies to maintain conversation context.
	ContextToken string `json:"context_token,omitempty"`
}

// wechatMessageItem is a single content item within a message.
type wechatMessageItem struct {
	Type         int              `json:"type"` // 1=TEXT, 2=IMAGE, 3=VOICE, 4=FILE, 5=VIDEO (see wechatItemType* constants)
	CreateTimeMS int64            `json:"create_time_ms,omitempty"`
	IsCompleted  bool             `json:"is_completed,omitempty"`
	TextItem     *wechatTextItem  `json:"text_item,omitempty"`
	ImageItem    *wechatImageItem `json:"image_item,omitempty"`
	// File metadata (populated for file items).
	FileName string `json:"file_name,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
}

// wechatTextItem holds text content in a received message.
type wechatTextItem struct {
	Text string `json:"text"`
}

// wechatImageItem holds the metadata for an inbound image message.
// The actual bytes must be fetched from `Media.FullURL` and decrypted with
// `Media.AESKey` (AES-128-ECB, PKCS7).
type wechatImageItem struct {
	// AESKey is the raw hex-encoded AES key. Media.AESKey is the same key
	// wrapped as base64(hex-string), which is what we actually use.
	AESKey      string          `json:"aeskey,omitempty"`
	Media       *wechatMediaRef `json:"media,omitempty"`
	MidSize     int64           `json:"mid_size,omitempty"`
	HDSize      int64           `json:"hd_size,omitempty"`
	ThumbSize   int64           `json:"thumb_size,omitempty"`
	ThumbWidth  int             `json:"thumb_width,omitempty"`
	ThumbHeight int             `json:"thumb_height,omitempty"`
}

// wechatMediaRef holds a CDN media reference. FullURL (when present) is a
// ready-to-GET URL already containing the encrypted_query_param; otherwise
// URL + EncryptQueryParam must be combined.
type wechatMediaRef struct {
	URL               string `json:"url,omitempty"`
	FullURL           string `json:"full_url,omitempty"`
	AESKey            string `json:"aes_key,omitempty"`
	EncryptQueryParam string `json:"encrypt_query_param,omitempty"`
	FileType          string `json:"file_type,omitempty"`
}

// Message type constants.
const (
	wechatMessageTypeUser   = 1
	wechatMessageTypeSystem = 2
)

// Item type constants.
// Reference: iLink Bot API protocol — type 4 is FILE, type 5 is VIDEO.
const (
	wechatItemTypeText  = 1
	wechatItemTypeImage = 2
	wechatItemTypeVoice = 3
	wechatItemTypeFile  = 4
	wechatItemTypeVideo = 5
)

const (
	wechatUploadMediaTypeImage = 1
	wechatUploadMediaTypeVideo = 2
	wechatUploadMediaTypeFile  = 3
	wechatUploadMediaTypeVoice = 4
)

// --- Send Message ---

// wechatSendMessageRequest is the request body for POST /ilink/bot/sendmessage.
type wechatSendMessageRequest struct {
	Msg      wechatOutgoingMessage `json:"msg"`
	BaseInfo wechatBaseInfo        `json:"base_info"`
}

// wechatOutgoingMessage is the message payload nested under "msg".
type wechatOutgoingMessage struct {
	ToUserID     string           `json:"to_user_id"`
	FromUserID   string           `json:"from_user_id"`  // empty string
	ClientID     string           `json:"client_id"`     // UUID for dedup
	MessageType  int              `json:"message_type"`  // 2 = bot message
	MessageState int              `json:"message_state"` // 2
	ContextToken string           `json:"context_token"`
	ItemList     []wechatSendItem `json:"item_list"`
}

// wechatSendItem is a single content item in an outgoing message.
type wechatSendItem struct {
	Type     int                 `json:"type"` // 1=text, 4=file
	TextItem *wechatSendTextItem `json:"text_item,omitempty"`
	FileItem *wechatSendFileItem `json:"file_item,omitempty"`
}

// wechatSendFileItem holds file content for sending.
// The current iLink protocol uses a CDN media reference plus file metadata.
type wechatSendFileItem struct {
	Media    *wechatSendMedia `json:"media,omitempty"`
	FileName string           `json:"file_name,omitempty"`
	MD5      string           `json:"md5,omitempty"`
	Len      string           `json:"len,omitempty"`
}

// wechatSendMedia holds the CDN media reference returned after upload.
type wechatSendMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param"`
	AESKey            string `json:"aes_key"`
	EncryptType       int    `json:"encrypt_type"`
}

// wechatSendTextItem holds text content for sending.
type wechatSendTextItem struct {
	Text string `json:"text"`
}

// wechatSendMessageResponse is the response from POST /ilink/bot/sendmessage.
type wechatSendMessageResponse struct {
	Errcode int    `json:"errcode"`
	Errmsg  string `json:"errmsg"`
}

// --- Config / Typing ---

// wechatGetConfigResponse is the response from POST /ilink/bot/getconfig.
type wechatGetConfigResponse struct {
	Ret          int    `json:"ret"`
	Errmsg       string `json:"errmsg"`
	TypingTicket string `json:"typing_ticket,omitempty"`
}

// wechatGetConfigRequest is the request body for POST /ilink/bot/getconfig.
type wechatGetConfigRequest struct {
	IlinkUserID  string         `json:"ilink_user_id"`
	ContextToken string         `json:"context_token"`
	BaseInfo     wechatBaseInfo `json:"base_info"`
}

// wechatSendTypingRequest is the request body for POST /ilink/bot/sendtyping.
type wechatSendTypingRequest struct {
	IlinkUserID  string         `json:"ilink_user_id"`
	TypingTicket string         `json:"typing_ticket"`
	Status       int            `json:"status"` // 1=typing, 2=cancel
	BaseInfo     wechatBaseInfo `json:"base_info"`
}

// --- CDN Upload ---

// wechatGetUploadURLRequest is the request body for POST /ilink/bot/getuploadurl.
type wechatGetUploadURLRequest struct {
	FileKey         string         `json:"filekey,omitempty"`
	MediaType       int            `json:"media_type,omitempty"` // 1=image, 2=video, 3=file, 4=voice
	ToUserID        string         `json:"to_user_id,omitempty"`
	RawSize         int64          `json:"rawsize,omitempty"`
	RawFileMD5      string         `json:"rawfilemd5,omitempty"`
	FileSize        int64          `json:"filesize,omitempty"` // AES-128-ECB + PKCS7 ciphertext size
	ThumbRawSize    int64          `json:"thumb_rawsize,omitempty"`
	ThumbRawFileMD5 string         `json:"thumb_rawfilemd5,omitempty"`
	ThumbFileSize   int64          `json:"thumb_filesize,omitempty"`
	NoNeedThumb     bool           `json:"no_need_thumb,omitempty"`
	AESKey          string         `json:"aeskey,omitempty"` // hex-encoded 16-byte key
	BaseInfo        wechatBaseInfo `json:"base_info"`
}

// wechatGetUploadURLResponse is the response from POST /ilink/bot/getuploadurl.
type wechatGetUploadURLResponse struct {
	Ret              int    `json:"ret"`
	Errmsg           string `json:"errmsg,omitempty"`
	UploadParam      string `json:"upload_param,omitempty"`
	ThumbUploadParam string `json:"thumb_upload_param,omitempty"`
	UploadFullURL    string `json:"upload_full_url,omitempty"`
}

// --- Credentials ---

// wechatCredentials holds the login credentials obtained via QR code scan.
type wechatCredentials struct {
	BotToken string `json:"bot_token"`
	BaseURL  string `json:"base_url"`
	BotID    string `json:"bot_id"`
	UserID   string `json:"user_id"` // the bot's own user ID
	// ConnectedAt is when the bot was connected; older messages are ignored.
	ConnectedAt string `json:"connected_at,omitempty"`
	// The owner is the WeChat user who sent the pairing code. Until then the
	// bot answers nobody but that pairing message.
	connectorOwnerState
}
