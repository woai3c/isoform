package main

import "sync"

// chatSendReceipt is retained for the life of the app so a retry after a lost
// RPC response cannot launch an already admitted turn again.
type chatSendReceipt struct {
	done chan struct{}
	err  error
}

type chatSendKey struct{ tag, traceID string }

func onceChatSend(receipts *sync.Map, tag, traceID string, send func() error) error {
	if tag == "" || traceID == "" {
		return send()
	}
	key := chatSendKey{tag: tag, traceID: traceID}
	receipt := &chatSendReceipt{done: make(chan struct{})}
	existing, loaded := receipts.LoadOrStore(key, receipt)
	if loaded {
		prior := existing.(*chatSendReceipt)
		<-prior.done
		return prior.err
	}
	receipt.err = send()
	if receipt.err != nil {
		receipts.CompareAndDelete(key, receipt)
	}
	close(receipt.done)
	return receipt.err
}
