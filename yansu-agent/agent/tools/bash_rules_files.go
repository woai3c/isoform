package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// This file holds the rules of the commands that copy, move, link, patch or
// rewrite files. Each records every file it changes, so the workspace check
// sees sources a move deletes and links a later write would follow.

// bashLink is a link a command makes: a write to its path, or to a path under
// it, reaches whatever it points to. target is what the link points at, so a
// read or write through the link in the same command can be resolved before
// the link exists on disk.
type bashLink struct {
	path   shWord
	target shWord
}

// cp records the destination, or for cp -s and cp -l the links it makes. A
// hard link shares the source's contents, so its source counts as written.
func (s *bashScan) cp(argv []shWord) bashVerdict {
	hits, operands := scanOptions(argv[1:], "tS", setOf("--target-directory", "--suffix", "--no-preserve", "--sparse", "--reflink", "--backup", "--preserve"))
	symbolic := optSeen(hits, "-s", "--symbolic-link")
	hard := optSeen(hits, "-l", "--link")
	targets := optValues(hits, "-t", "--target-directory")
	sources := operands
	if len(targets) == 0 && len(operands) > 0 {
		targets = operands[len(operands)-1:]
		sources = operands[:len(operands)-1]
	}
	if !symbolic && !hard {
		s.writes = append(s.writes, targets...)
		recursive := optSeen(hits, "-R", "-r", "--recursive", "-a", "--archive")
		if recursive {
			// The destination tree receives whatever the sources hold,
			// including symlinks, so a later write under it may follow one.
			s.extractDirs = append(s.extractDirs, targets...)
			// A copied symlink stays a link (cp -R, -a, -P, -d), so the
			// destination can itself be a link a later write follows.
			for _, src := range sources {
				if s.sourceIsLink(src) {
					s.addLinks(targets, []shWord{src})
				}
			}
			if optSeen(hits, "-L", "--dereference", "-H") {
				s.followReadRoots = append(s.followReadRoots, sources...)
			}
		}
		return localVerdict()
	}
	s.addLinks(targets, sources)
	if hard {
		s.writes = append(s.writes, sources...)
	}
	return localVerdict()
}

