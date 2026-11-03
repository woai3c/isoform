package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Data roots contain both private state and agent-visible tools/workspaces.
// Only the sensitive categories in protected_data.go are hard blocked.

// ProtectedPathError is returned when a tool call would touch the app data
// folder.
type ProtectedPathError struct {
	Tool string
	Path string
}

func (e *ProtectedPathError) Error() string {
	return fmt.Sprintf("Blocked: %s would reach %q, inside Isoform's own data folder "+
		"(private state, credentials or activity media), which agent runs may not read "+
		"or write. Leave that folder out of the path.", e.Tool, e.Path)
}

// BundledToolWriteError protects code the desktop also executes outside Bash.
// These files may be read and executed, but agent tools may never change them.
type BundledToolWriteError struct {
	Tool string
	Path string
}

func (e *BundledToolWriteError) Error() string {
	return fmt.Sprintf("Blocked: %s would change %q, inside Isoform's bundled tools. Agent runs may read or run these tools, but may not modify them.", e.Tool, e.Path)
}

// protectedDataRoots are the app data folders of every flavor.
func protectedDataRoots() []string {
	var roots []string
	seen := map[string]bool{}
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" || !filepath.IsAbs(dir) {
			return
		}
		canonical, err := canonicalPathLoose(dir)
		if err != nil {
			return
		}
		if key := foldPath(canonical); !seen[key] {
			seen[key] = true
			roots = append(roots, canonical)
		}
	}
	add(os.Getenv("YANSU_AGENT_HOME"))
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".yansu-agent"))
		add(filepath.Join(home, ".isoform-agent"))
	}
	return roots
}

// foldPath lowercases a path on the case-insensitive file systems of macOS
// and Windows, so ~/.YANSU-AGENT still names the data folder.
func foldPath(path string) string {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func foldedWithin(path, root string) bool {
	return pathWithin(foldPath(path), foldPath(root))
}

// protectedHit applies the file-tool policy; dedicated memory tools are unchanged.
func protectedHit(path, workDir string, roots []string, read bool) bool {
	return protectedDataHit(path, roots, false) || (!read && dataPortWriteHit(path, roots))
}

func protectedDataHit(path string, roots []string, bash bool) bool {
	canonical, err := canonicalPathLoose(path)
	if err != nil {
		return false
	}
	// A fixed private-content directory may be relocated with a symlink. Match
	// its real target even when the caller names that target directly.
	for _, root := range roots {
		for _, folder := range dataProtectedFolders {
			if credentialPathWithin(canonical, filepath.Join(root, folder)) {
				return true
			}
		}
	}
	for _, candidate := range []string{stripFirmlink(filepath.Clean(path)), canonical} {
		for _, root := range roots {
			if !foldedWithin(candidate, root) {
				continue
			}
			rel, err := filepath.Rel(foldPath(root), foldPath(candidate))
			if err == nil && dataRelativeSensitive(rel, bash) {
				return true
			}
		}
	}
	return false
}

// containsProtectedRoot reports whether a search under dir would descend
// into a data root.
func containsProtectedRoot(dir, workDir string, roots []string) bool {
	canonical, err := canonicalPathLoose(dir)
	if err != nil {
		return false
	}
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			// A search cannot descend into a folder that does not exist.
			continue
		}
		if foldedWithin(root, canonical) && foldPath(root) != foldPath(canonical) {
			return true
		}
		if foldPath(root) == foldPath(canonical) {
			return true
		}
	}
	return false
}

