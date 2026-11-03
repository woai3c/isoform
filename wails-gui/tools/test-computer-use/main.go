//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: test_computer_use <command> [args...]")
		fmt.Println("Commands:")
		fmt.Println("  apps                    - list running apps")
		fmt.Println("  windows <app>           - list windows for an app")
		fmt.Println("  see <app>               - capture accessibility tree")
		fmt.Println("  open <url>              - open URL in browser")
		fmt.Println("  click <x> <y>           - click at coordinates")
		fmt.Println("  type <text>             - type text")
		fmt.Println("  press <key>             - press a key")
		fmt.Println("  hotkey <combo>          - press key combo (e.g., cmd,c)")
		fmt.Println("  scroll <dy>             - scroll (negative=down)")
		fmt.Println("  demo <query>            - full Amazon search demo")
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "apps":
		apps := activitymon.ListRunningApps()
		for _, a := range apps {
			active := ""
			if a.IsActive {
				active = " *"
			}
			fmt.Printf("%-30s %s%s\n", a.Name, a.BundleID, active)
		}

	case "windows":
		if len(os.Args) < 3 {
			fmt.Println("usage: windows <app>")
			os.Exit(1)
		}
		windows := activitymon.ListAppWindows(os.Args[2])
		for _, w := range windows {
			fmt.Printf("%-80s %.0fx%.0f main=%v\n", w.Title, w.Width, w.Height, w.IsMain)
		}

	case "see":
		if len(os.Args) < 3 {
			fmt.Println("usage: see <app>")
			os.Exit(1)
		}
		start := time.Now()
		elements := activitymon.CollectAppElements(os.Args[2])
		elapsed := time.Since(start)
		fmt.Printf("Collected %d elements in %v\n\n", len(elements), elapsed)
		for _, el := range elements {
			text := el.Label
			if text == "" {
				text = el.Title
			}
			if text == "" {
				text = el.Value
			}
			if text == "" {
				continue
			}
			if len(text) > 100 {
				text = text[:100] + "..."
			}
			fmt.Printf("[%-15s] %s: %s\n", el.ID, el.Role, text)
		}

	case "open":
		if len(os.Args) < 3 {
			fmt.Println("usage: open <url>")
			os.Exit(1)
		}
		ok := activitymon.OpenURL(os.Args[2])
		fmt.Printf("OpenURL: %v\n", ok)

	case "click":
		if len(os.Args) < 4 {
			fmt.Println("usage: click <x> <y>")
			os.Exit(1)
		}
		var x, y float64
		fmt.Sscanf(os.Args[2], "%f", &x)
		fmt.Sscanf(os.Args[3], "%f", &y)
		activitymon.ClickAt(x, y)
		fmt.Printf("Clicked at (%.0f, %.0f)\n", x, y)

	case "type":
		if len(os.Args) < 3 {
			fmt.Println("usage: type <text>")
			os.Exit(1)
		}
		activitymon.TypeText(strings.Join(os.Args[2:], " "))
		fmt.Println("Typed text")

	case "press":
		if len(os.Args) < 3 {
			fmt.Println("usage: press <key>")
			os.Exit(1)
		}
		activitymon.PressKey(os.Args[2])
		fmt.Printf("Pressed %s\n", os.Args[2])

	case "hotkey":
		if len(os.Args) < 3 {
			fmt.Println("usage: hotkey <combo>")
			os.Exit(1)
		}
		activitymon.Hotkey(os.Args[2])
		fmt.Printf("Hotkey %s\n", os.Args[2])

	case "scroll":
		if len(os.Args) < 3 {
			fmt.Println("usage: scroll <dy>")
			os.Exit(1)
		}
		var dy int
		fmt.Sscanf(os.Args[2], "%d", &dy)
		activitymon.Scroll(0, dy)
		fmt.Printf("Scrolled %d\n", dy)

	case "demo":
		query := "iphone case"
		if len(os.Args) >= 3 {
			query = strings.Join(os.Args[2:], " ")
		}
		fmt.Printf("=== Amazon Search Demo: %q ===\n\n", query)

		// 1. Open Amazon search
		url := "https://www.amazon.com/s?k=" + strings.ReplaceAll(query, " ", "+")
		fmt.Printf("1. Opening %s\n", url)
		activitymon.OpenURL(url)

		// 2. Poll for page load
		fmt.Println("2. Waiting for page to load...")
		var elements []activitymon.UIElement
		for i := 0; i < 8; i++ {
			time.Sleep(1 * time.Second)
			elements = activitymon.CollectAppElements("Safari")
			fmt.Printf("   attempt %d: %d elements\n", i+1, len(elements))
			if len(elements) > 50 {
				break
			}
		}

		// 3. Parse products
		fmt.Println("\n3. Products found:")
		count := 0
		for _, el := range elements {
			if el.Role == "AXLink" && len(el.Label) > 40 {
				title := el.Label
				if len(title) > 100 {
					title = title[:100] + "..."
				}
				fmt.Printf("   %s\n", title)
				count++
				if count >= 10 {
					break
				}
			}
		}

		// Find prices
		fmt.Println("\n4. Prices found:")
		count = 0
		for _, el := range elements {
			text := el.Label
			if text == "" {
				text = el.Value
			}
			if strings.Contains(text, "$") && len(text) < 30 {
				fmt.Printf("   %s\n", text)
				count++
				if count >= 10 {
					break
				}
			}
		}

		// Summary
		data, _ := json.Marshal(map[string]any{
			"element_count": len(elements),
			"query":         query,
		})
		fmt.Printf("\n5. Summary: %s\n", string(data))

	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}
