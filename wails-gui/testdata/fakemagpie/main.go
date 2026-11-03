// Command fakemagpie stands in for the magpie CLI in the sidecar tests. It
// serves the gateway endpoints the sidecar and bootstrap use, and implements
// the CLI subcommands Yansu runs against a JSON state directory under
// $XDG_CONFIG_HOME/magpie. Every CLI invocation is appended to calls.log so
// tests can assert the exact upserts that were made.
//
// Environment knobs (for tests):
//
//	FAKE_MAGPIE_EXIT=1        serve exits at once (restart and degraded tests)
//	FAKE_MAGPIE_UNHEALTHY=1   healthcheck exits 1
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const defaultCatalog = "claude/claude-sonnet-5-5,claude/claude-sonnet-5,claude/claude-sonnet-4-6," +
	"claude/claude-haiku-5-5,claude/claude-haiku-4-5,codex/gpt-6.1-sol,codex/gpt-5.6-sol,codex/gpt-6-luna"

const defaultAccounts = `[
  {"agent":"claude","user":"tester@example.com","plan":"max","active":true,"on":true,
   "windows":[{"name":"5 hours","used":26,"remaining":74,"resetsAt":"2026-10-10T16:50:00-07:00"}]},
  {"agent":"codex","user":"tester@example.com","plan":"pro","active":true,"on":true,"windows":[]}
]`

const defaultQuotas = `{"data":[
  {"provider":"claude","name":"Claude Code","kind":"subscription","plan":"max","user":"tester@example.com",
   "windows":[{"name":"5 hours","used":26,"remaining":74,"resetsAt":"2026-10-10T16:50:00-07:00"}],
   "readAt":"2026-10-10T14:00:00-07:00"},
  {"provider":"codex","name":"Codex","kind":"subscription","plan":"pro","user":"tester@example.com",
   "windows":[],"balance":"62500 credits","readAt":"2026-10-10T14:00:00-07:00"}
],"object":"list"}`

type groupRecord struct {
	Name    string `json:"name"`
	Models  string `json:"models"`
	Routing string `json:"routing"`
	Stays   string `json:"stays"`
}

func stateDir() string {
	return filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "magpie")
}

func statePath(name string) string {
	return filepath.Join(stateDir(), name)
}

func readJSONFile(path string, v any) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, v)
}

func writeJSONFile(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fail(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "fakemagpie:", err)
	os.Exit(1)
}

func slugify(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
}

// kv returns the value of key=value in args, or "".
func kv(args []string, key string) string {
	prefix := key + "="
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

// providerRecord mirrors the bundled version's credential-bearing record.
type providerRecord struct {
	ID     string   `json:"id"`
	Key    string   `json:"key"`
	Models []string `json:"models"`
}

func readProviders() []providerRecord {
	var store struct {
		Providers []providerRecord `json:"providers"`
	}
	readJSONFile(statePath("providers.json"), &store)
	return store.Providers
}

func writeProviders(providers []providerRecord) {
	store := map[string]json.RawMessage{}
	readJSONFile(statePath("providers.json"), &store)
	store["providers"], _ = json.Marshal(providers)
	writeJSONFile(statePath("providers.json"), store)
}

// providerModels returns the catalog ids that custom providers contribute.
func providerModels() []string {
	providers := readProviders()
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	var ids []string
	for _, provider := range providers {
		for _, model := range provider.Models {
			ids = append(ids, provider.ID+"/"+model)
		}
	}
	return ids
}

func catalogIDs() []string {
	ids := strings.Split(defaultCatalog, ",")
	return append(ids, providerModels()...)
}

func serve() {
	if os.Getenv("FAKE_MAGPIE_EXIT") == "1" {
		fmt.Fprintln(os.Stderr, "fakemagpie: exiting on request")
		os.Exit(1)
	}
	if os.Getenv("FAKE_MAGPIE_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	addr := os.Getenv("MAGPIE_ADDR")
	if addr == "" {
		fail(fmt.Errorf("MAGPIE_ADDR is not set"))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		type model struct {
			ID string `json:"id"`
		}
		var data []model
		for _, id := range catalogIDs() {
			data = append(data, model{ID: id})
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	})
	mux.HandleFunc("/v1/magpie/quotas", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		body := defaultQuotas
		if data, err := os.ReadFile(statePath("quotas.json")); err == nil {
			body = string(data)
		}
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Magpie-Provider", "fake")
		w.Header().Set("X-Magpie-Model", "fake-model")
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":        "message",
			"role":        "assistant",
			"content":     []map[string]string{{"type": "text", "text": "pong"}},
			"stop_reason": "end_turn",
		})
	})
	fmt.Printf("magpie gateway on http://%s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fail(err)
	}
}

