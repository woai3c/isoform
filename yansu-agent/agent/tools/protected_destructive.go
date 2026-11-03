package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DestructiveAncestorError refuses removing or moving HOME or a directory
// containing app data, even when the Bash command has been approved.
type DestructiveAncestorError struct {
	Tool string
	Path string
}

func (e *DestructiveAncestorError) Error() string {
	return fmt.Sprintf("Blocked: %s cannot safely remove or move %q. The target is HOME, contains protected Isoform data, or is determined at run time. Choose a literal ordinary file or subfolder instead.", e.Tool, e.Path)
}

// Destructive sources need a separate check from write destinations: moving
// a report into HOME is ordinary work, but moving HOME would relocate every
// protected subtree. Resolve only the fixed roots and the command operands;
// do not enumerate HOME or expand globs against its contents.
func (s *bashScan) destructiveAncestorMention(roots []string) string {
	return s.destructiveAncestorMentionDepth(roots, 0)
}

func (s *bashScan) destructiveAncestorMentionDepth(roots []string, depth int) string {
	if depth > bashMaxDepth {
		return "nested inline program"
	}
	boundaries := destructiveProtectedBoundaries(s.home, roots)
	if hit := s.destructiveWrappedMention(); hit != "" {
		return hit
	}
	if hit := s.destructiveFindMention(boundaries); hit != "" {
		return hit
	}
	if hit := s.inlineDestructiveMention(roots, boundaries, depth); hit != "" {
		return hit
	}
	_, basesKnown := s.relativeBases()
	for _, command := range s.cmds {
		if len(command.argv) == 0 {
			continue
		}
		name, _, literal := commandName(command.argv[0])
		if !literal {
			continue
		}
		var sources []shWord
		switch name {
		case "rm", "grm", "unlink", "srm", "trash":
			_, sources = scanOptions(command.argv[1:], "", nil)
		case "rmdir", "grmdir":
			hits, operands := scanOptions(command.argv[1:], "", nil)
			sources = operands
			if optSeen(hits, "-p", "--parents") {
				sources = s.destructiveParentOperands(sources, boundaries)
			}
		case "mv", "gmv":
			// --backup has an optional attached value; it must not consume
			// the next source operand when written without '='.
			hits, operands := scanOptions(command.argv[1:], "tS", setOf("--target-directory", "--suffix"))
			sources = operands
			if !optSeen(hits, "-t", "--target-directory") && len(sources) > 0 {
				sources = sources[:len(sources)-1]
			}
			if optSeen(hits, "--exchange") {
				sources = append(sources, s.exchangeDestinations(hits, operands, sources)...)
			}
		default:
			continue
		}
		for _, source := range sources {
			if source.dynamic && !s.safeGeneratedDestructiveSource(source, roots, boundaries) {
				return strings.ReplaceAll(source.text, "\x00", "<dynamic>")
			}
			if !basesKnown {
				for _, member := range braceExpand(source.text) {
					if !filepath.IsAbs(s.expandLeadingTilde(member)) {
						return strings.ReplaceAll(source.text, "\x00", "<dynamic>")
					}
				}
			}
			for _, candidate := range s.destructiveSourceCandidates(source) {
				for _, boundary := range boundaries {
					if boundary == "" {
						continue
					}
					if destructiveSourceContains(candidate, boundary, source.pattern) {
						return strings.ReplaceAll(source.text, "\x00", "...")
					}
				}
			}
		}
	}
	return ""
}

// An expansion in a wrapper can change where its options end and the nested
// command begins. Keep destructive words visible even if normal unwrapping
// stopped at that expansion; an opaque classification alone is not a refusal
// in an attended chat with prompts disabled.
func (s *bashScan) destructiveWrappedMention() string {
	for _, argv := range s.dynamicWrappers {
		for i := 1; i < len(argv); i++ {
			if destructiveTraversalCommand(bashCommand{argv: argv[i:]}) {
				return display(argv)
			}
			name, _, literal := commandName(argv[i])
			if !literal {
				continue
			}
			switch name {
			case "find", "gfind", "fd", "fdfind":
				for _, arg := range argv[i+1:] {
					if arg.literal() && arg.text == "-delete" {
						return display(argv)
					}
				}
			default:
			}
		}
	}
	return ""
}

