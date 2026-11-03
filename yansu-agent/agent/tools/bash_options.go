package tools

import (
	"strings"
)

// This file reads the options of a command wherever they appear. Most tools
// parse options with GNU getopt, which permutes options and operands, so
// `rg hello --pre=./x a.txt`, `unzip x.zip -d dir` and `sort in -o out` all
// take an option after the first operand. Rules that look for an option that
// writes a file or runs a program read every word, not just the leading ones.

// optHit is one option of a command: its name ("-o", "--output") and the
// value it took, if any.
type optHit struct {
	name     string
	value    shWord
	hasValue bool
}

// scanOptions reads every option of a command whose parser permutes options
// and operands. shortValues lists the short letters that take a value: a
// cluster stops at its first such letter, which takes the rest of the cluster
// or the next word (`tar -xC/dir`, `sort -uo out`). longValues lists the long
// options that take a value; an abbreviation of one takes a value too. "--"
// ends the options.
func scanOptions(args []shWord, shortValues string, longValues map[string]bool) (hits []optHit, operands []shWord) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		switch {
		case t == "--":
			return hits, append(operands, args[i+1:]...)
		case strings.HasPrefix(t, "--"):
			name, _, hasValue := strings.Cut(t, "=")
			switch {
			case hasValue:
				hits = append(hits, optHit{name: name, value: tailWord(a, len(name)+1), hasValue: true})
			case longTakesValue(name, longValues) && i+1 < len(args):
				i++
				hits = append(hits, optHit{name: name, value: args[i], hasValue: true})
			default:
				hits = append(hits, optHit{name: name})
			}
		case strings.HasPrefix(t, "-") && len(t) > 1:
			for j := 1; j < len(t); j++ {
				name := "-" + string(t[j])
				if strings.IndexByte(shortValues, t[j]) < 0 {
					hits = append(hits, optHit{name: name})
					continue
				}
				switch {
				case j+1 < len(t):
					hits = append(hits, optHit{name: name, value: tailWord(a, j+1), hasValue: true})
				case i+1 < len(args):
					i++
					hits = append(hits, optHit{name: name, value: args[i], hasValue: true})
				default:
					hits = append(hits, optHit{name: name})
				}
				break
			}
		default:
			operands = append(operands, a)
		}
	}
	return hits, operands
}

// tailWord is the part of a word from byte n on, keeping its dynamic and
// pattern marks.
func tailWord(w shWord, n int) shWord {
	if n > len(w.text) {
		n = len(w.text)
	}
	return shWord{text: w.text[n:], dynamic: w.dynamic, pattern: w.pattern}
}

func longTakesValue(name string, longValues map[string]bool) bool {
	for long := range longValues {
		if optNameMatches(name, long) {
			return true
		}
	}
	return false
}

// optNameMatches reports whether an option as written names want: the same
// name, or for a long option an abbreviation of it (GNU getopt_long accepts
// `--out` for `--output`).
func optNameMatches(written, want string) bool {
	if written == want {
		return true
	}
	return strings.HasPrefix(want, "--") && strings.HasPrefix(written, "--") && len(written) > 3 &&
		strings.HasPrefix(want, written)
}

// optValues returns the values of every option named.
func optValues(hits []optHit, names ...string) []shWord {
	var out []shWord
	for _, h := range hits {
		if !h.hasValue {
			continue
		}
		for _, name := range names {
			if optNameMatches(h.name, name) {
				out = append(out, h.value)
				break
			}
		}
	}
	return out
}

// optSeen reports whether any option named appears, with or without a value.
func optSeen(hits []optHit, names ...string) bool {
	for _, h := range hits {
		for _, name := range names {
			if optNameMatches(h.name, name) {
				return true
			}
		}
	}
	return false
}