// CheckProtectedPaths returns a *ProtectedPathError when a tool call would
// read or write the app data folder.
func CheckProtectedPaths(toolName string, input map[string]any, workDir string) error {
	roots := protectedDataRoots()
	home := canonicalHome("")
	switch toolName {
	case "Bash", "Read", "Glob", "Grep", "Write", "Edit", "NotebookEdit":
		if home == "" {
			return fmt.Errorf("Blocked: %s cannot resolve HOME to enforce credential protection", toolName)
		}
	}
	switch toolName {
	case "Read", "Glob", "Grep", "Write", "Edit", "NotebookEdit":
		raw, _ := input["path"].(string)
		raw = strings.TrimSpace(raw)
		search := toolName == "Glob" || toolName == "Grep"
		if raw == "" && search {
			raw = workDir
		}
		if raw == "" || !filepath.IsAbs(raw) {
			return nil
		}
		read := toolName == "Read" || search
		if !read && bundledWriteHit(raw, roots) {
			return &BundledToolWriteError{Tool: toolName, Path: raw}
		}
		if !read && trustedYansuCLIWriteHit(raw) {
			return &HardBlockedPathError{Tool: toolName, Path: raw}
		}
		canonical, err := canonicalPathLoose(raw)
		if err != nil {
			return fmt.Errorf("resolve protected path: %w", err)
		}
		for _, candidate := range []string{stripFirmlink(filepath.Clean(raw)), canonical} {
			if sensitiveFileHit(candidate, home) && !(search && sshDirectory(candidate, home)) {
				return &HardBlockedPathError{Tool: toolName, Path: raw}
			}
		}
		if protectedHit(raw, workDir, roots, read) {
			return &ProtectedPathError{Tool: toolName, Path: raw}
		}
		// Recursive tools filter each entry while walking. A safe ancestor
		// remains searchable even when it contains a protected subtree.
	case "Bash":
		command, _ := input["command"].(string)
		scan := analyzeBash(command, workDir)
		if hit := scan.keychainCommand(); hit != "" {
			return &HardBlockedPathError{Tool: toolName, Path: hit}
		}
		if hit := scan.hardBlockMention(); hit != "" {
			return &HardBlockedPathError{Tool: toolName, Path: hit}
		}
		if hit := scan.bundledWriteMention(roots); hit != "" {
			return &BundledToolWriteError{Tool: toolName, Path: hit}
		}
		if hit := scan.destructiveAncestorMention(roots); hit != "" {
			return &DestructiveAncestorError{Tool: toolName, Path: hit}
		}
		if hit := scan.protectedMention(roots); hit != "" {
			return &ProtectedPathError{Tool: toolName, Path: hit}
		}
	default:
	}
	return nil
}

// bundledWriteHit resolves only the fixed bundled roots, never their contents.
// Ancestors within the data root are protected too. Destinations above the
// data root, such as HOME, are ordinary paths and do not themselves name a
// bundle; the sandbox separately pins protected directory entries.
func bundledWriteHit(path string, roots []string) bool {
	canonical, err := canonicalPathLoose(path)
	if err != nil {
		return true
	}
	for _, root := range roots {
		for _, name := range bundledToolFolders {
			rawBundle := filepath.Join(root, name)
			bundle, err := canonicalPathLoose(rawBundle)
			if err != nil {
				return true
			}
			for _, candidate := range []string{stripFirmlink(filepath.Clean(path)), canonical} {
				for _, boundary := range []string{rawBundle, bundle} {
					if foldedWithin(candidate, boundary) ||
						(foldedWithin(candidate, root) && foldedWithin(boundary, candidate)) {
						return true
					}
				}
			}
		}
	}
	return false
}

func (s *bashScan) bundledWriteMention(roots []string) string {
	words := append([]shWord(nil), s.writes...)
	for _, link := range s.links {
		words = append(words, link.path)
	}
	for _, word := range words {
		candidates := s.pathCandidates(word)
		// Keep lexical paths as well as resolved ones: a symlink leaf in a
		// bundled tree must not make that spelling writable.
		for _, member := range braceExpand(word.text) {
			member = s.expandLeadingTilde(member)
			if cut := strings.IndexAny(member, "\x00*?[{"); cut >= 0 {
				member = member[:cut]
			}
			if member == "" {
				continue
			}
			if filepath.IsAbs(member) {
				candidates = append(candidates, member)
			} else if bases, ok := s.relativeBases(); ok {
				for _, base := range bases {
					if base != "" {
						candidates = append(candidates, joinPathUnclean(base, member))
					}
				}
			}
		}
		for _, path := range candidates {
			if bundledWriteHit(path, roots) {
				return strings.ReplaceAll(word.text, "\x00", "...")
			}
		}
	}
	return ""
}

// recursiveReaders read every file under a folder they are given.
var recursiveReaders = setOf("ack", "ack-grep", "tar", "bsdtar", "gtar", "ditto", "rsync", "zip",
	"7z", "7za")

// searchCommands search the current folder when given no path.
var searchCommands = setOf("grep", "egrep", "fgrep", "zgrep", "rg", "ag", "ack", "ack-grep")

