package httpapi

import (
	"net/http"
	"regexp"
	"strings"

	"isoform-relay/internal/store"
	"isoform-relay/internal/wire"
)

// receiptActionPattern matches the action ids the phone registers.
var receiptActionPattern = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)

// receiptBody is the phone's report about one notice (PROTOCOL.md section 14.6).
type receiptBody struct {
	State  string `json:"state"`
	Action string `json:"action"`
	At     string `json:"at"`
}

// noticeRoute serves POST /v1/notices/{noticeId}/receipts.
func (h *Handler) noticeRoute(w http.ResponseWriter, r *http.Request) {
	noticeID, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/notices/"), "/")
	if rest != "receipts" || !wire.ValidUUID(noticeID) {
		reply(w, http.StatusNotFound, nil, "Route not found")
		return
	}
	if r.Method != http.MethodPost {
		reply(w, http.StatusMethodNotAllowed, nil, "Method not allowed")
		return
	}
	h.noticeReceipt(w, r, noticeID)
}

// noticeReceipt records one receipt. Repeats are accepted and stored once, and
// the hub is told only about a new receipt. The relay does not check that the
// notice exists: receipts are measurements, and the device rate limit applies.
func (h *Handler) noticeReceipt(w http.ResponseWriter, r *http.Request, noticeID string) {
	device, ok := h.authDevice(w, r, "push")
	if !ok {
		return
	}
	var body receiptBody
	if !decodeBody(w, r, &body) {
		return
	}
	receipt, valid := parseReceipt(body, noticeID)
	if !valid {
		reply(w, http.StatusBadRequest, nil, "Invalid receipt")
		return
	}
	inserted, err := h.Store.AddNoticeReceipt(r.Context(), device.ID, receipt, h.Now())
	if err != nil {
		reply(w, http.StatusInternalServerError, nil, "Unable to record the receipt")
		return
	}
	if inserted {
		h.Hubs.Kick(device.HubID)
	}
	reply(w, http.StatusAccepted, map[string]bool{"accepted": true}, "")
}

// parseReceipt checks a receipt body. An acted receipt names the action; the
// other states carry an action only when the phone has one to report.
func parseReceipt(body receiptBody, noticeID string) (store.Receipt, bool) {
	switch body.State {
	case "delivered", "opened", "acted", "dismissed":
	default:
		return store.Receipt{}, false
	}
	if body.Action == "" {
		if body.State == "acted" {
			return store.Receipt{}, false
		}
	} else if !receiptActionPattern.MatchString(body.Action) {
		return store.Receipt{}, false
	}
	at, err := wire.ParseTime(body.At)
	if err != nil {
		return store.Receipt{}, false
	}
	return store.Receipt{NoticeID: noticeID, State: body.State, Action: body.Action, At: at}, true
}