// Only scanner-produced file lists carry pathRoot. A shell variable or command
// substitution cannot establish confinement: even a literal ./build/ prefix
// can be followed by enough ../ components to leave the workspace.
func (s *bashScan) safeGeneratedDestructiveSource(source shWord, roots, boundaries []string) bool {
	if source.pathRoot == "" || s.workDir == "" {
		return false
	}
	paths := s.destructiveSourceCandidates(shWord{text: source.pathRoot})
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		if !foldedWithin(path, s.workDir) || protectedDataHit(path, roots, true) ||
			bundledWriteHit(path, roots) || sensitiveFileHit(path, s.home) {
			return false
		}
		for _, boundary := range boundaries {
			if boundary != "" && destructiveSourceContains(path, boundary, false) {
				return false
			}
		}
	}
	return true
}

// rmdir --parents removes only the explicitly named path prefixes. A relative
// cache/child stops at cache; it does not continue through the working folder.
// Synthesized prefixes must name a boundary itself: a common ancestor cannot
// be empty while HOME or a data root exists beneath it.
func (s *bashScan) destructiveParentOperands(operands []shWord, boundaries []string) []shWord {
	sources := append([]shWord(nil), operands...)
	for _, operand := range operands {
		if operand.dynamic {
			continue
		}
		for parent := filepath.Dir(operand.text); parent != "."; parent = filepath.Dir(parent) {
			word := shWord{text: parent, pattern: operand.pattern}
			for _, candidate := range s.destructiveSourceCandidates(word) {
				for _, boundary := range boundaries {
					matches := foldPath(candidate) == foldPath(boundary)
					if operand.pattern {
						matches, _ = filepath.Match(foldPath(candidate), foldPath(boundary))
					}
					if matches {
						sources = append(sources, word)
					}
				}
			}
			if parent == filepath.Dir(parent) {
				break
			}
		}
	}
	return sources
}

// GNU mv --exchange replaces both entries. A directory destination normally
// means destination/basename(source); only -T treats that directory itself as
// the entry to replace. Resolve these operands without walking either tree.
func (s *bashScan) exchangeDestinations(hits []optHit, operands, sources []shWord) []shWord {
	targets := optValues(hits, "-t", "--target-directory")
	directory := len(targets) > 0
	if !directory && len(operands) > 1 {
		targets = operands[len(operands)-1:]
	}
	var destinations []shWord
	for _, target := range targets {
		if target.dynamic {
			destinations = append(destinations, target)
			continue
		}
		for _, path := range s.destructiveSourceCandidates(target) {
			dir := directory
			if !dir && !optSeen(hits, "-T", "--no-target-directory") {
				info, err := os.Stat(path)
				dir = err == nil && info.IsDir()
			}
			if !dir {
				destinations = append(destinations, shWord{text: path, pattern: target.pattern})
				continue
			}
			for _, source := range sources {
				if source.dynamic {
					destinations = append(destinations, shWord{text: filepath.Join(path, "\x00"), dynamic: true})
					continue
				}
				for _, member := range braceExpand(source.text) {
					base := filepath.Base(strings.TrimRight(member, string(filepath.Separator)))
					destinations = append(destinations, shWord{
						text: filepath.Join(path, base), pattern: target.pattern || source.pattern,
					})
				}
			}
		}
	}
	return destinations
}

// rm and mv operands are paths, not URI or @file arguments. Preserve their
// literal prefixes and glob characters instead of stripping them with the
// broader pathCandidates helper used for arbitrary command arguments.
func (s *bashScan) destructiveSourceCandidates(source shWord) []string {
	if source.dynamic {
		return nil
	}
	bases, basesKnown := s.relativeBases()
	var candidates []string
	for _, member := range braceExpand(source.text) {
		member = s.expandLeadingTilde(member)
		for _, path := range append([]string{member}, s.linkResolved(member)...) {
			var absolute []string
			switch filepath.IsAbs(path) {
			case true:
				absolute = append(absolute, path)
			case false:
				if basesKnown {
					for _, base := range bases {
						if base != "" {
							absolute = append(absolute, joinPathUnclean(base, path))
						}
					}
				}
			}
			for _, path := range absolute {
				if canonical, err := canonicalPathLoose(path); err == nil {
					candidates = append(candidates, canonical)
				}
			}
		}
	}
	return candidates
}

func destructiveSourceContains(source, boundary string, pattern bool) bool {
	if !pattern {
		return foldedWithin(boundary, source)
	}
	// A static glob is destructive only if it can select the boundary or
	// one of its ancestors. HOME/*.log cannot select HOME, while a glob of
	// HOME's parent can. Checking fixed ancestors avoids a filesystem walk.
	for ancestor := filepath.Clean(boundary); ; ancestor = filepath.Dir(ancestor) {
		if matches, err := filepath.Match(foldPath(source), foldPath(ancestor)); err == nil && matches {
			return true
		}
		if ancestor == filepath.Dir(ancestor) {
			return false
		}
	}
}