func recursiveRead(name string, args []shWord) bool {
	if recursiveReaders[name] {
		return true
	}
	switch name {
	case "rg", "ag":
		// Both skip hidden folders such as ~/.yansu-agent unless told not to.
		opts, _ := splitOptions(args, nil)
		for _, o := range opts {
			if o.text == "--hidden" || o.text == "--unrestricted" || strings.HasPrefix(o.text, "-uu") ||
				(name == "ag" && o.text == "-u") {
				return true
			}
		}
		return false
	case "grep", "egrep", "fgrep", "zgrep", "cp", "gcp", "scp":
		opts, _ := splitOptions(args, nil)
		return shortCluster(opts, 'r') || shortCluster(opts, 'R') || shortCluster(opts, 'a') ||
			hasOption(opts, "--recursive", "--dereference-recursive", "--archive") ||
			hasOption(opts, "--directories=recurse")
	default:
	}
	return false
}

// mayReachProtected reports a word built partly at run time whose literal
// start could still grow into a data root ("/Users/bo$(...)"). Such a word
// is not blocked, since it may name anything, but it makes the command
// unknown.
func (s *bashScan) mayReachProtected(w shWord) bool {
	if !w.dynamic {
		return false
	}
	prefix := w.text
	if cut := strings.IndexByte(prefix, 0); cut >= 0 {
		prefix = prefix[:cut]
	}
	if prefix == "" {
		return true
	}
	var candidates []string
	if filepath.IsAbs(prefix) {
		candidates = []string{prefix}
	} else {
		bases, ok := s.relativeBases()
		if !ok {
			return true
		}
		for _, base := range bases {
			if base != "" {
				candidates = append(candidates, joinPathUnclean(base, prefix)+strings.Repeat("/", boolInt(strings.HasSuffix(prefix, "/"))))
			}
		}
	}
	roots := protectedDataRoots()
	for _, candidate := range candidates {
		for _, root := range roots {
			if strings.HasPrefix(foldPath(root)+"/", foldPath(candidate)) {
				return true
			}
		}
		// A workspace symlink in the literal prefix of a dynamic or glob word
		// (cat "linkdata/$(...)-secret", linkdata -> ~/.isoform-agent)
		// resolves into a root once canonicalized.
		if canonical, err := canonicalPathLoose(candidate); err == nil {
			if protectedDataHit(canonical, roots, true) {
				return true
			}
			for _, root := range roots {
				if foldedWithin(root, canonical) {
					return true
				}
			}
		}
	}
	return false
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// protectedMention returns a word of the script that names a data root (or
// the cron secret) outside the scratch folder, or "".
func (s *bashScan) protectedMention(roots []string) string {
	bases, basesKnown := s.relativeBases()
	if s.workDir == "" {
		bases, basesKnown = nil, true
	}
	resolve := func(text string) []string {
		if filepath.IsAbs(text) {
			return []string{text}
		}
		var out []string
		for _, base := range bases {
			out = append(out, joinPathUnclean(base, text))
		}
		return out
	}
	names := make([]string, 0, len(roots))
	for _, root := range roots {
		names = append(names, strings.ToLower(filepath.Base(root)))
	}
	check := func(w shWord, read bool) string {
		text := w.text
		lower := strings.ToLower(text)
		shown := strings.ReplaceAll(text, "\x00", "...")
		// Brace, same-command link, sqlite file:, @file, option-cluster and
		// ~user forms all resolve to a concrete path through pathCandidates,
		// so a data root reached by any of them is caught here, not only by
		// the literal-name match below.
		for _, candidate := range s.pathCandidates(w) {
			if protectedDataHit(candidate, roots, true) || (!read && dataPortWriteHit(candidate, roots)) {
				return shown
			}
		}
		literalPrefix := text
		if cut := strings.IndexByte(text, 0); cut >= 0 {
			literalPrefix = text[:cut]
		}
		for _, name := range names {
			if !strings.Contains(lower, name) {
				continue
			}
			if !basesKnown {
				return shown
			}
			for _, candidate := range resolve(text) {
				if protectedDataHit(candidate, roots, true) {
					return shown
				}
			}
		}
		if w.dynamic || w.pattern {
			// A word heading into a data root whose tail is built at run
			// time or by a glob: ~/.yan$(...), ~/.y*/...
			prefix := literalPrefix
			if w.pattern {
				if cut := strings.IndexAny(prefix, "*?[{"); cut >= 0 {
					prefix = prefix[:cut]
				}
			}
			candidates := resolve(prefix)
			if prefix == "" {
				candidates = nil
				if w.pattern {
					for _, base := range bases {
						for _, root := range roots {
							if foldPath(base) == foldPath(root) {
								return shown
							}
						}
					}
				}
			}
			for _, candidate := range candidates {
				// A workspace symlink in the literal prefix (linkdata ->
				// ~/.isoform-agent) resolves into a root once canonicalized,
				// even though the raw prefix does not name the root.
				if canonical, err := canonicalPathLoose(candidate); err == nil {
					if protectedDataHit(canonical, roots, true) {
						return shown
					}
					for _, root := range roots {
						if foldedWithin(root, canonical) && s.globMayReachDataRoot(w, root) {
							return shown
						}
					}
				}
				if strings.HasSuffix(prefix, "/") && !strings.HasSuffix(candidate, "/") {
					candidate += "/"
				}
				for _, root := range roots {
					parent := filepath.Dir(root)
					if len(candidate) > len(parent) && strings.HasPrefix(foldPath(root)+"/", foldPath(candidate)) && s.globMayReachDataRoot(w, root) {
						return shown
					}
				}
			}
		}
		if w.literal() && basesKnown {
			// Resolve the word itself, and the value of an --option=value,
			// so a symlink into a data root is caught like the root's name.
			texts := []string{text}
			if _, value, ok := strings.Cut(text, "="); ok && value != "" {
				texts = append(texts, value)
			}
			for _, t := range texts {
				for _, candidate := range resolve(t) {
					if protectedDataHit(candidate, roots, true) {
						return shown
					}
				}
			}
		}
		return ""
	}
	for _, arg := range s.args {
		if hit := check(arg.word, true); hit != "" {
			return hit
		}
	}
	for _, w := range s.writes {
		if hit := check(w, false); hit != "" {
			return hit
		}
	}
	for _, cd := range s.cds {
		if hit := check(cd, true); hit != "" {
			return hit
		}
	}
	for _, link := range s.links {
		if hit := check(link.path, false); hit != "" {
			return hit
		}
	}
	// A recursive read of a folder holding a data root reads the root too.
	for _, arg := range s.args {
		if !arg.recursive || !arg.word.literal() {
			continue
		}
		for _, candidate := range resolve(arg.word.text) {
			if containsProtectedRoot(candidate, s.workDir, roots) {
				return arg.word.text
			}
		}
	}
	for _, name := range s.recursiveNoPath {
		for _, base := range bases {
			if containsProtectedRoot(base, s.workDir, roots) {
				return name + " in " + base
			}
		}
	}
	return ""
}

// A static glob above a data folder is only a possible hit when its path
// components can match that folder. In particular, HOME/*.log does not name
// HOME/.isoform-agent. Keep unknown and dynamic forms conservative, and do
// not enumerate files to decide whether a future glob could reach a root.
func (s *bashScan) globMayReachDataRoot(word shWord, root string) bool {
	if !word.pattern || word.dynamic {
		return true
	}
	rootParts := strings.Split(foldPath(filepath.Clean(root)), string(filepath.Separator))
	for _, candidate := range s.pathCandidates(word) {
		parts := strings.Split(foldPath(filepath.Clean(candidate)), string(filepath.Separator))
		possible := true
		for i, part := range parts {
			if i >= len(rootParts) {
				break
			}
			matches, err := filepath.Match(part, rootParts[i])
			if err != nil {
				return true
			}
			if !matches {
				possible = false
				break
			}
		}
		if possible {
			return true
		}
	}
	return false
}

// cron-port is public connection metadata, but changing it could redirect the
// trusted CLI's credential to a listener controlled by a shell command.
func dataPortWriteHit(path string, roots []string) bool {
	canonical, err := canonicalPathLoose(path)
	if err != nil {
		return false
	}
	for _, root := range roots {
		if credentialPathEqual(canonical, filepath.Join(root, "cron-port")) {
			return true
		}
	}
	return false
}