func recordCall(args []string) {
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		fail(err)
	}
	f, err := os.OpenFile(statePath("calls.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	fmt.Fprintln(f, strings.Join(args, " "))
}

func groupCommand(args []string) {
	if os.Getenv("FAKE_MAGPIE_READ_ONLY_GROUPS") == "1" {
		fail(fmt.Errorf("magpie groups configuration is read-only"))
	}
	groups := map[string]groupRecord{}
	readJSONFile(statePath("groups.json"), &groups)
	if len(args) == 0 {
		fail(fmt.Errorf("group: missing subcommand"))
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			fail(fmt.Errorf("group add: missing name"))
		}
		name := args[1]
		id := kv(args[2:], "id")
		if id != "" {
			if _, exists := groups[id]; exists {
				fail(fmt.Errorf("group %s already exists", id))
			}
		} else {
			id = slugify(name)
			for existingID, group := range groups {
				if strings.EqualFold(group.Name, name) {
					id = existingID
					break
				}
			}
		}
		if os.Getenv("FAKE_MAGPIE_FAIL_GROUP") == id {
			fail(fmt.Errorf("magpie group %s write rejected", id))
		}
		_, replaced := groups[id]
		groups[id] = groupRecord{
			Name:    name,
			Models:  kv(args[2:], "models"),
			Routing: kv(args[2:], "routing"),
			Stays:   kv(args[2:], "stays"),
		}
		writeJSONFile(statePath("groups.json"), groups)
		verb := "added"
		if replaced {
			verb = "replaced"
		}
		fmt.Printf("%s %s - agents pick it as group/%s\n", verb, name, id)
	case "set":
		if len(args) < 3 {
			fail(fmt.Errorf("group set: missing arguments"))
		}
		group, ok := groups[args[1]]
		if !ok {
			fail(fmt.Errorf("group set: unknown group"))
		}
		if name := kv(args[2:], "name"); name != "" {
			group.Name = name
		}
		groups[args[1]] = group
		writeJSONFile(statePath("groups.json"), groups)
	case "rm":
		if len(args) < 2 {
			fail(fmt.Errorf("group rm: missing id"))
		}
		if _, ok := groups[args[1]]; !ok {
			fmt.Fprintf(os.Stderr, "magpie: no group %q\n", args[1])
			os.Exit(1)
		}
		delete(groups, args[1])
		writeJSONFile(statePath("groups.json"), groups)
		fmt.Printf("removed %s\n", args[1])
	case "copy":
		// magpie group copy <id> [name]: a duplicate with the same models.
		if len(args) < 2 {
			fail(fmt.Errorf("group copy: missing id"))
		}
		source, ok := groups[args[1]]
		if !ok {
			fmt.Fprintf(os.Stderr, "magpie: no group %q\n", args[1])
			os.Exit(1)
		}
		name := source.Name + " copy"
		if len(args) > 2 {
			name = args[2]
		}
		groups[slugify(name)] = groupRecord{Name: name, Models: source.Models, Routing: source.Routing, Stays: source.Stays}
		writeJSONFile(statePath("groups.json"), groups)
		fmt.Printf("copied %s as %s\n", args[1], slugify(name))
	case "template":
		// magpie group template <kind> [name]: the steady kind is "Never
		// stuck"; FAKE_MAGPIE_NO_TEMPLATES=1 makes it fail like a magpie
		// with no providers to build from.
		if len(args) < 2 {
			fail(fmt.Errorf("group template: missing kind"))
		}
		if os.Getenv("FAKE_MAGPIE_NO_TEMPLATES") == "1" {
			fmt.Fprintln(os.Stderr, "magpie: no providers to make a group from")
			os.Exit(1)
		}
		name := "Never stuck"
		if len(args) > 2 {
			name = args[2]
		}
		groups[slugify(name)] = groupRecord{Name: name, Models: "claude/claude-sonnet-5-5,codex/gpt-6-astra", Routing: "order", Stays: "session"}
		writeJSONFile(statePath("groups.json"), groups)
		fmt.Printf("added %s - agents pick it as group/%s\n", name, slugify(name))
	default:
		fail(fmt.Errorf("group: unknown subcommand %q", args[0]))
	}
}

