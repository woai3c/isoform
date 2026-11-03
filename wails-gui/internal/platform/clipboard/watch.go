package clipboard

import (
	"context"
	"sync/atomic"
	"time"
)

// WatchInterval is how often the watcher looks at the pasteboard's change
// counter. Reading the counter is cheap and never shows an alert.
const WatchInterval = time.Second

// App is an application as the watcher sees it: cheap to sample every tick.
// Private marks a browser whose window in front is private or incognito.
type App struct {
	BundleID string
	Name     string
	Private  bool
}

// Copy is what the watcher learned about one copy. Text, Length and Source
// are filled only when macOS lets Isoform read pasteboard content without
// asking (see Board.ContentReadable); otherwise only the kind is known.
type Copy struct {
	At    time.Time
	Types []string
	Kind  string
	// ContentRead reports whether the content was read.
	ContentRead bool
	// Text is at most ReadMaxUnits of the copied text, never stored as is:
	// callers keep only Preview(Text).
	Text string
	// Length is the copied text's length in UTF-16 code units, which is
	// about its number of characters.
	Length int
	// Source is the bundle id the copying app put in org.nspasteboard.source.
	Source string
}

// Board is the system pasteboard as the watcher needs it.
type Board interface {
	// ChangeCount is the pasteboard's change counter.
	ChangeCount() int
	// Types lists the pasteboard types of the current contents.
	Types() []string
	// ContentReadable reports whether reading the contents is allowed
	// without macOS asking the user.
	ContentReadable() bool
	// ReadText returns up to maxUnits UTF-16 code units of the plain text,
	// its full length and the org.nspasteboard.source bundle id; ok is false
	// when nothing could be read.
	ReadText(maxUnits int) (text string, length int, source string, ok bool)
}

// ownWrite is the change count right after Isoform itself last wrote to the
// pasteboard; that change is not the user's copy.
var ownWrite atomic.Int64

// ownWriting is above zero while Isoform is in the middle of its own writes
// (OwnWrites); every change seen then is Isoform's.
var ownWriting atomic.Int32

func init() { ownWrite.Store(-1) }

// noteOwnWrite records the change count a write by Isoform produced.
func noteOwnWrite(count int) { ownWrite.Store(int64(count)) }

// OwnWrites runs fn, which writes to the pasteboard one or more times (a
// paste that sets the text, presses Command-V and restores what was there),
// and marks every change it makes as Isoform's own.
func OwnWrites(fn func()) {
	ownWriting.Add(1)
	defer func() {
		NoteOwnWrite()
		ownWriting.Add(-1)
	}()
	fn()
}

// Watcher polls a Board and reports each new copy once.
type Watcher struct {
	Board    Board
	Interval time.Duration
	Now      func() time.Time
	// Allowed is checked on every tick before anything but the change
	// counter is read: the user's switch is on and observing is not paused.
	// While it is false, copies are counted as seen and never reported.
	Allowed func() bool
	// Front samples the frontmost app on every tick, so a copy is judged by
	// the app it was made in even when the user switched apps before the
	// tick that saw it.
	Front func() App
	// Accept is asked after the types are known and before any content is
	// read, with the apps in front at the previous tick and now; false skips
	// the copy (a password manager was in front, say).
	Accept func(types []string, front []App) bool
	// OnCopy receives each accepted copy.
	OnCopy func(Copy)

	baselined bool
	last      int
	lastFront App

	// Rebaseline marks the current pasteboard as seen from another
	// goroutine; epoch tells the tick it happened.
	epoch     atomic.Uint64
	rebasedAt atomic.Int64
	seenEpoch uint64
}

// Run polls until the context ends.
func (w *Watcher) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = WatchInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		w.Tick()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Rebaseline marks whatever is on the pasteboard now as seen. Call it the
// moment the switch or the pause changes, either way: a copy made while off
// is then never reported, even when off and on again fall between two ticks,
// and a copy made right after turning back on is.
func (w *Watcher) Rebaseline() {
	if w.Board == nil {
		return
	}
	w.rebasedAt.Store(int64(w.Board.ChangeCount()))
	w.epoch.Add(1)
}

// Tick looks at the pasteboard once.
func (w *Watcher) Tick() {
	if w.Board == nil {
		return
	}
	epoch := w.epoch.Load()
	if epoch != w.seenEpoch {
		w.seenEpoch = epoch
		w.last, w.baselined = int(w.rebasedAt.Load()), true
	}
	if w.Allowed != nil && !w.Allowed() {
		// Counted as seen, never looked at.
		w.last, w.baselined = w.Board.ChangeCount(), true
		w.lastFront = App{}
		return
	}
	var front App
	if w.Front != nil {
		front = w.Front()
	}
	previous := w.lastFront
	w.lastFront = front
	count := w.Board.ChangeCount()
	if !w.baselined {
		// Whatever is on the pasteboard at start was not copied while
		// Isoform was looking.
		w.last, w.baselined = count, true
		return
	}
	if count == w.last {
		return
	}
	w.last = count
	if ownWriting.Load() > 0 || int64(count) == ownWrite.Load() {
		return
	}
	types := w.Board.Types()
	if len(types) == 0 || SkipTypes(types) {
		return
	}
	if w.Accept != nil {
		seen := []App{front}
		if previous != (App{}) && previous != front {
			seen = append(seen, previous)
		}
		if !w.Accept(types, seen) {
			return
		}
	}
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	c := Copy{At: now(), Types: types, Kind: KindOf(types)}
	if HasText(types) && w.Board.ContentReadable() {
		text, length, source, ok := w.Board.ReadText(ReadMaxUnits)
		// The contents changed while being read: the next tick reports
		// the newer copy instead.
		if w.Board.ChangeCount() != count {
			return
		}
		if ok {
			c.ContentRead, c.Text, c.Length, c.Source = true, text, length, source
			if c.Kind == KindText && IsLink(text) {
				c.Kind = KindURL
			}
		}
	}
	// The switch or the pause changed while the copy was looked at.
	if w.epoch.Load() != epoch {
		return
	}
	if w.OnCopy != nil {
		w.OnCopy(c)
	}
}
