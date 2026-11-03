package terminal

import (
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Responder intercepts terminal queries (DA1, DA2, OSC 10/11, DECRQM, kitty
// keyboard, DSR, XTVERSION, DCS) in the PTY output stream, writes standard
// responses directly back to the PTY master, and **strips the query sequences
// from the data** so that xterm.js never sees them and never generates
// duplicate (delayed) responses that would corrupt the application's input.
type Responder struct {
	ptm       io.Writer
	fgColor   string // hex "#rrggbb"
	bgColor   string // hex "#rrggbb"
	state     responderState
	oscBuf    []byte
	csiBuf    []byte
	responses int
	debugLog  *os.File

	// seqStart marks the byte offset (within the current Filter call) where
	// the current escape sequence began. -1 means we are not inside a
	// sequence that might be stripped.
	seqStart int

	// dcsDepth tracks DCS nesting so we can skip until ST.
	dcsDepth int
}

type responderState int

const (
	stNormal responderState = iota
	stESC                   // saw \x1b
	stCSI                   // saw \x1b[
	stCSIGt                 // saw \x1b[>
	stOSC                   // saw \x1b]  (collecting OSC number)
	stOSCStr                // inside OSC string content
	stDCS                   // inside DCS string until ST
)

const maxOSCLen = 64

// NewResponder creates a responder that writes query responses to ptm.
// fgColor/bgColor are hex colors (e.g. "#c0caf5", "#0f0f14").
func NewResponder(ptm io.Writer, fgColor, bgColor string) *Responder {
	r := &Responder{
		ptm:      ptm,
		fgColor:  fgColor,
		bgColor:  bgColor,
		oscBuf:   make([]byte, 0, maxOSCLen),
		csiBuf:   make([]byte, 0, 16),
		seqStart: -1,
	}
	if f, err := os.OpenFile(filepath.Join(os.TempDir(), "yansu-pty-debug.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		r.debugLog = f
		r.logDebug("=== Responder created fg=%s bg=%s ===", fgColor, bgColor)
	} else {
		log.Printf("responder: failed to open debug log: %v", err)
	}
	return r
}

func (r *Responder) logDebug(format string, args ...interface{}) {
	if r.debugLog == nil {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(r.debugLog, "[%s] %s\n", ts, msg)
}

// Filter processes a chunk of PTY output. It intercepts terminal queries,
// writes responses directly to the PTY, and returns a (potentially smaller)
// byte slice with the query sequences removed. The returned slice is only
// valid until the next call to Filter.
//
// Callers should forward the returned bytes (not the original buf) to the
// WebSocket so that xterm.js never sees the queries and never generates
// duplicate responses.
func (r *Responder) Filter(buf []byte) []byte {
	r.logEscapeSequences(buf)

	// out accumulates the bytes we want to forward to xterm.js.
	// We pre-allocate with the same capacity as buf (common case: nothing stripped).
	out := make([]byte, 0, len(buf))

	// copyFrom tracks the start of the next segment to copy verbatim.
	copyFrom := 0

	for i, b := range buf {
		switch r.state {
		case stNormal:
			if b == 0x1b {
				r.state = stESC
				r.seqStart = i // remember where this sequence began
			}

		case stESC:
			switch b {
			case '[':
				r.state = stCSI
				r.csiBuf = r.csiBuf[:0]
			case ']':
				r.state = stOSC
				r.oscBuf = r.oscBuf[:0]
			case 'P':
				r.logDebug("DCS sequence started (\\eP)")
				r.state = stDCS
			default:
				r.state = stNormal
				r.seqStart = -1
			}

		case stCSI:
			strip := false
			done := true

			switch {
			case b == '>':
				r.state = stCSIGt
				r.csiBuf = r.csiBuf[:0]
				done = false // stay in sequence
			case b == '=':
				r.state = stNormal
			case b == '?':
				r.csiBuf = append(r.csiBuf, b)
				done = false
			case b == 'c':
				param := string(r.csiBuf)
				if param == "" || param == "0" {
					r.logDebug("DA1 query → responding & stripping")
					r.respondDA1()
					strip = true
				}
			case b == 'n':
				param := string(r.csiBuf)
				if param == "6" {
					r.logDebug("CPR query (CSI 6n) → responding & stripping")
					r.writeResponse("\x1b[1;1R")
					strip = true
				}
			case b == '$':
				if len(r.csiBuf) < 16 {
					r.csiBuf = append(r.csiBuf, b)
				}
				done = false
			case b == 'p':
				param := string(r.csiBuf)
				if strings.Contains(param, "$") {
					r.logDebug("DECRQM query: CSI %sp → responding & stripping", param)
					r.handleDECRQM(param)
					strip = true
				}
			case b == 'u':
				param := string(r.csiBuf)
				if param == "?" {
					r.logDebug("Kitty keyboard query → responding & stripping")
					r.writeResponse("\x1b[?0u")
					strip = true
				}
			case b >= 0x30 && b <= 0x3f:
				if len(r.csiBuf) < 16 {
					r.csiBuf = append(r.csiBuf, b)
				} else {
					// overflow — give up
				}
				done = false
			default:
				// Final byte of a non-query CSI sequence — pass through.
			}

			if done {
				if strip && r.seqStart >= 0 {
					// Copy everything before the stripped sequence.
					out = append(out, buf[copyFrom:r.seqStart]...)
					copyFrom = i + 1
				}
				r.state = stNormal
				r.seqStart = -1
			}

		case stCSIGt:
			strip := false
			done := true

			switch {
			case b == 'c':
				param := string(r.csiBuf)
				if param == "" || param == "0" {
					r.logDebug("DA2 query → responding & stripping")
					r.respondDA2()
					strip = true
				}
			case b == 'q':
				r.logDebug("XTVERSION query → responding & stripping")
				r.writeResponse("\x1bP>|\x1b\\")
				strip = true
			case b >= 0x30 && b <= 0x3f:
				if len(r.csiBuf) < 16 {
					r.csiBuf = append(r.csiBuf, b)
				}
				done = false
			default:
				// Unknown CSI > sequence — pass through.
			}

			if done {
				if strip && r.seqStart >= 0 {
					out = append(out, buf[copyFrom:r.seqStart]...)
					copyFrom = i + 1
				}
				r.state = stNormal
				r.seqStart = -1
			}

		case stOSC:
			if b >= '0' && b <= '9' {
				r.oscBuf = append(r.oscBuf, b)
				if len(r.oscBuf) > 4 {
					r.state = stNormal
					r.seqStart = -1
				}
			} else if b == ';' {
				r.state = stOSCStr
			} else {
				r.state = stNormal
				r.seqStart = -1
			}

		case stOSCStr:
			terminated := false
			switch b {
			case 0x07: // BEL terminates OSC
				terminated = true
			case 0x1b:
				// ST (\x1b\\) — treat ESC as end of OSC.
				// The backslash will be consumed next iteration as stNormal.
				terminated = true
			default:
				if len(r.oscBuf) < maxOSCLen {
					r.oscBuf = append(r.oscBuf, b)
				} else {
					r.state = stNormal
					r.seqStart = -1
				}
			}

			if terminated {
				strip := r.handleOSC()
				if strip && r.seqStart >= 0 {
					out = append(out, buf[copyFrom:r.seqStart]...)
					// If terminated by BEL, skip just past it.
					// If terminated by ESC (ST), also skip the next '\' if present.
					end := i + 1
					if b == 0x1b && end < len(buf) && buf[end] == '\\' {
						end++
					}
					copyFrom = end
				}
				r.state = stNormal
				r.seqStart = -1
			}

		case stDCS:
			// Skip everything inside DCS until ST (\x1b\\).
			if b == 0x1b {
				// Peek for backslash — handled on next byte.
				r.dcsDepth = 1 // flag: saw ESC inside DCS
			} else if r.dcsDepth == 1 && b == '\\' {
				// ST found — end of DCS.
				r.dcsDepth = 0
				if r.seqStart >= 0 {
					out = append(out, buf[copyFrom:r.seqStart]...)
					copyFrom = i + 1
					r.logDebug("DCS sequence stripped")
				}
				r.state = stNormal
				r.seqStart = -1
			} else {
				r.dcsDepth = 0
			}
		}
	}

	// Copy remaining data after the last stripped sequence.
	out = append(out, buf[copyFrom:]...)
	return out
}

// logEscapeSequences logs a hex summary of any escape sequences found in buf.
func (r *Responder) logEscapeSequences(buf []byte) {
	if r.debugLog == nil {
		return
	}
	for i := 0; i < len(buf); i++ {
		if buf[i] == 0x1b {
			end := i + 1
			for end < len(buf) && end < i+64 {
				b := buf[end]
				end++
				if b >= 0x40 && b <= 0x7e && end > i+2 {
					break
				}
				if b == 0x07 {
					break
				}
			}
			seq := buf[i:end]
			r.logDebug("ESC_SEQ raw: %s | hex: %s", formatEscSeq(seq), hex.EncodeToString(seq))
		}
	}
}

// Close releases resources held by the Responder. Safe to call multiple times.
func (r *Responder) Close() {
	if r.debugLog != nil {
		r.debugLog.Close()
		r.debugLog = nil
	}
}

// Responses returns the number of queries this responder has answered.
func (r *Responder) Responses() int {
	return r.responses
}

func (r *Responder) respondDA1() {
	r.writeResponse("\x1b[?62;22c")
}

func (r *Responder) respondDA2() {
	r.writeResponse("\x1b[>0;0;0c")
}

func (r *Responder) handleDECRQM(param string) {
	p := strings.TrimPrefix(param, "?")
	p = strings.TrimSuffix(p, "$")
	r.logDebug("DECRQM responding: mode=%s not-recognized", p)
	r.writeResponse(fmt.Sprintf("\x1b[?%s;0$y", p))
}

// handleOSC processes the completed OSC sequence. Returns true if the OSC was
// a query that should be stripped from the forwarded data.
func (r *Responder) handleOSC() bool {
	content := string(r.oscBuf)

	switch {
	case content == "10" || content == "10?" ||
		(strings.HasSuffix(content, "?") && strings.HasPrefix(content, "10")):
		r.logDebug("OSC 10 fg query → responding & stripping")
		r.respondOSCColor(10, r.fgColor)
		return true

	case content == "11" || content == "11?" ||
		(strings.HasSuffix(content, "?") && strings.HasPrefix(content, "11")):
		r.logDebug("OSC 11 bg query → responding & stripping")
		r.respondOSCColor(11, r.bgColor)
		return true
	}
	return false
}

func (r *Responder) respondOSCColor(code int, hexColor string) {
	rgb := hexToXColorSpec(hexColor)
	if rgb == "" {
		return
	}
	r.writeResponse(fmt.Sprintf("\x1b]%d;%s\x07", code, rgb))
}

func (r *Responder) writeResponse(data string) {
	_, _ = r.ptm.Write([]byte(data))
	r.responses++
}

// hexToXColorSpec converts "#rrggbb" to "rgb:rrrr/gggg/bbbb" (X11 color spec).
func hexToXColorSpec(hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return ""
	}
	r := hex[0:2]
	g := hex[2:4]
	b := hex[4:6]
	return fmt.Sprintf("rgb:%s%s/%s%s/%s%s", r, r, g, g, b, b)
}

// isDarkHex returns true if the hex color (e.g. "#0f0f14") is perceptually dark.
func isDarkHex(hex string) bool {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return true
	}
	r := hexByte(hex[0:2])
	g := hexByte(hex[2:4])
	b := hexByte(hex[4:6])
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	return lum < 128
}

func hexByte(s string) uint8 {
	var v uint8
	for _, c := range []byte(s) {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= c - '0'
		case c >= 'a' && c <= 'f':
			v |= c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v |= c - 'A' + 10
		}
	}
	return v
}

// LogInput logs keyboard/WebSocket input going to the PTY (for debugging).
func (r *Responder) LogInput(data []byte) {
	if r.debugLog == nil || len(data) == 0 {
		return
	}
	hasEsc := false
	for _, b := range data {
		if b == 0x1b || b < 0x20 {
			hasEsc = true
			break
		}
	}
	if hasEsc || len(data) > 10 {
		r.logDebug("WS→PTY input (%d bytes): hex=%s", len(data), hex.EncodeToString(data))
	}
}

// formatEscSeq formats an escape sequence for human-readable logging.
func formatEscSeq(seq []byte) string {
	var b strings.Builder
	for _, c := range seq {
		switch {
		case c == 0x1b:
			b.WriteString("ESC")
		case c == 0x07:
			b.WriteString("<BEL>")
		case c < 0x20:
			fmt.Fprintf(&b, "<%02x>", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