// flagValues returns the values of the named options anywhere in args, in
// the forms tools with single-dash long options accept (gcc, lipo, openssl,
// xcodebuild, go): "-o x", "-o=x", and for a one-letter name "-ox".
func flagValues(args []shWord, names ...string) []shWord {
	var out []shWord
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		if t == "--" {
			break
		}
		for _, name := range names {
			switch {
			case t == name:
				if i+1 < len(args) {
					out = append(out, args[i+1])
				}
			case strings.HasPrefix(t, name+"="):
				out = append(out, tailWord(a, len(name)+1))
			case len(name) == 2 && name[1] != '-' && strings.HasPrefix(t, name) && len(t) > 2:
				out = append(out, tailWord(a, 2))
			default:
			}
		}
	}
	return out
}

// mayBeOption reports a word the shell may turn into an option at run time:
// one whose first character comes from an expansion or a glob ("$X", "*",
// "{-o,x}"), so a variable holding "-o ~/x" or a file named "--pre=./x" in
// the folder reaches the command as an option.
func mayBeOption(w shWord) bool {
	if w.literal() {
		return false
	}
	if w.text == "" {
		return true
	}
	switch w.text[0] {
	case 0, '*', '?', '[', '{', '-':
		return true
	default:
	}
	return false
}

func anyMayBeOption(args []shWord) bool {
	for _, a := range args {
		if mayBeOption(a) {
			return true
		}
	}
	return false
}

// dynamicHead reports a word whose first character comes from an expansion,
// so the folder it names is not known until it runs.
func dynamicHead(w shWord) bool {
	return w.dynamic && (w.text == "" || w.text[0] == 0)
}

// bashOutputSpec says how a command takes the options that name files or
// folders it writes, or folders it works in.
type bashOutputSpec struct {
	// getopt marks a parser that clusters short options (`sort -uo out`):
	// shortValues and longValues describe its options for scanOptions.
	// Otherwise the options are single-dash words read with flagValues.
	getopt      bool
	shortValues string
	longValues  map[string]bool
	// outputs are the options whose value the command writes; dirs are the
	// options whose value is a folder later paths are relative to.
	outputs []string
	dirs    []string
}

var compilerOutputs = bashOutputSpec{outputs: []string{"-o", "-MF", "--output"}}

