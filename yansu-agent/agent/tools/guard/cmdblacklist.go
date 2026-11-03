package guard

import (
	"regexp"
	"strings"
)

// CommandRisk categorizes a Bash command's danger level.
type CommandRisk int

const (
	// RiskSafe means the command may run without asking the user.
	RiskSafe CommandRisk = iota
	// RiskNeedsPermission means the command's first-argv prefix matches
	// one of our "ask the user first" rules (rm, git push, etc).
	RiskNeedsPermission
	// RiskBlocked means the command is hard-blocked (rm -rf /, dd of /dev,
	// fork bomb patterns). The tool returns an error immediately.
	RiskBlocked
)

// CommandClassification reports the risk assessment along with the
// prefix the classifier used so the UI can say "always allow `git push`"
// instead of just "always allow this command".
type CommandClassification struct {
	Risk   CommandRisk
	Reason string
	Prefix string
}

// hardBlockedPatterns catches catastrophic commands regardless of quoting
// games. These are regexes instead of literal string matches so variations
// (`rm -rf ~/`, `rm -rf $HOME`) all get caught.
var hardBlockedPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"rm of root", regexp.MustCompile(`\brm\s+(-[A-Za-z]*r[A-Za-z]*\s+)?(--no-preserve-root\s+)?(-[A-Za-z]*f[A-Za-z]*\s+)?(/|/\*|~|~/|\$HOME|\$\{HOME\})\s*($|\s|;|&&|\|\|)`)},
	{"mkfs", regexp.MustCompile(`\bmkfs(\.|\s)`)},
	{"dd to /dev", regexp.MustCompile(`\bdd\s+[^#\n]*of=/dev/`)},
	{"shred device", regexp.MustCompile(`\bshred\s+[^#\n]*/dev/`)},
	{"fork bomb", regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`)},
	{"curl pipe shell", regexp.MustCompile(`\b(curl|wget)\s[^|]*\|\s*(sh|bash|zsh)\b`)},
	{"sudo rm rf root", regexp.MustCompile(`\bsudo\s+rm\s+-[A-Za-z]*r[A-Za-z]*f?\s+/`)},
}

// permissionRequiredPrefixes triggers a user confirmation. The user can
// choose "always allow this prefix" to skip future prompts.
var permissionRequiredPrefixes = []string{
	"rm", // any rm, not just rm -rf
	"git push",
	"git reset --hard",
	"git clean",
	"git checkout --",
	"git branch -D",
	"npm publish",
	"pip install",
	"ssh",
	"scp",
	"sudo",
}

// ClassifyCommand inspects the command string and returns the risk tier.
// Tokenization is intentionally conservative: we look at the first argv
// that isn't an `ENV=val` assignment and strip any shell keywords.
func ClassifyCommand(command string) CommandClassification {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return CommandClassification{Risk: RiskSafe}
	}

	// Hard-block scan — checked first so "sudo rm -rf /" isn't just
	// downgraded to "needs permission" by the sudo prefix match.
	for _, p := range hardBlockedPatterns {
		if p.re.MatchString(trimmed) {
			return CommandClassification{Risk: RiskBlocked, Reason: p.name, Prefix: p.name}
		}
	}

	// Permission scan over the first real argv token.
	prefix := firstSignificantArgv(trimmed)
	for _, required := range permissionRequiredPrefixes {
		if prefixMatches(prefix, required) {
			return CommandClassification{Risk: RiskNeedsPermission, Reason: "dangerous command prefix", Prefix: required}
		}
	}
	return CommandClassification{Risk: RiskSafe}
}

// firstSignificantArgv returns the first argv token that isn't `NAME=val`.
// For `ENV=x PATH=/usr/bin rm -rf foo` it returns `rm -rf foo`.
func firstSignificantArgv(cmd string) string {
	// Strip a leading `cd /tmp && ` style prelude — classify the actual
	// action, not the chdir.
	for {
		i := strings.Index(cmd, "&&")
		if i < 0 {
			break
		}
		before := strings.TrimSpace(cmd[:i])
		// Accept simple cd preludes.
		if strings.HasPrefix(before, "cd ") || before == "cd" {
			cmd = strings.TrimSpace(cmd[i+2:])
			continue
		}
		break
	}
	tokens := strings.Fields(cmd)
	for _, t := range tokens {
		// Skip `VAR=value` style env assignments.
		if eq := strings.IndexByte(t, '='); eq > 0 {
			prefix := t[:eq]
			if isIdentifier(prefix) {
				continue
			}
		}
		return t + " " + strings.TrimSpace(cmd[len(t):])
	}
	return cmd
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !isAlpha(r) && r != '_' {
			return false
		}
		if !isAlpha(r) && !isDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

func isAlpha(r rune) bool { return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') }
func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// prefixMatches reports whether cmd starts with the given prefix (at
// argv-token boundaries, so "git push" matches "git push origin main"
// but not "gitpush").
func prefixMatches(cmd, prefix string) bool {
	cmd = strings.TrimSpace(cmd)
	prefix = strings.TrimSpace(prefix)
	if cmd == prefix {
		return true
	}
	if !strings.HasPrefix(cmd, prefix) {
		return false
	}
	next := cmd[len(prefix)]
	return next == ' ' || next == '\t' || next == ';' || next == '&' || next == '|'
}

// ScrubSensitiveEnv returns env with any *_TOKEN, *_KEY, *_SECRET, *_PASSWORD
// entries removed. Used by the Bash tool before exec'ing a subprocess so
// the model can't leak the Anthropic auth token into a child process.
func ScrubSensitiveEnv(env []string) []string {
	bad := []string{"_TOKEN", "_KEY", "_SECRET", "_PASSWORD", "_PASSWD"}
	keep := env[:0]
	for _, pair := range env {
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			keep = append(keep, pair)
			continue
		}
		name := strings.ToUpper(pair[:eq])
		drop := false
		for _, suffix := range bad {
			if strings.HasSuffix(name, suffix) {
				drop = true
				break
			}
		}
		if drop {
			continue
		}
		keep = append(keep, pair)
	}
	return keep
}