func providerCommand(args []string) {
	providers := readProviders()
	if len(args) < 2 {
		fail(fmt.Errorf("provider: missing subcommand or name"))
	}
	switch args[0] {
	case "add":
		name := args[1]
		var models []string
		if list := kv(args[2:], "models"); list != "" {
			models = strings.Split(list, ",")
		}
		if name == "ollama" {
			models = []string{"llama3.2:latest"}
		}
		kept := providers[:0]
		for _, provider := range providers {
			if provider.ID != name {
				kept = append(kept, provider)
			}
		}
		providers = append(kept, providerRecord{ID: name, Key: kv(args[2:], "key"), Models: models})
		writeProviders(providers)
		fmt.Printf("added %s\n", name)
	case "rm":
		kept := providers[:0]
		found := false
		for _, provider := range providers {
			if provider.ID == args[1] {
				found = true
			} else {
				kept = append(kept, provider)
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "magpie: no provider %q\n", args[1])
			os.Exit(1)
		}
		writeProviders(kept)
		fmt.Printf("removed %s\n", args[1])
	default:
		fail(fmt.Errorf("provider: unknown subcommand %q", args[0]))
	}
}

func main() {
	// A migration can run before serve. Assert reconciliation at that first
	// command boundary, independently of the redirected agent-home config.
	if path := os.Getenv("FAKE_MAGPIE_NO_BYOK_STORE"); path != "" {
		var store struct {
			Providers []providerRecord `json:"providers"`
		}
		readJSONFile(path, &store)
		for _, provider := range store.Providers {
			if strings.HasPrefix(provider.ID, "yansu-byok") {
				fail(fmt.Errorf("bundled credential was not reconciled before command"))
			}
		}
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fakemagpie <serve|healthcheck|version|accounts|group|groups|provider|providers|ls|claude|codex>")
		os.Exit(2)
	}
	cmd, rest := os.Args[1], os.Args[2:]
	switch cmd {
	case "serve":
		serve()
	case "healthcheck":
		if os.Getenv("FAKE_MAGPIE_UNHEALTHY") == "1" {
			os.Exit(1)
		}
	case "version", "--version":
		fmt.Println("magpie 0.0.0-fake")
	case "accounts":
		recordCall(os.Args[1:])
		body := defaultAccounts
		if data, err := os.ReadFile(statePath("accounts.json")); err == nil {
			body = string(data)
		}
		fmt.Println(body)
	case "groups":
		recordCall(os.Args[1:])
		groups := map[string]groupRecord{}
		readJSONFile(statePath("groups.json"), &groups)
		for id, g := range groups {
			fmt.Printf("  %s  group/%s  %s · stays %s  %s\n", g.Name, id, g.Routing, g.Stays, g.Models)
		}
	case "ls":
		// The real CLI prints one agent per line: name, the model it uses
		// (a dash when none), then settings. agents.json maps provider ids
		// to models so tests can shape it.
		recordCall(os.Args[1:])
		agents := map[string]string{}
		readJSONFile(statePath("agents.json"), &agents)
		for _, agent := range []struct{ name, provider string }{{"Claude Code", "claude"}, {"Codex", "codex"}} {
			model := agents[agent.provider]
			if model == "" {
				model = "—"
			}
			fmt.Printf("  %-20s %s  ·  effort —  ~/.%s/settings.json\n", agent.name, model, agent.provider)
		}
	case "models":
		// The model catalog as the CLI prints it, one provider/model per line
		// with the effort steps it offers last.
		recordCall(os.Args[1:])
		for _, id := range catalogIDs() {
			fmt.Printf("  %s  %s  low/medium/high/xhigh/max\n", id, id)
		}
	case "claude", "codex":
		// Agent show (`magpie claude`): the per-agent model and effort older
		// Isoform versions saved through the CLI. agent-settings.json maps the
		// agent id to {"model","effort"}; a dash is unset. Only the one-time
		// choice migration runs this; set commands are intentionally absent.
		recordCall(os.Args[1:])
		if len(rest) != 0 {
			fmt.Fprintf(os.Stderr, "fakemagpie: %s: set commands are unsupported\n", cmd)
			os.Exit(2)
		}
		settings := map[string]map[string]string{}
		readJSONFile(statePath("agent-settings.json"), &settings)
		cur := settings[cmd]
		model, effort := cur["model"], cur["effort"]
		if model == "" {
			model = "—"
		}
		if effort == "" {
			effort = "—"
		}
		name, dir, file := "Claude Code", ".claude", "settings.json"
		if cmd == "codex" {
			name, dir, file = "Codex", ".codex", "config.toml"
		}
		fmt.Printf("  %s  %s  ·  effort %s  ~/%s/%s\n", name, model, effort, dir, file)
	case "group":
		recordCall(os.Args[1:])
		groupCommand(rest)
	case "providers":
		recordCall(os.Args[1:])
		for _, provider := range readProviders() {
			fmt.Printf("  %s  %s\n", provider.ID, provider.ID)
		}
	case "provider":
		recordCall(os.Args[1:])
		providerCommand(rest)
	default:
		fmt.Fprintf(os.Stderr, "fakemagpie: unknown command %q\n", cmd)
		os.Exit(2)
	}
}
