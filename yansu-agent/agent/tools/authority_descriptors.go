package tools

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"yansu-agent/agent/authority"
)

// DescribeAuthority classifies a Bash command without a known work folder.
func (t *BashTool) DescribeAuthority(input map[string]any) authority.Action {
	return t.DescribeAuthorityIn(input, "")
}

// DescribeAuthorityIn classifies a Bash command run in workDir. A command is
// local only when every command in it is on the read and build allowlist and
// every file it writes is in the workspace or a temp folder. Named external
// and privileged commands keep their own capability; anything else is
// unknown, which asks the user in a chat and is denied in an unattended run.
// No Bash action can be covered by a scoped grant except a single external
// command with a resource (an opencli search).
func (*BashTool) DescribeAuthorityIn(input map[string]any, workDir string) authority.Action {
	command, _ := input["command"].(string)
	return bashAuthorityAction(command, analyzeBash(command, workDir))
}

func bashAuthorityAction(command string, scan *bashScan) authority.Action {
	if mixed := scan.mixedApprovalAction(command); mixed != nil {
		return *mixed
	}
	if persistence := scan.persistenceApprovalAction(command); persistence != nil {
		return *persistence
	}
	if launcher := scan.launcherApprovalAction(command); launcher != nil {
		return *launcher
	}
	var chosen *bashVerdict
	seen := map[string]bool{}
	var parts []string
	for i := range scan.verdicts {
		v := &scan.verdicts[i]
		if v.kind == bashLocal {
			continue
		}
		key := v.subject + "\x00" + v.reason + "\x00" + v.action.Capability + "\x00" + v.detail
		if !seen[key] {
			seen[key] = true
			parts = append(parts, truncateSubject(v.subject))
		}
		if chosen == nil || bashVerdictRank(v) > bashVerdictRank(chosen) {
			chosen = v
		}
	}
	if chosen == nil {
		return authority.Action{
			Capability: "local.shell.execute",
			ToolName:   "Bash",
			Operation:  "execute",
			Effect:     authority.EffectLocalWrite,
			NoGrant:    true,
		}
	}
	action := chosen.action
	if chosen.kind == bashUnknown {
		action = unknownBashAction(command, *chosen)
	}
	if len(seen) > 1 {
		// Several different effects in one command: approve the exact
		// command, and never let a grant for one part cover the others.
		action.Resource = ""
		action.NoGrant = true
		action.ApprovalPrefix = "authority:bash.command:" + commandDigest(command)
		action.ApprovalReason = fmt.Sprintf("Allow Isoform to run this command? It runs: %s.", strings.Join(parts, "; "))
	} else if action.Capability == "connector.command.execute" {
		// One opencli call, possibly piped into head or tail: name the
		// search it runs so a grant can cover that search alone.
		if resource, ok := opencliResource(command); ok {
			action.Resource = resource
		}
	}
	return action
}

func bashVerdictRank(v *bashVerdict) int {
	if v.kind == bashUnknown {
		return 2
	}
	switch v.action.Effect {
	case authority.EffectPrivileged:
		return 4
	case authority.EffectExternalWrite:
		return 3
	default:
		return 2
	}
}

func unknownBashAction(command string, v bashVerdict) authority.Action {
	subject := truncateSubject(v.subject)
	prefix := "authority:local.shell.unknown:command:" + commandDigest(command)
	if v.trust != "" {
		prefix = "authority:local.shell.unknown:" + v.trust
	}
	return authority.Action{
		Capability:     "local.shell.unknown",
		ToolName:       "Bash",
		Operation:      subject,
		Effect:         authority.EffectUnknown,
		NoGrant:        true,
		ApprovalPrefix: prefix,
		ApprovalReason: fmt.Sprintf("Allow Isoform to run %s? It asks because %s.", subject, v.reason),
		Metadata:       map[string]string{"bash_reason": v.reason},
	}
}

func truncateSubject(subject string) string {
	subject = strings.Join(strings.Fields(subject), " ")
	if len(subject) > 120 {
		return subject[:117] + "..."
	}
	return subject
}

