package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/activitybuf"
)

// ── fixtures ──

// storageTestRotator returns a rotator over store with a fixed config, no
// saved screenshot time limit and no ffmpeg unless the test sets one.
func storageTestRotator(store *ActivityStore, maxBytes int64) *StorageRotator {
	r := NewStorageRotator(store, RotatorConfig{
		HotRetention:   24 * time.Hour,
		WarmRetention:  7 * 24 * time.Hour,
		MaxTotalSize:   maxBytes,
		TargetFreeSize: maxBytes * 9 / 10,
		CheckInterval:  time.Hour,
	})
	r.ffmpegPath = func() string { return "" }
	r.vacuumMoment = func() bool { return false }
	r.screenshotRetention = func() time.Duration { return 0 }
	// Validate the real plan, media conversion and accounting without asserting
	// OS background dispatch latency, which can exceed the cleanup deadline.
	r.mediaPriority = func(work func()) { work() }
	// Most tests are about a profile whose removals are confirmed.
	r.limitDecision = func() (bool, bool) { return true, true }
	r.confirmLimit = func() {}
	return r
}

// addSnapshot writes a screenshot file and its row. w and h are recorded on
// the row; data is written as is.
func addSnapshot(t *testing.T, store *ActivityStore, sessionID string, at time.Time, w, h int, data []byte, tier string) (int64, string) {
	t.Helper()
	rel := filepath.Join(at.Format("2006-01-02"), fmt.Sprintf("%d-%s.jpg", at.UnixMilli(), sessionID))
	path := filepath.Join(store.SnapshotsDir(), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := store.db.Exec(`INSERT INTO activity_snapshots (session_id, timestamp, file_path, width, height,
		size_bytes, trigger_kind, storage_tier, ax_text) VALUES (?, ?, ?, ?, ?, ?, 'test', ?, ?)`,
		sessionID, at.UnixMilli(), rel, w, h, len(data), tier, "window text "+sessionID)
	if err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
	id, _ := res.LastInsertId()
	if _, err := store.db.Exec(`UPDATE activity_sessions SET snapshot_count = snapshot_count + 1,
		total_size_bytes = total_size_bytes + ? WHERE id = ?`, len(data), sessionID); err != nil {
		t.Fatal(err)
	}
	return id, rel
}

// ocrDone marks sessions as OCR'd, so their images may be removed.
func ocrDone(t *testing.T, store *ActivityStore, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'done' WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
}

func snapshotRow(t *testing.T, store *ActivityStore, id int64) (tier string, size int64, w, h int, ax string) {
	t.Helper()
	if err := store.db.QueryRow(`SELECT storage_tier, size_bytes, width, height, COALESCE(ax_text, '')
		FROM activity_snapshots WHERE id = ?`, id).Scan(&tier, &size, &w, &h, &ax); err != nil {
		t.Fatalf("read snapshot %d: %v", id, err)
	}
	return
}

// screenJPEG draws a screen-like image (flat panels, lines of "text") and
// encodes it at the capture quality.
func screenJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rnd := rand.New(rand.NewSource(int64(w*h + 7)))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{250, 248, 246, 255})
		}
	}
	for y := 20; y+12 < h; y += 24 {
		for x := 20; x < w-40; {
			word := 8 + rnd.Intn(60)
			for yy := y; yy < y+12; yy++ {
				for xx := x; xx < min(x+word, w); xx++ {
					if rnd.Intn(3) > 0 {
						img.Set(xx, yy, color.RGBA{42, 35, 32, 255})
					}
				}
			}
			x += word + 10
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: snapshotQuality}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeTestWAV writes 16 kHz 16-bit PCM with a tone per channel.
func writeTestWAV(t *testing.T, path string, channels int, seconds float64) {
	t.Helper()
	const rate = 16000
	frames := int(seconds * rate)
	data := make([]byte, frames*channels*2)
	for i := 0; i < frames; i++ {
		for c := 0; c < channels; c++ {
			v := int16(8000 * math.Sin(2*math.Pi*float64(300+200*c)*float64(i)/rate))
			binary.LittleEndian.PutUint16(data[(i*channels+c)*2:], uint16(v))
		}
	}
	var hdr bytes.Buffer
	hdr.WriteString("RIFF")
	binary.Write(&hdr, binary.LittleEndian, uint32(36+len(data)))
	hdr.WriteString("WAVEfmt ")
	binary.Write(&hdr, binary.LittleEndian, uint32(16))
	binary.Write(&hdr, binary.LittleEndian, uint16(1))
	binary.Write(&hdr, binary.LittleEndian, uint16(channels))
	binary.Write(&hdr, binary.LittleEndian, uint32(rate))
	binary.Write(&hdr, binary.LittleEndian, uint32(rate*channels*2))
	binary.Write(&hdr, binary.LittleEndian, uint16(channels*2))
	binary.Write(&hdr, binary.LittleEndian, uint16(16))
	hdr.WriteString("data")
	binary.Write(&hdr, binary.LittleEndian, uint32(len(data)))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(hdr.Bytes(), data...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// testFFmpeg finds an ffmpeg with libopus for the audio tests.
func testFFmpeg(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("YANSU_TEST_FFMPEG"); p != "" {
		return p
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	t.Skip("ffmpeg not available (set YANSU_TEST_FFMPEG)")
	return ""
}

func setSessionAudio(t *testing.T, store *ActivityStore, sessionID, rel string, size int64, transcript string) {
	t.Helper()
	if _, err := store.db.Exec(`UPDATE activity_sessions SET audio_path = ?, audio_size_bytes = ?, transcript_path = ?, is_meeting = 1
		WHERE id = ?`, rel, size, transcript, sessionID); err != nil {
		t.Fatal(err)
	}
}

// ── usage ──

func TestMeasureActivityStorageCountsFilesWithoutRows(t *testing.T) {
	store := newTestStore(t)
	base := measureActivityStorage(store)
	if base.DatabaseBytes <= 0 {
		t.Fatalf("database bytes not measured: %+v", base)
	}

	insertSession(t, store, "s1", time.Now().Add(-48*time.Hour), "pending")
	addSnapshot(t, store, "s1", time.Now().Add(-48*time.Hour), 100, 100, make([]byte, 1000), "warm")
	// A file no row refers to and an audio file still count.
	orphan := filepath.Join(store.SnapshotsDir(), "2026-01-02", "1-x.jpg")
	os.MkdirAll(filepath.Dir(orphan), 0o700)
	os.WriteFile(orphan, make([]byte, 3000), 0o600)
	os.MkdirAll(filepath.Join(store.AudioDir(), "2026-01-02"), 0o700)
	os.WriteFile(filepath.Join(store.AudioDir(), "2026-01-02", "1-a.wav"), make([]byte, 5000), 0o600)

	u := measureActivityStorage(store)
	if u.ScreenshotBytes != 4000 || u.ScreenshotFiles != 2 || u.ScreenshotDays != 2 {
		t.Errorf("screenshots: got %d bytes, %d files, %d days", u.ScreenshotBytes, u.ScreenshotFiles, u.ScreenshotDays)
	}
	if u.AudioBytes != 5000 || u.AudioFiles != 1 {
		t.Errorf("audio: got %d bytes, %d files", u.AudioBytes, u.AudioFiles)
	}
	day, _ := time.ParseInLocation("2006-01-02", "2026-01-02", time.Local)
	if u.OldestScreenshotMs != day.UnixMilli() {
		t.Errorf("oldest day: got %d, want %d", u.OldestScreenshotMs, day.UnixMilli())
	}
	if u.TotalBytes != u.ScreenshotBytes+u.AudioBytes+u.DatabaseBytes+u.OtherBytes {
		t.Errorf("total does not add up: %+v", u)
	}
}

// ── storage limit ──

func TestEnforceBudgetRemovesOldestImagesAndKeepsText(t *testing.T) {
	store := newTestStore(t)
	const mb = 1 << 20
	// The database counts with its write-ahead log. Start from an empty log so
	// the limits below measure the images, not when SQLite next checkpoints
	// (an extra migration page moved that point and broke this test).
	if _, err := store.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	base := measureActivityStorage(store).TotalBytes
	// Room for about two of the four 1 MB images above the database.
	r := storageTestRotator(store, base+int64(2.5*mb))
	cfg := r.snapshotConfig()
	cfg.TargetFreeSize = base + 22*mb/10
	r.UpdateConfig(cfg)

	now := time.Now()
	var ids []int64
	var rels []string
	for i, age := range []time.Duration{30 * 24 * time.Hour, 20 * 24 * time.Hour, 3 * 24 * time.Hour, time.Hour} {
		sid := fmt.Sprintf("s%d", i)
		insertSession(t, store, sid, now.Add(-age), "pending")
		ocrDone(t, store, sid)
		id, rel := addSnapshot(t, store, sid, now.Add(-age), 100, 100, make([]byte, mb), "warm")
		ids = append(ids, id)
		rels = append(rels, rel)
	}

	removed, _ := r.enforceBudget(r.snapshotConfig(), r.measure(), 0, false)
	if removed != 2 {
		t.Fatalf("removed %d images, want the 2 oldest", removed)
	}
	for i, id := range ids {
		tier, size, _, _, ax := snapshotRow(t, store, id)
		gone := i < 2
		if gone != (tier == snapshotTierMissing) || gone == fileExists(filepath.Join(store.SnapshotsDir(), rels[i])) {
			t.Errorf("image %d: tier %q, file present %v, want removed=%v", i, tier, fileExists(filepath.Join(store.SnapshotsDir(), rels[i])), gone)
		}
		if gone && size != 0 {
			t.Errorf("image %d: size %d after removal, want 0", i, size)
		}
		if ax == "" {
			t.Errorf("image %d lost its text", i)
		}
		if !sessionExists(t, store, fmt.Sprintf("s%d", i)) {
			t.Errorf("session s%d deleted; the limit must keep rows", i)
		}
	}
	var count int
	var total int64
	store.db.QueryRow(`SELECT snapshot_count, total_size_bytes FROM activity_sessions WHERE id = 's0'`).Scan(&count, &total)
	if count != 0 || total != 0 {
		t.Errorf("session s0 not recounted: count %d total %d", count, total)
	}
}

func TestEnforceBudgetProtectsTheLastDay(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1) // far over
	insertSession(t, store, "fresh", time.Now().Add(-time.Hour), "pending")
	id, _ := addSnapshot(t, store, "fresh", time.Now().Add(-time.Hour), 100, 100, make([]byte, 1000), "hot")
	r.enforceBudget(r.snapshotConfig(), r.measure(), 0, false)
	if tier, _, _, _, _ := snapshotRow(t, store, id); tier == snapshotTierMissing {
		t.Error("an image from the last hour was removed")
	}
}

func TestEnforceBudgetCountsFilesWithoutRows(t *testing.T) {
	store := newTestStore(t)
	const mb = 1 << 20
	base := measureActivityStorage(store).TotalBytes
	r := storageTestRotator(store, base+int64(1.5*mb))
	insertSession(t, store, "old", time.Now().Add(-10*24*time.Hour), "pending")
	ocrDone(t, store, "old")
	id, _ := addSnapshot(t, store, "old", time.Now().Add(-10*24*time.Hour), 100, 100, make([]byte, mb), "cold")
	// 1 MB of files no row refers to: the rows alone fit, the disk does not.
	orphan := filepath.Join(store.SnapshotsDir(), "2026-01-02", "1-x.jpg")
	os.MkdirAll(filepath.Dir(orphan), 0o700)
	os.WriteFile(orphan, make([]byte, mb), 0o600)

	r.enforceBudget(r.snapshotConfig(), r.measure(), 0, false)
	if tier, _, _, _, _ := snapshotRow(t, store, id); tier != snapshotTierMissing {
		t.Error("files without rows were not counted against the limit")
	}
}

func TestEnforceBudgetCountsPendingCompaction(t *testing.T) {
	store := newTestStore(t)
	const mb = 1 << 20
	base := measureActivityStorage(store).TotalBytes
	r := storageTestRotator(store, base+2*mb)
	// 3 MB of cold 8 MP screenshots: compacted they take about 0.7 MB, so
	// nothing should be removed.
	for i := 0; i < 3; i++ {
		sid := fmt.Sprintf("big%d", i)
		at := time.Now().Add(-time.Duration(10+i) * 24 * time.Hour)
		insertSession(t, store, sid, at, "pending")
		store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'done' WHERE id = ?`, sid)
		addSnapshot(t, store, sid, at, 4000, 2000, make([]byte, mb), "cold")
	}
	if removed, _ := r.enforceBudget(r.snapshotConfig(), r.measure(), 0, false); removed != 0 {
		t.Errorf("removed %d images that compaction would have made fit", removed)
	}
}

func TestEnforceBudgetRemovesOldRecordingsKeepsTranscript(t *testing.T) {
	store := newTestStore(t)
	const mb = 1 << 20
	base := measureActivityStorage(store).TotalBytes
	r := storageTestRotator(store, base+mb)
	at := time.Now().Add(-5 * 24 * time.Hour)
	insertSession(t, store, "call", at, "pending")
	rel := filepath.Join(at.Format("2006-01-02"), fmt.Sprintf("%d-call.wav", at.UnixMilli()))
	path := filepath.Join(store.AudioDir(), rel)
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, make([]byte, 2*mb), 0o600)
	setSessionAudio(t, store, "call", rel, 2*mb, "/t/notes.md")

	r.enforceBudget(r.snapshotConfig(), r.measure(), 0, false)
	s, err := store.GetSessionByID("call")
	if err != nil {
		t.Fatal(err)
	}
	if s.AudioPath != "" || fileExists(path) {
		t.Errorf("old recording kept over the limit: path %q, file present %v", s.AudioPath, fileExists(path))
	}
	if s.TranscriptPath != "/t/notes.md" {
		t.Errorf("transcript path lost: %q", s.TranscriptPath)
	}
}

// ── screenshot time limit ──

func TestExpireScreenshotsKeepsRowsAndText(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	cfg := r.snapshotConfig()
	cfg.ScreenshotRetention = 14 * 24 * time.Hour
	insertSession(t, store, "old", time.Now().Add(-20*24*time.Hour), "pending")
	insertSession(t, store, "new", time.Now().Add(-2*24*time.Hour), "pending")
	ocrDone(t, store, "old", "new")
	oldID, oldRel := addSnapshot(t, store, "old", time.Now().Add(-20*24*time.Hour), 100, 100, make([]byte, 100), "cold")
	newID, _ := addSnapshot(t, store, "new", time.Now().Add(-2*24*time.Hour), 100, 100, make([]byte, 100), "warm")

	if removed, _ := r.expireScreenshots(cfg); removed != 1 {
		t.Fatalf("removed %d, want 1", removed)
	}
	if tier, _, _, _, ax := snapshotRow(t, store, oldID); tier != snapshotTierMissing || ax == "" {
		t.Errorf("old screenshot: tier %q text %q", tier, ax)
	}
	if fileExists(filepath.Join(store.SnapshotsDir(), oldRel)) {
		t.Error("old screenshot file still on disk")
	}
	if tier, _, _, _, _ := snapshotRow(t, store, newID); tier == snapshotTierMissing {
		t.Error("recent screenshot removed")
	}
}

// ── compaction ──

func TestCompactSnapshotJPEG(t *testing.T) {
	big := screenJPEG(t, 3000, 2000)
	out, w, h, ok, err := compactSnapshotJPEG(big)
	if err != nil || !ok {
		t.Fatalf("compact: ok=%v err=%v", ok, err)
	}
	if int64(w)*int64(h) > snapshotCompactMaxPixels || w < 1700 || w > 1740 {
		t.Errorf("compacted to %dx%d", w, h)
	}
	if len(out) >= len(big) {
		t.Errorf("not smaller: %d -> %d", len(big), len(out))
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != w || cfg.Height != h {
		t.Errorf("result does not decode as %dx%d JPEG: %+v %v", w, h, cfg, err)
	}

	small := screenJPEG(t, 1200, 800)
	if _, w, h, ok, err := compactSnapshotJPEG(small); ok || err != nil || w != 1200 || h != 800 {
		t.Errorf("small image: ok=%v %dx%d err=%v", ok, w, h, err)
	}
}

func TestCompactColdSnapshots(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	cfg := r.snapshotConfig()
	at := time.Now().Add(-10 * 24 * time.Hour)
	insertSession(t, store, "done", at, "pending")
	store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'done' WHERE id = 'done'`)
	insertSession(t, store, "ocr", at, "pending")
	store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'pending' WHERE id = 'ocr'`)
	insertSession(t, store, "recent", time.Now().Add(-time.Hour), "pending")
	store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'done' WHERE id = 'recent'`)

	big := screenJPEG(t, 3000, 2000)
	coldID, coldRel := addSnapshot(t, store, "done", at, 3000, 2000, big, "cold")
	ocrID, _ := addSnapshot(t, store, "ocr", at.Add(time.Second), 3000, 2000, big, "cold")
	hotID, _ := addSnapshot(t, store, "recent", time.Now().Add(-time.Hour), 3000, 2000, big, "hot")
	smallID, _ := addSnapshot(t, store, "done", at.Add(2*time.Second), 0, 0, screenJPEG(t, 1000, 700), "cold")

	compacted, saved := r.compactColdSnapshots(cfg, time.Now().Add(time.Minute), 0, nil, nil)
	if compacted != 1 || saved <= 0 {
		t.Fatalf("compacted %d (saved %d), want 1", compacted, saved)
	}
	_, size, w, h, _ := snapshotRow(t, store, coldID)
	data, _ := os.ReadFile(filepath.Join(store.SnapshotsDir(), coldRel))
	if int64(len(data)) != size || int64(w)*int64(h) > snapshotCompactMaxPixels {
		t.Errorf("row after compaction: size %d (file %d) %dx%d", size, len(data), w, h)
	}
	var total int64
	store.db.QueryRow(`SELECT total_size_bytes FROM activity_sessions WHERE id = 'done'`).Scan(&total)
	if want := size + int64(len(screenJPEG(t, 1000, 700))); total != want {
		t.Errorf("session total %d, want %d", total, want)
	}
	if _, size, _, _, _ := snapshotRow(t, store, ocrID); size != int64(len(big)) {
		t.Error("screenshot of a session waiting for OCR was compacted")
	}
	if _, size, _, _, _ := snapshotRow(t, store, hotID); size != int64(len(big)) {
		t.Error("hot screenshot was compacted")
	}
	if _, _, w, h, _ := snapshotRow(t, store, smallID); w != 1000 || h != 700 {
		t.Errorf("unknown size not recorded: %dx%d", w, h)
	}
	// A second pass finds nothing to do.
	if compacted, _ := r.compactColdSnapshots(cfg, time.Now().Add(time.Minute), 0, nil, nil); compacted != 0 {
		t.Errorf("second pass compacted %d", compacted)
	}
}

// ── call audio ──

func TestCompressCallAudioAndDecodeBack(t *testing.T) {
	ffmpeg := testFFmpeg(t)
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	r.ffmpegPath = func() string { return ffmpeg }

	at := time.Now().Add(-2 * time.Hour)
	insertSession(t, store, "call", at, "pending")
	insertSession(t, store, "fresh", at.Add(time.Minute), "pending")
	rel := filepath.Join(at.Format("2006-01-02"), fmt.Sprintf("%d-call.wav", at.UnixMilli()))
	freshRel := filepath.Join(at.Format("2006-01-02"), fmt.Sprintf("%d-fresh.wav", at.Add(time.Minute).UnixMilli()))
	writeTestWAV(t, filepath.Join(store.AudioDir(), rel), 2, 3)
	writeTestWAV(t, filepath.Join(store.AudioDir(), freshRel), 1, 1)
	info, _ := os.Stat(filepath.Join(store.AudioDir(), rel))
	setSessionAudio(t, store, "call", rel, info.Size(), "/t/call.md")
	setSessionAudio(t, store, "fresh", freshRel, 32044, "") // no transcript yet

	compressed, saved := r.compressCallAudio(time.Now().Add(time.Minute), nil, nil)
	if compressed != 1 || saved <= 0 {
		t.Fatalf("compressed %d (saved %d), want 1", compressed, saved)
	}
	s, _ := store.GetSessionByID("call")
	if filepath.Ext(s.AudioPath) != meetingAudioOpusExt || fileExists(filepath.Join(store.AudioDir(), rel)) {
		t.Fatalf("row %q, WAV present %v", s.AudioPath, fileExists(filepath.Join(store.AudioDir(), rel)))
	}
	opus, _ := os.Stat(filepath.Join(store.AudioDir(), s.AudioPath))
	if opus == nil || opus.Size() != s.AudioSizeBytes || opus.Size()*5 > info.Size() {
		t.Errorf("opus size %v vs row %d, wav %d", opus, s.AudioSizeBytes, info.Size())
	}
	if fresh, _ := store.GetSessionByID("fresh"); fresh.AudioPath != freshRel {
		t.Errorf("untranscribed recent recording was compressed: %q", fresh.AudioPath)
	}

	wav, cleanup, err := decodeMeetingAudioToWAV(ffmpeg, filepath.Join(store.AudioDir(), s.AudioPath))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if start, ok := meetingAudioStartFromName(wav); !ok || start != at.UnixMilli() {
		t.Errorf("decoded copy lost the start time in its name: %s", filepath.Base(wav))
	}
	wf, err := readWAVFormat(wav)
	if err != nil {
		t.Fatal(err)
	}
	if wf.channels != 2 || wf.sampleRate != 16000 {
		t.Errorf("decoded format %+v", wf)
	}
	if secs := float64(wf.dataBytes) / float64(wf.byteRate); secs < 2.9 || secs > 3.1 {
		t.Errorf("decoded duration %.2fs, want 3s", secs)
	}
}

// meetingAudioStartFromName reads "<startMs>-<id>.<ext>".
func meetingAudioStartFromName(path string) (int64, bool) {
	var ms int64
	_, err := fmt.Sscanf(filepath.Base(path), "%d-", &ms)
	return ms, err == nil && ms > 0
}

func TestSweepOrphanAudio(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	dir := filepath.Join(store.AudioDir(), "2026-01-02")
	os.MkdirAll(dir, 0o700)
	old := time.Now().Add(-20 * 24 * time.Hour)
	files := map[string]bool{ // name -> should survive
		"1-orphan.wav":     false,
		"2-orphan.opus":    false,
		"3-kept.wav":       true,
		"4-call.json":      true,
		"5-x.opus.tmp":     false,
		"6-recent.wav":     true,
		"7-referenced.wav": true,
	}
	for name := range files {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("x"), 0o600)
		if name != "6-recent.wav" {
			os.Chtimes(p, old, old)
		}
	}
	insertSession(t, store, "ref", old, "pending")
	setSessionAudio(t, store, "ref", filepath.Join("2026-01-02", "7-referenced.wav"), 1, "")
	insertSession(t, store, "kept", old, "pending")
	setSessionAudio(t, store, "kept", filepath.Join("2026-01-02", "3-kept.wav"), 1, "")

	r.sweepOrphanAudio(time.Now())
	for name, survive := range files {
		if fileExists(filepath.Join(dir, name)) != survive {
			t.Errorf("%s: present %v, want %v", name, !survive, survive)
		}
	}
}

// ── database ──

func TestMaybeVacuumOnlyWhenLockedAndWorthIt(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	saved := processStartedAt
	processStartedAt = time.Now().Add(-time.Hour)
	t.Cleanup(func() { processStartedAt = saved })

	store.db.Exec(`CREATE TABLE junk (b BLOB)`)
	for i := 0; i < 40; i++ {
		store.db.Exec(`INSERT INTO junk VALUES (randomblob(1048576))`)
	}
	store.db.Exec(`DROP TABLE junk`)
	free, _, _ := store.databaseFreeBytes()
	if free < 30<<20 {
		t.Fatalf("setup: only %d free bytes", free)
	}

	if r.maybeVacuum(time.Now()) {
		t.Fatal("VACUUM ran while the screen was not locked")
	}
	r.vacuumMoment = func() bool { return true }
	if r.maybeVacuum(time.Now()) {
		t.Fatal("VACUUM ran below the free-space threshold without a request")
	}
	r.RequestVacuum()
	if !r.maybeVacuum(time.Now()) {
		t.Fatal("requested VACUUM did not run while locked")
	}
	if free, _, _ := store.databaseFreeBytes(); free != 0 {
		t.Errorf("free bytes after VACUUM: %d", free)
	}
	r.RequestVacuum()
	if r.maybeVacuum(time.Now()) {
		t.Error("VACUUM ran again within the minimum interval")
	}
}

func TestRetiredTablesPresentAndDropped(t *testing.T) {
	store := newTestStore(t)
	if tables, _, err := store.retiredTablesPresent(); err != nil || len(tables) != 0 {
		t.Fatalf("fresh store: %v %v", tables, err)
	}
	if _, err := store.db.Exec(`CREATE TABLE agency_chat_messages_v1 (id TEXT, payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	store.db.Exec(`INSERT INTO agency_chat_messages_v1 VALUES ('a', randomblob(200000))`)
	tables, bytes, err := store.retiredTablesPresent()
	if err != nil || len(tables) != 1 {
		t.Fatalf("present: %v %v", tables, err)
	}
	if bytes < 200000 {
		t.Errorf("retired bytes %d, want at least the blob", bytes)
	}
	if err := store.dropRetiredTables(); err != nil {
		t.Fatal(err)
	}
	if tables, _, _ := store.retiredTablesPresent(); len(tables) != 0 {
		t.Errorf("still present: %v", tables)
	}
}

// ── cleanup ──

func TestCleanupPlanChangesNothingAndRunMatchesIt(t *testing.T) {
	store := newTestStore(t)
	const mb = 1 << 20
	// The migrations leave megabytes in the WAL; an automatic checkpoint
	// during the run would move the limit's baseline. Start from a
	// checkpointed database so the plan and the run see the same overhead.
	if _, err := store.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	base := measureActivityStorage(store).TotalBytes
	r := storageTestRotator(store, base+3*mb)
	r.screenshotRetention = func() time.Duration { return 60 * 24 * time.Hour }

	now := time.Now()
	// Expired (90 days), over the limit (40 and 30 days), kept (2 days).
	type snap struct {
		id  int64
		rel string
	}
	var snaps []snap
	for i, age := range []int{90, 40, 30, 20, 2} {
		sid := fmt.Sprintf("s%d", i)
		at := now.Add(-time.Duration(age) * 24 * time.Hour)
		insertSession(t, store, sid, at, "pending")
		store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'done' WHERE id = ?`, sid)
		id, rel := addSnapshot(t, store, sid, at, 100, 100, make([]byte, mb), "cold")
		snaps = append(snaps, snap{id, rel})
	}
	orphan := filepath.Join(store.SnapshotsDir(), now.Add(-50*24*time.Hour).Format("2006-01-02"), "1-orphan.jpg")
	os.MkdirAll(filepath.Dir(orphan), 0o700)
	os.WriteFile(orphan, make([]byte, mb/2), 0o600)
	os.Chtimes(orphan, now.Add(-50*24*time.Hour), now.Add(-50*24*time.Hour))
	// A compactable screenshot that is kept.
	insertSession(t, store, "big", now.Add(-9*24*time.Hour), "pending")
	store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'done' WHERE id = 'big'`)
	bigData := screenJPEG(t, 3000, 2000)
	bigID, _ := addSnapshot(t, store, "big", now.Add(-9*24*time.Hour), 3000, 2000, bigData, "warm")

	listing := func() map[string]int64 {
		out := map[string]int64{}
		filepath.Walk(store.baseDir(), func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && filepath.Ext(p) == ".jpg" {
				out[p] = info.Size()
			}
			return nil
		})
		return out
	}
	before := listing()

	plan, err := r.PlanCleanup()
	if err != nil {
		t.Fatal(err)
	}
	if after := listing(); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatal("building the plan changed files")
	}
	if plan.UnlinkedScreenshots.Count != 1 || plan.UnlinkedScreenshots.Bytes != mb/2 {
		t.Errorf("unlinked: %+v", plan.UnlinkedScreenshots)
	}
	if plan.ExpiredScreenshots.Count != 1 || plan.ExpiredScreenshots.Bytes != mb {
		t.Errorf("expired: %+v", plan.ExpiredScreenshots)
	}
	if plan.ShrinkScreenshots.Count != 1 || plan.ShrinkScreenshots.AfterBytes >= int64(len(bigData)) {
		t.Errorf("shrink: %+v", plan.ShrinkScreenshots)
	}
	if plan.OverLimitScreenshots.Count < 1 {
		t.Errorf("over limit: %+v", plan.OverLimitScreenshots)
	}
	if plan.FreedBytes <= 0 || plan.AfterBytes != plan.Usage.TotalBytes-plan.FreedBytes {
		t.Errorf("totals: freed %d after %d usage %d", plan.FreedBytes, plan.AfterBytes, plan.Usage.TotalBytes)
	}

	// Exercise the same cleanup as StartCleanup synchronously, at caller
	// priority. macOS background QoS can starve real JPEG work under load;
	// the test checks cleanup results, not OS scheduling. Returning also
	// ensures no worker outlives the test store.
	r.runCleanupWithMediaPriority(func(work func()) { work() })
	st := r.cleanupStatus()
	if st.Running || st.Phase != "done" {
		t.Fatalf("cleanup did not finish: %+v", st)
	}
	if fileExists(orphan) {
		t.Error("unlinked screenshot still there")
	}
	removed := 0
	for _, s := range snaps {
		if tier, _, _, _, _ := snapshotRow(t, store, s.id); tier == snapshotTierMissing {
			removed++
		}
	}
	if want := plan.ExpiredScreenshots.Count + plan.OverLimitScreenshots.Count; removed != want {
		t.Errorf("cleanup removed %d images, plan said %d", removed, want)
	}
	if _, size, _, _, _ := snapshotRow(t, store, bigID); size >= int64(len(bigData)) {
		t.Error("kept screenshot was not shrunk")
	}
	if st.FreedBytes <= 0 || st.AfterBytes >= st.BeforeBytes {
		t.Errorf("status totals: %+v", st)
	}
}

func TestScreenshotDaysSettingSurvivesOtherSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &App{}
	if got := a.GetScreenshotRetentionDays(); got != -1 {
		t.Errorf("unset: got %d, want -1 (with recordings)", got)
	}
	if savedScreenshotRetention() != 0 {
		t.Error("unset screenshot limit must not remove images on its own")
	}
	if err := a.SetScreenshotRetentionDays(14); err != nil {
		t.Fatal(err)
	}
	if err := a.SetActivityMaxStorageGB(20); err != nil {
		t.Fatal(err)
	}
	if err := a.SetActivityRetentionDays(90); err != nil {
		t.Fatal(err)
	}
	if got := a.GetScreenshotRetentionDays(); got != 14 {
		t.Errorf("after other settings: got %d, want 14", got)
	}
	if savedScreenshotRetention() != 14*24*time.Hour {
		t.Errorf("rotator limit %v", savedScreenshotRetention())
	}
	if err := a.SetScreenshotRetentionDays(0); err != nil {
		t.Fatal(err)
	}
	if got := a.GetScreenshotRetentionDays(); got != -1 || savedScreenshotRetention() != 0 {
		t.Errorf("0 = with recordings: got %d / %v", got, savedScreenshotRetention())
	}
	// Not shorter than the recording limit (90 days now): with recordings.
	if err := a.SetScreenshotRetentionDays(120); err != nil {
		t.Fatal(err)
	}
	if got := a.GetScreenshotRetentionDays(); got != -1 || savedScreenshotRetention() != 0 {
		t.Errorf("120 days under a 90-day recording limit: got %d / %v", got, savedScreenshotRetention())
	}
	if err := a.SetScreenshotRetentionDays(60); err != nil {
		t.Fatal(err)
	}
	if err := a.SetActivityRetentionDays(30); err != nil {
		t.Fatal(err)
	}
	if got := a.GetScreenshotRetentionDays(); got != -1 {
		t.Errorf("60 days after the recording limit dropped to 30: got %d, want -1", got)
	}
	if err := a.SetScreenshotRetentionDays(-1); err != nil || a.GetScreenshotRetentionDays() != -1 {
		t.Errorf("-1 should clear the choice: %v, %d", err, a.GetScreenshotRetentionDays())
	}
}

func TestSlimRetiredAgencyRunsKeepsRowsAndReferences(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	if !store.tableExists("agency_origination_runs_v1") {
		t.Skip("agency runs table not created by migrations")
	}
	now := time.Now().UnixMilli()
	for i := 0; i < 25; i++ {
		_, err := store.db.Exec(`INSERT INTO agency_origination_runs_v1 (id, idempotency_key, status, wake_reason, wake_at,
			source_input_json, source_manifest_json, planning_input_json, prompt, raw_output, output_json,
			rejected_evidence_refs_json, rejected_candidate_keys_json, resource_policy_json, resource_usage_json,
			payload_json, payload_hash, created_at)
			VALUES (?, ?, 'planner_error', 'periodic', ?, ?, '{}', '{}', ?, '', '{}', '[]', '[]', '{}', '{}', '{}', 'h', ?)`,
			fmt.Sprintf("run-%02d", i), fmt.Sprintf("key-%02d", i), now,
			`{"big":"`+string(bytes.Repeat([]byte("x"), 100000))+`"}`, string(bytes.Repeat([]byte("p"), 100000)), now)
		if err != nil {
			t.Fatalf("insert run: %v", err)
		}
	}
	if _, err := store.db.Exec(`UPDATE agency_origination_runs_v1 SET prompt = 'x' WHERE id = 'run-00'`); err == nil {
		t.Fatal("setup: the immutability trigger should refuse updates")
	}
	if n := r.slimRetiredAgencyRuns(time.Now().Add(time.Minute), 0, nil); n != 25 {
		t.Fatalf("cleared %d runs, want 25", n)
	}
	var rows int
	var longest int
	store.db.QueryRow(`SELECT COUNT(*), MAX(length(prompt) + length(source_input_json)) FROM agency_origination_runs_v1`).Scan(&rows, &longest)
	if rows != 25 || longest > 2 {
		t.Errorf("after clearing: %d rows, longest %d", rows, longest)
	}
	if ids, _ := store.agencyRunsToSlim(); len(ids) != 0 {
		t.Errorf("runs still listed after the pass: %d", len(ids))
	}
	if free, _, _ := store.databaseFreeBytes(); free < 2<<20 {
		t.Errorf("cleared runs freed only %d bytes", free)
	}
	r.mu.RLock()
	requested := r.vacuumRequested
	r.mu.RUnlock()
	if !requested {
		t.Error("clearing runs did not ask for a VACUUM")
	}
}

// ── review fixes ──

func TestEnforceBudgetRemovesNothingWhenTheLimitCannotBeReached(t *testing.T) {
	store := newTestStore(t)
	const mb = 1 << 20
	base := measureActivityStorage(store).TotalBytes
	// The database alone is over this limit: removing every old file would
	// not reach it, so nothing goes.
	r := storageTestRotator(store, base/2)
	at := time.Now().Add(-10 * 24 * time.Hour)
	insertSession(t, store, "old", at, "pending")
	store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'done' WHERE id = 'old'`)
	id, rel := addSnapshot(t, store, "old", at, 100, 100, make([]byte, mb), "cold")
	audioRel := filepath.Join(at.Format("2006-01-02"), fmt.Sprintf("%d-old.wav", at.UnixMilli()))
	os.MkdirAll(filepath.Join(store.AudioDir(), filepath.Dir(audioRel)), 0o700)
	os.WriteFile(filepath.Join(store.AudioDir(), audioRel), make([]byte, mb), 0o600)
	setSessionAudio(t, store, "old", audioRel, mb, "/t/old.md")

	if removed, _ := r.enforceBudget(r.snapshotConfig(), r.measure(), 0, false); removed != 0 {
		t.Fatalf("removed %d files under an unreachable limit", removed)
	}
	if tier, _, _, _, _ := snapshotRow(t, store, id); tier == snapshotTierMissing || !fileExists(filepath.Join(store.SnapshotsDir(), rel)) {
		t.Error("image removed under an unreachable limit")
	}
	if !fileExists(filepath.Join(store.AudioDir(), audioRel)) {
		t.Error("recording removed under an unreachable limit")
	}
	st := r.BudgetStatus()
	if st.State != budgetStateUnreachable || st.KeptBytes <= st.LimitBytes {
		t.Errorf("status %+v, want unreachable with kept bytes over the limit", st)
	}
}