// bashOutputOptions are, per command, the options that write a file or set
// the folder it works in. classify reads every word of the command for them
// and checks each value as a write target.
var bashOutputOptions = map[string]bashOutputSpec{
	"sort": {getopt: true, shortValues: "kotST", longValues: setOf("--key", "--output", "--field-separator",
		"--buffer-size", "--temporary-directory", "--compress-program", "--files0-from", "--random-source",
		"--batch-size", "--parallel"), outputs: []string{"-o", "--output", "-T", "--temporary-directory"}},
	"shuf":    {getopt: true, shortValues: "ino", longValues: setOf("--input-range", "--head-count", "--output", "--random-source"), outputs: []string{"-o", "--output"}},
	"iconv":   {getopt: true, shortValues: "fto", longValues: setOf("--from-code", "--to-code", "--output"), outputs: []string{"-o", "--output"}},
	"base64":  {getopt: true, shortValues: "biow", longValues: setOf("--break", "--input", "--output", "--wrap"), outputs: []string{"-o", "--output"}},
	"tree":    {getopt: true, shortValues: "LPIoHT", longValues: setOf("--charset", "--filelimit", "--timefmt", "--sort", "--hintro", "--houtro"), outputs: []string{"-o"}},
	"less":    {getopt: true, shortValues: "bhjkoOpPtTxyz#", longValues: setOf("--log-file", "--LOG-FILE", "--lesskey-file", "--tag", "--pattern"), outputs: []string{"-o", "-O", "--log-file", "--LOG-FILE"}},
	"log":     {getopt: true, longValues: setOf("--output", "--last", "--start", "--size", "--device-name", "--device-udid", "--predicate", "--style"), outputs: []string{"--output"}},
	"mktemp":  {getopt: true, shortValues: "pt", longValues: setOf("--suffix"), outputs: []string{"-p", "--tmpdir"}},
	"zip":     {getopt: true, shortValues: "bntxiOPZs", longValues: setOf("--out", "--output-file", "--password", "--temp-path"), outputs: []string{"-O", "--out", "--output-file", "-b", "--temp-path"}},
	"xmllint": {outputs: []string{"--output", "-o"}},
	"gcc":     compilerOutputs, "cc": compilerOutputs, "clang": compilerOutputs, "g++": compilerOutputs,
	"clang++": compilerOutputs, "c++": compilerOutputs,
	"ld":         {outputs: []string{"-o", "-map", "-dependency_info", "-object_path_lto"}},
	"rustc":      {outputs: []string{"-o", "--out-dir"}},
	"lipo":       {outputs: []string{"-output"}},
	"esbuild":    {outputs: []string{"--outfile", "--outdir", "--metafile"}},
	"openssl":    {outputs: []string{"-out", "-keyout", "-writerand"}},
	"xcodebuild": {outputs: []string{"-derivedDataPath", "-archivePath", "-resultBundlePath", "-exportPath", "-clonedSourcePackagesDirPath", "-packageCachePath"}},
	"swift":      {outputs: []string{"--scratch-path", "--build-path", "--cache-path"}, dirs: []string{"--package-path"}},
	"tsc":        {outputs: []string{"--outDir", "--outFile", "--out", "--declarationDir", "--tsBuildInfoFile", "-outDir", "-outFile", "-out", "-declarationDir", "-tsBuildInfoFile"}},
	"vue-tsc":    {outputs: []string{"--outDir", "--outFile", "--out", "--declarationDir", "--tsBuildInfoFile", "-outDir", "-outFile", "-out", "-declarationDir", "-tsBuildInfoFile"}},
	"vite":       {outputs: []string{"--outDir"}},
	"rollup":     {outputs: []string{"-o", "--file", "-d", "--dir"}},
	"webpack":    {outputs: []string{"-o", "--output-path"}},
	"go": {outputs: []string{"-o", "--o", "-coverprofile", "--coverprofile", "-cpuprofile", "--cpuprofile",
		"-memprofile", "--memprofile", "-blockprofile", "--blockprofile", "-mutexprofile", "--mutexprofile",
		"-trace", "--trace", "-outputdir", "--outputdir"}, dirs: []string{"-C"}},
	"javac":    {outputs: []string{"-d", "-s", "-h"}},
	"kotlinc":  {outputs: []string{"-d"}},
	"zig":      {outputs: []string{"-femit-bin", "-femit-h", "-femit-docs"}},
	"dotnet":   {outputs: []string{"-o", "--output"}},
	"cargo":    {outputs: []string{"--target-dir", "--out-dir", "--artifact-dir"}, dirs: []string{"-C"}},
	"pytest":   {outputs: []string{"--junitxml", "--junit-xml", "--basetemp", "--result-log"}},
	"coverage": {outputs: []string{"-o", "-d", "--directory", "--data-file"}},
	"bazel":    {outputs: []string{"--output_base", "--output_user_root"}},
	"bazelisk": {outputs: []string{"--output_base", "--output_user_root"}},
	"cmake":    {outputs: []string{"-B"}},
	"ninja":    {dirs: []string{"-C"}},
	// -j, -l, --jobs and --load-average take a value only attached (-j4,
	// --jobs=4), so they are not value options here; otherwise "make -j -f x"
	// would swallow -f as the job count and never see the makefile.
	"make":  {getopt: true, shortValues: "CfoWI", longValues: setOf("--directory", "--file", "--makefile", "--old-file", "--what-if", "--include-dir"), dirs: []string{"-C", "--directory"}},
	"gmake": {getopt: true, shortValues: "CfoWI", longValues: setOf("--directory", "--file", "--makefile", "--old-file", "--what-if", "--include-dir"), dirs: []string{"-C", "--directory"}},
}