func (*CuaDriverTool) DescribeAuthority(input map[string]any) authority.Action {
	name, _ := input["name"].(string)
	name = strings.TrimSpace(name)
	action := authority.Action{
		Capability:     "computer." + strings.ReplaceAll(name, "_", "."),
		ToolName:       "cua_driver",
		Operation:      name,
		Effect:         authority.EffectUnknown,
		ApprovalReason: fmt.Sprintf("Allow Yansu to operate the computer using %s?", name),
	}
	action.ApprovalPrefix = "authority:" + action.Capability
	switch name {
	case "check_permissions", "get_window_state", "list_apps", "list_tools",
		"list_windows", "screenshot":
		action.Effect = authority.EffectRead
		action.Reversible = true
	case "launch_app", "open_url", "scroll":
		action.Effect = authority.EffectLocalWrite
		action.Reversible = true
	}
	if args, ok := input["args"].(map[string]any); ok {
		action.Resource = cuaResource(name, args)
	}
	return action
}

// cuaResource names what a cua-driver call acts on, for grants to match.
// The daemon resolves a pid or a window id before a bundle, so a call that
// names either is about that process or window, not the bundle it also
// names: its resource never matches a bundle grant. A bundle is written as
// "app:<bundle>/" so a grant for one app never covers another whose id
// merely starts the same way. A page open names its link, unless it also
// forces an app, so a grant can allow one site in the default browser.
func cuaResource(name string, args map[string]any) string {
	for _, key := range []string{"pid", "window_id"} {
		if v, ok := args[key]; ok && v != nil {
			return fmt.Sprintf("%s:%v", key, v)
		}
	}
	bundle := ""
	for _, key := range []string{"bundle", "bundle_id"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			bundle = CuaAppResource(v)
			break
		}
	}
	if name == "open_url" {
		if link, ok := args["url"].(string); ok {
			return bundle + strings.TrimSpace(link)
		}
	}
	return bundle
}

// CuaAppResource is the resource of a cua-driver call on one app, as a
// grant's ResourcePrefix names it.
func CuaAppResource(bundle string) string {
	return "app:" + strings.TrimSpace(bundle) + "/"
}

// opencliPipeSuffix is the one pipe a plain call may end with: into head or
// tail with a line count.
var opencliPipeSuffix = regexp.MustCompile(`\s*\|\s*(?:head|tail)\s+(?:-n\s*[0-9]{1,4}|-[0-9]{1,4})\s*$`)

// opencliStderrSuffix is the one redirect a plain call may carry, as its own
// word at the end: "ephemeral2>&1" passes "ephemeral2" to opencli, so the
// redirect needs a space before it.
var opencliStderrSuffix = regexp.MustCompile(`\s+2>&1\s*$`)

// opencliName is an adapter name as opencli spells it.
var opencliName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// opencliResource returns the resource of one plain opencli call: "opencli
// list" (optionally with -f <format>), "opencli <adapter> -h" or "opencli
// <adapter> ask -h" (or --help) with nothing after it, or "opencli <adapter>
// ask" with one question and only the flags in opencliAskFlags,
// --site-session ephemeral among them. Anything it cannot read in full
// returns false. The Bash classifier calls it with the whole command when
// opencli is the command's only non-local part.
func opencliResource(command string) (string, bool) {
	head := strings.TrimSpace(command)
	head = opencliPipeSuffix.ReplaceAllString(head, "")
	head = opencliStderrSuffix.ReplaceAllString(head, "")
	words, ok := plainWords(head)
	if !ok || len(words) < 2 || words[0] != "opencli" {
		return "", false
	}
	if words[1] == "list" {
		if !opencliListArgs(words[2:]) {
			return "", false
		}
		return "opencli:list/", true
	}
	adapter := words[1]
	if !opencliName.MatchString(adapter) || len(words) < 3 {
		return "", false
	}
	switch words[2] {
	case "-h", "--help":
		if len(words) != 3 {
			return "", false
		}
		return "opencli:" + adapter + ":help/", true
	case "ask":
		if len(words) == 4 && (words[3] == "-h" || words[3] == "--help") {
			return "opencli:" + adapter + ":help/", true
		}
		if !opencliAskArgs(words[3:]) {
			return "", false
		}
		return "opencli:" + adapter + ":ask/", true
	default:
		return "", false
	}
}