func TestEnforceBudgetStopsAtTheLimit(t *testing.T) {
	store := newTestStore(t)
	const mb = 1 << 20
	for i := 0; i < 5; i++ {
		sid := fmt.Sprintf("s%d", i)
		at := time.Now().Add(-time.Duration(20-i) * 24 * time.Hour)
		insertSession(t, store, sid, at, "pending")
		store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'done' WHERE id = ?`, sid)
		addSnapshot(t, store, sid, at, 100, 100, make([]byte, mb), "cold")
	}
	// Measure after setup: metadata writes grow the WAL too, independently
	// of the five 1 MB screenshots whose removal this test checks.
	usage := measureActivityStorage(store)
	r := storageTestRotator(store, usage.TotalBytes-int64(1.5*mb))
	// 5 MB over a limit of 3.5 MB: two go, not down to 90%.
	if removed, _ := r.enforceBudget(r.snapshotConfig(), usage, 0, false); removed != 2 {
		t.Errorf("removed %d, want 2 (stop once at the limit)", removed)
	}
}

func TestEnforceBudgetWaitsForTheUsersYesOnAnUpgradedProfile(t *testing.T) {
	store := newTestStore(t)
	const mb = 1 << 20
	base := measureActivityStorage(store).TotalBytes
	r := storageTestRotator(store, base+mb)
	confirmed, decided := false, false
	r.limitDecision = func() (bool, bool) { return confirmed, decided }
	r.confirmLimit = func() { confirmed, decided = true, true }
	at := time.Now().Add(-10 * 24 * time.Hour)
	insertSession(t, store, "old", at, "pending")
	store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'done' WHERE id = 'old'`)
	id, _ := addSnapshot(t, store, "old", at, 100, 100, make([]byte, 2*mb), "cold")

	if removed, _ := r.enforceBudget(r.snapshotConfig(), r.measure(), 0, false); removed != 0 {
		t.Fatalf("removed %d before the user said yes", removed)
	}
	st := r.BudgetStatus()
	if st.State != budgetStateConfirm || st.Screenshots.Count != 1 || st.Screenshots.Bytes != 2*mb {
		t.Fatalf("status %+v, want confirm listing the image", st)
	}
	if decided {
		t.Fatal("an over-limit profile was decided without the user")
	}
	r.confirmLimit()
	if removed, _ := r.enforceBudget(r.snapshotConfig(), r.measure(), 0, false); removed != 1 {
		t.Fatalf("removed %d after the yes, want 1", removed)
	}
	if tier, _, _, _, _ := snapshotRow(t, store, id); tier != snapshotTierMissing {
		t.Error("image kept after the yes")
	}
}

