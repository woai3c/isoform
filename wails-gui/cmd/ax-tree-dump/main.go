// ax-tree-dump prints the AX tree of the frontmost window plus parser
// dispatch info. Use it to diagnose why an app-specific parser
// (Slack/Notion/etc) misses for a real-world window: focus the target app,
// run the tool with a short delay, and inspect the resulting JSON.
//
// Usage:
//
//	go run ./cmd/ax-tree-dump --delay 5 > /tmp/slack-axtree.json
//	# switch to Slack within 5 seconds
//
// Flags:
//
//	--delay N       sleep N seconds before capture (default 0)
//	--out PATH      write to PATH instead of stdout
//	--no-tree       omit the full AX tree (keep only metadata + parser result)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/internal/platform/activitymon/appparser"
)

func main() {
	delay := flag.Int("delay", 0, "seconds to sleep before capture (lets you focus the target app)")
	outPath := flag.String("out", "", "write JSON to this file instead of stdout")
	omitTree := flag.Bool("no-tree", false, "omit the full AX tree (keep metadata + parser result only)")
	bundle := flag.String("bundle", "", "bypass frontmost — read AX tree of the running app with this bundle ID (e.g. com.tinyspeck.slackmacgap). Workaround for Universal Control hijacking frontmost on multi-monitor setups.")
	listWindows := flag.Bool("list-windows", false, "with --bundle: list all top-level windows of the app with role/title/treeBytes, then exit. Useful for picking the right window in multi-window Electron apps.")
	flag.Parse()

	if *delay > 0 {
		fmt.Fprintf(os.Stderr, "sleeping %d s — switch to the target app now…\n", *delay)
		time.Sleep(time.Duration(*delay) * time.Second)
	}

	if *listWindows {
		if *bundle == "" {
			log.Fatal("--list-windows requires --bundle")
		}
		raw := activitymon.ListWindowsByBundleJSON(*bundle)
		if raw == "" {
			log.Fatalf("no windows or app not running: %s", *bundle)
		}
		fmt.Println(raw)
		return
	}

	var (
		info       activitymon.AppInfo
		axTreeJSON string
		axText     string
		axTree     *activitymon.AXNode
		browserURL string
	)
	if *bundle != "" {
		info = activitymon.AppInfo{BundleID: *bundle}
		axTreeJSON = activitymon.CollectWindowTreeByBundleJSON(*bundle)
		axTree = activitymon.CollectWindowTreeByBundle(*bundle)
		// CollectFocusedWindowText still uses frontmost; for --bundle mode
		// we leave it empty since the parser will run on the structured
		// tree only. Tree-based parsers (Slack/ChatGPT/Notion) don't need
		// the flat text fallback.
	} else {
		info = activitymon.FrontmostAppInfo()
		browserURL = activitymon.GetBrowserURL(info.BundleID)
		axTreeJSON = activitymon.CollectFocusedWindowTreeJSON()
		axText = activitymon.CollectFocusedWindowText()
		axTree = activitymon.CollectFocusedWindowTree()
	}

	parsed := appparser.ParseTree(axTree, axText, info.BundleID, info.WindowTitle, browserURL)

	type dispatch struct {
		MatchedApp     string `json:"matchedApp,omitempty"`
		ContentType    string `json:"contentType,omitempty"`
		MessagesCount  int    `json:"messagesCount"`
		Title          string `json:"title,omitempty"`
		Channel        string `json:"channel,omitempty"`
		ParsedNonEmpty bool   `json:"parsedNonEmpty"`
	}
	d := dispatch{ParsedNonEmpty: parsed != nil}
	if parsed != nil {
		d.ContentType = parsed.ContentType
		d.MessagesCount = len(parsed.Messages)
		d.Title = parsed.Title
		d.Channel = parsed.Channel
		if parsed.Metadata != nil {
			d.MatchedApp = parsed.Metadata["app"]
		}
	}

	var rawTree json.RawMessage
	if !*omitTree && axTreeJSON != "" {
		// Validate before embedding — the C tree walker truncates on
		// budget exhaustion (TREE_MAX_NODES / 1 MB buffer) and can leave
		// a stray trailing comma + missing closing brace, which breaks
		// json.RawMessage marshalling. When invalid, fall back to a
		// quoted string so the dump file still serializes cleanly.
		if json.Valid([]byte(axTreeJSON)) {
			rawTree = json.RawMessage(axTreeJSON)
		} else {
			fmt.Fprintf(os.Stderr,
				"warn: AX tree JSON is truncated (%d bytes) — embedding as raw string\n",
				len(axTreeJSON))
			quoted, _ := json.Marshal(axTreeJSON)
			rawTree = json.RawMessage(quoted)
		}
	}

	out := struct {
		Timestamp   int64                    `json:"timestamp"`
		BundleID    string                   `json:"bundleId"`
		AppName     string                   `json:"appName"`
		WindowTitle string                   `json:"windowTitle"`
		BrowserURL  string                   `json:"browserURL,omitempty"`
		AXTreeBytes int                      `json:"axTreeBytes"`
		AXTextBytes int                      `json:"axTextBytes"`
		AXTree      json.RawMessage          `json:"axTree,omitempty"`
		AXText      string                   `json:"axText,omitempty"`
		Parsed      *appparser.ParsedContent `json:"parsed,omitempty"`
		Dispatch    dispatch                 `json:"dispatch"`
	}{
		Timestamp:   time.Now().UnixMilli(),
		BundleID:    info.BundleID,
		AppName:     info.AppName,
		WindowTitle: info.WindowTitle,
		BrowserURL:  browserURL,
		AXTreeBytes: len(axTreeJSON),
		AXTextBytes: len(axText),
		AXTree:      rawTree,
		AXText:      axText,
		Parsed:      parsed,
		Dispatch:    d,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}

	if *outPath != "" {
		if err := os.WriteFile(*outPath, data, 0o644); err != nil {
			log.Fatalf("write %s: %v", *outPath, err)
		}
		fmt.Fprintf(os.Stderr, "wrote %d bytes to %s (parser hit: %v)\n", len(data), *outPath, d.ParsedNonEmpty)
		return
	}
	if _, err := os.Stdout.Write(data); err != nil {
		log.Fatalf("write stdout: %v", err)
	}
	if _, err := os.Stdout.WriteString("\n"); err != nil {
		log.Fatalf("write stdout: %v", err)
	}
}
