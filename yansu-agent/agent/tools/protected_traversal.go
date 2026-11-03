package tools

import (
	"path/filepath"
	"strings"
)

type bashFindTraversal struct {
	roots       []shWord
	destructive bool
	shown       string
}

// Include fixed relocated bundles without enumerating their contents. Removing
// a bundle's real parent must not evade the bundled-code integrity boundary.
func destructiveProtectedBoundaries(home string, roots []string) []string {
	boundaries := []string{canonicalHome(home)}
	boundaries = append(boundaries, roots...)
	for _, root := range roots {
		for _, folder := range bundledToolFolders {
			if resolved, err := canonicalPathLoose(filepath.Join(root, folder)); err == nil {
				boundaries = append(boundaries, resolved)
			}
		}
	}
	return boundaries
}

func (s *bashScan) destructiveFindMention(boundaries []string) string {
	for _, traversal := range s.findTraversals {
		if !traversal.destructive {
			continue
		}
		for _, root := range traversal.roots {
			if root.dynamic {
				return traversal.shown
			}
			candidates := s.destructiveSourceCandidates(root)
			if len(candidates) == 0 {
				return traversal.shown
			}
			for _, candidate := range candidates {
				for _, boundary := range boundaries {
					if boundary != "" && destructiveSourceContains(candidate, boundary, root.pattern) {
						return traversal.shown
					}
				}
			}
		}
	}
	return ""
}

func destructiveTraversalCommand(command bashCommand) bool {
	if len(command.argv) == 0 {
		return false
	}
	name, _, literal := commandName(command.argv[0])
	if !literal {
		return false
	}
	switch name {
	case "rm", "grm", "rmdir", "grmdir", "unlink", "srm", "trash", "mv", "gmv":
		return true
	default:
		return false
	}
}

func traversalExecFlag(name, text string) bool {
	switch name {
	case "find", "gfind":
		return text == "-exec" || text == "-execdir" || text == "-ok" || text == "-okdir"
	case "fd", "fdfind":
		return text == "--exec" || text == "--exec-batch" ||
			(len(text) > 1 && text[0] == '-' && text[1] != '-' && strings.ContainsAny(text[1:], "xX"))
	default:
		return false
	}
}

// traversalRoots reads only command arguments. fd's first positional operand
// is its search pattern; subsequent operands (or --search-path) name roots.
func traversalRoots(name string, argv []shWord) []shWord {
	if len(argv) == 0 {
		return nil
	}
	args := argv[1:]
	var roots []shWord
	switch name {
	case "find", "gfind":
		roots = findGlobalRoots(args)
	case "fd", "fdfind":
		for i, arg := range args {
			if traversalExecFlag(name, arg.text) {
				args = args[:i]
				break
			}
		}
		hits, operands := scanOptions(args, "etEdjS", setOf(
			"--extension", "--type", "--exclude", "--max-depth", "--min-depth", "--exact-depth",
			"--size", "--changed-within", "--changed-before", "--owner", "--threads",
			"--base-directory", "--search-path", "--path-separator", "--format", "--color",
			"--ignore-file", "--max-results"))
		roots = append(roots, optValues(hits, "--search-path")...)
		if len(operands) > 1 {
			roots = append(roots, operands[1:]...)
		}
		if len(roots) == 0 {
			roots = []shWord{{text: "."}}
		}
		if bases := optValues(hits, "--base-directory"); len(bases) > 0 {
			base := bases[len(bases)-1]
			for i, root := range roots {
				if !filepath.IsAbs(root.text) {
					roots[i] = shWord{text: joinPathUnclean(base.text, root.text),
						dynamic: root.dynamic || base.dynamic, pattern: root.pattern || base.pattern}
				}
			}
		}
	default:
		return nil
	}
	if len(roots) == 0 {
		return []shWord{{text: "."}}
	}
	return roots
}

// A common canonical ancestor is a conservative bound for multiple roots or
// possible working directories. Unknown roots do not establish provenance.
func (s *bashScan) traversalPathRoot(roots []shWord) string {
	common := ""
	for _, root := range roots {
		if !root.literal() {
			return ""
		}
		candidates := s.destructiveSourceCandidates(root)
		if len(candidates) == 0 {
			return ""
		}
		for _, candidate := range candidates {
			if !filepath.IsAbs(candidate) {
				return ""
			}
			if common == "" {
				common = candidate
			}
			for !foldedWithin(candidate, common) {
				parent := filepath.Dir(common)
				if parent == common {
					return ""
				}
				common = parent
			}
		}
	}
	return common
}

// Unknown roots could include protected paths. Use the HOME volume's root
// only as a conservative guard prefix for consumers of the generated paths;
// it never becomes execution text or trusted pathRoot provenance.
func (s *bashScan) traversalGuardRoot(roots []shWord) string {
	if known := s.traversalPathRoot(roots); known != "" {
		return known
	}
	return filepath.VolumeName(canonicalHome(s.home)) + string(filepath.Separator)
}

func traversalFollowsLinks(name string, argv []shWord) bool {
	for _, arg := range argv[1:] {
		switch name {
		case "find", "gfind":
			if arg.text == "-L" {
				return true
			}
		case "fd", "fdfind":
			if arg.text == "--follow" || (strings.HasPrefix(arg.text, "-") &&
				!strings.HasPrefix(arg.text, "--") && strings.Contains(arg.text, "L")) {
				return true
			}
		}
	}
	return false
}

// NUL provenance requires a producer that emits only paths, with no custom
// formatting or nested command output. Ordinary read-prefix checks do not
// rely on this stronger property.
func traversalNULPaths(name string, argv []shWord) bool {
	if len(argv) == 0 || traversalFollowsLinks(name, argv) {
		return false
	}
	nul := false
	for _, arg := range argv[1:] {
		if !arg.literal() || traversalExecFlag(name, arg.text) {
			return false
		}
		switch name {
		case "find", "gfind":
			switch arg.text {
			case "-print0":
				nul = true
			case "-print", "-printf", "-ls", "-fls", "-fprint", "-fprint0", "-fprintf":
				return false
			}
		case "fd", "fdfind":
			if arg.text == "--print0" || arg.text == "-0" {
				nul = true
			}
			if strings.HasPrefix(arg.text, "--format") || strings.HasPrefix(arg.text, "--path-separator") ||
				arg.text == "-l" || arg.text == "--list-details" {
				return false
			}
		default:
			return false
		}
	}
	return nul
}

func substituteTraversalPlaceholder(word shWord, prefix, root string) shWord {
	provenance := ""
	if word.literal() && word.text == "{}" {
		provenance = root
	}
	return shWord{text: prefix + "\x00" + word.text, dynamic: true, pathRoot: provenance}
}
