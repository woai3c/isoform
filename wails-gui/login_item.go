package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
)

const loginItemCommandFlag = "--yansu-login-item"

// loginItemBundlePath keeps login items tied to the installed copy instead
// of a worktree that may disappear. Absent or incompatible installs keep the
// running bundle's native service without launching the installed binary.
func loginItemBundlePath(home, flavor, runningBundle string) string {
	if home == "" {
		return runningBundle
	}
	name := appFlavorName(flavor) + ".app"
	if flavor == appFlavorYansu && filepath.Base(runningBundle) == "Yansu Private.app" {
		name = "Yansu Private.app"
	}
	installed := filepath.Join(home, "Applications", name)
	if info, err := os.Stat(installed); err == nil && info.IsDir() && loginItemBundleSupportsProtocol(installed) {
		// Canonical paths prevent a symlinked installation recursively
		// delegating to itself when findAppBundle resolves the running path.
		if resolved, err := filepath.EvalSymlinks(installed); err == nil {
			return resolved
		}
		return installed
	}
	return runningBundle
}

// The build script stamps this capability before signing. Inspect metadata
// without starting the binary: older bundles ignore the private command and
// would otherwise launch the UI. Unknown versions and formats fail closed to
// the running bundle's native API. Our build script keeps Info.plist as XML.
func loginItemBundleSupportsProtocol(bundle string) bool {
	data, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return false
	}
	var plist struct {
		XMLName xml.Name `xml:"plist"`
		Dict    struct {
			Entries []struct {
				XMLName xml.Name
				Value   string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &plist); err != nil {
		return false
	}
	entries := plist.Dict.Entries
	compatible := false
	for i, entry := range entries {
		if entry.XMLName.Local == "key" && entry.Value == "YansuLoginItemProtocolVersion" {
			if compatible || i+1 >= len(entries) {
				return false
			}
			value := entries[i+1]
			if value.XMLName.Local != "integer" || strings.TrimSpace(value.Value) != "1" {
				return false
			}
			compatible = true
		}
	}
	return compatible
}

// loginItemCommandAction recognizes only the private command at argv[1].
// Malformed requests are handled as errors, never as a normal app launch.
func loginItemCommandAction(args []string) (action string, requested bool) {
	if len(args) == 0 || args[0] != loginItemCommandFlag {
		return "", false
	}
	if len(args) != 2 {
		return "", true
	}
	switch args[1] {
	case "register", "unregister", "status":
		return args[1], true
	default:
		return "", true
	}
}
