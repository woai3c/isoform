package clipboard

import (
	"testing"
)

// fakeBoard is a pasteboard the test controls.
type fakeBoard struct {
	count    int
	types    []string
	text     string
	source   string
	readable bool
	reads    int
	typeAsks int
	// bumpOnRead simulates a copy landing while the text is read.
	bumpOnRead bool
}

func (b *fakeBoard) ChangeCount() int { return b.count }
func (b *fakeBoard) Types() []string {
	b.typeAsks++
	return b.types
}
func (b *fakeBoard) ContentReadable() bool { return b.readable }
func (b *fakeBoard) ReadText(maxRunes int) (string, int, string, bool) {
	b.reads++
	if b.bumpOnRead {
		b.count++
	}
	runes := []rune(b.text)
	length := len(runes)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes), length, b.source, b.text != ""
}

func (b *fakeBoard) copy(text string, types ...string) {
	b.count++
	b.text = text
	b.types = types
}

func newTestWatcher(board *fakeBoard) (*Watcher, *[]Copy, *bool) {
	var got []Copy
	allowed := true
	w := &Watcher{
		Board:   board,
		Allowed: func() bool { return allowed },
		OnCopy:  func(c Copy) { got = append(got, c) },
	}
	return w, &got, &allowed
}

func TestWatcherReportsEachCopyOnceAndSkipsWhatWasThere(t *testing.T) {
	board := &fakeBoard{count: 7, types: []string{"public.utf8-plain-text"}, text: "already there", readable: true}
	w, got, _ := newTestWatcher(board)

	w.Tick() // baseline: what was copied before Isoform looked is not reported
	w.Tick()
	if len(*got) != 0 || board.reads != 0 {
		t.Fatalf("baseline reported %+v (reads %d)", *got, board.reads)
	}
	board.copy("Ship the deck", "public.utf8-plain-text")
	w.Tick()
	w.Tick()
	if len(*got) != 1 || (*got)[0].Text != "Ship the deck" || (*got)[0].Length != 13 || (*got)[0].Kind != KindText || !(*got)[0].ContentRead {
		t.Fatalf("copy %+v", *got)
	}
	board.copy("https://example.com/a?b=1", "public.utf8-plain-text")
	w.Tick()
	if len(*got) != 2 || (*got)[1].Kind != KindURL {
		t.Fatalf("a copied link is a url: %+v", *got)
	}
}

func TestWatcherSkipsConcealedTypesWithoutReading(t *testing.T) {
	board := &fakeBoard{readable: true}
	w, got, _ := newTestWatcher(board)
	w.Tick()
	board.copy("hunter2", "public.utf8-plain-text", TypeConcealed)
	w.Tick()
	if len(*got) != 0 || board.reads != 0 {
		t.Fatalf("a concealed copy was read or reported: %+v (reads %d)", *got, board.reads)
	}
}

func TestWatcherAcceptGateRunsBeforeAnyRead(t *testing.T) {
	board := &fakeBoard{readable: true}
	w, got, _ := newTestWatcher(board)
	w.Accept = func([]string, []App) bool { return false }
	w.Tick()
	board.copy("secret from a protected app", "public.utf8-plain-text")
	w.Tick()
	if len(*got) != 0 || board.reads != 0 {
		t.Fatalf("a refused copy was read or reported: %+v (reads %d)", *got, board.reads)
	}
}

func TestWatcherWithoutContentAccessKeepsOnlyTheKind(t *testing.T) {
	board := &fakeBoard{readable: false}
	w, got, _ := newTestWatcher(board)
	w.Tick()
	board.copy("private words", "public.utf8-plain-text")
	w.Tick()
	if len(*got) != 1 || (*got)[0].ContentRead || (*got)[0].Text != "" || board.reads != 0 {
		t.Fatalf("content read without access: %+v (reads %d)", *got, board.reads)
	}
}