func TestEnforceBudgetDecidesUnderTheLimitWithoutAsking(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	decided := false
	r.limitDecision = func() (bool, bool) { return decided, decided }
	r.confirmLimit = func() { decided = true }
	r.enforceBudget(r.snapshotConfig(), r.measure(), 0, false)
	if !decided {
		t.Error("a profile within the limit should be decided on the first pass")
	}
}

func TestEnforceBudgetKeepsRecordingsWithoutAFinalTranscript(t *testing.T) {
	store := newTestStore(t)
	const mb = 1 << 20
	base := measureActivityStorage(store).TotalBytes
	r := storageTestRotator(store, base+mb)
	write := func(id string, start, saved time.Time, transcript string) string {
		insertSession(t, store, id, start, "pending")
		rel := filepath.Join(start.Format("2006-01-02"), fmt.Sprintf("%d-%s.wav", start.UnixMilli(), id))
		os.MkdirAll(filepath.Join(store.AudioDir(), filepath.Dir(rel)), 0o700)
		os.WriteFile(filepath.Join(store.AudioDir(), rel), make([]byte, 2*mb), 0o600)
		setSessionAudio(t, store, id, rel, 2*mb, transcript)
		store.db.Exec(`UPDATE activity_sessions SET created_at = ? WHERE id = ?`, saved.UnixMilli(), id)
		return rel
	}
	old := time.Now().Add(-5 * 24 * time.Hour)
	untranscribed := write("untranscribed", old, old, "")
	savedNow := write("saved-now", old.Add(time.Hour), time.Now().Add(-time.Hour), "/t/a.md")

	r.enforceBudget(r.snapshotConfig(), r.measure(), 0, false)
	if !fileExists(filepath.Join(store.AudioDir(), untranscribed)) {
		t.Error("a recording without a transcript was removed")
	}
	if !fileExists(filepath.Join(store.AudioDir(), savedNow)) {
		t.Error("a recording saved within the last day was removed")
	}
}

