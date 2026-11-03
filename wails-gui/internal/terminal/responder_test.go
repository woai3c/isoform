package terminal

import (
	"os"
	"strings"
	"testing"
)

// testPipe creates a pipe and returns (read-side, write-side).
// The Responder writes to the write-side, and we read responses from the read-side.
func testPipe(t *testing.T) (r *os.File, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return r, w
}

func readAll(r *os.File) string {
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

func TestResponder_DA1(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	out := resp.Filter([]byte("\x1b[c"))

	got := readAll(r)
	want := "\x1b[?62;22c"
	if got != want {
		t.Errorf("DA1 response = %q, want %q", got, want)
	}
	if resp.Responses() != 1 {
		t.Errorf("Responses() = %d, want 1", resp.Responses())
	}
	// Query should be stripped from output
	if len(out) != 0 {
		t.Errorf("DA1 query not stripped: out = %q", out)
	}
}

func TestResponder_DA1WithParam(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	out := resp.Filter([]byte("\x1b[0c"))

	got := readAll(r)
	want := "\x1b[?62;22c"
	if got != want {
		t.Errorf("DA1 response = %q, want %q", got, want)
	}
	if len(out) != 0 {
		t.Errorf("DA1 query not stripped: out = %q", out)
	}
}

func TestResponder_DA2(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	out := resp.Filter([]byte("\x1b[>c"))

	got := readAll(r)
	want := "\x1b[>0;0;0c"
	if got != want {
		t.Errorf("DA2 response = %q, want %q", got, want)
	}
	if len(out) != 0 {
		t.Errorf("DA2 query not stripped: out = %q", out)
	}
}

func TestResponder_DA2WithParam(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	out := resp.Filter([]byte("\x1b[>0c"))

	got := readAll(r)
	want := "\x1b[>0;0;0c"
	if got != want {
		t.Errorf("DA2 response = %q, want %q", got, want)
	}
	if len(out) != 0 {
		t.Errorf("DA2 query not stripped: out = %q", out)
	}
}

func TestResponder_OSC11_BEL(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#c0caf5", "#0f0f14")

	out := resp.Filter([]byte("\x1b]11;?\x07"))

	got := readAll(r)
	want := "\x1b]11;rgb:0f0f/0f0f/1414\x07"
	if got != want {
		t.Errorf("OSC 11 response = %q, want %q", got, want)
	}
	if len(out) != 0 {
		t.Errorf("OSC 11 query not stripped: out = %q", out)
	}
}

func TestResponder_OSC10_BEL(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#c0caf5", "#0f0f14")

	out := resp.Filter([]byte("\x1b]10;?\x07"))

	got := readAll(r)
	want := "\x1b]10;rgb:c0c0/caca/f5f5\x07"
	if got != want {
		t.Errorf("OSC 10 response = %q, want %q", got, want)
	}
	if len(out) != 0 {
		t.Errorf("OSC 10 query not stripped: out = %q", out)
	}
}

func TestResponder_OSC11_ST(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#f6f0e8")

	out := resp.Filter([]byte("\x1b]11;?\x1b\\"))

	got := readAll(r)
	want := "\x1b]11;rgb:f6f6/f0f0/e8e8\x07"
	if got != want {
		t.Errorf("OSC 11 (ST) response = %q, want %q", got, want)
	}
	if len(out) != 0 {
		t.Errorf("OSC 11 (ST) query not stripped: out = %q", out)
	}
}

func TestResponder_MixedData_StripsQuery(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	// Normal output mixed with DA1 query
	out := resp.Filter([]byte("Hello world\x1b[cMore text"))

	got := readAll(r)
	want := "\x1b[?62;22c"
	if got != want {
		t.Errorf("mixed data response = %q, want %q", got, want)
	}
	// Query should be stripped, surrounding text preserved
	if string(out) != "Hello worldMore text" {
		t.Errorf("mixed data filter = %q, want %q", string(out), "Hello worldMore text")
	}
}

func TestResponder_SplitAcrossChunks(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	// DA1 split across two Filter calls
	out1 := resp.Filter([]byte("\x1b"))
	out2 := resp.Filter([]byte("[c"))

	got := readAll(r)
	want := "\x1b[?62;22c"
	if got != want {
		t.Errorf("split chunk response = %q, want %q", got, want)
	}
	// The ESC byte in first chunk can't be stripped yet (we don't know if
	// it's a query), but once [c arrives the query is complete and stripped.
	_ = out1
	if len(out2) != 0 {
		t.Errorf("second chunk not stripped: out2 = %q", out2)
	}
}

func TestResponder_IgnoresNonQueryCSI(t *testing.T) {
	_, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	// CSI H (cursor home) — should NOT trigger a response and NOT be stripped
	out := resp.Filter([]byte("\x1b[H"))

	if resp.Responses() != 0 {
		t.Errorf("non-query CSI triggered %d responses, want 0", resp.Responses())
	}
	if string(out) != "\x1b[H" {
		t.Errorf("non-query CSI should pass through: out = %q", out)
	}
}

func TestResponder_IgnoresDA1WithNonZeroParam(t *testing.T) {
	_, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	// \x1b[1c — not a standard DA1 query, should pass through
	out := resp.Filter([]byte("\x1b[1c"))

	if resp.Responses() != 0 {
		t.Errorf("DA1 with param 1 triggered %d responses, want 0", resp.Responses())
	}
	if string(out) != "\x1b[1c" {
		t.Errorf("non-DA1 CSI should pass through: out = %q", out)
	}
}

func TestHexToXColorSpec(t *testing.T) {
	tests := []struct {
		hex  string
		want string
	}{
		{"#0f0f14", "rgb:0f0f/0f0f/1414"},
		{"#c0caf5", "rgb:c0c0/caca/f5f5"},
		{"#f6f0e8", "rgb:f6f6/f0f0/e8e8"},
		{"0f0f14", "rgb:0f0f/0f0f/1414"},
		{"#fff", ""}, // too short
		{"", ""},     // empty
	}
	for _, tt := range tests {
		got := hexToXColorSpec(tt.hex)
		if got != tt.want {
			t.Errorf("hexToXColorSpec(%q) = %q, want %q", tt.hex, got, tt.want)
		}
	}
}

func TestIsDarkHex(t *testing.T) {
	tests := []struct {
		hex  string
		dark bool
	}{
		{"#000000", true},
		{"#0f0f14", true},
		{"#ffffff", false},
		{"#f6f0e8", false},
		{"#808080", true}, // mid-gray luminance = 128, boundary → dark
	}
	for _, tt := range tests {
		got := isDarkHex(tt.hex)
		if got != tt.dark {
			t.Errorf("isDarkHex(%q) = %v, want %v", tt.hex, got, tt.dark)
		}
	}
}

func TestHexByte(t *testing.T) {
	tests := []struct {
		s    string
		want uint8
	}{
		{"00", 0},
		{"ff", 255},
		{"FF", 255},
		{"0f", 15},
		{"a0", 160},
	}
	for _, tt := range tests {
		got := hexByte(tt.s)
		if got != tt.want {
			t.Errorf("hexByte(%q) = %d, want %d", tt.s, got, tt.want)
		}
	}
}

func TestResponder_MultipleQueries(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#c0caf5", "#0f0f14")

	// nvim typically sends DA1 + OSC 11 in sequence
	out := resp.Filter([]byte("\x1b[c\x1b]11;?\x07"))

	got := readAll(r)
	if !strings.Contains(got, "\x1b[?62;22c") {
		t.Errorf("missing DA1 response in %q", got)
	}
	if !strings.Contains(got, "\x1b]11;rgb:0f0f/0f0f/1414\x07") {
		t.Errorf("missing OSC 11 response in %q", got)
	}
	if resp.Responses() != 2 {
		t.Errorf("Responses() = %d, want 2", resp.Responses())
	}
	// Both queries should be stripped
	if len(out) != 0 {
		t.Errorf("queries not stripped: out = %q", out)
	}
}

func TestResponder_DECRQM(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	out := resp.Filter([]byte("\x1b[?2026$p"))

	got := readAll(r)
	want := "\x1b[?2026;0$y"
	if got != want {
		t.Errorf("DECRQM response = %q, want %q", got, want)
	}
	if len(out) != 0 {
		t.Errorf("DECRQM query not stripped: out = %q", out)
	}
}

func TestResponder_KittyKeyboard(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	out := resp.Filter([]byte("\x1b[?u"))

	got := readAll(r)
	want := "\x1b[?0u"
	if got != want {
		t.Errorf("Kitty keyboard response = %q, want %q", got, want)
	}
	if len(out) != 0 {
		t.Errorf("Kitty keyboard query not stripped: out = %q", out)
	}
}

func TestResponder_DCS_Stripped(t *testing.T) {
	_, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	// DCS $q ... ST — should be stripped entirely
	out := resp.Filter([]byte("before\x1bP$q m\x1b\\after"))

	if string(out) != "beforeafter" {
		t.Errorf("DCS not stripped: out = %q, want %q", string(out), "beforeafter")
	}
}

func TestResponder_NvimStartupSequence(t *testing.T) {
	r, w := testPipe(t)
	resp := NewResponder(w, "#c0caf5", "#0f0f14")

	// Simulate a realistic nvim startup burst:
	// alternate screen + clear + DECRQM queries + DCS + kitty + DA1 + cursor show
	input := []byte(
		"\x1b[?1049h" + // alternate screen (pass through)
			"\x1b[H\x1b[2J" + // cursor home + clear (pass through)
			"\x1b[?2026$p" + // DECRQM 2026 (strip)
			"\x1b[?2027$p" + // DECRQM 2027 (strip)
			"\x1bP$q m\x1b\\" + // DCS DECRQSS (strip)
			"\x1b[?u" + // kitty keyboard query (strip)
			"\x1b[c" + // DA1 (strip)
			"\x1b]11;?\x07" + // OSC 11 (strip)
			"\x1b[?25h", // cursor show (pass through)
	)

	out := resp.Filter(input)

	// Verify responses were generated
	got := readAll(r)
	if !strings.Contains(got, "\x1b[?62;22c") {
		t.Errorf("missing DA1 response")
	}
	if !strings.Contains(got, "\x1b[?2026;0$y") {
		t.Errorf("missing DECRQM 2026 response")
	}
	if !strings.Contains(got, "\x1b[?0u") {
		t.Errorf("missing kitty keyboard response")
	}
	if !strings.Contains(got, "\x1b]11;") {
		t.Errorf("missing OSC 11 response")
	}

	// Verify only non-query sequences passed through
	outStr := string(out)
	if !strings.Contains(outStr, "\x1b[?1049h") {
		t.Errorf("alternate screen should pass through")
	}
	if !strings.Contains(outStr, "\x1b[H") {
		t.Errorf("cursor home should pass through")
	}
	if !strings.Contains(outStr, "\x1b[?25h") {
		t.Errorf("cursor show should pass through")
	}
	// Queries should NOT be in output
	if strings.Contains(outStr, "\x1b[c") {
		t.Errorf("DA1 query should be stripped from output")
	}
	if strings.Contains(outStr, "\x1b]11;?") {
		t.Errorf("OSC 11 query should be stripped from output")
	}
	if strings.Contains(outStr, "\x1b[?u") {
		t.Errorf("Kitty keyboard query should be stripped from output")
	}
}

func TestResponder_NonQueryOSC_PassesThrough(t *testing.T) {
	_, w := testPipe(t)
	resp := NewResponder(w, "#ffffff", "#000000")

	// OSC 2 (set title) — should NOT be stripped
	out := resp.Filter([]byte("\x1b]2;nvim\x07"))

	if string(out) != "\x1b]2;nvim\x07" {
		t.Errorf("OSC 2 should pass through: out = %q", string(out))
	}
	if resp.Responses() != 0 {
		t.Errorf("non-query OSC triggered %d responses, want 0", resp.Responses())
	}
}