func TestWatcherPauseNeverRecordsWhatWasCopiedMeanwhile(t *testing.T) {
	board := &fakeBoard{readable: true}
	w, got, allowed := newTestWatcher(board)
	w.Tick()
	*allowed = false
	board.copy("copied while paused", "public.utf8-plain-text")
	w.Tick()
	if board.typeAsks != 0 || board.reads != 0 {
		t.Fatal("the pasteboard was looked at while paused")
	}
	*allowed = true
	w.Tick()
	if len(*got) != 0 {
		t.Fatalf("a copy made during the pause was reported: %+v", *got)
	}
	board.copy("after the pause", "public.utf8-plain-text")
	w.Tick()
	if len(*got) != 1 || (*got)[0].Text != "after the pause" {
		t.Fatalf("after resume %+v", *got)
	}
}

func TestWatcherPauseBetweenTwoTicksIsStillAPause(t *testing.T) {
	board := &fakeBoard{readable: true}
	w, got, allowed := newTestWatcher(board)
	w.Tick()
	// Off, a copy, on again: all between two ticks.
	*allowed = false
	w.Rebaseline()
	board.copy("copied while off", "public.utf8-plain-text")
	*allowed = true
	w.Rebaseline()
	w.Tick()
	if len(*got) != 0 || board.reads != 0 {
		t.Fatalf("a copy made while off was reported: %+v", *got)
	}
	// A copy right after turning back on, before the next tick, counts.
	*allowed = false
	w.Rebaseline()
	*allowed = true
	w.Rebaseline()
	board.copy("right after", "public.utf8-plain-text")
	w.Tick()
	if len(*got) != 1 || (*got)[0].Text != "right after" {
		t.Fatalf("the first copy after resuming was lost: %+v", *got)
	}
}

func TestWatcherJudgesTheAppsAroundTheCopy(t *testing.T) {
	board := &fakeBoard{readable: true}
	w, got, _ := newTestWatcher(board)
	front := App{BundleID: "com.1password.1password", Name: "1Password"}
	w.Front = func() App { return front }
	var seen [][]App
	w.Accept = func(_ []string, apps []App) bool {
		seen = append(seen, apps)
		for _, app := range apps {
			if SensitiveSource(app.BundleID, app.Name) {
				return false
			}
		}
		return true
	}
	w.Tick()
	// Copied in the password manager, then switched away before the tick.
	board.copy("a password without a concealed marker", "public.utf8-plain-text")
	front = App{BundleID: "com.apple.Notes", Name: "Notes"}
	w.Tick()
	if len(*got) != 0 || board.reads != 0 {
		t.Fatalf("a copy made in the password manager was reported: %+v", *got)
	}
	if len(seen) != 1 || len(seen[0]) != 2 {
		t.Fatalf("Accept should see the app now and the one before: %+v", seen)
	}
}

func TestWatcherDropsACopyThatChangedWhileRead(t *testing.T) {
	board := &fakeBoard{readable: true}
	w, got, _ := newTestWatcher(board)
	w.Tick()
	board.copy("first", "public.utf8-plain-text")
	board.bumpOnRead = true
	w.Tick()
	if len(*got) != 0 {
		t.Fatalf("a copy that changed while read was reported: %+v", *got)
	}
	board.bumpOnRead = false
	w.Tick()
	if len(*got) != 1 {
		t.Fatalf("the newer copy is reported on the next tick: %+v", *got)
	}
}

func TestWatcherIgnoresIsoformsOwnWrite(t *testing.T) {
	board := &fakeBoard{readable: true}
	w, got, _ := newTestWatcher(board)
	w.Tick()
	board.copy("written by Isoform", "public.utf8-plain-text")
	noteOwnWrite(board.count)
	t.Cleanup(func() { ownWrite.Store(-1) })
	w.Tick()
	if len(*got) != 0 {
		t.Fatalf("Isoform's own write was reported: %+v", *got)
	}
	// A paste that sets the text and then restores the old one: every change
	// made inside OwnWrites is Isoform's, seen mid-way or not.
	ownWriting.Add(1)
	board.copy("pasted for the user", "public.utf8-plain-text")
	w.Tick()
	board.copy("restored", "public.utf8-plain-text")
	ownWriting.Add(-1)
	noteOwnWrite(board.count)
	w.Tick()
	if len(*got) != 0 {
		t.Fatalf("a paste by Isoform was reported: %+v", *got)
	}
}