func TestOrphanAudioKeepsRecordingsWithASidecar(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	dir := filepath.Join(store.AudioDir(), "2026-01-02")
	os.MkdirAll(dir, 0o700)
	old := time.Now().Add(-30 * 24 * time.Hour)
	tenDays := time.Now().Add(-10 * 24 * time.Hour)
	for name, mtime := range map[string]time.Time{
		"1-kept.wav": old, "1-kept.json": old, "2-gone.wav": old, "3-young.wav": tenDays,
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("x"), 0o600)
		os.Chtimes(p, mtime, mtime)
	}
	r.sweepOrphanAudio(time.Now())
	for name, want := range map[string]bool{"1-kept.wav": true, "1-kept.json": true, "2-gone.wav": false, "3-young.wav": true} {
		if fileExists(filepath.Join(dir, name)) != want {
			t.Errorf("%s present=%v, want %v", name, !want, want)
		}
	}
}

func TestUntranscribedRecordingStaysWAVHoweverOld(t *testing.T) {
	ffmpeg := testFFmpeg(t)
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	r.ffmpegPath = func() string { return ffmpeg }
	at := time.Now().Add(-30 * 24 * time.Hour)
	insertSession(t, store, "paused", at, "pending")
	rel := filepath.Join(at.Format("2006-01-02"), fmt.Sprintf("%d-paused.wav", at.UnixMilli()))
	writeTestWAV(t, filepath.Join(store.AudioDir(), rel), 1, 1)
	setSessionAudio(t, store, "paused", rel, 32044, "")
	if n, _ := r.compressCallAudio(time.Now().Add(time.Minute), nil, nil); n != 0 {
		t.Errorf("compressed %d untranscribed recording(s)", n)
	}
}

