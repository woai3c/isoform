package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The read stamp touches no memory folder and no database: reads of it stay
// cheap however the folder looks.
func TestTheReadStampReadsNoMemory(t *testing.T) {
	rig := newHandoffTestRig(t)
	a := rig.app
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	writeMemoryFixture(t, "stamp-note", "Stamp note")
	settleMemoryFolder(t, dir)
	reads := countMemoryFolderReads(t)
	first := a.savedCopyStamp(t.Context())
	for i := 0; i < 20; i++ {
		if got := a.savedCopyStamp(t.Context()); got != first {
			t.Fatalf("the read stamp changed with no privacy change: %q -> %q", first, got)
		}
	}
	if n := reads.Load(); n != 0 {
		t.Fatalf("20 read stamps read the memory folder %d times, want 0", n)
	}
}

// A withdrawal in Isoform changes the read stamp at once, with no background
// work: the durable epoch advances synchronously.
func TestAWithdrawalChangesTheReadStampAtOnce(t *testing.T) {
	rig := newHandoffTestRig(t)
	a := rig.app
	adoptTestBriefPrivacyEpoch(a)
	before := a.savedCopyStamp(t.Context())
	briefPrivacyChanged()
	if after := a.savedCopyStamp(t.Context()); after == before {
		t.Fatal("a withdrawal did not change the read stamp")
	}
}

// A memory file changed outside Isoform is caught by the background check: the
// copy is dropped, and the next read does not serve it.
func TestABackgroundCheckDropsACopyWhenMemoryChangedOutside(t *testing.T) {
	a, session := streamPageTestApp(t)
	latestPage(t, a, session)
	key := streamPageKey{session: session, limit: 50}
	if _, reason := readRawStreamPageSnapshot(key); reason != "" {
		t.Fatalf("the first read did not save a copy: %s", reason)
	}
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	// Changed outside Isoform: written directly, no app call.
	if err := os.WriteFile(filepath.Join(dir, "outside-note.md"), []byte("# Outside\n\nA note added by hand.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settleMemoryFolder(t, dir)
	a.verifyStreamPageCopy(key)
	if _, reason := readRawStreamPageSnapshot(key); reason != "no-copy" {
		t.Fatalf("the background check kept a copy made before the outside change (%s)", reason)
	}
	_ = time.Second
}
