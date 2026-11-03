package tools

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// This file holds the hard-block layer that refuses, in every mode (attended
// with asking off included), the private keys and credential stores listed in
// the product decision, plus the keychain-dump commands. A hard block is a
// refusal with a plain reason, never a prompt. It also carries the shared
// path-candidate normalization (brace expansion, @/file:/option-cluster and
// ?query stripping, ~user resolution, same-command link resolution) used by
// both this layer and the Isoform-data-folder check in protected_paths.go, so
// every bypass closed here is closed for the data folder too.

// HardBlockedPathError is returned when a tool call would reach a private key,
// a credential store, or a keychain the agent must never read or write.
type HardBlockedPathError struct {
	Tool string
	Path string
}

func (e *HardBlockedPathError) Error() string {
	return "Blocked: " + e.Tool + " would reach " + e.Path + ", a private key or " +
		"credential store that agent runs may never read or write, in any mode. " +
		"This is refused, not asked. Leave it out of the command."
}

// sshPublicSuffixes and sshNonKeyNames are the files under ~/.ssh that are not
// private keys, so they stay readable (public keys, the known-hosts files and
// the client config).
func sshExemptName(base string) bool {
	if strings.HasSuffix(base, ".pub") {
		return true
	}
	switch base {
	case "config", "authorized_keys", "authorized_keys2", "rc", "known_hosts", "known_hosts2", "known_hosts.old":
		return true
	default:
	}
	return strings.HasPrefix(base, "known_hosts")
}

// browserSecretNames are the basenames of the browser cookie and login
// databases, which hold session tokens and saved passwords.
var browserSecretNames = setOf("Login Data", "Login Data For Account", "Cookies",
	"Cookies.binarycookies", "cookies.sqlite", "key4.db", "key3.db", "logins.json",
	"Web Data", "Account Web Data", "Token Service", "Trust Tokens", "Network Persistent State", "Local State", "Extension Cookies", "Safe Browsing Cookies")

func credentialPath(path string) string {
	canonical, _ := canonicalPathLoose(path)
	return canonical
}

func credentialPathWithin(path, root string) bool {
	return foldedWithin(path, stripFirmlink(filepath.Clean(root))) || foldedWithin(path, credentialPath(root))
}

func credentialPathEqual(path, root string) bool {
	return foldPath(path) == foldPath(stripFirmlink(filepath.Clean(root))) || foldPath(path) == foldPath(credentialPath(root))
}

func sshDirectory(path, home string) bool {
	return home != "" && credentialPathEqual(path, filepath.Join(home, ".ssh"))
}

func browserSecretName(base string) bool {
	base = foldPath(base)
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		base = strings.TrimSuffix(base, suffix)
	}
	for name := range browserSecretNames {
		if base == foldPath(name) {
			return true
		}
	}
	return false
}

func browserSecretHit(canonical, home string) bool {
	if !browserSecretName(filepath.Base(canonical)) || home == "" {
		return false
	}
	for _, relative := range []string{
		"Library/Application Support", "Library/Containers", "Library/Cookies",
		"Library/Safari", ".mozilla", ".config", "AppData/Local", "AppData/Roaming",
	} {
		if credentialPathWithin(canonical, filepath.Join(home, filepath.FromSlash(relative))) {
			return true
		}
	}
	return false
}

// sensitiveFileHit reports whether a canonical absolute path is a hard-blocked
// private key or credential store. home must be a canonical absolute path.
func sensitiveFileHit(canonical, home string) bool {
	if canonical == "" {
		return false
	}
	base := foldPath(filepath.Base(canonical))
	if browserSecretHit(canonical, home) {
		return true
	}
	if home == "" {
		return false
	}
	ssh := filepath.Join(home, ".ssh")
	if credentialPathEqual(canonical, ssh) {
		// The folder is refused for readers that might copy its contents.
		// Names-only ls and filtered Glob/Grep walks handle it separately.
		return true
	}
	if credentialPathWithin(canonical, ssh) {
		return !sshExemptName(base)
	}
	for _, dir := range []string{
		filepath.Join(home, "Library", "Keychains"),
		filepath.Join(home, ".gnupg"),
	} {
		if credentialPathWithin(canonical, dir) {
			return true
		}
	}
	for _, file := range []string{
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".config", "gh", "hosts.yml"),
		filepath.Join(home, ".netrc"),
		filepath.Join(home, ".docker", "config.json"),
		filepath.Join(home, ".kube", "config"),
		filepath.Join(home, ".git-credentials"),
	} {
		if credentialPathEqual(canonical, file) {
			return true
		}
	}
	return false
}