func TestCompressKeepsWAVWhenTheHeaderUnderstatesItsLength(t *testing.T) {
	ffmpeg := testFFmpeg(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "1700000000000-x.wav")
	writeTestWAV(t, path, 2, 3)
	data, _ := os.ReadFile(path)
	// Declare half the samples: ffmpeg would encode 1.5 s of the 3 s.
	binary.LittleEndian.PutUint32(data[40:44], binary.LittleEndian.Uint32(data[40:44])/2)
	os.WriteFile(path, data, 0o600)
	if _, _, err := compressMeetingAudio(ffmpeg, path); err == nil {
		t.Fatal("a truncated encode was accepted")
	}
	if !fileExists(path) {
		t.Error("the WAV was removed")
	}
	if fileExists(filepath.Join(dir, "1700000000000-x.opus")) {
		t.Error("the short .opus was kept")
	}
}

func TestSweepLeavesFramesTheCaptureStillHolds(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	old := time.Now().Add(-3 * 24 * time.Hour)
	dir := filepath.Join(store.SnapshotsDir(), old.Format("2006-01-02"))
	os.MkdirAll(dir, 0o700)
	held := filepath.Join(old.Format("2006-01-02"), "1-held.jpg")
	stray := filepath.Join(old.Format("2006-01-02"), "2-stray.jpg")
	for _, rel := range []string{held, stray} {
		p := filepath.Join(store.SnapshotsDir(), rel)
		os.WriteFile(p, []byte("x"), 0o600)
		os.Chtimes(p, old, old)
	}

	open := true
	r.framesInUse = func() (map[string]bool, bool) { return map[string]bool{held: true}, open }
	r.sweepOrphanSnapshots(time.Now(), true)
	if !fileExists(filepath.Join(store.SnapshotsDir(), stray)) {
		t.Fatal("the sweep ran while a call was open")
	}
	open = false
	r.sweepOrphanSnapshots(time.Now(), true)
	if !fileExists(filepath.Join(store.SnapshotsDir(), held)) {
		t.Error("a frame the capture holds was removed")
	}
	if fileExists(filepath.Join(store.SnapshotsDir(), stray)) {
		t.Error("an unreferenced frame was kept")
	}
}