// outputTargets records the files and folders the command's output options
// name, wherever they appear among its words.
func (s *bashScan) outputTargets(name string, argv []shWord) {
	spec, ok := bashOutputOptions[name]
	if !ok {
		return
	}
	args := argv[1:]
	var outputs, dirs []shWord
	if spec.getopt {
		hits, _ := scanOptions(args, spec.shortValues, spec.longValues)
		outputs = optValues(hits, spec.outputs...)
		dirs = optValues(hits, spec.dirs...)
	} else {
		outputs = flagValues(args, spec.outputs...)
		dirs = flagValues(args, spec.dirs...)
	}
	s.writes = append(s.writes, outputs...)
	s.cds = append(s.cds, dirs...)
}

// bashFormatter describes a formatter or fixer that rewrites the files it is
// given: whether it does so by default, the options that make it write, and
// the options that keep it from writing.
type bashFormatter struct {
	writes     bool
	writeFlags []string
	checkFlags []string
	// subcommands are leading words that are not files (ruff format).
	subcommands map[string]bool
}

var bashFormatters = map[string]bashFormatter{
	"black":        {writes: true, checkFlags: []string{"--check", "--diff"}},
	"isort":        {writes: true, checkFlags: []string{"--check", "--check-only", "-c", "--diff"}},
	"rustfmt":      {writes: true, checkFlags: []string{"--check"}},
	"swiftformat":  {writes: true, checkFlags: []string{"--lint", "--dryrun"}},
	"gofmt":        {writeFlags: []string{"-w"}},
	"goimports":    {writeFlags: []string{"-w"}},
	"clang-format": {writeFlags: []string{"-i"}},
	"shfmt":        {writeFlags: []string{"-w", "--write"}},
	"prettier":     {writeFlags: []string{"--write", "-w"}},
	"eslint":       {writeFlags: []string{"--fix"}},
	"stylelint":    {writeFlags: []string{"--fix"}},
	"markdownlint": {writeFlags: []string{"--fix"}},
	"oxlint":       {writeFlags: []string{"--fix"}},
	"rubocop":      {writeFlags: []string{"-a", "-A", "--autocorrect", "--autocorrect-all", "-x"}},
	"swiftlint":    {writeFlags: []string{"--fix", "autocorrect"}, subcommands: setOf("lint", "autocorrect", "analyze")},
	"biome":        {writeFlags: []string{"--write", "--apply", "--apply-unsafe", "--fix"}, subcommands: setOf("format", "check", "lint", "ci")},
	"ruff":         {writeFlags: []string{"--fix", "format"}, checkFlags: []string{"--check", "--diff"}, subcommands: setOf("check", "format")},
}

// formatterTargets records the files a formatter rewrites in place.
func (s *bashScan) formatterTargets(name string, argv []shWord) {
	f, ok := bashFormatters[name]
	if !ok {
		return
	}
	writes := f.writes
	var files []shWord
	for _, a := range argv[1:] {
		t := a.text
		switch {
		case containsString(f.checkFlags, t) || containsPrefixFlag(f.checkFlags, t):
			return
		case containsString(f.writeFlags, t) || containsPrefixFlag(f.writeFlags, t):
			writes = true
			if !strings.HasPrefix(t, "-") {
				continue
			}
		case strings.HasPrefix(t, "-"):
		case f.subcommands[t] && len(files) == 0:
		default:
			files = append(files, a)
		}
	}
	if writes {
		if len(files) == 0 {
			files = []shWord{{text: "."}}
		}
		s.writes = append(s.writes, files...)
	}
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// containsPrefixFlag matches a long flag written with a value (--fix=true).
func containsPrefixFlag(list []string, word string) bool {
	for _, item := range list {
		if strings.HasPrefix(item, "--") && strings.HasPrefix(word, item+"=") {
			return true
		}
	}
	return false
}