// canonicalHome returns the canonical home folder for the sensitive-file
// checks, or "". An empty argument falls back to the session's home.
func canonicalHome(home string) string {
	if strings.TrimSpace(home) == "" {
		home, _ = os.UserHomeDir()
	}
	home = cleanAbs(home)
	if home == "" {
		return ""
	}
	if c, err := canonicalPathLoose(home); err == nil {
		return c
	}
	return home
}

// braceExpand expands the unquoted brace forms "{a,b,c}" and "{m..n}" in a
// literal word into its members. It expands one brace group (the first) and
// recurses on the rest, capping the total so a hostile "{a,b}{a,b}..." cannot
// blow up. A word with no brace returns itself.
func braceExpand(text string) []string {
	out := braceExpandLimited(text, 0)
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

func braceExpandLimited(text string, depth int) []string {
	if depth > 8 {
		return []string{text}
	}
	open := strings.IndexByte(text, '{')
	if open < 0 {
		return []string{text}
	}
	// Find the matching close brace for this open.
	level, close := 0, -1
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '{':
			level++
		case '}':
			level--
			if level == 0 {
				close = i
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 {
		return []string{text}
	}
	inner := text[open+1 : close]
	members := splitBraceMembers(inner)
	if members == nil {
		// Not a real expansion ("{}" or "{a}"): keep the brace literal but
		// still expand any later group.
		rest := braceExpandLimited(text[close+1:], depth+1)
		out := make([]string, 0, len(rest))
		for _, r := range rest {
			out = append(out, text[:close+1]+r)
		}
		return out
	}
	pre, post := text[:open], text[close+1:]
	posts := braceExpandLimited(post, depth+1)
	var out []string
	for _, m := range members {
		for _, mm := range braceExpandLimited(m, depth+1) {
			for _, p := range posts {
				out = append(out, pre+mm+p)
				if len(out) > 256 {
					return out
				}
			}
		}
	}
	return out
}

// splitBraceMembers splits the inside of a brace at top-level commas, or
// expands an "m..n" numeric range. It returns nil when the content is not a
// real expansion (no comma and not a range).
func splitBraceMembers(inner string) []string {
	var parts []string
	level, start := 0, 0
	hasComma := false
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '{':
			level++
		case '}':
			level--
		case ',':
			if level == 0 {
				parts = append(parts, inner[start:i])
				start = i + 1
				hasComma = true
			}
		}
	}
	parts = append(parts, inner[start:])
	if hasComma {
		return parts
	}
	if lo, hi, ok := numericRange(inner); ok {
		var out []string
		step := 1
		if lo > hi {
			step = -1
		}
		for n := lo; ; n += step {
			out = append(out, itoa(n))
			if n == hi || len(out) > 256 {
				break
			}
		}
		return out
	}
	return nil
}

func numericRange(inner string) (int, int, bool) {
	dots := strings.Index(inner, "..")
	if dots < 0 {
		return 0, 0, false
	}
	loStr, rest := inner[:dots], inner[dots+2:]
	// A second ".." is a step we do not model: bail.
	if strings.Contains(rest, "..") {
		return 0, 0, false
	}
	lo, ok1 := atoi(loStr)
	hi, ok2 := atoi(rest)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return lo, hi, true
}