func TestImagesWaitingForOCRAreNotRemoved(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	cfg := r.snapshotConfig()
	cfg.ScreenshotRetention = 7 * 24 * time.Hour
	at := time.Now().Add(-20 * 24 * time.Hour)
	insertSession(t, store, "ocr", at, "pending")
	store.db.Exec(`UPDATE activity_sessions SET ocr_status = 'processing' WHERE id = 'ocr'`)
	id, _ := addSnapshot(t, store, "ocr", at, 100, 100, make([]byte, 100), "cold")
	if n, _ := r.expireScreenshots(cfg); n != 0 {
		t.Errorf("expired %d image(s) still waiting for OCR", n)
	}
	if tier, _, _, _, _ := snapshotRow(t, store, id); tier == snapshotTierMissing {
		t.Error("image waiting for OCR removed")
	}
	if n, err := store.ResetInterruptedOCR(); err != nil || n != 1 {
		t.Errorf("reset interrupted OCR: %d, %v", n, err)
	}
}

func TestCompactKeepsTheColorProfile(t *testing.T) {
	src := screenJPEG(t, 3000, 2000)
	profile := append([]byte("ICC_PROFILE\x00\x01\x01"), bytes.Repeat([]byte{7}, 300)...)
	seg := append([]byte{0xFF, 0xE2, byte((len(profile) + 2) >> 8), byte(len(profile) + 2)}, profile...)
	tagged := append(append([]byte{0xFF, 0xD8}, seg...), src[2:]...)
	out, _, _, ok, err := compactSnapshotJPEG(tagged)
	if err != nil || !ok {
		t.Fatalf("compact: %v %v", ok, err)
	}
	if len(jpegICCSegments(out)) != 1 || !bytes.Contains(out, profile) {
		t.Error("the ICC profile was dropped")
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(out)); err != nil {
		t.Errorf("result does not decode: %v", err)
	}
}