// sourceIsLink reports a copy source that is a symlink, or sits at or under a
// link this command makes, so copying it keeps a link the caller can follow.
func (s *bashScan) sourceIsLink(src shWord) bool {
	if !src.literal() {
		return false
	}
	for _, p := range s.targetPaths(src) {
		if s.underLink(p) {
			return true
		}
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// mv deletes every source and writes the destination: all its operands count
// as written.
func (s *bashScan) mv(argv []shWord) bashVerdict {
	hits, operands := scanOptions(argv[1:], "tS", setOf("--target-directory", "--suffix", "--backup"))
	s.writes = append(s.writes, optValues(hits, "-t", "--target-directory")...)
	s.writes = append(s.writes, operands...)
	return localVerdict()
}

// ln records the links it makes. A hard link shares its source's contents,
// so the source counts as written; a symbolic link is followed by any later
// write to it in the same command.
func (s *bashScan) ln(argv []shWord) bashVerdict {
	hits, operands := scanOptions(argv[1:], "tS", setOf("--target-directory", "--suffix", "--backup"))
	symbolic := optSeen(hits, "-s", "--symbolic")
	dirs := optValues(hits, "-t", "--target-directory")
	sources := operands
	var linkAt []shWord
	switch {
	case len(dirs) > 0:
		linkAt = dirs
	case len(operands) == 1:
		linkAt = []shWord{{text: "."}}
	case len(operands) > 1:
		linkAt = operands[len(operands)-1:]
		sources = operands[:len(operands)-1]
	default:
	}
	s.addLinks(linkAt, sources)
	if !symbolic {
		s.writes = append(s.writes, sources...)
	}
	return localVerdict()
}

// addLinks records links made at each place: the place itself (a link name)
// and the source's name inside it (a link made into a folder).
func (s *bashScan) addLinks(places, sources []shWord) {
	// A single source links to a single place (ln -s target link); with more
	// than one source each links into the place folder under its own name.
	var singleTarget shWord
	if len(sources) == 1 {
		singleTarget = sources[0]
	}
	for _, place := range places {
		s.links = append(s.links, bashLink{path: place, target: singleTarget})
		for _, src := range sources {
			base := filepath.Base(strings.TrimRight(src.text, "/"))
			if !src.literal() {
				// The link's name is not known: every path under the
				// folder may be it.
				continue
			}
			s.links = append(s.links, bashLink{
				path:   shWord{text: filepath.Join(place.text, base), dynamic: place.dynamic, pattern: place.pattern},
				target: src,
			})
		}
	}
}

var chmodMode = regexp.MustCompile(`^(?:[0-7]{1,5}|[ugoa]*[-+=][rwxXstugo]*(?:,[ugoa]*[-+=][rwxXstugo]*)*)$`)

// chmodLike records the files chmod, chown, chgrp and chflags change: every
// operand but the mode, owner or flags.
func (s *bashScan) chmodLike(name string, args []shWord) {
	hits, operands := scanOptions(args, "", setOf("--reference"))
	if optSeen(hits, "--reference") {
		s.writes = append(s.writes, operands...)
		return
	}
	switch name {
	case "chmod":
		// A symbolic mode such as -x reads as an option, so only an operand
		// that looks like a mode is skipped.
		for i, op := range operands {
			if i == 0 && op.literal() && chmodMode.MatchString(op.text) {
				continue
			}
			s.writes = append(s.writes, op)
		}
	default:
		if len(operands) > 1 {
			s.writes = append(s.writes, operands[1:]...)
		}
	}
}

// patch records the files it writes: the original file named on the command
// line or, without one, every file the patch text names. Text it cannot read
// (input from a pipe or a file the same command writes) makes it unknown.
func (s *bashScan) patch(argv []shWord, in bashStdin) bashVerdict {
	hits, operands := scanOptions(argv[1:], "BDFVYdiopzrg", setOf("--prefix", "--basename-prefix",
		"--ifdef", "--fuzz", "--version-control", "--directory", "--input", "--output", "--strip",
		"--suffix", "--reject-file", "--get", "--quoting-style", "--reject-format", "--backup-if-mismatch"))
	for _, g := range optValues(hits, "-g", "--get") {
		if g.text != "0" {
			return unknownVerdict(display(argv), "it checks files out of version control, which runs commands", "")
		}
	}
	s.cds = append(s.cds, optValues(hits, "-d", "--directory")...)
	outputs := optValues(hits, "-o", "--output")
	s.writes = append(s.writes, outputs...)
	for _, r := range optValues(hits, "-r", "--reject-file") {
		if r.text != "-" {
			s.writes = append(s.writes, r)
		}
	}
	s.writes = append(s.writes, optValues(hits, "-B", "--prefix", "-Y", "--basename-prefix")...)
	if len(operands) > 0 && len(outputs) == 0 {
		// The original-file operand is written; its later sections may name
		// other files, so keep reading the patch text below.
		s.writes = append(s.writes, operands[0])
	}
	strip := -1
	for _, p := range optValues(hits, "-p", "--strip") {
		if n, err := strconv.Atoi(p.text); err == nil {
			strip = n
		} else {
			return unknownVerdict(display(argv), "its strip count is not a number", "")
		}
	}
	text, ok := s.patchText(argv, hits, operands, in)
	if !ok {
		// A patch with an output option or explicit operand still applies
		// its sections; if the text cannot be read, its later +++ headers
		// may write outside the workspace, so ask.
		if len(outputs) > 0 && len(operands) == 0 {
			return localVerdict()
		}
		return unknownVerdict(display(argv), "it applies a patch this check cannot read, which may name files outside the workspace", "")
	}
	for _, name := range patchFileNames(text) {
		s.writes = append(s.writes, shWord{text: stripPatchName(name, strip)})
	}
	return localVerdict()
}

// patchText returns the patch: the -i file or a file operand, or its input
// when that is a here-document, a here-string, literal piped text or a file.
// A file the same command writes is not read, since it changes before patch
// runs.
func (s *bashScan) patchText(argv []shWord, hits []optHit, operands []shWord, in bashStdin) (string, bool) {
	var file shWord
	inputs := optValues(hits, "-i", "--input")
	switch {
	case len(inputs) > 0:
		file = inputs[len(inputs)-1]
	case len(operands) > 1:
		file = operands[1]
	case in.present && in.known:
		return in.text, true
	case in.present && in.file.text != "":
		file = in.file
	default:
		return "", false
	}
	if !file.literal() || file.text == "-" {
		return "", false
	}
	for _, w := range s.writes {
		if filepath.Clean(w.text) == filepath.Clean(file.text) || !w.literal() {
			return "", false
		}
	}
	path := file.text
	if !filepath.IsAbs(path) {
		bases, ok := s.relativeBases()
		if !ok || len(bases) != 1 || bases[0] == "" {
			return "", false
		}
		path = filepath.Join(bases[0], path)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// patchFileNames returns the file names in a patch's headers.
func patchFileNames(text string) []string {
	var names []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		var rest string
		switch {
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "*** "):
			rest = line[4:]
		case strings.HasPrefix(line, "Index: "):
			rest = line[len("Index: "):]
		case strings.HasPrefix(line, "rename to "), strings.HasPrefix(line, "copy to "):
			rest = line[strings.Index(line, " to ")+4:]
		default:
			continue
		}
		if tab := strings.IndexByte(rest, '\t'); tab >= 0 {
			rest = rest[:tab]
		}
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, `"`) {
			if unquoted, err := strconv.Unquote(rest); err == nil {
				rest = unquoted
			}
		} else if fields := strings.Fields(rest); len(fields) > 0 {
			rest = fields[0]
		}
		if rest == "" || rest == "/dev/null" || strings.HasSuffix(rest, " ***") {
			continue
		}
		names = append(names, rest)
	}
	return names
}

// stripPatchName applies patch's -p: strip removes that many leading
// components; without -p (strip < 0) patch keeps only the base name.
func stripPatchName(name string, strip int) string {
	if strip < 0 {
		return filepath.Base(name)
	}
	for i := 0; i < strip; i++ {
		slash := strings.IndexByte(name, '/')
		if slash < 0 {
			break
		}
		name = name[slash+1:]
	}
	return name
}

// sipsReadOptions only print image properties.
var sipsReadOptions = setOf("-g", "--getProperty", "-1", "--oneLine", "-h", "--help", "-H",
	"--helpProperties", "--formats", "--debug", "-v", "--verify")

// sips rewrites its input images unless every option only reads, or it is
// given an output file or folder.
func (s *bashScan) sips(argv []shWord) bashVerdict {
	args := argv[1:]
	readOnly := true
	var words []shWord
	for i := 0; i < len(args); i++ {
		t := args[i].text
		switch {
		case t == "--out" || t == "-o":
			if i+1 < len(args) {
				s.writes = append(s.writes, args[i+1])
			}
			return localVerdict()
		case strings.HasPrefix(t, "--out="):
			s.writes = append(s.writes, tailWord(args[i], len("--out=")))
			return localVerdict()
		case strings.HasPrefix(t, "-"):
			if !sipsReadOptions[t] {
				readOnly = false
			}
		default:
			words = append(words, args[i])
		}
	}
	if !readOnly {
		s.writes = append(s.writes, words...)
	}
	return localVerdict()
}

// textutil writes each converted file next to its input unless it is told to
// print (-stdout), only reads (-info), or is given an output file.
func (s *bashScan) textutil(argv []shWord) bashVerdict {
	args := argv[1:]
	converts := false
	var words []shWord
	for i := 0; i < len(args); i++ {
		t := args[i].text
		switch t {
		case "-stdout":
			return localVerdict()
		case "-output":
			if i+1 < len(args) {
				s.writes = append(s.writes, args[i+1])
			}
			return localVerdict()
		case "-convert", "-cat":
			converts = true
		default:
			if !strings.HasPrefix(t, "-") {
				words = append(words, args[i])
			}
		}
	}
	if converts {
		s.writes = append(s.writes, words...)
	}
	return localVerdict()
}
