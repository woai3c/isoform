package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// A magpie the user installed themselves is used as it is: Isoform talks to
// that gateway and thinks with the routing group the user already set up,
// instead of running its own copy with its own groups. The bundled copy is
// only for Macs with no magpie of their own; there, the groups are built
// from the models the user's agents (Claude Code, Codex) currently use.

// magpieInstall describes a user-installed magpie.
type magpieInstall struct {
	BinaryPath string
	ConfigDir  string
	// Addr is the gateway's host:port: MAGPIE_ADDR when set, else magpie's
	// default 127.0.0.1:3425.
	Addr string
}

// ServerURL is the gateway base URL.
func (in magpieInstall) ServerURL() string {
	return "http://" + in.Addr
}

// Port is the gateway port, or 0 when Addr does not parse.
func (in magpieInstall) Port() int {
	_, portText, err := net.SplitHostPort(in.Addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return 0
	}
	return port
}

// magpieDefaultAddr is where a user's magpie serves unless MAGPIE_ADDR says
// otherwise.
const magpieDefaultAddr = "127.0.0.1:3425"

// userMagpieAddr returns MAGPIE_ADDR when it names a loopback host:port, else
// the default. A remote address is ignored: the gateway must be local.
func userMagpieAddr() string {
	addr := strings.TrimSpace(os.Getenv("MAGPIE_ADDR"))
	if addr == "" {
		return magpieDefaultAddr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return magpieDefaultAddr
	}
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return addr
	}
	return magpieDefaultAddr
}

// userMagpieConfigDir is $XDG_CONFIG_HOME/magpie or ~/.config/magpie, or ""
// when neither exists.
func userMagpieConfigDir() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		if dir := filepath.Join(xdg, "magpie"); isDir(dir) {
			return dir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if dir := filepath.Join(home, ".config", "magpie"); isDir(dir) {
		return dir
	}
	return ""
}

// userMagpieBinary finds the user's magpie: on PATH, in the usual user bins,
// or inside /Applications/magpie.app. The bundled copy never counts.
func userMagpieBinary() string {
	if found, err := exec.LookPath("magpie"); err == nil && !isBundledMagpiePath(found) {
		return found
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "bin", "magpie"),
		"/opt/homebrew/bin/magpie",
		"/usr/local/bin/magpie",
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/magpie.app/Contents/MacOS/magpie",
			filepath.Join(home, "Applications", "magpie.app", "Contents", "MacOS", "magpie"),
		)
	}
	for _, candidate := range candidates {
		if home == "" && strings.HasPrefix(candidate, string(filepath.Separator)+".local") {
			continue
		}
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

func isBundledMagpiePath(path string) bool {
	return strings.Contains(path, "magpie-bundle") || strings.Contains(path, "Isoform.app") || strings.Contains(path, "Yansu.app")
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// detectUserMagpieInstall reports the user's own magpie when both its binary
// and its config directory exist.
func detectUserMagpieInstall() (magpieInstall, bool) {
	bin := userMagpieBinary()
	dir := userMagpieConfigDir()
	if bin == "" || dir == "" {
		return magpieInstall{}, false
	}
	return magpieInstall{BinaryPath: bin, ConfigDir: dir, Addr: userMagpieAddr()}, true
}

// magpieGroupInfo is one routing group as `magpie groups` prints it.
type magpieGroupInfo struct {
	ID      string
	Name    string
	Routing string
	Stays   string
	Members []string
}

var (
	magpieGroupIDPattern = regexp.MustCompile(`group/([A-Za-z0-9._-]+)`)
	magpieModelIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*/[^\s→·]+$`)
	magpieRoutingWords   = map[string]bool{"order": true, "pace": true, "usage": true, "smart": true, "rotate": true, "manual": true}
)

// parseMagpieGroups reads the text `magpie groups` prints:
//
//	Never stuck  group/never-stuck  order  claude/claude-sonnet-5-5 → codex/gpt-6-astra
//
// Column widths vary, so the group id anchors each line: the name is what
// comes before it, the routing word (when present) and the provider/model
// members come after.
func parseMagpieGroups(text string) []magpieGroupInfo {
	var groups []magpieGroupInfo
	for _, line := range strings.Split(text, "\n") {
		loc := magpieGroupIDPattern.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		group := magpieGroupInfo{
			ID:   line[loc[2]:loc[3]],
			Name: strings.TrimSpace(line[:loc[0]]),
		}
		rest := strings.NewReplacer("→", " ", "·", " ", ",", " ").Replace(line[loc[1]:])
		fields := strings.Fields(rest)
		for i, token := range fields {
			switch true {
			case token == "stays" && i+1 < len(fields):
				group.Stays = fields[i+1]
			case group.Routing == "" && magpieRoutingWords[token]:
				group.Routing = token
			case magpieModelIDPattern.MatchString(token):
				group.Members = append(group.Members, token)
			}
		}
		groups = append(groups, group)
	}
	return groups
}

// magpieAgentNames maps the agent names `magpie ls` prints to the provider
// ids magpie uses for their subscriptions.
var magpieAgentNames = map[string]string{
	"Claude Code": "claude",
	"Codex":       "codex",
}

// parseMagpieAgentModels reads the text `magpie ls` prints and returns the
// model each known agent currently uses, keyed by provider id:
//
//	Claude Code          claude/claude-haiku-5-5[1m]  ·  effort —  ~/.claude/settings.json
//	Codex                gpt-6-astra  ·  effort ultra              ~/.codex/config.toml
//
// A dash means the agent has no model set. A bare model id gets the agent's
// provider prefix and a bracketed variant suffix is dropped, so the result
// can be looked up in the gateway's catalog.
func parseMagpieAgentModels(text string) map[string]string {
	models := make(map[string]string, len(magpieAgentNames))
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		for name, provider := range magpieAgentNames {
			if !strings.HasPrefix(trimmed, name+"  ") {
				continue
			}
			fields := strings.Fields(strings.TrimPrefix(trimmed, name))
			if len(fields) == 0 {
				continue
			}
			model := normalizeAgentModel(provider, fields[0])
			if model != "" {
				models[provider] = model
			}
		}
	}
	return models
}

// normalizeAgentModel turns an agent's model setting into a catalog id.
func normalizeAgentModel(provider, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "—" || raw == "-" || strings.EqualFold(raw, "default") {
		return ""
	}
	if i := strings.IndexByte(raw, '['); i > 0 {
		raw = raw[:i]
	}
	if strings.HasPrefix(raw, "group/") {
		return raw
	}
	if !strings.Contains(raw, "/") {
		return provider + "/" + raw
	}
	return raw
}

// agentCatalogModel returns the model an agent currently uses when it is a
// provider/model id; a routing group (group/...) is not a member candidate.
func agentCatalogModel(agents map[string]string, provider string) string {
	model := strings.TrimSpace(agents[provider])
	if strings.HasPrefix(model, "group/") {
		return ""
	}
	return model
}