func TestPlanAndAutomaticPassAgreeWithRetiredDatabaseBytes(t *testing.T) {
	store := newTestStore(t)
	const mb = 1 << 20
	if !store.tableExists("agency_origination_runs_v1") {
		t.Skip("agency runs table not created by migrations")
	}
	// A retired Agency payload of 4 MB that clearing alone brings under the
	// limit: neither the plan nor the automatic pass removes an image.
	now := time.Now().UnixMilli()
	for i := 0; i < 4; i++ {
		store.db.Exec(`INSERT INTO agency_origination_runs_v1 (id, idempotency_key, status, wake_reason, wake_at,
			source_input_json, source_manifest_json, planning_input_json, prompt, raw_output, output_json,
			rejected_evidence_refs_json, rejected_candidate_keys_json, resource_policy_json, resource_usage_json,
			payload_json, payload_hash, created_at)
			VALUES (?, ?, 'planner_error', 'periodic', ?, '{}', '{}', '{}', ?, '', '{}', '[]', '[]', '{}', '{}', '{}', 'h', ?)`,
			fmt.Sprintf("run-%d", i), fmt.Sprintf("key-%d", i), now, string(bytes.Repeat([]byte("p"), mb)), now)
	}
	base := measureActivityStorage(store).TotalBytes
	r := storageTestRotator(store, base+mb)
	var ids []int64
	for i := 0; i < 4; i++ {
		sid := fmt.Sprintf("s%d", i)
		at := time.Now().Add(-time.Duration(20-i) * 24 * time.Hour)
		insertSession(t, store, sid, at, "pending")
		ocrDone(t, store, sid)
		id, _ := addSnapshot(t, store, sid, at, 100, 100, make([]byte, mb), "cold")
		ids = append(ids, id)
	}
	plan, err := r.PlanCleanup()
	if err != nil {
		t.Fatal(err)
	}
	if plan.DatabaseSpace.Bytes < 4*mb || plan.OverLimitScreenshots.Count != 0 {
		t.Errorf("plan: database %d bytes, over the limit %d images; want >= 4 MB and none", plan.DatabaseSpace.Bytes, plan.OverLimitScreenshots.Count)
	}
	r.rotate()
	for _, id := range ids {
		if tier, _, _, _, _ := snapshotRow(t, store, id); tier == snapshotTierMissing {
			t.Errorf("the automatic pass removed image %d although clearing the Agency runs fits", id)
		}
	}
	if left, _ := store.agencyRunsToSlim(); len(left) != 0 {
		t.Errorf("Agency runs not cleared before the limit: %d left", len(left))
	}
	if st := r.BudgetStatus(); st.State != budgetStateOK || st.Screenshots.Count != 0 {
		t.Errorf("budget after the pass: %+v", st)
	}
}

func TestUsageCountsOnlyLiveDatabasePages(t *testing.T) {
	store := newTestStore(t)
	before := measureActivityStorage(store)
	store.db.Exec(`CREATE TABLE junk (b BLOB)`)
	for i := 0; i < 8; i++ {
		store.db.Exec(`INSERT INTO junk VALUES (randomblob(1048576))`)
	}
	store.db.Exec(`DROP TABLE junk`)
	store.CheckpointTruncate()
	u := measureActivityStorage(store)
	if u.DatabaseFreeBytes < 7<<20 {
		t.Fatalf("free pages not reported: %d", u.DatabaseFreeBytes)
	}
	if u.DatabaseBytes > before.DatabaseBytes+1<<20 {
		t.Errorf("free pages counted as used: %d before, %d after", before.DatabaseBytes, u.DatabaseBytes)
	}
}

func TestCompactionWaitsForTriage(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	at := time.Now().Add(-10 * 24 * time.Hour)
	insertSession(t, store, "triage", at, "pending")
	ocrDone(t, store, "triage")
	// Saved two days ago and still waiting for triage.
	store.db.Exec(`UPDATE activity_sessions SET created_at = ? WHERE id = 'triage'`, time.Now().Add(-2*24*time.Hour).UnixMilli())
	big := screenJPEG(t, 3000, 2000)
	id, _ := addSnapshot(t, store, "triage", at, 3000, 2000, big, "cold")
	if n, _ := r.compactColdSnapshots(r.snapshotConfig(), time.Now().Add(time.Minute), 0, nil, nil); n != 0 {
		t.Errorf("compacted %d image(s) of a session waiting for triage", n)
	}
	if _, size, _, _, _ := snapshotRow(t, store, id); size != int64(len(big)) {
		t.Error("image shrunk before triage read it")
	}
}

func TestForgetMeetingAudioFilesFailsClosed(t *testing.T) {
	store := newTestStore(t)
	day := filepath.Join(store.AudioDir(), "2026-10-15")
	os.MkdirAll(day, 0o700)
	base := filepath.Join(day, "1700000000002-x")
	meta, _ := json.Marshal(meetingAudioMeta{MeetingID: "meeting-c"})
	os.WriteFile(base+".json", meta, 0o600)
	os.WriteFile(base+".wav", []byte("x"), 0o600)
	store.db.Close() // rows unreadable
	a := &App{activity: &activityMonitorState{store: store}}
	a.forgetMeetingAudioFiles(store.AudioDir(), meetingKnowledgeEntryID(meetingNotesKey(ActivitySessionRow{}, "meeting-c")))
	if !fileExists(base+".wav") || !fileExists(base+".json") {
		t.Error("recordings removed although the session rows could not be read")
	}
}

func TestLoweringTheLimitIsTheUsersYes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &App{}
	if _, decided := savedStorageLimitDecision(); decided {
		t.Fatal("fresh settings already decided")
	}
	if err := a.SetActivityMaxStorageGB(20); err != nil {
		t.Fatal(err)
	}
	if _, decided := savedStorageLimitDecision(); decided {
		t.Error("raising the limit counted as a yes")
	}
	if err := a.SetScreenshotRetentionDays(14); err != nil {
		t.Fatal(err)
	}
	if err := a.SetActivityMaxStorageGB(5); err != nil {
		t.Fatal(err)
	}
	if confirmed, _ := savedStorageLimitDecision(); !confirmed {
		t.Error("lowering the limit did not count as a yes")
	}
	if err := a.SetActivityRetentionDays(90); err != nil {
		t.Fatal(err)
	}
	if confirmed, _ := savedStorageLimitDecision(); !confirmed || a.GetScreenshotRetentionDays() != 14 {
		t.Error("other settings lost the yes or the screenshot limit")
	}
}

// ── call-recording integration ──

