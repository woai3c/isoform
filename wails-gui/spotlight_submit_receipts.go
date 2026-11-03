package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
)

type spotlightSubmitRequest struct {
	RequestID string `json:"requestId,omitempty"`
	Prompt    string `json:"prompt"`
	BundleID  string `json:"bundleID,omitempty"`
	// SessionLocalID is the existing spotlight session id to extend
	// with this prompt. Empty (first turn) → mint a fresh id.
	SessionLocalID string `json:"sessionLocalId,omitempty"`
	// EditItemID + EditFromSortOrder switch this submission into the
	// edit/reset path: instead of appending another user row, the
	// server rewrites the item at EditItemID to the new prompt and
	// drops every row with sort_order > EditFromSortOrder. Both must
	// be set together; SessionLocalID is required. *int for
	// EditFromSortOrder lets us distinguish "edit at slot 0" from
	// "field omitted".
	EditItemID        string `json:"editItemId,omitempty"`
	EditFromSortOrder *int   `json:"editFromSortOrder,omitempty"`
}

type spotlightSubmitResponse struct {
	SessionLocalID string `json:"sessionLocalId"`
	UserItemID     string `json:"userItemId,omitempty"`
	SortOrder      int    `json:"sortOrder"`
}

func validSpotlightRequestID(id string) bool {
	if len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

type spotlightSubmitReceipt struct {
	requestHash [32]byte
	done        chan struct{}
	response    spotlightSubmitResponse
	err         error
}

// A helper can lose the response after the host starts a turn. Repeating the
// same request ID returns the host's original result instead of starting it again.
func onceSpotlightSubmit(receipts *sync.Map, request spotlightSubmitRequest, submit func() (spotlightSubmitResponse, error)) (spotlightSubmitResponse, error) {
	if request.RequestID == "" {
		return submit()
	}
	if len(request.RequestID) > 128 {
		return spotlightSubmitResponse{}, errors.New("request ID is too long")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return spotlightSubmitResponse{}, err
	}
	receipt := &spotlightSubmitReceipt{requestHash: sha256.Sum256(encoded), done: make(chan struct{})}
	existing, loaded := receipts.LoadOrStore(request.RequestID, receipt)
	if loaded {
		prior := existing.(*spotlightSubmitReceipt)
		if prior.requestHash != receipt.requestHash {
			return spotlightSubmitResponse{}, errors.New("request ID already used")
		}
		<-prior.done
		return prior.response, prior.err
	}
	receipt.response, receipt.err = submit()
	if receipt.err != nil {
		receipts.CompareAndDelete(request.RequestID, receipt)
	}
	close(receipt.done)
	return receipt.response, receipt.err
}