func atoi(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	neg := false
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// pathSuffixes returns the path-like candidates hidden inside a word: the word
// itself, the part after a leading or name=@ at-sign (curl -d @file, -F f=@f),
// a leading file: URI (with any ?query trimmed), and the tail starting at the
// first "/" or "~" after an option cluster (-f/path, -C/path, -d@/path,
// --opt=/path). Each candidate is a plain filesystem path to resolve.
func pathSuffixes(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = stripURIish(t)
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	add(text)
	if i := strings.IndexByte(text, '@'); i >= 0 {
		add(text[i+1:])
	}
	if i := strings.IndexAny(text, "/~"); i > 0 {
		add(text[i:])
	}
	return out
}

// stripURIish removes a leading "file:" scheme (and its "//") and any "?query"
// suffix, so sqlite3's "file:/path?mode=ro" resolves as "/path".
func stripURIish(text string) string {
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "file:") {
		text = text[len("file:"):]
		text = strings.TrimPrefix(text, "//")
	}
	if i := strings.IndexByte(text, '?'); i >= 0 {
		text = text[:i]
	}
	return text
}

// resolveUserTilde turns a "~user/rest" word into "<that user's home>/rest"
// when the user exists, so ~testuser/.isoform-agent is seen as the data
// folder. It returns ("", false) when the word is not a ~user path or the
// user is unknown.
func resolveUserTilde(text string) (string, bool) {
	if len(text) < 2 || text[0] != '~' || text[1] == '/' {
		return "", false
	}
	rest := text[1:]
	name := rest
	after := ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		name, after = rest[:i], rest[i:]
	}
	u, err := user.Lookup(name)
	if err != nil || strings.TrimSpace(u.HomeDir) == "" {
		return "", false
	}
	return u.HomeDir + after, true
}

