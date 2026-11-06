package main

import (
	"io/fs"
	"sync"
)

// admissionFileDigests keeps the content digest of each file the chat
// admission stamp hashes, keyed on size and modification time, so an
// unchanged file is not opened again on every stamp. Any edit changes one of
// the two, so the next stamp misses and hashes the new bytes. The digest only
// feeds the stamp; admission itself still runs on every read.
var admissionFileDigests = struct {
	sync.Mutex
	entries map[string]admissionFileDigest
}{entries: map[string]admissionFileDigest{}}

type admissionFileDigest struct {
	size int64
	mod  int64
	sum  []byte
}

const admissionFileDigestLimit = 4096

func cachedAdmissionFileSum(path string, info fs.FileInfo) ([]byte, bool) {
	admissionFileDigests.Lock()
	defer admissionFileDigests.Unlock()
	e, ok := admissionFileDigests.entries[path]
	if !ok || e.size != info.Size() || e.mod != info.ModTime().UnixNano() {
		return nil, false
	}
	return append([]byte(nil), e.sum...), true
}

func rememberAdmissionFileSum(path string, info fs.FileInfo, sum []byte) {
	admissionFileDigests.Lock()
	defer admissionFileDigests.Unlock()
	if len(admissionFileDigests.entries) >= admissionFileDigestLimit {
		admissionFileDigests.entries = map[string]admissionFileDigest{}
	}
	admissionFileDigests.entries[path] = admissionFileDigest{size: info.Size(), mod: info.ModTime().UnixNano(), sum: append([]byte(nil), sum...)}
}