func TestCompressedRecordingKeepsItsSidecar(t *testing.T) {
	ffmpeg := testFFmpeg(t)
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	r.ffmpegPath = func() string { return ffmpeg }
	at := time.Now().Add(-2 * time.Hour)
	insertSession(t, store, "call", at, "pending")
	rel := filepath.Join(at.Format("2006-01-02"), fmt.Sprintf("%d-call.wav", at.UnixMilli()))
	writeTestWAV(t, filepath.Join(store.AudioDir(), rel), 1, 2)
	keepMeetingWAVMeta(store.AudioDir(), rel, "", meetingAudioMeta{MeetingID: "m-1", App: "Zoom"})
	setSessionAudio(t, store, "call", rel, 64044, "/t/call.md")

	if n, _ := r.compressCallAudio(time.Now().Add(time.Minute), nil, nil); n != 1 {
		t.Fatalf("compressed %d", n)
	}
	s, _ := store.GetSessionByID("call")
	if meta := readMeetingWAVMeta(store.AudioDir(), s.AudioPath); meta.MeetingID != "m-1" {
		t.Errorf("sidecar not found for %s: %+v", s.AudioPath, meta)
	}
	// The storage limit takes the recording with its sidecar.
	if !r.removeRecording(audioMedia{SessionID: "call", RelPath: s.AudioPath}) {
		t.Fatal("removeRecording")
	}
	if fileExists(meetingWAVMetaPath(store.AudioDir(), s.AudioPath)) || fileExists(filepath.Join(store.AudioDir(), s.AudioPath)) {
		t.Error("recording or sidecar left behind")
	}
}

func TestForgetMeetingAudioFilesRemovesCompressedRecordings(t *testing.T) {
	dir := t.TempDir()
	day := filepath.Join(dir, "2026-10-15")
	os.MkdirAll(day, 0o700)
	base := filepath.Join(day, "1700000000000-abc")
	meta, _ := json.Marshal(meetingAudioMeta{MeetingID: "meeting-7"})
	os.WriteFile(base+".json", meta, 0o600)
	os.WriteFile(base+".opus", []byte("x"), 0o600)
	os.WriteFile(base+".opus.tmp", []byte("x"), 0o600)
	a := &App{}
	a.forgetMeetingAudioFiles(dir, meetingKnowledgeEntryID(meetingNotesKey(ActivitySessionRow{}, "meeting-7")))
	for _, p := range []string{base + ".json", base + ".opus", base + ".opus.tmp"} {
		if fileExists(p) {
			t.Errorf("%s left behind", filepath.Base(p))
		}
	}
}

func TestStorageRecordingFinal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	a := &App{activity: &activityMonitorState{store: store}}
	rel := filepath.Join("2026-10-15", "1700000000000-x.wav")
	os.MkdirAll(filepath.Join(store.AudioDir(), "2026-10-15"), 0o700)
	recent := audioMedia{RelPath: rel, Transcribed: true, SavedAt: time.Now().Add(-time.Hour).UnixMilli()}

	if a.storageRecordingFinal(audioMedia{RelPath: rel, SavedAt: 1}) {
		t.Error("a recording without a transcript is final")
	}
	keepMeetingWAVMeta(store.AudioDir(), rel, "", meetingAudioMeta{MeetingID: "m"})
	recordMeetingTranscribedBy(store.AudioDir(), rel, meetingTranscribedByAfterCall)
	if !a.storageRecordingFinal(recent) {
		t.Error("an after-call transcript is not final")
	}
	if readMeetingWAVMeta(store.AudioDir(), rel).MeetingID != "m" {
		t.Error("recording the engine lost the sidecar's meeting")
	}
	recordMeetingTranscribedBy(store.AudioDir(), rel, meetingTranscribedByLive)
	if a.afterCallModelView().Supported {
		if a.storageRecordingFinal(recent) {
			t.Error("a recent live-engine transcript is final while the after-call model is on")
		}
		old := recent
		old.SavedAt = time.Now().Add(-8 * 24 * time.Hour).UnixMilli()
		if !a.storageRecordingFinal(old) {
			t.Error("a live-engine transcript is not final after 7 days")
		}
		if err := a.SetAfterCallSpeechModelEnabled(false); err != nil {
			t.Fatal(err)
		}
	}
	if !a.storageRecordingFinal(recent) {
		t.Error("with the after-call model off, the live transcript is not final")
	}
}

// ── verify2 ──

func TestForgetMeetingAudioFilesKeepsRecordingsARowLinks(t *testing.T) {
	store := newTestStore(t)
	day := filepath.Join(store.AudioDir(), "2026-10-15")
	os.MkdirAll(day, 0o700)
	linked := filepath.Join(day, "1700000000000-linked")
	loose := filepath.Join(day, "1700000000001-loose")
	meta, _ := json.Marshal(meetingAudioMeta{MeetingID: "meeting-9"})
	for _, base := range []string{linked, loose} {
		os.WriteFile(base+".json", meta, 0o600)
		os.WriteFile(base+".wav", []byte("x"), 0o600)
	}
	insertSession(t, store, "other-slice", time.Now().Add(-time.Hour), "pending")
	setSessionAudio(t, store, "other-slice", filepath.Join("2026-10-15", "1700000000000-linked.wav"), 1, "/t/x.md")
	a := &App{activity: &activityMonitorState{store: store}}
	a.forgetMeetingAudioFiles(store.AudioDir(), meetingKnowledgeEntryID(meetingNotesKey(ActivitySessionRow{}, "meeting-9")))
	if !fileExists(linked+".wav") || !fileExists(linked+".json") {
		t.Error("a recording a session row links was removed")
	}
	if fileExists(loose+".wav") || fileExists(loose+".json") {
		t.Error("a recording no row links was kept")
	}
}

func TestImagesWaitingForTriageAreNotRemovedForAWeek(t *testing.T) {
	store := newTestStore(t)
	r := storageTestRotator(store, 1<<40)
	cfg := r.snapshotConfig()
	cfg.ScreenshotRetention = 2 * 24 * time.Hour
	waiting := time.Now().Add(-4 * 24 * time.Hour)
	stuck := time.Now().Add(-10 * 24 * time.Hour)
	insertSession(t, store, "waiting", waiting, "pending")
	insertSession(t, store, "stuck", stuck, "pending")
	ocrDone(t, store, "waiting", "stuck")
	waitingID, _ := addSnapshot(t, store, "waiting", waiting, 100, 100, make([]byte, 100), "cold")
	stuckID, _ := addSnapshot(t, store, "stuck", stuck, 100, 100, make([]byte, 100), "cold")
	r.expireScreenshots(cfg)
	if tier, _, _, _, _ := snapshotRow(t, store, waitingID); tier == snapshotTierMissing {
		t.Error("an image triage has not read yet was removed")
	}
	if tier, _, _, _, _ := snapshotRow(t, store, stuckID); tier != snapshotTierMissing {
		t.Error("a session stuck in triage for over a week pins its images")
	}
}

func TestCallEndingWhilePausedIsKept(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newLifecycleStore(t)
	app := mainChatTestApp(t)
	app.audioScreenLocked = func() bool { return false }
	app.activity = &activityMonitorState{store: store, buf: activitybuf.NewRingBuffer(16, time.Minute)}
	writer, err := newMeetingAudioWriter(store.AudioDir())
	if err != nil {
		t.Fatal(err)
	}
	writer.SetMeeting("Zoom", "us.zoom.xos", "m-E")
	writer.WriteCapturedChunk(timedChunk(backgroundListeningSourceMicrophone, time.Now(), constSamples(2*meetingAudioSampleRate, 10)))
	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	epoch, _ := app.automaticMeetingRecordingEpoch()
	app.persistEndedCallRecording(writer, epoch, "meeting_ended")

	sessions := meetingSessions(t, store)
	if len(sessions) != 1 || sessions[0].AudioPath == "" || sessions[0].TriggerKind != pausedCallTrigger {
		t.Fatalf("a call ending while paused keeps its recording: %+v", sessions)
	}
	if _, err := os.Stat(filepath.Join(store.AudioDir(), sessions[0].AudioPath)); err != nil {
		t.Fatalf("recording missing: %v", err)
	}
	app.callsKeptAtPauseMu.Lock()
	waiting := len(app.callsKeptAtPause)
	app.callsKeptAtPauseMu.Unlock()
	if waiting != 1 {
		t.Fatalf("its transcript waits for observation to resume: %d", waiting)
	}
}