// pathCandidates returns the canonical absolute paths a literal word may name,
// after brace expansion, @/file:/option-cluster and ?query stripping, ~user
// resolution, and resolution of a leading same-command link name. A word with
// a dynamic part yields nothing (those are handled speculatively by the
// data-root check). read is unused; the hard block applies to reads and
// writes alike.
func (s *bashScan) pathCandidates(w shWord) []string {
	if w.dynamic {
		return nil
	}
	bases, basesKnown := s.relativeBases()
	if s.workDir == "" {
		bases, basesKnown = []string{""}, true
	}
	seen := map[string]bool{}
	var out []string
	addResolved := func(text string) {
		if text == "" {
			return
		}
		var raw []string
		if filepath.IsAbs(text) {
			raw = []string{text}
		} else if basesKnown {
			for _, base := range bases {
				if base == "" {
					continue
				}
				raw = append(raw, joinPathUnclean(base, text))
			}
		}
		for _, r := range raw {
			if c, err := canonicalPathLoose(r); err == nil && !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	for _, member := range braceExpand(w.text) {
		member = s.expandLeadingTilde(member)
		for _, suffix := range pathSuffixes(member) {
			if resolved, ok := resolveUserTilde(suffix); ok {
				addResolved(resolved)
				continue
			}
			addResolved(suffix)
			for _, alt := range s.linkResolved(suffix) {
				addResolved(alt)
			}
		}
	}
	return out
}

// linkResolved rewrites a path whose leading component names a link this
// command makes into the link's target, so a read or write through a
// not-yet-created same-command link is resolved. It returns the rewritten
// path texts (still possibly relative, to be joined with the bases).
func (s *bashScan) linkResolved(text string) []string {
	var out []string
	for _, link := range s.links {
		if !link.path.literal() || !link.target.literal() || link.target.text == "" {
			continue
		}
		name := strings.TrimRight(link.path.text, "/")
		name = strings.TrimPrefix(name, "./")
		cand := strings.TrimPrefix(text, "./")
		switch {
		case cand == name:
			out = append(out, link.target.text)
		case strings.HasPrefix(cand, name+"/"):
			out = append(out, link.target.text+cand[len(name):])
		default:
		}
	}
	return out
}

// hardBlockMention returns a word of the script that reaches a private key or
// credential store, or "". It uses the same candidate normalization as the
// data-folder check, so brace, link, sqlite file:, @file, option-cluster and
// ~user forms are all covered.
func (s *bashScan) hardBlockMention() string {
	home := canonicalHome(s.home)
	if home == "" {
		return ""
	}
	words := make([]shWord, 0, len(s.args)+len(s.writes)+len(s.cds)+len(s.links))
	for _, a := range s.args {
		words = append(words, a.word)
	}
	words = append(words, s.writes...)
	words = append(words, s.cds...)
	for _, l := range s.links {
		words = append(words, l.path, l.target)
	}
	for _, w := range words {
		for _, candidate := range s.pathCandidates(w) {
			if sshDirectory(candidate, home) && s.sshNamesOnlyListing() {
				continue
			}
			if sensitiveFileHit(candidate, home) {
				return strings.ReplaceAll(w.text, "\x00", "...")
			}
		}
	}
	return ""
}

// A plain ls can expose key names, but never key bytes. Keep recursive reads,
// redirects, dynamic commands and compound scripts outside this exception.
func (s *bashScan) sshNamesOnlyListing() bool {
	if s.parseFailed || len(s.cmds) != 1 || len(s.writes) != 0 || len(s.cds) != 0 || len(s.links) != 0 {
		return false
	}
	argv := s.cmds[0].argv
	if len(argv) == 0 {
		return false
	}
	name, hasDir, ok := commandName(argv[0])
	if !ok || name != "ls" || (hasDir && !s.trustedBinPath(argv[0].text)) {
		return false
	}
	for _, word := range argv[1:] {
		if !word.literal() {
			return false
		}
	}
	opts, _ := splitOptions(argv[1:], nil)
	return !shortCluster(opts, 'R') && !hasOption(opts, "--recursive")
}

// extractWriteHit reports a write under a folder an archive extraction or
// recursive copy filled in this command: the write may land on an extracted
// symlink and reach outside the workspace, which cannot be checked here.
func (s *bashScan) extractWriteHit() string {
	if len(s.extractDirs) == 0 {
		return ""
	}
	var dests []string
	for _, d := range s.extractDirs {
		for _, p := range s.targetPaths(d) {
			dests = append(dests, p)
		}
	}
	for _, w := range expandBraceWordsStandalone(s.writes) {
		if !w.literal() {
			continue
		}
		for _, p := range s.targetPaths(w) {
			for _, dest := range dests {
				// A write strictly inside an extraction folder (not the
				// folder itself) may hit an extracted entry.
				if foldPath(p) != foldPath(dest) && foldedWithin(p, dest) {
					return strings.ReplaceAll(w.text, "\x00", "...")
				}
			}
		}
	}
	return ""
}

// expandBraceWordsStandalone is expandBraceWords without tilde expansion, for
// callers that only need the brace members.
func expandBraceWordsStandalone(words []shWord) []shWord {
	var out []shWord
	for _, w := range words {
		if w.dynamic || !strings.ContainsAny(w.text, "{}") {
			out = append(out, w)
			continue
		}
		members := braceExpand(w.text)
		if len(members) == 1 {
			out = append(out, w)
			continue
		}
		for _, m := range members {
			out = append(out, shWord{text: m, pattern: isGlobPattern(m)})
		}
	}
	return out
}

// keychainCommand reports a macOS keychain dump the agent must never run:
// `security find-generic-password -w`, `security find-internet-password -w`
// and `security dump-keychain`. These print secrets to stdout.
func (s *bashScan) keychainCommand() string {
	for _, cmd := range s.cmds {
		texts, ok := literalTexts(cmd.argv)
		if !ok || len(texts) < 2 {
			continue
		}
		name, _, nameOK := commandName(cmd.argv[0])
		if !nameOK || name != "security" {
			continue
		}
		sub := ""
		for _, t := range texts[1:] {
			if !strings.HasPrefix(t, "-") {
				sub = t
				break
			}
		}
		switch sub {
		case "dump-keychain":
			return strings.Join(texts, " ")
		case "find-generic-password", "find-internet-password":
			for _, t := range texts[2:] {
				if t == "-w" || (len(t) > 1 && t[0] == '-' && t[1] != '-' && strings.IndexByte(t, 'w') > 0) {
					return strings.Join(texts, " ")
				}
			}
		default:
		}
	}
	return ""
}