// opencliFlag is a flag an "ask" may carry: whether it takes a value, and
// the values it may take.
type opencliFlag struct {
	value    *regexp.Regexp
	optional bool
}

// opencliAskFlags are the flags of the grok, gemini and doubao "ask"
// commands (opencli 1.8.8) and the universal ones. --trace, --keep-tab,
// --window, --tab, --out, -h and every flag not listed are refused, and so
// is --site-session with any value but "ephemeral".
var opencliAskFlags = map[string]opencliFlag{
	"--site-session": {value: regexp.MustCompile(`^ephemeral$`)},
	"--timeout":      {value: regexp.MustCompile(`^[0-9]{1,4}$`)},
	"--new":          {value: regexp.MustCompile(`^(?:true|false)$`), optional: true},
	"--model":        {value: regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,40}$`)},
	"--thinking":     {value: regexp.MustCompile(`^(?:standard|extended)$`)},
	"-f":             {value: regexp.MustCompile(`^(?:table|plain|json|yaml|md|csv)$`)},
	"--format":       {value: regexp.MustCompile(`^(?:table|plain|json|yaml|md|csv)$`)},
	"-v":             {},
	"--verbose":      {},
}

// opencliListArgs accepts nothing, or one output format ("-f yaml").
func opencliListArgs(args []string) bool {
	format := opencliAskFlags["--format"].value
	switch len(args) {
	case 0:
		return true
	case 1:
		value, ok := strings.CutPrefix(args[0], "--format=")
		return ok && format.MatchString(value)
	case 2:
		return (args[0] == "-f" || args[0] == "--format") && format.MatchString(args[1])
	default:
		return false
	}
}

// opencliAskArgs accepts one question and the flags in opencliAskFlags,
// with --site-session ephemeral.
func opencliAskArgs(args []string) bool {
	ephemeral := false
	questions := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			questions++
			continue
		}
		name, value, inline := strings.Cut(arg, "=")
		if inline && !strings.HasPrefix(name, "--") {
			return false
		}
		flag, known := opencliAskFlags[name]
		if !known {
			return false
		}
		switch {
		case flag.value == nil:
			if inline {
				return false
			}
		case inline:
			if !flag.value.MatchString(value) {
				return false
			}
		case i+1 < len(args) && flag.value.MatchString(args[i+1]):
			value = args[i+1]
			i++
		case flag.optional:
		default:
			return false
		}
		if name == "--site-session" {
			ephemeral = true
		}
	}
	return ephemeral && questions == 1
}

// plainWords splits a command into words the way the shell would, refusing
// anything it cannot read as one plain command of literal words: an
// operator, a redirect, a substitution, a variable, a glob, a brace, a tilde,
// an escape or a line break outside quotes, a word that starts with "=" (zsh
// expands "=ls" to a path), and "$", "`", "\" or "!" inside double quotes.
// Outside quotes only letters, digits and - _ . / : = , + @ % are plain. Text
// in quotes stays one word, so a search for "R&D tax credits" is fine.
func plainWords(command string) ([]string, bool) {
	var words []string
	var b strings.Builder
	inWord := false
	quote := rune(0)
	for _, r := range command {
		if quote != 0 {
			switch {
			case r == quote:
				quote = 0
			case quote == '"' && (r == '$' || r == '`' || r == '\\' || r == '!'):
				return nil, false
			default:
				b.WriteRune(r)
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, b.String())
				b.Reset()
				inWord = false
			}
		case r == '=' && !inWord:
			return nil, false
		case unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_./:=,+@%", r):
			b.WriteRune(r)
			inWord = true
		default:
			return nil, false
		}
	}
	if quote != 0 {
		return nil, false
	}
	if inWord {
		words = append(words, b.String())
	}
	return words, true
}

func externalBashAction(capability, operation string) authority.Action {
	return authority.Action{
		Capability:     capability,
		ToolName:       "Bash",
		Operation:      operation,
		Effect:         authority.EffectExternalWrite,
		ApprovalPrefix: "authority:" + capability + ":" + operation,
		ApprovalReason: fmt.Sprintf("Allow external operation %s?", operation),
	}
}

func privilegedBashAction(capability, operation string) authority.Action {
	action := externalBashAction(capability, operation)
	action.Effect = authority.EffectPrivileged
	return action
}
