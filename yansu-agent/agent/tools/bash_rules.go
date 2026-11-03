package tools

import (
	"os"
	"path/filepath"
	"strings"

	"yansu-agent/agent/authority"
)

// This file holds the per-command rules of the Bash scanner: the wrappers it
// unwraps, the shells and interpreters whose code it reads, the named
// external and privileged commands, and the allowlist of read, build and
// workspace-file commands that run without asking.

func localVerdict() bashVerdict { return bashVerdict{kind: bashLocal} }

func unknownVerdict(subject, reason, trust string) bashVerdict {
	return bashVerdict{kind: bashUnknown, subject: subject, reason: reason, trust: trust}
}

func opaqueVerdict(subject, reason string) bashVerdict {
	return bashVerdict{kind: bashUnknown, subject: subject, reason: reason, opaque: true}
}

func actionVerdict(action authority.Action) bashVerdict {
	return bashVerdict{kind: bashAction, action: action, subject: action.Operation}
}

// bashReadCommands only read files, print text or inspect the system. Their
// arguments cannot make them write files or run other programs. Tools that
// also have an output option or a mode that changes the system (sort -o,
// lipo -output, pmset, diskutil, scutil --set, ...) have their own rule.
var bashReadCommands = setOf(
	"ls", "ll", "la", "dir", "vdir", "cat", "tac", "head", "tail", "wc", "grep", "egrep",
	"fgrep", "zgrep", "pwd", "echo", "printf", "true", "false",
	"test", "[", ":", "date", "cal", "whoami", "id", "groups", "uname",
	"sw_vers", "arch", "which", "whereis", "type", "stat", "du", "df", "diff",
	"cmp", "comm", "cut", "tr", "paste", "join", "column", "fold", "fmt", "nl", "rev",
	"expand", "unexpand", "basename", "dirname", "realpath", "readlink", "md5", "md5sum",
	"shasum", "sha1sum", "sha224sum", "sha256sum", "sha384sum", "sha512sum", "b2sum",
	"cksum", "sum", "base32", "hexdump", "od", "strings", "jq", "gojq",
	"more", "printenv", "seq", "sleep", "wait", "unset", "set", "shopt", "umask",
	"ulimit", "shift", "exit", "return", "break", "continue", "getopts", "times", "jobs",
	"fg", "bg", "disown", "uptime", "ps", "pgrep", "pstree", "lsof", "top", "vm_stat",
	"iostat", "netstat", "mdfind", "mdls", "nproc", "getconf", "locale",
	"tput", "clear", "yes", "expr", "bc", "factor", "numfmt", "otool", "nm",
	"objdump", "size", "where", "findstr", "ver", "apropos", "whatis", "look",
	"tsort", "zcat", "gzcat", "bzcat", "xzcat", "zstdcat", "zless", "zmore",
	"envsubst", "colrm", "units", "kill", "pkill", "killall", "pbcopy",
	"pbpaste", "fd", "fdfind", "sha3sum", "tldr",
	"system_profiler", "ioreg",
	"logname", "users", "w", "who", "last", "env", "nohup", "uuidgen",
	"stty", "tty", "dirs", "popd", "afinfo",
)

// bashBuildCommands build, test, lint or format the project in the work folder.
// They run project code the user can read, which this check accepts.
var bashBuildCommands = setOf(
	"tsc", "vite", "vitest", "jest", "mocha", "esbuild", "webpack", "rollup", "eslint",
	"prettier", "biome", "oxlint", "stylelint", "markdownlint", "pytest", "mypy", "ruff",
	"black", "isort", "flake8", "pylint", "pyright", "golangci-lint", "staticcheck",
	"govulncheck", "gofmt", "goimports", "gopls", "shellcheck", "shfmt", "swiftlint",
	"swiftformat", "rubocop", "clang-format", "rustfmt", "rustc", "gcc", "cc", "clang",
	"g++", "clang++", "c++", "javac", "kotlinc", "cmake", "ninja", "meson", "xcodebuild",
	"pre-commit", "yamllint", "actionlint", "hadolint", "tox", "nox", "playwright",
	"cypress", "turbo", "nx", "lerna", "wails", "protoc", "sqlc", "templ", "air",
	"ld", "ar", "ranlib", "pkg-config", "autoconf", "automake", "libtool", "bazel",
	"bazelisk", "gradle", "dotnet", "mix", "stack", "cabal", "dune", "zig",
	"node-gyp", "electron-builder", "storybook", "next", "nuxt", "astro", "svelte-kit",
	"vue-tsc", "ts-prune", "knip", "depcheck", "madge", "c8", "nyc", "istanbul",
	"coverage", "jsonlint", "markdown-link-check", "typos", "codespell",
)

// bashPublishWords mark a build-runner task or option that publishes,
// deploys or uploads (mvn deploy, gradle publish, lerna publish, buf push).
var bashPublishWords = []string{"publish", "deploy", "upload", "push", "release"}

// bashBuildRunners run tasks named on the command line; a task or option with
// a publish word is an external action.
var bashBuildRunners = setOf("gradle", "turbo", "nx", "lerna", "dotnet", "electron-builder", "mix",
	"cabal", "stack", "bazel", "bazelisk", "tox", "nox", "dune", "zig")

func setOf(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// splitOptions separates leading options from operands. valueFlags names the
// options that take a value ("-n", "--max-args"); a short option in a cluster
// takes the rest of the cluster or the next word.
func splitOptions(args []shWord, valueFlags map[string]bool) (opts []shWord, rest []shWord) {
	i := 0
	for i < len(args) {
		a := args[i]
		t := a.text
		if t == "--" {
			return opts, args[i+1:]
		}
		if !strings.HasPrefix(t, "-") || t == "-" {
			break
		}
		opts = append(opts, a)
		if strings.HasPrefix(t, "--") {
			if !strings.Contains(t, "=") && valueFlags[t] && i+1 < len(args) {
				opts = append(opts, args[i+1])
				i += 2
				continue
			}
			i++
			continue
		}
		for j := 1; j < len(t); j++ {
			if valueFlags["-"+string(t[j])] {
				if j == len(t)-1 && i+1 < len(args) {
					opts = append(opts, args[i+1])
					i++
				}
				break
			}
		}
		i++
	}
	return opts, args[i:]
}

// optionValue returns the value of option name (short "-o" or long "--out")
// among opts, in separate, attached or --name=value form.
func optionValue(opts []shWord, names ...string) (shWord, bool) {
	var found shWord
	ok := false
	for i := 0; i < len(opts); i++ {
		t := opts[i].text
		for _, name := range names {
			switch {
			case t == name:
				if i+1 < len(opts) {
					found, ok = opts[i+1], true
				}
			case strings.HasPrefix(name, "--") && strings.HasPrefix(t, name+"="):
				found = shWord{text: t[len(name)+1:], dynamic: opts[i].dynamic, pattern: opts[i].pattern}
				ok = true
			case len(name) == 2 && !strings.HasPrefix(t, "--") && strings.HasPrefix(t, name) && len(t) > 2:
				found = shWord{text: t[2:], dynamic: opts[i].dynamic, pattern: opts[i].pattern}
				ok = true
			default:
			}
		}
	}
	return found, ok
}

func hasOption(opts []shWord, names ...string) bool {
	for _, o := range opts {
		for _, name := range names {
			if o.text == name || (strings.HasPrefix(name, "--") && strings.HasPrefix(o.text, name+"=")) {
				return true
			}
		}
	}
	return false
}

// shortCluster reports whether any short-option cluster in opts holds letter.
func shortCluster(opts []shWord, letter byte) bool {
	for _, o := range opts {
		t := o.text
		if len(t) > 1 && t[0] == '-' && t[1] != '-' && strings.IndexByte(t[1:], letter) >= 0 {
			return true
		}
	}
	return false
}

func anyDynamic(args []shWord) bool {
	for _, a := range args {
		if !a.literal() {
			return true
		}
	}
	return false
}

func joinTexts(args []shWord) (string, bool) {
	texts, ok := literalTexts(args)
	if !ok {
		return "", false
	}
	return strings.Join(texts, " "), true
}

func (s *bashScan) recordDynamicWrapper(name string, argv []shWord) {
	switch name {
	case "env", "nice", "nohup", "timeout", "gtimeout", "command", "exec", "time", "gtime", "sudo":
		if anyDynamic(argv[1:]) {
			s.dynamicWrappers = append(s.dynamicWrappers, argv)
			s.opaque(display(argv), "its wrapper arguments are not literal words")
		}
	default:
	}
}

// unwrap handles wrappers. It returns the wrapped command to classify next, or
// handled=true when it classified everything itself, or (nil, false) when
// name is not a wrapper.
func (s *bashScan) unwrap(name string, argv []shWord, in bashStdin, depth int) ([]shWord, bashStdin, bool) {
	args := argv[1:]
	switch name {
	case "env":
		return s.unwrapEnv(argv, in, depth)
	case "nohup", "setsid", "chronic", "unbuffer", "noglob", "nocorrect", "builtin",
		"torsocks", "nocache", "catchsegv":
		_, rest := splitOptions(args, nil)
		return s.wrapped(argv, rest, in)
	case "command":
		opts, rest := splitOptions(args, nil)
		if shortCluster(opts, 'v') || shortCluster(opts, 'V') {
			s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
			s.add(localVerdict())
			return nil, in, true
		}
		return s.wrapped(argv, rest, in)
	case "exec":
		_, rest := splitOptions(args, setOf("-a"))
		if len(rest) == 0 {
			return nil, in, true
		}
		return rest, in, false
	case "time", "gtime":
		opts, rest := splitOptions(args, setOf("-o", "-f", "--output", "--format"))
		if v, ok := optionValue(opts, "-o", "--output"); ok {
			s.writes = append(s.writes, v)
		}
		return s.wrapped(argv, rest, in)
	case "xcrun":
		return s.unwrapXcrun(argv, in)
	case "hyperfine":
		s.hyperfine(argv, depth)
		return nil, in, true
	case "timeout", "gtimeout":
		_, rest := splitOptions(args, setOf("-s", "-k", "--signal", "--kill-after"))
		if len(rest) > 0 {
			rest = rest[1:]
		}
		return s.wrapped(argv, rest, in)
	case "nice":
		_, rest := splitOptions(args, setOf("-n", "--adjustment"))
		return s.wrapped(argv, rest, in)
	case "caffeinate":
		_, rest := splitOptions(args, setOf("-t", "-w"))
		return s.wrapped(argv, rest, in)
	case "stdbuf":
		_, rest := splitOptions(args, setOf("-i", "-o", "-e", "--input", "--output", "--error"))
		return s.wrapped(argv, rest, in)
	case "ionice":
		_, rest := splitOptions(args, setOf("-c", "-n", "-p", "-P", "-u", "--class", "--classdata"))
		return s.wrapped(argv, rest, in)
	case "proxychains", "proxychains4":
		_, rest := splitOptions(args, setOf("-f"))
		return s.wrapped(argv, rest, in)
	case "sudo", "doas", "pkexec", "run0":
		return s.unwrapSudo(name, argv, in, depth)
	case "su":
		opts, _ := splitOptions(args, setOf("-c", "--command", "-s", "--shell", "-g", "--group", "-G", "-w"))
		s.unknown(name, "it runs with another account's rights", name)
		if code, ok := optionValue(opts, "-c", "--command"); ok {
			if !code.literal() {
				s.opaque(display(argv), "the shell code it runs is not a literal")
			} else {
				s.scanNested(display(argv), code.text, depth)
			}
		} else {
			s.opaque(display(argv), "it starts a shell")
		}
		return nil, in, true
	case "xargs":
		s.xargs(argv, in, depth)
		return nil, in, true
	case "find", "gfind", "fd", "fdfind":
		s.find(name, argv, depth)
		return nil, in, true
	case "eval":
		code, ok := joinTexts(args)
		if !ok {
			s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
			s.opaque(display(argv), "it evaluates text built at run time")
			return nil, in, true
		}
		s.scanNested(display(argv), code, depth)
		return nil, in, true
	case "source", ".":
		s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
		s.opaque(display(argv), "it runs a script file in this shell")
		return nil, in, true
	case "trap":
		if len(args) == 0 || args[0].text == "-l" || args[0].text == "-p" {
			s.add(localVerdict())
		} else {
			s.opaque(display(argv), "it sets a trap that runs code later")
		}
		return nil, in, true
	case "alias":
		for _, a := range args {
			if strings.Contains(a.text, "=") || !a.literal() {
				s.opaque(display(argv), "it defines an alias")
				return nil, in, true
			}
		}
		s.add(localVerdict())
		return nil, in, true
	case "watch":
		opts, rest := splitOptions(args, setOf("-n", "--interval"))
		if hasOption(opts, "-x", "--exec") {
			return s.wrapped(argv, rest, bashStdin{})
		}
		if len(rest) == 0 {
			return nil, in, true
		}
		code, ok := joinTexts(rest)
		if !ok {
			s.opaque(display(argv), "the command it repeats is not a literal")
			return nil, in, true
		}
		s.scanNested(display(argv), code, depth)
		return nil, in, true
	case "script":
		opts, rest := splitOptions(args, setOf("-c", "--command", "-t", "-T", "-I", "-O", "-B",
			"-E", "--timing", "--log-in", "--log-out", "--log-io", "--log-timing", "--echo",
			"--logging-format"))
		if code, ok := optionValue(opts, "-c", "--command"); ok {
			if !code.literal() {
				s.opaque(display(argv), "the command it records is not a literal")
			} else {
				s.scanNested(display(argv), code.text, depth)
			}
			return nil, in, true
		}
		if len(rest) == 0 {
			s.opaque(display(argv), "it starts a shell")
			return nil, in, true
		}
		s.writes = append(s.writes, rest[0])
		if len(rest) == 1 {
			s.opaque(display(argv), "it starts a shell")
			return nil, in, true
		}
		return rest[1:], in, false
	case "flock":
		_, rest := splitOptions(args, setOf("-w", "--wait", "--timeout", "-E", "--conflict-exit-code"))
		if len(rest) <= 1 {
			s.add(localVerdict())
			return nil, in, true
		}
		rest = rest[1:]
		if rest[0].text == "-c" || rest[0].text == "--command" {
			if len(rest) < 2 || !rest[1].literal() {
				s.opaque(display(argv), "the command it locks is not a literal")
			} else {
				s.scanNested(display(argv), rest[1].text, depth)
			}
			return nil, in, true
		}
		return rest, in, false
	case "busybox", "toybox":
		if len(args) == 0 || strings.HasPrefix(args[0].text, "-") {
			s.add(localVerdict())
			return nil, in, true
		}
		return args, in, false
	case "parallel", "entr", "watchexec", "nodemon", "tmux", "screen", "dtach", "abduco",
		"runuser", "chroot", "unshare", "nsenter", "firejail", "sandbox-exec", "systemd-run",
		"launchctl-asuser", "open-in-terminal", "expect":
		s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
		s.opaque(display(argv), "it runs commands this check cannot follow")
		return nil, in, true
	case "sh", "bash", "zsh", "dash", "ksh", "ash", "mksh", "yash", "posh", "rbash", "lksh":
		s.shell(name, argv, in, depth)
		return nil, in, true
	case "pwsh", "powershell", "cmd", "fish", "csh", "tcsh", "nu", "xonsh", "elvish", "rc", "es":
		s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
		if len(args) == 1 && (args[0].text == "--version" || args[0].text == "-v") {
			s.add(localVerdict())
		} else {
			s.opaque(display(argv), "it runs a shell whose code this check cannot read")
		}
		return nil, in, true
	default:
	}
	return nil, in, false
}

// unwrapXcrun continues with the developer tool xcrun runs. Its options that
// only print a path (-f, --show-sdk-path, ...) run nothing.
func (s *bashScan) unwrapXcrun(argv []shWord, in bashStdin) ([]shWord, bashStdin, bool) {
	opts, rest := splitOptions(argv[1:], setOf("-sdk", "--sdk", "-toolchain", "--toolchain"))
	for _, o := range opts {
		if !o.literal() {
			s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
			s.opaque(display(argv), "its options are not literal words")
			return nil, in, true
		}
		switch o.text {
		case "-f", "--find", "--show-sdk-path", "--show-sdk-version", "--show-sdk-build-version",
			"--show-sdk-platform-path", "--show-sdk-platform-version", "--version", "-version", "-h", "--help":
			s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
			s.add(localVerdict())
			return nil, in, true
		default:
		}
	}
	return s.wrapped(argv, rest, in)
}

// hyperfine runs each command operand, and its --prepare, --setup,
// --conclude and --cleanup commands, through a shell. A parameter list or a
// custom shell rewrites those commands, so they cannot be read.
func (s *bashScan) hyperfine(argv []shWord, depth int) {
	s.cmds = append(s.cmds, bashCommand{argv: argv})
	hits, operands := scanOptions(argv[1:], "wmMrpcsSPLDnu", setOf("--warmup", "--min-runs",
		"--max-runs", "--runs", "--prepare", "--conclude", "--setup", "--cleanup", "--shell",
		"--parameter-scan", "--parameter-step-size", "--parameter-list", "--style", "--sort",
		"--time-unit", "--export-asciidoc", "--export-csv", "--export-json", "--export-markdown",
		"--export-orgmode", "--output", "--input", "--command-name", "--reference"))
	if optSeen(hits, "-P", "--parameter-scan", "-L", "--parameter-list", "-D", "--parameter-step-size", "-S", "--shell") {
		s.opaque(display(argv), "it rewrites the commands it runs")
		return
	}
	s.writes = append(s.writes, optValues(hits, "--export-asciidoc", "--export-csv", "--export-json",
		"--export-markdown", "--export-orgmode")...)
	for _, out := range optValues(hits, "--output") {
		switch out.text {
		case "null", "pipe", "inherit":
		default:
			s.writes = append(s.writes, out)
		}
	}
	commands := append(optValues(hits, "-p", "--prepare", "-c", "--cleanup", "-s", "--setup", "--conclude", "--reference"), operands...)
	for _, c := range commands {
		if !c.literal() {
			s.opaque(display(argv), "a command it runs is not a literal")
			continue
		}
		s.scanNested(display(argv), c.text, depth)
	}
	s.add(localVerdict())
}

// wrapped continues with the wrapped command, or classifies a wrapper that
// was given no command as local.
func (s *bashScan) wrapped(argv, rest []shWord, in bashStdin) ([]shWord, bashStdin, bool) {
	if len(rest) == 0 {
		s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
		s.add(localVerdict())
		return nil, in, true
	}
	return rest, in, false
}

func (s *bashScan) unwrapEnv(argv []shWord, in bashStdin, depth int) ([]shWord, bashStdin, bool) {
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		if !a.literal() {
			s.opaque(display(argv), "its arguments are not literal words")
			return nil, in, true
		}
		switch {
		case t == "--":
			return s.wrapped(argv, args[i+1:], in)
		case t == "-":
			continue
		case strings.HasPrefix(t, "--"):
			name, value, hasValue := strings.Cut(t, "=")
			switch name {
			case "--split-string":
				if !hasValue {
					if i+1 >= len(args) {
						return nil, in, true
					}
					i++
					value = args[i].text
				}
				return s.envSplit(argv, value, args[i+1:], depth, in)
			case "--unset", "--chdir":
				dir := shWord{text: value}
				if !hasValue {
					i++
					if i < len(args) {
						dir = args[i]
					}
				}
				if name == "--chdir" {
					s.cds = append(s.cds, dir)
				}
			default:
			}
		case strings.HasPrefix(t, "-"):
			for j := 1; j < len(t); j++ {
				switch t[j] {
				case 'S':
					value := t[j+1:]
					next := i + 1
					if value == "" {
						if next >= len(args) {
							return nil, in, true
						}
						value = args[next].text
						next++
					}
					return s.envSplit(argv, value, args[next:], depth, in)
				case 'u', 'C':
					dir := shWord{text: t[j+1:]}
					if dir.text == "" && i+1 < len(args) {
						i++
						dir = args[i]
					}
					if t[j] == 'C' {
						s.cds = append(s.cds, dir)
					}
					j = len(t)
				case 'P':
					s.opaque(display(argv), "it changes the program search path")
					return nil, in, true
				default:
				}
			}
		case isAssignment(t):
			if name := t[:strings.IndexByte(t, '=')]; dangerousEnvAssign(name, shWord{text: t[strings.IndexByte(t, '=')+1:]}) {
				s.opaque(display(argv), "it changes "+name+", which decides what the command runs")
				return nil, in, true
			}
		default:
			return args[i:], in, false
		}
	}
	return s.wrapped(argv, nil, in)
}

// envSplit handles env -S: the string is split into words the way a shell
// would, followed by the remaining arguments.
func (s *bashScan) envSplit(argv []shWord, value string, rest []shWord, depth int, in bashStdin) ([]shWord, bashStdin, bool) {
	tail, ok := joinTexts(rest)
	if !ok {
		s.opaque(display(argv), "its arguments are not literal words")
		return nil, in, true
	}
	s.scanNested(display(argv), strings.TrimSpace(value+" "+tail), depth)
	return nil, in, true
}

func isAssignment(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	for j, r := range s[:eq] {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (j > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func (s *bashScan) unwrapSudo(name string, argv []shWord, in bashStdin, depth int) ([]shWord, bashStdin, bool) {
	valueFlags := setOf("-u", "-g", "-C", "-D", "-h", "-p", "-r", "-t", "-T", "-U", "--user",
		"--group", "--close-from", "--chdir", "--host", "--prompt", "--role", "--type",
		"--command-timeout", "--other-user")
	opts, rest := splitOptions(argv[1:], valueFlags)
	if len(rest) == 0 {
		if shortCluster(opts, 'l') || shortCluster(opts, 'v') || shortCluster(opts, 'k') ||
			shortCluster(opts, 'K') || shortCluster(opts, 'V') {
			s.add(localVerdict())
			return nil, in, true
		}
		s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
		s.opaque(display(argv), "it starts a shell with administrator rights")
		return nil, in, true
	}
	s.unknown(name, "it runs with administrator rights", name)
	if name == "sudo" && (shortCluster(opts, 's') || shortCluster(opts, 'i') ||
		hasOption(opts, "--shell", "--login")) {
		code, ok := joinTexts(rest)
		if !ok {
			s.opaque(display(argv), "the shell code it runs is not a literal")
			return nil, in, true
		}
		s.scanNested(display(argv), code, depth)
		return nil, in, true
	}
	if name == "sudo" && shortCluster(opts, 'e') {
		s.unknown(display(argv), "it edits files with administrator rights", "")
		return nil, in, true
	}
	return rest, in, false
}

func (s *bashScan) xargs(argv []shWord, in bashStdin, depth int) {
	opts, rest := splitOptions(argv[1:], setOf("-a", "-d", "-E", "-I", "-J", "-L", "-n", "-P",
		"-R", "-S", "-s", "--arg-file", "--delimiter", "--eof", "--max-lines", "--max-args",
		"--max-procs", "--max-chars", "--process-slot-var"))
	repl := ""
	if v, ok := optionValue(opts, "-I", "-J", "--replace"); ok {
		repl = v.text
	} else if hasOption(opts, "--replace") || shortCluster(opts, 'i') {
		repl = "{}"
	}
	if len(rest) == 0 {
		rest = []shWord{{text: "echo"}}
	}
	// The words xargs reads are paths under a known folder when its input
	// lists files (find ., git ls-files); otherwise they are anything.
	input := in.paths + "\x00"
	pathRoot := ""
	if hasOption(opts, "--null") || shortCluster(opts, '0') {
		pathRoot = in.pathRoot
	}
	inner := make([]shWord, 0, len(rest)+1)
	for _, w := range rest {
		if repl != "" && strings.Contains(w.text, repl) {
			provenance := ""
			if w.literal() && w.text == repl {
				provenance = pathRoot
			}
			replaced := strings.ReplaceAll(w.text, repl, "\x00")
			if strings.HasPrefix(w.text, repl) {
				replaced = in.paths + replaced
			}
			w = shWord{text: replaced, dynamic: true, pathRoot: provenance}
		}
		inner = append(inner, w)
	}
	if repl == "" {
		// xargs appends the words it reads from its input.
		inner = append(inner, shWord{text: input, dynamic: true, pathRoot: pathRoot})
	}
	s.run(inner, bashStdin{}, depth)
}

// find classifies the commands find runs with -exec, -execdir, -ok and -okdir
// (fd with -x and -X), and the files find itself deletes or writes.
func (s *bashScan) find(name string, argv []shWord, depth int) {
	args := argv[1:]
	findArgs := []shWord{argv[0]}
	roots := traversalRoots(name, argv)
	traversal := bashFindTraversal{roots: roots, shown: display(argv)}
	prefix := s.listedPathPrefix(name, argv)
	pathRoot := s.traversalPathRoot(roots)
	if traversalFollowsLinks(name, argv) {
		pathRoot = ""
	}
	deletes := false
	for i := 0; i < len(args); i++ {
		t := args[i].text
		execFlag := traversalExecFlag(name, t)
		switch name {
		case "find", "gfind":
			switch t {
			case "-delete":
				deletes = true
			case "-fprint", "-fprint0", "-fprintf", "-fls":
				if i+1 < len(args) {
					s.writes = append(s.writes, args[i+1])
				}
			default:
			}
		default:
			// fd normally deletes through -x/-X. A literal -delete is not
			// accepted as an escape from the traversal policy either.
			deletes = deletes || t == "-delete"
		}
		if !execFlag {
			findArgs = append(findArgs, args[i])
			continue
		}
		var inner []shWord
		placeholder := false
		j := i + 1
		for ; j < len(args); j++ {
			if args[j].text == ";" || args[j].text == "+" || args[j].text == `\;` {
				break
			}
			w := args[j]
			if strings.Contains(w.text, "{") && strings.Contains(w.text, "}") {
				// A found path takes the placeholder's place: it starts
				// with the root when find has one literal root.
				w = substituteTraversalPlaceholder(w, prefix, pathRoot)
				placeholder = true
			}
			inner = append(inner, w)
		}
		if name != "find" && name != "gfind" && !placeholder {
			inner = append(inner, shWord{text: prefix + "\x00", dynamic: true, pathRoot: pathRoot})
			placeholder = true
		}
		firstCommand := len(s.cmds)
		s.run(inner, bashStdin{}, depth)
		if placeholder || t == "-execdir" || t == "-okdir" {
			for _, command := range s.cmds[firstCommand:] {
				traversal.destructive = traversal.destructive || destructiveTraversalCommand(command)
			}
		}
		i = j
	}
	if deletes {
		s.writes = append(s.writes, roots...)
		traversal.destructive = true
	}
	// find -L follows symlinks; find -type l -exec reads the links it finds.
	// Either way a link under the roots that resolves into a protected folder
	// is read through, so record the roots for the symlink walk.
	if name == "find" || name == "gfind" {
		followL, typeL, execd := false, false, false
		for i := 0; i < len(args); i++ {
			switch args[i].text {
			case "-L":
				followL = true
			case "-type":
				if i+1 < len(args) && args[i+1].text == "l" {
					typeL = true
				}
			case "-exec", "-execdir", "-ok", "-okdir":
				execd = true
			default:
			}
		}
		if followL || (typeL && execd) {
			rootsL := findGlobalRoots(args)
			if len(rootsL) == 0 {
				rootsL = []shWord{{text: "."}}
			}
			s.followReadRoots = append(s.followReadRoots, rootsL...)
		}
	}
	s.findTraversals = append(s.findTraversals, traversal)
	s.cmds = append(s.cmds, bashCommand{argv: findArgs})
	s.pathWords(name, findArgs)
	s.add(localVerdict())
}

// findGlobalRoots returns positional roots and BSD -f roots. GNU runtime
// root lists remain unknown; never treat them as the default working folder.
func findGlobalRoots(args []shWord) []shWord {
	// GNU permits this input option among expression options as well as
	// before roots. Do not confuse a nested -exec argument with a find option.
	for i := 0; i < len(args); i++ {
		if traversalExecFlag("find", args[i].text) {
			for i++; i < len(args) && args[i].text != ";" && args[i].text != "+" && args[i].text != `\;`; i++ {
			}
			continue
		}
		if args[i].text == "-files0-from" {
			return []shWord{{text: "\x00", dynamic: true}}
		}
	}
	var out []shWord
	i := 0
	for i < len(args) {
		t := args[i].text
		switch t {
		case "-L", "-H", "-P", "-s", "-x", "-d", "-E", "-X":
			i++
		case "-D":
			i += 2
		case "-f":
			if i+1 >= len(args) {
				return append(out, shWord{text: "\x00", dynamic: true})
			}
			out = append(out, args[i+1])
			i += 2
		case "--":
			i++
			goto roots
		case "-files0-from":
			return append(out, shWord{text: "\x00", dynamic: true})
		default:
			if strings.HasPrefix(t, "-O") {
				i++
				continue
			}
			goto roots
		}
	}
roots:
	for ; i < len(args); i++ {
		t := args[i].text
		if t == "-files0-from" {
			return append(out, shWord{text: "\x00", dynamic: true})
		}
		if strings.HasPrefix(t, "-") || t == "(" || t == "!" {
			break
		}
		out = append(out, args[i])
	}
	return out
}

// shell reads the code a POSIX shell will run: the -c string, a here-doc,
// here-string or literal pipe input. A script file or unknown input is opaque.
func (s *bashScan) shell(name string, argv []shWord, in bashStdin, depth int) {
	args := argv[1:]
	dashC := false
	i := 0
loop:
	for ; i < len(args); i++ {
		a := args[i]
		if !a.literal() {
			break
		}
		t := a.text
		switch {
		case t == "--" || t == "-":
			i++
			break loop
		case t == "-o" || t == "+o" || t == "-O" || t == "+O" || t == "--rcfile" || t == "--init-file":
			i++
		case strings.HasPrefix(t, "--"):
		case strings.HasPrefix(t, "-") && len(t) > 1:
			if strings.IndexByte(t[1:], 'c') >= 0 {
				dashC = true
			}
		case strings.HasPrefix(t, "+") && len(t) > 1:
		default:
			break loop
		}
	}
	rest := args[min(i, len(args)):]
	s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
	if dashC {
		if len(rest) == 0 {
			s.add(localVerdict())
			return
		}
		if !rest[0].literal() {
			s.opaque(display(argv), "the shell code it runs is not a literal")
			return
		}
		s.scanNested(display(argv), rest[0].text, depth)
		return
	}
	if len(rest) > 0 {
		s.opaque(display(argv), "it runs a script file")
		return
	}
	if versionOnly(args) {
		s.add(localVerdict())
		return
	}
	switch {
	case !in.present:
		s.opaque(display(argv), "it starts a shell that reads commands from its input")
	case !in.known:
		s.opaque(display(argv), "it runs commands read from "+in.from)
	default:
		s.scanNested(display(argv), in.text, depth)
	}
}

func versionOnly(args []shWord) bool {
	if len(args) == 0 {
		return false
	}
	for _, a := range args {
		switch a.text {
		case "--version", "-V", "-v", "--help", "-h", "-version", "version":
		default:
			return false
		}
	}
	return true
}

// classify records the resolved command and its verdict.
func (s *bashScan) classify(name string, argv []shWord, in bashStdin, depth int) {
	s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
	if recursiveRead(name, argv[1:]) {
		for _, w := range argv[1:] {
			s.args = append(s.args, scanArg{word: w, recursive: true})
		}
		if _, operands := scanOptions(argv[1:], "", nil); len(operands) <= 1 && searchCommands[name] {
			s.recursiveNoPath = append(s.recursiveNoPath, name)
		}
	}
	v := s.rule(name, argv, in, depth)
	s.outputTargets(name, argv)
	s.formatterTargets(name, argv)
	s.pathWords(name, argv)
	v.detail = display(argv)
	s.add(v)
}

// bashNonPathCommands take no file or folder operands, so a word built at
// run time is only text to them.
var bashNonPathCommands = setOf("echo", "printf", "true", "false", ":", "test", "[", "expr", "seq",
	"sleep", "date", "cal", "basename", "dirname", "printenv", "set", "unset", "shift", "exit",
	"return", "break", "continue", "getopts", "times", "jobs", "fg", "bg", "disown", "wait", "kill",
	"pkill", "killall", "pgrep", "yes", "bc", "factor", "numfmt", "tput", "clear", "uuidgen", "read",
	"mapfile", "readarray", "hash", "type", "which", "whereis", "command", "units", "alias", "umask",
	"ulimit", "shopt", "whoami", "id", "groups", "uname", "arch", "sw_vers", "ps", "pstree", "uptime",
	"nproc", "getconf", "locale", "tty", "stty", "logname", "users", "w", "who", "last", "dirs",
	"popd", "apropos", "whatis", "tldr", "history", "compgen", "complete", "pbcopy", "pbpaste",
	"gh", "curl", "wget", "opencli", "yansu", "ping", "ping6", "dig", "host", "nslookup",
	"traceroute", "traceroute6", "whois", "mtr", "drill")

// bashTextOptions are, per command, options whose values are text rather
// than paths (a commit message, a jq variable), with the number of words
// each takes.
var bashTextOptions = map[string]map[string]int{
	"git": {"-m": 1, "--message": 1, "--grep": 1, "--author": 1, "--committer": 1, "-S": 1, "-G": 1,
		"--format": 1, "--pretty": 1, "--date": 1, "--since": 1, "--until": 1},
	"jq":   {"--arg": 2, "--argjson": 2},
	"gojq": {"--arg": 2, "--argjson": 2},
}

// pathWords makes a command unknown when one of its words starts with a value
// known only at run time: a path built that way can reach any folder,
// Isoform's data folder included, so it is not left to the name checks.
func (s *bashScan) pathWords(name string, argv []shWord) {
	if bashNonPathCommands[name] {
		return
	}
	text := bashTextOptions[name]
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		t := args[i].text
		if n, ok := text[t]; ok {
			i += n
			continue
		}
		if opt, _, ok := strings.Cut(t, "="); ok && text[opt] == 1 {
			continue
		}
		if len(t) > 2 && t[0] == '-' && t[1] != '-' && text[t[:2]] == 1 {
			continue
		}
		if dynamicHead(args[i]) || s.mayReachProtected(args[i]) {
			s.unknown(display(argv), "it reads or writes a path built at run time, which could reach Isoform's data folder", "")
			return
		}
	}
}

// isArOperation reports an ar operation code (rcs, t, x, dmpqrsa...) rather
// than a file name: it is short, all letters from ar's op/modifier set, and
// holds an action letter.
func isArOperation(text string) bool {
	text = strings.TrimLeft(text, "-+")
	if text == "" || len(text) > 8 {
		return false
	}
	action := false
	for _, r := range text {
		if strings.IndexRune("dmpqrstxabcDfiloNPsSTuvV", r) < 0 {
			return false
		}
		if strings.IndexRune("dmpqrtx", r) >= 0 {
			action = true
		}
	}
	return action
}

func (s *bashScan) rule(name string, argv []shWord, in bashStdin, depth int) bashVerdict {
	args := argv[1:]
	if bashOptionSensitive[name] && anyMayBeOption(args) {
		return unknownVerdict(display(argv), "a word built at run time could become an option that writes a file or runs a program", "")
	}
	switch name {
	case "git":
		return s.git(argv, depth)
	case "gh":
		return ghVerdict(argv)
	case "curl":
		return s.curl(argv)
	case "wget":
		return s.wget(argv)
	case "yansu":
		return yansuVerdict(argv)
	case "opencli":
		return actionVerdict(externalBashAction("connector.command.execute", "opencli"))
	case "open", "shortcuts":
		if launcherReadOnly(name, args) {
			return localVerdict()
		}
		return unknownVerdict(name, "it can start a process outside the shell sandbox", "")
	case "osascript":
		if launcherReadOnly(name, args) {
			return localVerdict()
		}
		return actionVerdict(privilegedBashAction("computer.apple_event.execute", "osascript"))
	case "ssh", "scp", "sftp", "mosh", "autossh", "sshpass":
		return actionVerdict(externalBashAction("remote.shell.execute", name))
	case "nc", "ncat", "netcat", "socat", "telnet", "nmap", "masscan":
		return actionVerdict(externalBashAction("remote.shell.execute", name))
	case "ftp", "tftp", "lftp", "smbclient", "aria2c", "axel":
		return actionVerdict(externalBashAction("network.request.write", name))
	case "mail", "mailx", "sendmail", "msmtp", "mutt", "swaks":
		return actionVerdict(externalBashAction("mail.send", name))
	case "rsync":
		return s.rsync(argv)
	case "openssl":
		return opensslVerdict(argv)
	case "crontab":
		opts, rest := splitOptions(args, setOf("-u"))
		if len(rest) == 0 && shortCluster(opts, 'l') {
			return localVerdict()
		}
		return actionVerdict(externalBashAction("os.schedule.write", "crontab"))
	case "at", "batch", "atrm":
		if name == "at" && len(args) == 1 && (args[0].text == "-l" || args[0].text == "-V") {
			return localVerdict()
		}
		return actionVerdict(externalBashAction("os.schedule.write", name))
	case "atq":
		return localVerdict()
	case "launchctl":
		_, rest := splitOptions(args, nil)
		if len(rest) == 0 {
			return localVerdict()
		}
		switch rest[0].text {
		case "list", "print", "print-disabled", "print-cache", "version", "help", "blame",
			"procinfo", "dumpstate", "managerpid", "manageruid", "managername", "getenv":
			return localVerdict()
		default:
		}
		return actionVerdict(externalBashAction("os.schedule.write", "launchctl "+rest[0].text))
	case "systemctl":
		_, rest := splitOptions(args, nil)
		if len(rest) > 0 {
			switch rest[0].text {
			case "status", "list-units", "list-timers", "list-unit-files", "show", "cat",
				"is-active", "is-enabled", "is-failed":
				return localVerdict()
			default:
			}
		}
		return unknownVerdict("systemctl", "it changes system services", "systemctl")
	case "npx", "bunx", "pnpx", "uvx":
		return actionVerdict(externalBashAction("package.run", name))
	case "pipx":
		_, rest := splitOptions(args, nil)
		if len(rest) > 0 && rest[0].text == "run" {
			return actionVerdict(externalBashAction("package.run", "pipx run"))
		}
		return packageReadVerdict(name, rest, "list", "--version", "environment")
	case "npm", "pnpm", "yarn", "bun":
		return s.nodePackage(name, argv, in, depth)
	case "go":
		return goVerdict(argv)
	case "cargo":
		return cargoVerdict(argv)
	case "pip", "pip3", "uv", "poetry", "pdm", "hatch", "gem", "brew", "twine", "conda", "mamba":
		return packageManagerVerdict(name, argv)
	case "docker", "podman", "nerdctl":
		return dockerVerdict(name, argv)
	case "make", "gmake":
		if in.present {
			return opaqueVerdict(display(argv), "it reads a makefile from its input")
		}
		return makeVerdict(argv)
	case "mvn", "mvnw":
		return s.mvn(argv)
	case "buf":
		return s.buf(argv)
	case "protoc":
		return s.protoc(argv)
	case "cmake":
		for _, a := range args {
			if a.text == "-P" || a.text == "-E" {
				return opaqueVerdict(display(argv), "it runs a CMake script or command")
			}
		}
		return localVerdict()
	case "gcc", "cc", "clang", "g++", "clang++", "c++", "ld":
		return s.compiler(argv)
	case "rustc":
		return s.rustc(argv)
	case "terraform", "tofu", "pulumi", "kubectl", "helm", "aws", "az", "gcloud", "gsutil",
		"fly", "flyctl", "heroku", "stripe", "twilio", "vercel", "netlify", "firebase", "doctl",
		"wrangler", "railway", "supabase", "eksctl", "sam", "cdk", "serverless", "sls":
		return infraVerdict(name, argv)
	case "python", "python2", "python3", "pypy", "pypy3":
		return s.python(name, argv, in)
	case "node", "nodejs":
		return s.node(name, argv, in)
	case "deno":
		return s.deno(argv, in)
	case "awk", "gawk", "mawk", "nawk":
		return s.awk(argv)
	case "sed", "gsed":
		return s.sed(argv)
	case "sqlite3":
		return s.sqlite(argv, in)
	case "java":
		return runtimeVerdict(name, args, in, nil)
	case "swift":
		_, rest := splitOptions(args, nil)
		if len(rest) > 0 {
			switch rest[0].text {
			case "build", "test", "package", "format":
				return localVerdict()
			default:
			}
		}
		if versionOnly(args) {
			return localVerdict()
		}
		return opaqueVerdict(display(argv), "it runs a Swift program")
	case "ruby", "perl", "php", "lua", "luajit", "rscript", "r", "tclsh", "wish", "groovy",
		"julia", "elixir", "erl", "escript", "racket", "guile", "sbcl", "swipl", "ocaml",
		"runghc", "runhaskell", "ghci", "scala", "kotlin", "jshell", "clojure", "clj", "bb",
		"tsx", "ts-node", "esno", "jsc", "qjs", "rhino", "dart", "crystal", "nim", "v",
		"vlang":
		return s.interpreter(name, argv, in)
	case "tar", "bsdtar", "gtar":
		return s.tar(argv)
	case "zip":
		for _, a := range args {
			if strings.HasPrefix(a.text, "-TT") || strings.HasPrefix(a.text, "--unzip-command") {
				return opaqueVerdict(display(argv), "it runs a test command")
			}
		}
		_, operands := scanOptions(args, "bntxiOPZs", setOf("--out", "--output-file", "--password", "--temp-path", "--suffixes", "--exclude", "--include"))
		if len(operands) > 0 {
			s.writes = append(s.writes, operands[0])
		}
		return localVerdict()
	case "unzip":
		hits, _ := scanOptions(args, "dPx", nil)
		if optSeen(hits, "-:", "-^") {
			return unknownVerdict(display(argv), "it may extract files outside its folder", "")
		}
		dests := optValues(hits, "-d")
		s.writes = append(s.writes, dests...)
		if len(dests) == 0 {
			dests = []shWord{{text: "."}}
		}
		s.extractDirs = append(s.extractDirs, dests...)
		return localVerdict()
	case "cp", "gcp":
		return s.cp(argv)
	case "mv", "gmv":
		return s.mv(argv)
	case "ln", "gln":
		return s.ln(argv)
	case "install", "ditto":
		hits, operands := scanOptions(args, "tSmogBfTM", setOf("--target-directory", "--suffix", "--mode", "--owner", "--group", "--arch", "--bom"))
		s.writes = append(s.writes, optValues(hits, "-t", "--target-directory")...)
		switch {
		case name == "install" && optSeen(hits, "-d", "--directory"):
			s.writes = append(s.writes, operands...)
		case len(operands) > 0 && !optSeen(hits, "-t", "--target-directory"):
			s.writes = append(s.writes, operands[len(operands)-1])
		default:
		}
		if name == "ditto" && len(operands) > 1 {
			// ditto copies a tree; the destination may receive a symlink a
			// later write follows.
			s.extractDirs = append(s.extractDirs, operands[len(operands)-1])
		}
		return localVerdict()
	case "mkdir", "touch", "rm", "rmdir", "unlink", "truncate", "shred", "tee", "dos2unix",
		"unix2dos", "gzip", "gunzip", "bzip2", "bunzip2", "xz", "unxz", "zstd", "unzstd",
		"lz4", "srm", "trash":
		// Every operand counts as a target, wherever it is, and so does every
		// attached option value: a value read as a path is harmless, a path
		// read as a value is not.
		_, operands := scanOptions(args, "", nil)
		s.writes = append(s.writes, operands...)
		s.writes = append(s.writes, attachedValues(args)...)
		return localVerdict()
	case "chmod", "chown", "chgrp", "chflags":
		s.chmodLike(name, args)
		return localVerdict()
	case "dd":
		for _, a := range args {
			if strings.HasPrefix(a.text, "of=") {
				s.writes = append(s.writes, shWord{text: a.text[3:], dynamic: a.dynamic, pattern: a.pattern})
			}
		}
		return localVerdict()
	case "sort":
		hits, _ := scanOptions(args, "kotST", setOf("--key", "--output", "--field-separator", "--buffer-size",
			"--temporary-directory", "--compress-program", "--files0-from", "--random-source", "--batch-size", "--parallel"))
		if optSeen(hits, "--compress-program") {
			return opaqueVerdict(display(argv), "it runs a compression program")
		}
		return localVerdict()
	case "uniq":
		_, operands := scanOptions(args, "fsw", setOf("--skip-fields", "--skip-chars", "--check-chars", "--group", "--all-repeated"))
		if len(operands) > 1 {
			s.writes = append(s.writes, operands[1:]...)
		}
		return localVerdict()
	case "split":
		hits, operands := scanOptions(args, "abClnpt", setOf("--suffix-length", "--additional-suffix", "--bytes",
			"--line-bytes", "--lines", "--number", "--separator", "--filter"))
		if optSeen(hits, "--filter") {
			return opaqueVerdict(display(argv), "it runs a command for each piece")
		}
		if len(operands) > 1 {
			s.writes = append(s.writes, operands[1:]...)
		} else {
			s.writes = append(s.writes, shWord{text: "x"})
		}
		return localVerdict()
	case "csplit":
		hits, _ := scanOptions(args, "fnb", setOf("--prefix", "--suffix-format", "--digits"))
		if prefixes := optValues(hits, "-f", "--prefix"); len(prefixes) > 0 {
			s.writes = append(s.writes, prefixes...)
		} else {
			s.writes = append(s.writes, shWord{text: "xx"})
		}
		return localVerdict()
	case "patch":
		return s.patch(argv, in)
	case "xattr":
		hits, operands := scanOptions(args, "", nil)
		if optSeen(hits, "-w", "-d", "-c") && len(operands) > 0 {
			s.writes = append(s.writes, operands[len(operands)-1])
		}
		return localVerdict()
	case "xxd":
		_, operands := scanOptions(args, "cglosnC", nil)
		if len(operands) > 1 {
			s.writes = append(s.writes, operands[1:]...)
		}
		return localVerdict()
	case "base64", "iconv", "shuf", "lipo", "less", "mktemp":
		if name == "mktemp" {
			_, operands := scanOptions(args, "pt", setOf("--suffix"))
			s.writes = append(s.writes, operands...)
		}
		return localVerdict()
	case "tree":
		hits, _ := scanOptions(args, "LPIoHT", setOf("--charset", "--filelimit", "--timefmt", "--sort", "--hintro", "--houtro"))
		if optSeen(hits, "-R") {
			return unknownVerdict(display(argv), "it writes a listing file into every folder", "")
		}
		return localVerdict()
	case "sips":
		return s.sips(argv)
	case "textutil":
		return s.textutil(argv)
	case "xmllint":
		for _, a := range args {
			lower := strings.ToLower(a.text)
			if !a.literal() && (strings.Contains(lower, "://") || dynamicHead(a)) {
				return unknownVerdict(display(argv), "it sends a value built at run time over the network", "")
			}
		}
		return localVerdict()
	case "yq":
		hits, operands := scanOptions(args, "sopIe", setOf("--split-exp", "--output-format", "--input-format", "--indent", "--expression", "--front-matter"))
		if optSeen(hits, "-s", "--split-exp") {
			return unknownVerdict(display(argv), "it writes a file for each document", "")
		}
		if optSeen(hits, "-i", "--inplace") {
			s.writes = append(s.writes, operands...)
		}
		return localVerdict()
	case "file":
		hits, _ := scanOptions(args, "mfFeP", nil)
		if optSeen(hits, "-C") {
			s.writes = append(s.writes, shWord{text: "magic.mgc"})
			for _, m := range optValues(hits, "-m") {
				s.writes = append(s.writes, shWord{text: m.text + ".mgc", dynamic: m.dynamic, pattern: m.pattern})
			}
		}
		return localVerdict()
	case "cd", "pushd", "chdir":
		_, rest := splitOptions(args, nil)
		switch {
		case len(rest) == 0:
			s.cds = append(s.cds, shWord{text: s.home, dynamic: s.home == ""})
		case rest[0].text == "-":
			s.cds = append(s.cds, shWord{text: "\x00", dynamic: true})
		default:
			s.cds = append(s.cds, rest[0])
		}
		return localVerdict()
	case "read", "printf", "mapfile", "readarray":
		// -t is a value option for read (a timeout) but a bool for mapfile
		// (strip newlines), so the array name after it is an operand, not a
		// value. Keep the option sets apart.
		var valueFlags map[string]bool
		switch name {
		case "read":
			valueFlags = setOf("-d", "-n", "-N", "-p", "-t", "-u", "-a", "-i")
		case "printf":
			valueFlags = setOf("-v")
		default: // mapfile, readarray
			valueFlags = setOf("-d", "-n", "-O", "-s", "-u", "-C", "-c")
		}
		opts, rest := splitOptions(args, valueFlags)
		if _, ok := optionValue(opts, "-C"); ok && name != "read" && name != "printf" {
			return opaqueVerdict(display(argv), "it runs a callback")
		}
		var targets []shWord
		switch name {
		case "printf":
			if v, ok := optionValue(opts, "-v"); ok {
				targets = append(targets, v)
			}
		case "read":
			targets = rest
			if v, ok := optionValue(opts, "-a"); ok {
				targets = append(targets, v)
			}
		default: // mapfile, readarray: the array name is the sole operand
			targets = rest
		}
		for _, t := range targets {
			if !t.literal() {
				// read $V / printf -v "$V" / mapfile -t "$V": the variable set
				// is built at run time and could be PATH, BASH_ENV, GIT_PAGER.
				return opaqueVerdict(display(argv), "it sets a variable whose name is built at run time, which could change what later commands run")
			}
			if dangerousEnvName(t.text) {
				return opaqueVerdict(display(argv), "it changes "+t.text+", which decides what later commands run")
			}
		}
		return localVerdict()
	case "history":
		opts, operands := splitOptions(args, nil)
		switch {
		case shortCluster(opts, 'w') || shortCluster(opts, 'a'):
			// -w and -a write the history file; with no operand it is the
			// shell history file outside the workspace.
			if len(operands) == 0 {
				return unknownVerdict(display(argv), "it writes the shell history file outside the workspace", "")
			}
			s.writes = append(s.writes, operands...)
		case shortCluster(opts, 'r') || shortCluster(opts, 'n'):
			for _, op := range operands {
				s.args = append(s.args, scanArg{word: op})
			}
		default:
		}
		return localVerdict()
	case "ar":
		_, operands := scanOptions(args, "", nil)
		archive := -1
		switch {
		case len(operands) == 0:
		case isArOperation(operands[0].text) && len(operands) > 1:
			archive = 1
		default:
			archive = 0
		}
		if archive >= 0 {
			if !operands[archive].literal() {
				return unknownVerdict(display(argv), "it writes an archive whose path is built at run time", "")
			}
			s.writes = append(s.writes, operands[archive])
		}
		return localVerdict()
	case "date":
		for _, a := range args {
			if a.text == "-s" || a.text == "--set" || strings.HasPrefix(a.text, "--set=") {
				return unknownVerdict("date -s", "it sets the system clock", "date -s")
			}
		}
		return localVerdict()
	case "simctl":
		_, rest := splitOptions(args, nil)
		if len(rest) == 0 {
			return localVerdict()
		}
		switch rest[0].text {
		case "list", "help", "getenv":
			return localVerdict()
		default:
		}
		return unknownVerdict("simctl "+rest[0].text, "it changes a simulator", "simctl "+rest[0].text)
	case "hash":
		opts, _ := splitOptions(args, setOf("-p", "-d", "-t"))
		if _, ok := optionValue(opts, "-p"); ok {
			return opaqueVerdict(display(argv), "it binds a command name to a file")
		}
		return localVerdict()
	case "compgen":
		opts, _ := splitOptions(args, setOf("-C", "-F", "-W", "-A", "-G", "-o", "-P", "-S", "-X"))
		if _, ok := optionValue(opts, "-C", "-F"); ok {
			return opaqueVerdict(display(argv), "it runs a completion command")
		}
		return localVerdict()
	case "enable", "fc", "bind", "complete":
		return opaqueVerdict(display(argv), "it changes the shell itself")
	case "man":
		hits, _ := scanOptions(args, "PHMSmpCLREe", setOf("--pager", "--html", "--manpath", "--sections", "--systems", "--preprocessor", "--config-file", "--locale", "--encoding", "--extension"))
		if optSeen(hits, "-P", "-H", "-p", "--pager", "--html", "--preprocessor") {
			return unknownVerdict(display(argv), "it runs a pager command", "")
		}
		return localVerdict()
	case "rg":
		hits, _ := scanOptions(args, "efgtTmABCjMrEd", setOf("--regexp", "--file", "--glob", "--iglob", "--type",
			"--type-not", "--max-count", "--after-context", "--before-context", "--context", "--threads",
			"--max-columns", "--replace", "--encoding", "--max-depth", "--pre", "--pre-glob", "--type-add",
			"--colors", "--sort", "--sortr", "--max-filesize", "--path-separator", "--ignore-file",
			"--context-separator", "--field-match-separator", "--field-context-separator", "--engine", "--dfa-size-limit",
			"--regex-size-limit", "--hostname-bin", "--hyperlink-format"))
		if optSeen(hits, "--pre", "--hostname-bin") {
			return opaqueVerdict(display(argv), "it runs a preprocessor command")
		}
		return localVerdict()
	case "ag", "ack", "ack-grep":
		for _, a := range args {
			if strings.HasPrefix(a.text, "--pager") || (name != "ag" && strings.HasPrefix(a.text, "--output")) {
				return opaqueVerdict(display(argv), "it runs a pager or evaluates code it was given")
			}
		}
		return localVerdict()
	case "ping", "ping6", "dig", "host", "nslookup", "traceroute", "traceroute6", "whois", "mtr", "drill":
		if anyDynamic(args) {
			return unknownVerdict(display(argv), "it sends a value built at run time over the network", "")
		}
		return localVerdict()
	case "defaults":
		_, rest := splitOptions(args, setOf("-host"))
		if len(rest) == 0 {
			return localVerdict()
		}
		switch rest[0].text {
		case "read", "read-type", "domains", "find", "help":
			return localVerdict()
		default:
		}
		return unknownVerdict("defaults "+rest[0].text, "it changes app preferences", "defaults "+rest[0].text)
	case "plutil":
		for _, a := range args {
			switch a.text {
			case "-p", "-lint", "-help", "-type", "-extract":
			default:
				if strings.HasPrefix(a.text, "-") {
					return unknownVerdict("plutil", "it changes a property list", "plutil")
				}
			}
		}
		return localVerdict()
	case "codesign":
		for _, a := range args {
			t := a.text
			if t == "--sign" || strings.HasPrefix(t, "--sign=") || t == "--remove-signature" ||
				(len(t) > 1 && t[0] == '-' && t[1] != '-' && strings.IndexByte(t[1:], 's') >= 0) {
				return unknownVerdict("codesign", "it signs or changes code", "codesign")
			}
		}
		return localVerdict()
	case "sysctl":
		for _, a := range args {
			if strings.Contains(a.text, "=") || a.text == "-w" {
				return unknownVerdict("sysctl", "it changes kernel settings", "sysctl")
			}
		}
		return localVerdict()
	case "hostname":
		if _, operands := scanOptions(args, "", nil); len(operands) > 0 {
			return unknownVerdict(display(argv), "it renames this computer", "")
		}
		return localVerdict()
	case "ifconfig":
		if _, operands := scanOptions(args, "", nil); len(operands) > 1 {
			return unknownVerdict(display(argv), "it changes network settings", "")
		}
		return localVerdict()
	case "diskutil", "networksetup", "pmset", "scutil", "spctl", "csrutil", "mdutil", "log":
		return systemSettingVerdict(name, argv)
	case "xdg-open", "start", "security", "screencapture", "say", "afplay", "osacompile",
		"automator", "code", "vim", "vi", "nvim", "nano", "emacs", "pico", "ed", "ex":
		return unknownVerdict(name, "it is not a known read or build command", name)
	default:
	}
	if bashBuildRunners[name] {
		if v, ok := buildRunnerPublish(name, args); ok {
			return v
		}
	}
	if bashReadCommands[name] || bashBuildCommands[name] {
		return localVerdict()
	}
	return unknownVerdict(name, "it is not a known read or build command", name)
}

// bashOptionSensitive commands have options that write a file or run a
// program, so a word that may expand into an option makes them unknown.
var bashOptionSensitive = setOf("rg", "sort", "unzip", "tar", "bsdtar", "gtar", "make", "gmake", "uniq",
	"split", "csplit", "patch", "xxd", "base64", "iconv", "shuf", "lipo", "less", "tree", "sips",
	"textutil", "xmllint", "yq", "cp", "gcp", "mv", "gmv", "ln", "gln", "install", "ditto", "zip", "man",
	"ag", "ack", "ack-grep", "file", "sed", "gsed", "awk", "gawk", "mawk", "nawk", "openssl", "rsync",
	"mktemp", "xattr", "gcc", "cc", "clang", "g++", "clang++", "c++", "ld", "rustc", "protoc", "go",
	"cargo", "esbuild", "tsc", "xcodebuild", "swift")

// attachedValues are the values written inside option words: "--opt=value"
// and the tail of a short option ("-o~/x").
func attachedValues(args []shWord) []shWord {
	var out []shWord
	for _, a := range args {
		t := a.text
		switch {
		case t == "--" || !strings.HasPrefix(t, "-"):
		case strings.HasPrefix(t, "--"):
			if _, _, ok := strings.Cut(t, "="); ok {
				out = append(out, tailWord(a, strings.IndexByte(t, '=')+1))
			}
		case len(t) > 2:
			out = append(out, tailWord(a, 2))
		default:
		}
	}
	return out
}

// cobraPositionals returns up to max positional words of a cobra CLI. Like
// cobra's own command lookup, a --flag without "=" and a two-letter -f flag
// take the next word as their value unless they are known boolean flags.
func cobraPositionals(args []string, boolFlags map[string]bool, max int) []string {
	var out []string
	for i := 0; i < len(args) && len(out) < max; i++ {
		t := args[i]
		switch {
		case t == "--":
			for _, rest := range args[i+1:] {
				if len(out) < max {
					out = append(out, rest)
				}
			}
			return out
		case strings.HasPrefix(t, "--"):
			if !strings.Contains(t, "=") && !boolFlags[t] {
				i++
			}
		case strings.HasPrefix(t, "-") && len(t) > 1:
			if len(t) == 2 && !boolFlags[t] {
				i++
			}
		default:
			out = append(out, t)
		}
	}
	return out
}

// ghBoolFlags are the only flags gh's command lookup knows are boolean. Like
// cobra, any other flag is assumed to take the next word, so `gh pr --web
// view merge` resolves to merge, as gh itself would.
var ghBoolFlags = setOf("-h", "--help", "--version")

// ghReadVerbs are gh subcommands that only read.
var ghReadVerbs = map[string]map[string]bool{
	"pr":        setOf("view", "list", "ls", "diff", "checks", "status"),
	"issue":     setOf("view", "list", "ls", "status"),
	"repo":      setOf("view", "list", "ls", "clone"),
	"run":       setOf("view", "list", "ls", "watch", "download"),
	"workflow":  setOf("view", "list", "ls"),
	"release":   setOf("view", "list", "ls", "download"),
	"gist":      setOf("view", "list", "ls"),
	"label":     setOf("list", "ls"),
	"auth":      setOf("status"),
	"cache":     setOf("list", "ls"),
	"secret":    setOf("list", "ls"),
	"variable":  setOf("list", "ls", "get"),
	"ruleset":   setOf("list", "ls", "view", "check"),
	"project":   setOf("list", "ls", "view", "field-list", "item-list"),
	"codespace": setOf("list", "ls"),
	"org":       setOf("list", "ls"),
	"extension": setOf("list", "ls"),
	"alias":     setOf("list", "ls"),
	"config":    setOf("get", "list"),
}

func ghVerdict(argv []shWord) bashVerdict {
	texts := make([]string, len(argv))
	for i, w := range argv {
		texts[i] = w.text
	}
	pos := cobraPositionals(texts[1:], ghBoolFlags, 2)
	for _, p := range pos {
		if strings.ContainsRune(p, 0) {
			return opaqueVerdict(display(argv), "its subcommand is not a literal word")
		}
	}
	mutate := actionVerdict(externalBashAction("github.mutate", "gh"))
	if len(pos) == 0 {
		return localVerdict()
	}
	group := pos[0]
	verb := ""
	if len(pos) > 1 {
		verb = pos[1]
	}
	read := false
	switch group {
	case "api":
		read = ghAPIRead(argv[1:])
	case "search", "status", "help", "version", "completion":
		read = true
	default:
		if verb == "" || verb == "--help" {
			read = len(texts) <= 2 || containsText(texts, "--help") || containsText(texts, "-h")
		} else {
			read = ghReadVerbs[group][verb]
		}
	}
	if containsText(texts, "--help") || containsText(texts, "-h") {
		read = true
	}
	if !read {
		return mutate
	}
	if anyDynamic(argv[1:]) {
		return unknownVerdict(display(argv), "it sends a value built at run time to GitHub", "")
	}
	return localVerdict()
}

func containsText(texts []string, want string) bool {
	for _, t := range texts {
		if t == want {
			return true
		}
	}
	return false
}

// ghAPIRead reports a gh api call that only reads: the last -X/--method is
// GET (or absent with no body fields), and a GraphQL call holds no mutation.
func ghAPIRead(args []shWord) bool {
	method := ""
	fields := false
	input := false
	graphql := false
	mutation := false
	for i := 0; i < len(args); i++ {
		t := args[i].text
		next := func() shWord {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return shWord{}
		}
		switch {
		case t == "-X" || t == "--method":
			method = strings.ToUpper(next().text)
		case strings.HasPrefix(t, "--method="):
			method = strings.ToUpper(t[len("--method="):])
		case strings.HasPrefix(t, "-X"):
			method = strings.ToUpper(t[2:])
		case t == "-f" || t == "-F" || t == "--field" || t == "--raw-field":
			fields = true
			v := next()
			if fieldMutates(v) {
				mutation = true
			}
		case strings.HasPrefix(t, "--field=") || strings.HasPrefix(t, "--raw-field="):
			fields = true
			_, v, _ := strings.Cut(t, "=")
			if fieldMutates(shWord{text: v, dynamic: args[i].dynamic}) {
				mutation = true
			}
		case (strings.HasPrefix(t, "-f") || strings.HasPrefix(t, "-F")) && len(t) > 2 && !strings.HasPrefix(t, "--"):
			fields = true
			if fieldMutates(shWord{text: t[2:], dynamic: args[i].dynamic}) {
				mutation = true
			}
		case t == "--input" || strings.HasPrefix(t, "--input="):
			input = true
		case t == "graphql":
			graphql = true
		case t == "-H" || t == "--header" || t == "-q" || t == "--jq" || t == "-t" ||
			t == "--template" || t == "--hostname" || t == "-p" || t == "--preview" || t == "--cache":
			next()
		default:
		}
	}
	if input {
		return false
	}
	if graphql {
		return !mutation && (method == "" || method == "POST" || method == "GET")
	}
	switch method {
	case "GET", "HEAD":
		return true
	case "":
		return !fields
	default:
		return false
	}
}

// fieldMutates reports a GraphQL field that may hold a mutation: one that
// names a mutation, reads its value from a file, or is not a literal.
func fieldMutates(w shWord) bool {
	if !w.literal() {
		return true
	}
	_, value, _ := strings.Cut(w.text, "=")
	return strings.HasPrefix(value, "@") || strings.Contains(strings.ToLower(w.text), "mutation")
}

// gitGlobalValueFlags are git's global options that take a separate value.
var gitGlobalValueFlags = setOf("-C", "-c", "--git-dir", "--work-tree", "--namespace",
	"--super-prefix", "--config-env", "--attr-source", "--exec-path")

// gitSafeConfig are configuration keys a `git -c` override may set without
// changing which program git runs.
var gitSafeConfig = setOf("core.quotepath", "user.name", "user.email", "safe.directory",
	"init.defaultbranch", "core.autocrlf", "core.safecrlf", "core.filemode", "core.ignorecase",
	"diff.noprefix", "diff.renames", "diff.algorithm", "diff.mnemonicprefix", "log.decorate",
	"log.date", "merge.conflictstyle", "pull.rebase", "pull.ff", "fetch.prune", "gc.auto",
	"protocol.version", "column.ui", "status.short", "status.branch", "rebase.autostash",
	"core.longpaths", "maintenance.auto", "commit.gpgsign", "tag.gpgsign", "feature.manyfiles",
	"index.threads", "pack.threads", "http.postbuffer", "core.precomposeunicode")

// gitSubcommand returns git's subcommand and its index in argv, skipping
// global options and their values. A non-nil verdict means the global
// options alone make the call unknown (an alias or program override).
func gitSubcommand(argv []shWord) (string, int, *bashVerdict) {
	for i := 1; i < len(argv); i++ {
		w := argv[i]
		t := w.text
		if !w.literal() {
			v := opaqueVerdict(display(argv), "its subcommand or options are not literal words")
			return "", i, &v
		}
		if !strings.HasPrefix(t, "-") {
			return t, i, nil
		}
		name, value, hasValue := strings.Cut(t, "=")
		if strings.HasPrefix(t, "-c") && !strings.HasPrefix(t, "--") && len(t) > 2 {
			name, value, hasValue = "-c", t[2:], true
		}
		if strings.HasPrefix(t, "-C") && !strings.HasPrefix(t, "--") && len(t) > 2 {
			continue
		}
		if !gitGlobalValueFlags[name] {
			continue
		}
		if !hasValue {
			if name == "--exec-path" {
				// Without a value it prints the path.
				continue
			}
			i++
			if i >= len(argv) {
				return "", i, nil
			}
			if !argv[i].literal() {
				v := opaqueVerdict(display(argv), "a git option value is not a literal word")
				return "", i, &v
			}
			value = argv[i].text
		}
		switch name {
		case "-c":
			key := strings.ToLower(strings.SplitN(value, "=", 2)[0])
			if strings.HasPrefix(key, "alias.") {
				v := opaqueVerdict(display(argv), "it defines a git alias")
				return "", i, &v
			}
			if !gitSafeConfig[key] && !strings.HasPrefix(key, "color.") &&
				!strings.HasPrefix(key, "advice.") && !strings.HasPrefix(key, "i18n.") {
				v := unknownVerdict(display(argv), "it overrides git configuration ("+key+") that can run commands", "")
				return "", i, &v
			}
		case "--exec-path", "--config-env":
			v := opaqueVerdict(display(argv), "it changes which programs git runs")
			return "", i, &v
		default:
		}
	}
	return "", len(argv), nil
}

// gitLocalSubcommands only touch the local repository or read a remote.
var gitLocalSubcommands = setOf("status", "diff", "log", "show", "blame", "annotate", "branch",
	"checkout", "switch", "restore", "add", "rm", "mv", "commit", "reset", "revert",
	"cherry-pick", "merge", "rebase", "stash", "tag", "fetch", "pull", "clone", "init",
	"worktree", "remote", "ls-files", "ls-tree", "ls-remote", "rev-parse", "rev-list",
	"cat-file", "describe", "shortlog", "reflog", "grep", "apply", "am", "format-patch",
	"diff-tree", "diff-index", "diff-files", "merge-base", "name-rev", "show-ref",
	"symbolic-ref", "for-each-ref", "count-objects", "fsck", "gc", "prune", "repack", "clean",
	"notes", "bisect", "version", "help", "archive", "bundle", "sparse-checkout", "submodule",
	"check-ignore", "check-attr", "check-ref-format", "range-diff", "whatchanged",
	"show-branch", "verify-commit", "verify-tag", "hash-object", "update-index", "update-ref",
	"write-tree", "read-tree", "commit-tree", "mktree", "var", "stripspace",
	"interpret-trailers", "request-pull", "cherry", "mailinfo", "mailsplit", "rerere",
	"citool", "blame", "restore", "lfs", "config", "fast-export", "fast-import", "pack-refs",
	"multi-pack-index", "commit-graph", "replace", "column", "get-tar-commit-id",
	"unpack-objects", "pack-objects", "index-pack", "verify-pack", "show-index", "patch-id")

// gitReadSubcommands never change the repository or its working tree. Their
// output options are checked as writes on their own.
var gitReadSubcommands = setOf("status", "diff", "log", "show", "blame", "annotate", "ls-files",
	"ls-tree", "ls-remote", "rev-parse", "rev-list", "cat-file", "describe", "shortlog", "grep",
	"diff-tree", "diff-index", "diff-files", "merge-base", "name-rev", "show-ref", "for-each-ref",
	"count-objects", "version", "help", "check-ignore", "check-attr", "check-ref-format",
	"range-diff", "whatchanged", "show-branch", "verify-commit", "verify-tag", "var", "stripspace",
	"cherry", "column", "get-tar-commit-id", "verify-pack", "show-index", "patch-id", "fsck",
	"archive", "format-patch", "bundle", "request-pull", "config", "interpret-trailers",
	"mailinfo", "fast-export")

// gitListSubcommands only list when given no operand, or list or show.
var gitListSubcommands = setOf("branch", "tag", "remote", "stash", "notes", "worktree", "reflog",
	"submodule", "lfs", "sparse-checkout")

// gitSubValues are, per subcommand, the short letters and long options that
// take a value, so its operands are told apart from option values.
var gitSubValues = map[string]struct {
	short string
	long  map[string]bool
}{
	"checkout":     {"bB", setOf("--orphan", "--conflict", "--pathspec-from-file")},
	"switch":       {"cC", setOf("--create", "--force-create", "--orphan", "--conflict")},
	"restore":      {"s", setOf("--source", "--pathspec-from-file", "--conflict")},
	"clone":        {"bocju", setOf("--branch", "--origin", "--config", "--jobs", "--upload-pack", "--reference", "--reference-if-able", "--separate-git-dir", "--depth", "--shallow-since", "--shallow-exclude", "--filter", "--template", "--server-option", "--bundle-uri", "--ref-format")},
	"init":         {"b", setOf("--template", "--separate-git-dir", "--initial-branch", "--object-format", "--ref-format")},
	"worktree":     {"bB", setOf("--reason")},
	"submodule":    {"b", setOf("--branch", "--name", "--reference", "--depth")},
	"format-patch": {"ov", setOf("--output-directory", "--output", "--subject-prefix", "--in-reply-to", "--to", "--cc", "--from", "--suffix", "--start-number", "--signature", "--signature-file", "--base", "--reroll-count", "--range-diff", "--interdiff", "--creation-factor", "--cover-from-description", "--filename-max-length")},
	"archive":      {"o", setOf("--output", "--format", "--prefix", "--remote", "--exec", "--add-file", "--add-virtual-file", "--mtime")},
	"commit":       {"mFcCt", setOf("--message", "--file", "--reuse-message", "--reedit-message", "--template", "--author", "--date", "--fixup", "--squash", "--cleanup", "--trailer", "--pathspec-from-file")},
	"stash":        {"m", setOf("--message", "--pathspec-from-file")},
	"tag":          {"mFu", setOf("--message", "--file", "--local-user", "--sort", "--format", "--contains", "--no-contains", "--points-at", "--merged", "--no-merged", "--cleanup")},
	"branch":       {"u", setOf("--set-upstream-to", "--sort", "--format", "--contains", "--no-contains", "--points-at", "--merged", "--no-merged", "--track")},
	"reset":        {"", setOf("--pathspec-from-file")},
	"clean":        {"e", setOf("--exclude")},
}

func (s *bashScan) git(argv []shWord, depth int) bashVerdict {
	sub, idx, verdict := gitSubcommand(argv)
	if verdict != nil {
		return *verdict
	}
	if sub == "" {
		return localVerdict()
	}
	rest := argv[min(idx+1, len(argv)):]
	values := gitSubValues[sub]
	if values.long == nil {
		values.long = setOf("--output")
	}
	hits, operands := scanOptions(rest, values.short, values.long)
	v := s.gitSubcommandVerdict(argv, sub, rest, hits, operands, depth)
	if v.kind != bashLocal {
		return v
	}
	if gitDiscards(sub, rest, hits, operands, s.gitBases(argv[1:idx])) {
		return unknownVerdict(display(argv), "it discards uncommitted work or deletes history in the repository", "")
	}
	s.gitOutputs(sub, hits, operands)
	if gitMutates(sub, operands) {
		s.gitRepoWrites(sub, argv[1:idx])
	}
	return v
}

func (s *bashScan) gitSubcommandVerdict(argv []shWord, sub string, rest []shWord, hits []optHit, operands []shWord, depth int) bashVerdict {
	switch sub {
	case "push":
		return actionVerdict(externalBashAction("github.repository.push", "git push"))
	case "send-email", "imap-send":
		return actionVerdict(externalBashAction("mail.send", "git "+sub))
	case "lfs":
		if len(operands) > 0 && operands[0].text == "push" {
			return actionVerdict(externalBashAction("github.repository.push", "git lfs push"))
		}
	case "config":
		if optSeen(hits, "--get", "--get-all", "--get-regexp", "--get-urlmatch", "--list", "-l",
			"--get-color", "--get-colorbool") ||
			(len(operands) <= 1 && !optSeen(hits, "--unset", "--unset-all", "--add", "--replace-all",
				"--rename-section", "--remove-section", "-e", "--edit")) {
			return localVerdict()
		}
		if len(operands) > 0 && (operands[0].text == "get" || operands[0].text == "list") {
			return localVerdict()
		}
		return unknownVerdict("git config", "it changes git configuration, which can run commands", "")
	case "rebase":
		for _, o := range rest {
			if o.text == "-x" || o.text == "--exec" || strings.HasPrefix(o.text, "--exec=") ||
				(strings.HasPrefix(o.text, "-x") && !strings.HasPrefix(o.text, "--")) {
				return opaqueVerdict(display(argv), "it runs a command after each commit")
			}
		}
	case "bisect":
		if len(operands) > 0 && operands[0].text == "run" {
			s.run(operands[1:], bashStdin{}, depth)
		}
	case "submodule":
		for i, o := range operands {
			if o.text == "foreach" {
				code, ok := joinTexts(operands[i+1:])
				if !ok {
					return opaqueVerdict(display(argv), "the command it runs is not a literal")
				}
				s.scanNested(display(argv), code, depth)
				break
			}
		}
	case "grep":
		for _, o := range rest {
			if o.text == "-O" || strings.HasPrefix(o.text, "--open-files-in-pager") ||
				(strings.HasPrefix(o.text, "-O") && !strings.HasPrefix(o.text, "--")) {
				return opaqueVerdict(display(argv), "it runs a pager command")
			}
		}
	case "apply", "am":
		if optSeen(hits, "--unsafe-paths", "--directory") {
			return unknownVerdict(display(argv), "it applies a patch outside the repository", "")
		}
	case "fetch", "pull", "clone", "ls-remote", "archive", "remote":
		for _, o := range rest {
			name, _, _ := strings.Cut(o.text, "=")
			// git accepts any unique prefix of a long option, so --upl is
			// --upload-pack and --templ is --template. optNameMatches tests
			// the abbreviation both ways.
			if optNameMatches(name, "--upload-pack") || optNameMatches(name, "--receive-pack") ||
				optNameMatches(name, "--exec") || optNameMatches(name, "--template") ||
				optNameMatches(name, "--config") || (sub == "clone" && (name == "-c" || name == "-u")) ||
				(sub == "ls-remote" && name == "-u") {
				return opaqueVerdict(display(argv), "it runs a program on the remote side or from a template")
			}
		}
		if anyDynamic(rest) {
			return unknownVerdict(display(argv), "it contacts a remote with a value built at run time", "")
		}
	case "svn", "p4", "cvsimport", "cvsserver", "archimport", "quiltimport", "daemon",
		"instaweb", "http-backend", "shell", "upload-pack", "receive-pack", "upload-archive",
		"credential", "credential-cache", "credential-store", "difftool", "mergetool", "gui",
		"gitk", "web--browse", "maintenance", "hook", "filter-branch", "filter-repo":
		return unknownVerdict("git "+sub, "it runs other programs or reads credentials", "")
	default:
		if !gitLocalSubcommands[sub] {
			// An unknown subcommand may be an alias from the user's git config.
			return opaqueVerdict(display(argv), "it may be a git alias")
		}
	}
	return localVerdict()
}

// gitMutates reports a subcommand that changes the repository or its working
// tree, so the repository folder must be one the run may write.
func gitMutates(sub string, operands []shWord) bool {
	if gitReadSubcommands[sub] {
		return false
	}
	if gitListSubcommands[sub] {
		if len(operands) == 0 {
			// A bare `git stash` saves and clears the working tree.
			return sub == "stash"
		}
		switch operands[0].text {
		case "list", "show", "status", "ls-files", "env", "version":
			return false
		default:
		}
	}
	return true
}

// gitGlobalDirs returns the folders git's global options point it at: -C
// (each relative to the one before), --git-dir and --work-tree.
func gitGlobalDirs(globals []shWord) []shWord {
	var dirs []shWord
	base := ""
	for i := 0; i < len(globals); i++ {
		t := globals[i].text
		name, value, hasValue := strings.Cut(t, "=")
		switch {
		case t == "-C" && i+1 < len(globals):
			i++
			value = globals[i].text
			if base != "" && !filepath.IsAbs(value) {
				value = filepath.Join(base, value)
			}
			base = value
			dirs = append(dirs, shWord{text: value, dynamic: globals[i].dynamic, pattern: globals[i].pattern})
		case strings.HasPrefix(t, "-C") && len(t) > 2:
			value = t[2:]
			if base != "" && !filepath.IsAbs(value) {
				value = filepath.Join(base, value)
			}
			base = value
			dirs = append(dirs, shWord{text: value, dynamic: globals[i].dynamic, pattern: globals[i].pattern})
		case name == "--git-dir" || name == "--work-tree":
			if !hasValue {
				if i+1 >= len(globals) {
					continue
				}
				i++
				value = globals[i].text
			}
			dirs = append(dirs, shWord{text: value, dynamic: globals[i].dynamic, pattern: globals[i].pattern})
		default:
		}
	}
	return dirs
}

// gitBases are the folders git's relative paths are relative to.
func (s *bashScan) gitBases(globals []shWord) []string {
	dirs := gitGlobalDirs(globals)
	bases, ok := s.relativeBases()
	if !ok {
		return nil
	}
	if len(dirs) == 0 {
		return bases
	}
	var out []string
	for _, d := range dirs {
		if !d.literal() {
			return nil
		}
		if filepath.IsAbs(d.text) {
			out = append(out, d.text)
			continue
		}
		for _, b := range bases {
			out = append(out, filepath.Join(b, d.text))
		}
	}
	return out
}

// gitRepoWrites records the repository folder a changing subcommand writes:
// the -C, --git-dir and --work-tree folders, or the current folder.
func (s *bashScan) gitRepoWrites(sub string, globals []shWord) {
	dirs := gitGlobalDirs(globals)
	for _, g := range dirs {
		if g.text != "" {
			s.cds = append(s.cds, g)
		}
	}
	if len(dirs) == 0 {
		dirs = []shWord{{text: "."}}
	}
	s.writes = append(s.writes, dirs...)
}

// gitOutputs records the files and folders a git subcommand writes outside
// its repository: --output files, format-patch and archive output, a new
// bundle, and the folders init, clone, worktree add and submodule add make.
func (s *bashScan) gitOutputs(sub string, hits []optHit, operands []shWord) {
	s.writes = append(s.writes, optValues(hits, "--output", "--separate-git-dir")...)
	switch sub {
	case "format-patch":
		s.writes = append(s.writes, optValues(hits, "-o", "--output-directory")...)
	case "archive":
		s.writes = append(s.writes, optValues(hits, "-o")...)
	case "bundle":
		if len(operands) > 1 && operands[0].text == "create" {
			s.writes = append(s.writes, operands[1])
		}
	case "init":
		if len(operands) > 0 {
			s.writes = append(s.writes, operands[0])
		}
	case "clone":
		switch {
		case len(operands) > 1:
			s.writes = append(s.writes, operands[1])
		case len(operands) == 1:
			name := strings.TrimSuffix(filepath.Base(strings.TrimRight(operands[0].text, "/")), ".git")
			s.writes = append(s.writes, shWord{text: name, dynamic: operands[0].dynamic})
		default:
		}
	case "worktree":
		if len(operands) > 1 && operands[0].text == "add" {
			s.writes = append(s.writes, operands[1])
		}
	case "submodule":
		if len(operands) > 2 && operands[0].text == "add" {
			s.writes = append(s.writes, operands[2])
		}
	default:
	}
}

// gitDiscards reports a subcommand that throws away uncommitted work or
// history: reset --hard, clean, a checkout or restore of paths, a forced
// switch, branch -D, stash clear or drop, gc, prune, tag -d, reflog expire,
// update-ref -d, a forced rm or worktree remove. It always asks.
func gitDiscards(sub string, rest []shWord, hits []optHit, operands []shWord, bases []string) bool {
	first := ""
	if len(operands) > 0 {
		first = operands[0].text
	}
	switch sub {
	case "reset":
		return optSeen(hits, "--hard", "--merge")
	case "clean":
		return !optSeen(hits, "-n", "--dry-run")
	case "checkout":
		if optSeen(hits, "-f", "--force", "-p", "--patch", "--ours", "--theirs", "-m", "--merge", "--pathspec-from-file") {
			return true
		}
		for _, w := range rest {
			if w.text == "--" {
				return true
			}
		}
		if len(operands) > 1 {
			return true
		}
		if len(operands) == 1 {
			op := operands[0]
			if !op.literal() || op.text == "." || strings.HasPrefix(op.text, ":") {
				return true
			}
			if bases == nil {
				return true
			}
			for _, b := range bases {
				// Without a known folder, only a path check is possible.
				if b == "" {
					continue
				}
				if _, err := os.Lstat(filepath.Join(b, op.text)); err == nil {
					return true
				}
			}
		}
		return false
	case "restore":
		return !optSeen(hits, "-S", "--staged") || optSeen(hits, "-W", "--worktree")
	case "switch":
		return optSeen(hits, "-f", "--force", "--discard-changes")
	case "branch":
		return optSeen(hits, "-D") || (optSeen(hits, "-d", "--delete") && optSeen(hits, "-f", "--force"))
	case "stash":
		return first == "clear" || first == "drop"
	case "gc", "prune":
		return true
	case "tag":
		return optSeen(hits, "-d", "--delete")
	case "reflog":
		return first == "expire" || first == "delete"
	case "update-ref":
		return optSeen(hits, "-d")
	case "rm":
		return optSeen(hits, "-f", "--force")
	case "worktree":
		return (first == "remove" && optSeen(hits, "-f", "--force")) || first == "prune"
	case "read-tree":
		// -m, -u and --reset overwrite the index and working tree.
		return optSeen(hits, "-m", "-u", "--reset")
	case "submodule":
		return first == "deinit" && optSeen(hits, "-f", "--force")
	case "merge":
		return optSeen(hits, "--abort", "--quit")
	default:
	}
	return false
}

// curlBodyLong are curl options that send a request body or upload a file.
var curlBodyLong = setOf("--data", "--data-ascii", "--data-binary", "--data-raw",
	"--data-urlencode", "--form", "--form-string", "--json", "--upload-file", "--config",
	"--mail-rcpt", "--mail-from", "--quote", "--ftp-port", "--telnet-option", "--variable")

// curlValueLong are curl options that take a separate value.
var curlValueLong = setOf("--header", "--user-agent", "--referer", "--user", "--cookie",
	"--proxy", "--connect-timeout", "--max-time", "--retry", "--cacert", "--cert", "--key",
	"--resolve", "--interface", "--range", "--limit-rate", "--max-filesize", "--url",
	"--proto", "--proto-redir", "--retry-delay", "--retry-max-time", "--capath", "--ciphers",
	"--connect-to", "--dns-servers", "--netrc-file", "--oauth2-bearer", "--pinnedpubkey",
	"--proxy-user", "--socks5", "--socks5-hostname", "--unix-socket", "--abstract-unix-socket",
	"--write-out", "--continue-at", "--expect100-timeout", "--keepalive-time", "--local-port",
	"--max-redirs", "--speed-limit", "--speed-time", "--time-cond", "--tls-max", "--aws-sigv4",
	"--request-target", "--sasl-authzid", "--service-name", "--happy-eyeballs-timeout-ms",
	"--parallel-max", "--rate", "--retry-all-errors", "--create-file-mode", "--url-query",
	"--json-file")

// curlWriteLong are curl options whose value is a file curl writes.
var curlWriteLong = setOf("--output", "--output-dir", "--dump-header", "--cookie-jar", "--trace",
	"--trace-ascii", "--stderr", "--libcurl", "--etag-save", "--hsts", "--alt-svc")

// curlFileValues are curl options whose value may name a file (@file) that
// curl reads and sends: headers, URL query parts, cookies.
var curlFileValues = setOf("--header", "--proxy-header", "--url-query", "--cookie", "--url", "--user-agent",
	"--referer", "--user", "--oauth2-bearer", "--aws-sigv4")

func (s *bashScan) curl(argv []shWord) bashVerdict {
	args := argv[1:]
	write := actionVerdict(externalBashAction("network.request.write", "curl"))
	dynamic := unknownVerdict(display(argv), "it sends a value built at run time over the network", "")
	method := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		if !a.literal() {
			return dynamic
		}
		// next consumes an option's value. A value built at run time is
		// sent like any other.
		dyn := false
		next := func() (shWord, bool) {
			if i+1 < len(args) {
				i++
				if !args[i].literal() {
					dyn = true
				}
				return args[i], true
			}
			return shWord{}, false
		}
		switch {
		case t == "--" || t == "-":
			continue
		case strings.HasPrefix(t, "--"):
			name, value, hasValue := strings.Cut(t, "=")
			name = strings.Replace(name, "--expand-", "--", 1)
			if strings.HasPrefix(name, "--data") || strings.HasPrefix(name, "--form") || curlBodyLong[name] {
				return write
			}
			if name == "--request" {
				if !hasValue {
					v, _ := next()
					value = v.text
				}
				method = strings.ToUpper(value)
				break
			}
			if curlWriteLong[name] {
				if !hasValue {
					v, ok := next()
					if !ok {
						break
					}
					s.writes = append(s.writes, v)
					break
				}
				s.writes = append(s.writes, shWord{text: value})
				break
			}
			if curlValueLong[name] && !hasValue {
				v, _ := next()
				value = v.text
			}
			if name == "--write-out" && strings.Contains(value, "%output") {
				return unknownVerdict(display(argv), "it writes its output to a file", "")
			}
			if curlFileValues[name] && (strings.HasPrefix(value, "@") || (name == "--url-query" && strings.Contains(value, "@"))) {
				return write
			}
			if name == "--cookie" && value != "" && !strings.Contains(value, "=") {
				// A cookie value without "=" is a file whose cookies are sent.
				return write
			}
		case strings.HasPrefix(t, "-") && len(t) > 1:
			cluster := t[1:]
			for j := 0; j < len(cluster); j++ {
				ch := cluster[j]
				rest := cluster[j+1:]
				value := func() string {
					if rest != "" {
						return rest
					}
					v, _ := next()
					return v.text
				}
				switch ch {
				case 'd', 'F', 'T', 'K', 'Q', 'P', 't':
					return write
				case 'X':
					method = strings.ToUpper(value())
				case 'o', 'D', 'c':
					if rest != "" {
						s.writes = append(s.writes, shWord{text: rest})
					} else if v, ok := next(); ok {
						s.writes = append(s.writes, v)
					}
				case 'w':
					if strings.Contains(value(), "%output") {
						return unknownVerdict(display(argv), "it writes its output to a file", "")
					}
				case 'H', 'A', 'e', 'u', 'b':
					v := value()
					if strings.HasPrefix(v, "@") || (ch == 'b' && v != "" && !strings.Contains(v, "=")) {
						return write
					}
				case 'x', 'm', 'r', 'E', 'U', 'Y', 'y', 'z', 'C':
					value()
				default:
					continue
				}
				break
			}
		default:
			if scheme, _, ok := strings.Cut(t, "://"); ok {
				switch strings.ToLower(scheme) {
				case "http", "https", "file", "ftp", "ftps":
				default:
					return write
				}
			}
		}
		if dyn {
			return dynamic
		}
	}
	if method != "" && method != "GET" && method != "HEAD" {
		return write
	}
	return localVerdict()
}

func (s *bashScan) wget(argv []shWord) bashVerdict {
	args := argv[1:]
	write := actionVerdict(externalBashAction("network.request.write", "wget"))
	dynamic := unknownVerdict(display(argv), "it sends a value built at run time over the network", "")
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		if !a.literal() {
			return dynamic
		}
		dyn := false
		next := func() (shWord, bool) {
			if i+1 < len(args) {
				i++
				if !args[i].literal() {
					dyn = true
				}
				return args[i], true
			}
			return shWord{}, false
		}
		switch {
		case strings.HasPrefix(t, "--"):
			name, value, hasValue := strings.Cut(t, "=")
			switch name {
			case "--post-data", "--post-file", "--body-data", "--body-file", "--execute", "--config",
				"--input-file", "--base", "--load-cookies":
				// An input file's lines become request URLs: its contents
				// leave the machine.
				return write
			case "--method":
				if !hasValue {
					v, _ := next()
					value = v.text
				}
				if m := strings.ToUpper(value); m != "GET" && m != "HEAD" {
					return write
				}
			case "--output-document", "--output-file", "--append-output", "--directory-prefix",
				"--save-cookies", "--warc-file", "--save-headers", "--rejected-log", "--hsts-file":
				if !hasValue {
					v, ok := next()
					if !ok {
						break
					}
					s.writes = append(s.writes, v)
					break
				}
				s.writes = append(s.writes, shWord{text: value})
			case "--header", "--user-agent", "--referer", "--user", "--password", "--http-user",
				"--http-password", "--certificate", "--private-key", "--ca-certificate":
				if !hasValue {
					next()
				}
			default:
			}
		case strings.HasPrefix(t, "-") && len(t) > 1:
			if strings.HasPrefix(t, "-n") {
				continue
			}
			cluster := t[1:]
			for j := 0; j < len(cluster); j++ {
				rest := cluster[j+1:]
				switch cluster[j] {
				case 'e', 'i', 'B':
					return write
				case 'O', 'o', 'a', 'P':
					if rest != "" {
						s.writes = append(s.writes, shWord{text: rest})
					} else if v, ok := next(); ok {
						s.writes = append(s.writes, v)
					}
				case 't', 'T', 'w', 'Q', 'U', 'l', 'A', 'R', 'D', 'X', 'I':
					if rest == "" {
						next()
					}
				default:
					continue
				}
				break
			}
		default:
		}
		if dyn {
			return dynamic
		}
	}
	return localVerdict()
}

func (s *bashScan) rsync(argv []shWord) bashVerdict {
	hits, operands := scanOptions(argv[1:], "efTB", setOf("--rsh", "--rsync-path", "--filter",
		"--exclude", "--include", "--exclude-from", "--include-from", "--files-from",
		"--temp-dir", "--log-file", "--partial-dir", "--backup-dir", "--suffix", "--chmod",
		"--compare-dest", "--copy-dest", "--link-dest", "--block-size", "--bwlimit", "--port",
		"--password-file", "--timeout", "--contimeout", "--max-size", "--min-size", "--info",
		"--debug", "--out-format", "--iconv", "--checksum-choice", "--compress-choice",
		"--write-batch", "--only-write-batch", "--read-batch", "--log-file-format", "--usermap",
		"--groupmap", "--chown", "--max-delete", "--modify-window", "--sockopts", "--outbuf"))
	for _, o := range operands {
		if strings.Contains(o.text, ":") && !strings.HasPrefix(o.text, "/") && !strings.HasPrefix(o.text, ".") {
			return actionVerdict(externalBashAction("remote.filesystem.write", "rsync"))
		}
	}
	if optSeen(hits, "-e", "--rsh", "--rsync-path") {
		return opaqueVerdict(display(argv), "it runs a remote shell command")
	}
	s.writes = append(s.writes, optValues(hits, "--log-file", "--write-batch", "--only-write-batch",
		"--backup-dir", "--partial-dir", "-T", "--temp-dir")...)
	if optSeen(hits, "--remove-source-files", "--remove-sent-files") {
		// The sources are deleted once sent.
		s.writes = append(s.writes, operands...)
	} else if len(operands) > 0 {
		s.writes = append(s.writes, operands[len(operands)-1])
	}
	if len(operands) > 1 {
		dest := operands[len(operands)-1]
		sources := operands[:len(operands)-1]
		// -a and -r preserve symlinks, so the destination tree may hold one a
		// later write follows. -L and --copy-links follow symlinks while
		// reading, so a link into a protected folder is read through.
		if optSeen(hits, "-a", "--archive", "-r", "--recursive") {
			s.extractDirs = append(s.extractDirs, dest)
		}
		if optSeen(hits, "-L", "--copy-links") {
			s.followReadRoots = append(s.followReadRoots, sources...)
		}
	}
	return localVerdict()
}

// yansuBoolFlags are the yansu CLI's root boolean flags. Any other flag is
// assumed to take the next word, as cobra's command lookup does, so the verb
// after cron is found past flag values (yansu cron --prompt x update JOB).
var yansuBoolFlags = setOf("-h", "--help", "--version")

// yansuReadVerbs are the yansu CLI commands that only read. Writes to
// schedules, MCP servers and skills are external; everything else asks.
var yansuReadVerbs = map[string]map[string]bool{
	"cron":      setOf("", "list", "show", "help"),
	"mcp":       setOf("", "list", "sources", "discover", "tools", "tail", "registry", "help"),
	"skill":     setOf("", "sources", "list", "discover", "search", "help"),
	"skills":    setOf("", "sources", "list", "discover", "search", "help"),
	"knowledge": setOf("", "list", "search", "show", "help"),
	"activity":  setOf("", "list", "show", "summary", "search", "help"),
	"memory":    setOf("", "list", "search", "show", "help"),
	"local":     setOf("", "list", "path", "help"),
	"stage":     setOf("", "list", "show", "tasks", "versions", "help"),
	"scenario":  setOf("", "list", "show", "help"),
	"feedback":  setOf("", "list", "show", "pending", "help"),
	"hook":      setOf("", "status", "help"),
	"daemon":    setOf("", "status", "logs", "help"),
	"service":   setOf("", "status", "help"),
	"status":    setOf(""),
	"whoami":    setOf(""),
	"list":      nil,
	"help":      nil,
	"version":   setOf(""),
}

func yansuVerdict(argv []shWord) bashVerdict {
	texts := make([]string, len(argv)-1)
	for i, w := range argv[1:] {
		if !w.literal() && strings.HasPrefix(w.text, "\x00") {
			return opaqueVerdict(display(argv), "its arguments are not literal words")
		}
		texts[i] = w.text
	}
	pos := cobraPositionals(texts, yansuBoolFlags, 2)
	if len(pos) == 0 {
		return localVerdict()
	}
	group := pos[0]
	verb := ""
	if len(pos) > 1 {
		verb = pos[1]
	}
	if strings.ContainsRune(group, 0) || strings.ContainsRune(verb, 0) {
		return opaqueVerdict(display(argv), "its subcommand is not a literal word")
	}
	if containsText(texts, "--help") || containsText(texts, "-h") {
		return localVerdict()
	}
	switch group {
	case "cron":
		switch verb {
		case "add", "update", "delete", "run":
			// Creating, changing, running or deleting a schedule reaches the
			// desktop cron API, which applies it with no other approval.
			action := externalBashAction("schedule.write", "yansu cron "+verb)
			action.ApprovalReason = "Allow Isoform to change your schedules (yansu cron " + verb + ")?"
			return actionVerdict(action)
		default:
		}
	case "mcp":
		if verb == "registry" || yansuReadVerbs["mcp"][verb] {
			return localVerdict()
		}
		action := externalBashAction("extension.mcp.write", "yansu mcp "+verb)
		action.ApprovalReason = "Allow Isoform to change your MCP servers (yansu mcp " + verb + ")?"
		return actionVerdict(action)
	case "skill", "skills":
		if yansuReadVerbs[group][verb] {
			return localVerdict()
		}
		action := externalBashAction("extension.skill.write", "yansu skill "+verb)
		action.ApprovalReason = "Allow Isoform to change your skills (yansu skill " + verb + ")?"
		return actionVerdict(action)
	case "list", "help":
		return localVerdict()
	default:
	}
	if verbs, ok := yansuReadVerbs[group]; ok && verbs[verb] {
		return localVerdict()
	}
	return unknownVerdict("yansu "+strings.TrimSpace(group+" "+verb), "it changes Isoform data", "yansu "+strings.TrimSpace(group+" "+verb))
}

// nodeValueFlags are npm, pnpm, yarn and bun global options that take a value.
var nodeValueFlags = setOf("--registry", "--prefix", "--workspace", "--userconfig",
	"--globalconfig", "--cache", "--loglevel", "--tag", "--otp", "--access", "--scope",
	"--before", "--omit", "--include", "-C", "--dir", "--filter", "-F", "--reporter",
	"--store-dir", "--workspace-dir", "--cwd", "--mutex", "--network-timeout",
	"--cache-folder", "--modules-folder", "--config", "--cpu", "--os", "--libc")

// nodeSafeScripts are package.json script names that build, test, lint or
// start the project.
func nodeSafeScript(name string) bool {
	name = strings.ToLower(name)
	for _, prefix := range []string{"test", "build", "lint", "typecheck", "type-check", "check",
		"format", "fmt", "tsc", "dev", "start", "preview", "storybook", "e2e", "coverage",
		"clean", "compile", "watch", "serve", "verify", "validate", "prettier", "eslint", "vitest", "jest"} {
		if name == prefix || strings.HasPrefix(name, prefix+":") || strings.HasPrefix(name, prefix+"-") {
			return true
		}
	}
	return false
}

func (s *bashScan) nodePackage(name string, argv []shWord, in bashStdin, depth int) bashVerdict {
	args := argv[1:]
	if versionOnly(args) {
		return localVerdict()
	}
	// --script-shell, --userconfig, --globalconfig and --node-options let the
	// package manager run an agent-written shell, config or node flags when it
	// runs a script. (npm_config_* env is covered by dangerousEnvName.)
	for _, a := range args {
		opt, _, _ := strings.Cut(a.text, "=")
		switch opt {
		case "--script-shell", "--userconfig", "--globalconfig", "--node-options", "--npm-path":
			return unknownVerdict(display(argv), "it overrides the shell, config or node options the package manager runs scripts with", "")
		default:
		}
	}
	_, rest := splitOptions(args, nodeValueFlags)
	if name == "npm" {
		// npm -w PKG: -w takes a value for npm but is a flag for pnpm.
		_, rest = splitOptions(args, mergeSets(nodeValueFlags, setOf("-w")))
	}
	if len(rest) == 0 {
		if name == "bun" {
			for _, a := range args {
				switch a.text {
				case "-e", "--eval", "-p", "--print":
					return actionVerdict(externalBashAction("process.interpreter.inline", "bun"))
				default:
				}
			}
		}
		if name == "yarn" || name == "pnpm" || name == "bun" {
			return unknownVerdict(name+" install", "it installs packages, which runs their scripts", name+" install")
		}
		return localVerdict()
	}
	if !rest[0].literal() {
		return opaqueVerdict(display(argv), "its subcommand is not a literal word")
	}
	sub := rest[0].text
	operands := rest[1:]
	publish := actionVerdict(externalBashAction("package.publish", name+" "+sub))
	switch sub {
	case "publish", "unpublish", "deprecate", "dist-tag", "owner", "access", "team", "token",
		"star", "unstar", "hook", "pack-and-publish":
		return publish
	case "npm":
		// yarn npm publish
		if len(operands) > 0 && (operands[0].text == "publish" || operands[0].text == "tag") {
			return actionVerdict(externalBashAction("package.publish", name+" npm "+operands[0].text))
		}
		return localVerdict()
	case "dlx", "x", "create":
		return actionVerdict(externalBashAction("package.run", name+" "+sub))
	case "exec":
		if name == "npm" {
			return actionVerdict(externalBashAction("package.run", "npm exec"))
		}
		_, inner := splitOptions(operands, nil)
		if len(inner) > 0 {
			if _, hasDir, ok := commandName(inner[0]); ok && !hasDir {
				// Classify the binary with its arguments like any command,
				// so a build tool stays local and git push stays external.
				s.run(inner, bashStdin{}, depth)
				return localVerdict()
			}
		}
		return opaqueVerdict(display(argv), "it runs a package binary this check cannot name")
	case "init":
		if len(operands) > 0 && !strings.HasPrefix(operands[0].text, "-") {
			return actionVerdict(externalBashAction("package.run", name+" init "+operands[0].text))
		}
		return localVerdict()
	case "test", "t", "tst", "build", "lint", "typecheck", "type-check", "check", "ls", "list",
		"why", "outdated", "audit", "info", "view", "show", "help", "version", "doctor", "root",
		"bin", "prefix", "explain", "query", "licenses", "store", "fund", "pack", "config",
		"get", "search", "repo", "docs", "bugs", "home", "ping", "whoami", "pm":
		if sub == "config" && len(operands) > 0 && operands[0].text != "get" && operands[0].text != "list" {
			return unknownVerdict(name+" config", "it changes package manager configuration", "")
		}
		if anyDynamic(args) {
			return unknownVerdict(display(argv), "it sends a value built at run time to the package registry", "")
		}
		return localVerdict()
	case "run", "run-script", "rs":
		if len(operands) == 0 {
			return localVerdict()
		}
		if operands[0].literal() && nodeSafeScript(operands[0].text) {
			return localVerdict()
		}
		return unknownVerdict(display(argv), "it runs a package script", name+" run "+operands[0].text)
	case "install", "i", "add", "ci", "update", "up", "upgrade", "remove", "rm", "uninstall",
		"un", "link", "ln", "dedupe", "rebuild", "rb", "prune", "patch", "patch-commit", "import",
		"fetch", "install-test", "it", "a":
		return unknownVerdict(name+" "+sub, "it installs packages, which runs their scripts", name+" "+sub)
	default:
	}
	if name == "bun" && (strings.Contains(sub, ".") || strings.Contains(sub, "/")) {
		return opaqueVerdict(display(argv), "it runs a script file")
	}
	if nodeSafeScript(sub) {
		return localVerdict()
	}
	return unknownVerdict(display(argv), "it runs a package script", name+" "+sub)
}

func mergeSets(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func goVerdict(argv []shWord) bashVerdict {
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		t := args[i].text
		opt, val, hasEq := strings.Cut(t, "=")
		nextVal := func() string {
			if hasEq {
				return val
			}
			if i+1 < len(args) {
				return args[i+1].text
			}
			return ""
		}
		switch opt {
		case "-exec", "-toolexec", "--exec", "--toolexec", "-vettool", "--vettool":
			return opaqueVerdict(display(argv), "it runs a program it was given")
		case "-overlay", "--overlay":
			return opaqueVerdict(display(argv), "it remaps source files through an overlay it was given")
		case "-ldflags", "--ldflags", "-gcflags", "--gcflags", "-asmflags", "--asmflags":
			v := nextVal()
			if strings.Contains(v, "-extld") || strings.Contains(v, "-extldflags") || strings.Contains(v, "-linkmode") {
				return opaqueVerdict(display(argv), "it runs an external linker it was given")
			}
		default:
		}
	}
	_, rest := splitOptions(args, nil)
	if len(rest) == 0 {
		return localVerdict()
	}
	switch rest[0].text {
	case "run":
		return actionVerdict(externalBashAction("package.run", "go run"))
	case "generate":
		return opaqueVerdict(display(argv), "it runs the commands named in the source")
	case "install":
		// go install builds a binary into $GOBIN or $GOPATH/bin, outside the
		// work folder, so it is not a plain workspace build.
		return unknownVerdict("go install", "it installs a binary outside the work folder", "go install")
	case "env":
		for _, a := range rest[1:] {
			if a.text == "-w" || a.text == "-u" {
				return unknownVerdict("go env -w", "it changes Go configuration", "go env -w")
			}
		}
		return localVerdict()
	case "build", "test", "vet", "fmt", "mod", "list", "version", "doc", "clean", "get",
		"work", "tool", "fix", "help", "telemetry":
		return localVerdict()
	default:
	}
	return unknownVerdict("go "+rest[0].text, "it is not a known read or build command", "go "+rest[0].text)
}

func cargoVerdict(argv []shWord) bashVerdict {
	_, rest := splitOptions(argv[1:], setOf("--manifest-path", "-Z", "--config", "--color", "-C"))
	if len(rest) == 0 {
		return localVerdict()
	}
	switch rest[0].text {
	case "search", "fetch", "update", "info":
		if anyDynamic(argv[1:]) {
			return unknownVerdict(display(argv), "it sends a value built at run time to the package registry", "")
		}
	default:
	}
	switch rest[0].text {
	case "publish", "yank", "owner", "login", "logout":
		return actionVerdict(externalBashAction("package.publish", "cargo "+rest[0].text))
	case "build", "b", "test", "t", "check", "c", "clippy", "fmt", "doc", "d", "tree", "metadata",
		"bench", "clean", "update", "fetch", "search", "version", "help", "verify-project",
		"locate-project", "pkgid", "package", "vendor", "generate-lockfile", "read-manifest", "nextest":
		return localVerdict()
	default:
	}
	return unknownVerdict("cargo "+rest[0].text, "it runs a program or installs packages", "cargo "+rest[0].text)
}

// packageReadVerdict is local for read-only subcommands and unknown otherwise.
func packageReadVerdict(name string, rest []shWord, reads ...string) bashVerdict {
	if len(rest) == 0 {
		return localVerdict()
	}
	for _, r := range reads {
		if rest[0].text == r {
			return localVerdict()
		}
	}
	return unknownVerdict(name+" "+rest[0].text, "it installs or runs packages", name+" "+rest[0].text)
}

func packageManagerVerdict(name string, argv []shWord) bashVerdict {
	args := argv[1:]
	if versionOnly(args) {
		return localVerdict()
	}
	if anyDynamic(args) {
		// A search or lookup sends its operands to the package index.
		return unknownVerdict(display(argv), "it sends a value built at run time to the package index", "")
	}
	_, rest := splitOptions(args, setOf("--index-url", "-i", "--extra-index-url", "--python", "-p", "--directory", "-C", "--prefix", "--cache-dir"))
	sub := ""
	if len(rest) > 0 {
		sub = rest[0].text
	}
	switch name {
	case "twine":
		if sub == "upload" || sub == "register" {
			return actionVerdict(externalBashAction("package.publish", "twine "+sub))
		}
		return localVerdict()
	case "gem":
		switch sub {
		case "push", "yank", "owner", "signin", "signout":
			return actionVerdict(externalBashAction("package.publish", "gem "+sub))
		default:
		}
		return packageReadVerdict(name, rest, "list", "search", "env", "info", "contents", "which", "query", "specification", "help", "outdated")
	case "poetry", "pdm", "hatch":
		if sub == "publish" {
			return actionVerdict(externalBashAction("package.publish", name+" publish"))
		}
		return packageReadVerdict(name, rest, "show", "check", "env", "version", "about", "list", "build", "lock", "config")
	case "uv":
		switch sub {
		case "publish":
			return actionVerdict(externalBashAction("package.publish", "uv publish"))
		case "tool":
			if len(rest) > 1 && rest[1].text == "run" {
				return actionVerdict(externalBashAction("package.run", "uv tool run"))
			}
		case "pip":
			if len(rest) > 1 {
				return packageReadVerdict("uv pip", rest[1:], "list", "show", "freeze", "check", "tree")
			}
		default:
		}
		return packageReadVerdict(name, rest, "version", "help", "tree", "lock", "build", "cache")
	case "brew":
		return packageReadVerdict(name, rest, "list", "ls", "info", "search", "outdated", "doctor",
			"config", "deps", "leaves", "uses", "desc", "home", "--prefix", "--cellar", "--cache", "--repository", "help", "shellenv")
	case "conda", "mamba":
		return packageReadVerdict(name, rest, "list", "info", "search", "env")
	default:
		// pip, pip3
		return packageReadVerdict(name, rest, "list", "show", "freeze", "check", "config", "help", "inspect", "debug", "cache", "index", "hash")
	}
}

// dockerReadVerbs are the read-only verbs of docker's object groups.
var dockerReadVerbs = setOf("ls", "list", "inspect", "df", "info", "events", "history", "show",
	"version", "du", "help")

func dockerVerdict(name string, argv []shWord) bashVerdict {
	_, rest := splitOptions(argv[1:], setOf("-H", "--host", "--context", "-c", "--config", "-l", "--log-level", "--tlscacert", "--tlscert", "--tlskey"))
	if len(rest) == 0 {
		return localVerdict()
	}
	sub := rest[0].text
	for _, a := range rest[1:] {
		if a.text == "--push" || strings.HasPrefix(a.text, "--push=") || strings.HasPrefix(a.text, "--output=type=registry") {
			return actionVerdict(externalBashAction("package.publish", name+" "+sub+" push"))
		}
	}
	switch sub {
	case "push":
		return actionVerdict(externalBashAction("package.publish", name+" push"))
	case "login", "logout":
		return unknownVerdict(name+" "+sub, "it signs in to a registry", name+" "+sub)
	case "ps", "images", "logs", "inspect", "version", "info", "pull", "build", "history",
		"search", "stats", "top", "port", "diff", "events", "help":
		return localVerdict()
	case "image", "system", "volume", "network", "context", "manifest", "buildx", "container", "builder":
		_, inner := splitOptions(rest[1:], nil)
		verb := ""
		if len(inner) > 0 {
			verb = inner[0].text
		}
		switch {
		case verb == "" || dockerReadVerbs[verb]:
			return localVerdict()
		case verb == "push":
			return actionVerdict(externalBashAction("package.publish", name+" "+sub+" push"))
		case (sub == "image" || sub == "buildx" || sub == "builder") && (verb == "build" || verb == "pull" || verb == "bake"):
			return localVerdict()
		case sub == "container" && (verb == "logs" || verb == "top" || verb == "stats" || verb == "port" || verb == "diff"):
			return localVerdict()
		default:
		}
		return unknownVerdict(name+" "+sub+" "+verb, "it changes or removes containers, images, volumes or networks", name+" "+sub+" "+verb)
	case "compose":
		_, inner := splitOptions(rest[1:], setOf("-f", "--file", "-p", "--project-name", "--profile", "--env-file", "--project-directory"))
		if len(inner) > 0 {
			switch inner[0].text {
			case "ps", "logs", "config", "ls", "images", "version", "top", "build", "pull":
				return localVerdict()
			case "push":
				return actionVerdict(externalBashAction("package.publish", name+" compose push"))
			default:
			}
		}
		return unknownVerdict(name+" compose", "it starts or changes containers", name+" compose")
	default:
	}
	return unknownVerdict(name+" "+sub, "it starts or changes containers", name+" "+sub)
}

func makeVerdict(argv []shWord) bashVerdict {
	spec := bashOutputOptions["make"]
	hits, targets := scanOptions(argv[1:], spec.shortValues, spec.longValues)
	for _, h := range hits {
		if optNameMatches(h.name, "--eval") {
			return opaqueVerdict(display(argv), "it evaluates makefile text it was given")
		}
	}
	for _, file := range optValues(hits, "-f", "--file", "--makefile") {
		if !file.literal() || file.text == "-" || strings.HasPrefix(file.text, "/dev/") {
			return opaqueVerdict(display(argv), "it reads a makefile from its input")
		}
		if filepath.IsAbs(file.text) || strings.Contains(file.text, "..") {
			return unknownVerdict("make -f "+file.text, "it runs a makefile from outside the project", "")
		}
	}
	for _, target := range targets {
		if !target.literal() {
			return opaqueVerdict(display(argv), "its targets are not literal words")
		}
		if isAssignment(target.text) {
			return unknownVerdict(display(argv), "it overrides a make variable, which can change the commands it runs", "")
		}
		lower := strings.ToLower(target.text)
		segments := strings.FieldsFunc(lower, func(r rune) bool {
			return r == '-' || r == '_' || r == '.' || r == ':' || r == '/'
		})
		for _, seg := range segments {
			switch seg {
			case "publish", "release", "deploy", "push", "upload", "install", "ship", "tag":
				// Match whole segments so a build or lint target such as
				// build-stage or lint-staged does not look like a publish.
				return unknownVerdict("make "+target.text, "its target may publish or install", "make "+target.text)
			default:
			}
		}
	}
	return localVerdict()
}

// infraReadVerbs are cloud and cluster CLI verbs that only read.
func infraRead(name string, rest []shWord) bool {
	if len(rest) == 0 {
		return true
	}
	words := make([]string, 0, len(rest))
	for _, w := range rest {
		if strings.HasPrefix(w.text, "-") {
			continue
		}
		words = append(words, strings.ToLower(w.text))
	}
	if len(words) == 0 {
		return true
	}
	last := words[len(words)-1]
	switch name {
	case "terraform", "tofu":
		switch words[0] {
		case "plan", "validate", "fmt", "show", "output", "graph", "providers", "version", "init", "console", "get", "test":
			return true
		case "state", "workspace":
			return len(words) > 1 && (words[1] == "list" || words[1] == "show" || words[1] == "pull")
		default:
		}
		return false
	case "pulumi":
		switch words[0] {
		case "preview", "whoami", "version", "about", "config", "logs":
			return words[0] != "config" || len(words) == 1 || words[1] == "get"
		case "stack":
			return len(words) == 1 || words[1] == "ls" || words[1] == "output" || words[1] == "export" || words[1] == "history"
		default:
		}
		return false
	case "kubectl":
		switch words[0] {
		case "api-resources", "api-versions", "cluster-info", "describe", "diff", "explain", "get",
			"logs", "top", "version", "auth", "config":
			return words[0] != "config" || len(words) < 2 || strings.HasPrefix(words[1], "get") ||
				words[1] == "view" || words[1] == "current-context"
		default:
		}
		return false
	case "helm":
		switch words[0] {
		case "list", "ls", "status", "get", "show", "template", "lint", "version", "search", "history", "env", "dependency":
			return true
		default:
		}
		return false
	case "aws":
		for _, w := range words {
			if strings.HasPrefix(w, "describe-") || strings.HasPrefix(w, "list-") || strings.HasPrefix(w, "get-") ||
				w == "ls" || w == "get-caller-identity" || w == "help" {
				return true
			}
		}
		return words[0] == "configure" && len(words) > 1 && (words[1] == "list" || words[1] == "get")
	case "gcloud", "az", "doctl", "gsutil":
		return last == "list" || last == "describe" || last == "show" || last == "info" ||
			last == "version" || strings.HasPrefix(last, "get") || last == "ls" || last == "cat" ||
			(len(words) > 1 && (words[len(words)-2] == "list" || words[len(words)-2] == "describe" || words[len(words)-2] == "show"))
	case "vercel", "netlify", "fly", "flyctl", "heroku", "railway", "wrangler", "firebase", "supabase":
		switch words[0] {
		case "ls", "list", "whoami", "inspect", "logs", "status", "version", "help", "info", "open":
			return true
		case "env", "secrets", "apps", "projects", "domains":
			return len(words) == 1 || words[1] == "ls" || words[1] == "list"
		default:
		}
		return false
	default:
	}
	return false
}

func infraVerdict(name string, argv []shWord) bashVerdict {
	args := argv[1:]
	if versionOnly(args) {
		return localVerdict()
	}
	_, rest := splitOptions(args, setOf("--profile", "--region", "--project", "-n", "--namespace",
		"--context", "--kubeconfig", "--output", "-o", "--cluster", "--subscription", "--format",
		"-chdir", "--cwd", "--scope", "--team", "-t", "--token", "-a", "--app"))
	for _, w := range rest {
		if !w.literal() {
			return unknownVerdict(display(argv), "its arguments are not literal words", "")
		}
	}
	if infraRead(name, rest) {
		return localVerdict()
	}
	return actionVerdict(privilegedBashAction("infrastructure.mutate", name))
}

// python reads python's options: -c (alone or in a cluster such as -Sc) is
// inline code, -m runs a module, and the first operand is a script file.
func (s *bashScan) python(name string, argv []shWord, in bashStdin) bashVerdict {
	args := argv[1:]
	inline := actionVerdict(externalBashAction("process.interpreter.inline", name))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.literal() {
			return opaqueVerdict(display(argv), "its arguments are not literal words")
		}
		t := a.text
		switch {
		case t == "-":
			return s.stdinCode(name, argv, in, inline)
		case t == "--":
			if i+1 < len(args) {
				return opaqueVerdict(display(argv), "it runs a script file")
			}
			return s.stdinCode(name, argv, in, inline)
		case strings.HasPrefix(t, "--"):
			if t == "--command" || strings.HasPrefix(t, "--command=") {
				return inline
			}
			continue
		case strings.HasPrefix(t, "-") && len(t) > 1:
			cluster := t[1:]
			for j := 0; j < len(cluster); j++ {
				switch cluster[j] {
				case 'c':
					return inline
				case 'm':
					module := cluster[j+1:]
					next := i + 1
					if module == "" && next < len(args) {
						module = args[next].text
						next++
					}
					return s.pythonModule(name, module, args[min(next, len(args)):])
				case 'W', 'X', 'Q':
					if j == len(cluster)-1 {
						i++
					}
					j = len(cluster)
				default:
				}
			}
		default:
			return opaqueVerdict(name+" "+t, "it runs a script file")
		}
	}
	return s.stdinCode(name, argv, in, inline)
}

// stdinCode classifies an interpreter that reads its program from input.
func (s *bashScan) stdinCode(name string, argv []shWord, in bashStdin, inline bashVerdict) bashVerdict {
	if in.present {
		return inline
	}
	return localVerdict()
}

// pythonLocalModules run tests, linters, formatters or print information.
var pythonLocalModules = setOf("pytest", "unittest", "mypy", "black", "ruff", "flake8", "pylint",
	"isort", "compileall", "py_compile", "json.tool", "venv", "doctest", "pydoc", "timeit",
	"tokenize", "ast", "dis", "pyclbr", "site", "sysconfig", "platform", "coverage",
	"pyright", "tabnanny", "calendar", "zipfile", "tarfile", "gzip", "base64", "uuid",
	"this", "pstats", "pip_audit", "build", "wheel")

// pythonModule classifies python -m: pip goes through the package rules,
// network and browser modules ask, and the local modules that write files
// (json.tool's output file, venv, compileall, gzip, zipfile, tarfile, pydoc
// -w) record what they write.
func (s *bashScan) pythonModule(name, module string, rest []shWord) bashVerdict {
	switch module {
	case "pip":
		return packageManagerVerdict("pip", append([]shWord{{text: "pip"}}, rest...))
	case "http.server", "smtpd", "ftplib", "telnetlib", "xmlrpc.server", "webbrowser":
		return unknownVerdict(name+" -m "+module, "it opens a network service or a browser", name+" -m "+module)
	default:
	}
	if s.workFolderShadowsModule(module) {
		// python -m puts the work folder first on sys.path, so a file the run
		// wrote with this module's name would run instead of the real one.
		return opaqueVerdict(name+" -m "+module, "the work folder has a module of this name, so -m could run a file the run wrote")
	}
	if !pythonLocalModules[module] {
		return unknownVerdict(name+" -m "+module, "it runs a module that is not a known test or build tool", name+" -m "+module)
	}
	hits, operands := scanOptions(rest, "dxiope", setOf("--indent", "--prompt", "--extract", "--create",
		"--list", "--test", "--output", "--outdir"))
	switch module {
	case "json.tool":
		// json.tool's first operand is read and the second written; every
		// operand after the first counts.
		_, words := scanOptions(rest, "", setOf("--indent"))
		if len(words) > 1 {
			s.writes = append(s.writes, words[1:]...)
		}
	case "venv", "compileall", "gzip", "py_compile":
		s.writes = append(s.writes, operands...)
	case "zipfile", "tarfile":
		if optSeen(hits, "-c", "--create", "-e", "--extract") {
			s.writes = append(s.writes, operands...)
		}
	case "build":
		s.writes = append(s.writes, optValues(hits, "-o", "--outdir")...)
	case "pydoc":
		if optSeen(hits, "-p", "-b") {
			return unknownVerdict(name+" -m pydoc", "it opens a network service or a browser", name+" -m pydoc")
		}
	default:
	}
	return localVerdict()
}

func (s *bashScan) node(name string, argv []shWord, in bashStdin) bashVerdict {
	args := argv[1:]
	inline := actionVerdict(externalBashAction("process.interpreter.inline", name))
	check := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.literal() {
			return opaqueVerdict(display(argv), "its arguments are not literal words")
		}
		t := a.text
		switch {
		case t == "-e" || t == "--eval" || t == "-p" || t == "--print" ||
			strings.HasPrefix(t, "--eval=") || strings.HasPrefix(t, "--print="):
			return inline
		case t == "-r" || t == "--require" || t == "--import" || t == "--loader" ||
			t == "--experimental-loader" || strings.HasPrefix(t, "--require=") ||
			strings.HasPrefix(t, "--import=") || strings.HasPrefix(t, "--loader=") ||
			strings.HasPrefix(t, "--inspect"):
			return opaqueVerdict(display(argv), "it loads a module or opens a debugger port")
		case t == "-c" || t == "--check":
			check = true
		case t == "-":
			return s.stdinCode(name, argv, in, inline)
		case strings.HasPrefix(t, "--"):
		case strings.HasPrefix(t, "-") && len(t) > 1:
			if strings.ContainsAny(t[1:], "ep") {
				return inline
			}
		default:
			if check {
				return localVerdict()
			}
			return opaqueVerdict(name+" "+t, "it runs a script file")
		}
	}
	return s.stdinCode(name, argv, in, inline)
}

func (s *bashScan) deno(argv []shWord, in bashStdin) bashVerdict {
	args := argv[1:]
	_, rest := splitOptions(args, nil)
	if len(rest) == 0 {
		if versionOnly(args) {
			return localVerdict()
		}
		return s.stdinCode("deno", argv, in, actionVerdict(externalBashAction("process.interpreter.inline", "deno")))
	}
	switch rest[0].text {
	case "eval", "repl":
		return actionVerdict(externalBashAction("process.interpreter.inline", "deno "+rest[0].text))
	case "test", "bench", "check", "fmt", "lint", "doc", "info", "types", "coverage", "help", "upgrade", "outdated":
		return localVerdict()
	default:
	}
	return opaqueVerdict(display(argv), "it runs a program")
}

// interpreterInline lists each interpreter's inline-code options.
var interpreterInline = map[string]string{
	"ruby": "e", "perl": "eE", "php": "rRBE", "lua": "e", "luajit": "e", "rscript": "e", "r": "e",
	"groovy": "e", "julia": "eE", "elixir": "e", "erl": "e", "racket": "e", "guile": "c",
	"sbcl": "", "swipl": "g", "ocaml": "", "scala": "e", "kotlin": "e", "clojure": "e",
	"clj": "e", "bb": "e", "tsx": "ep", "ts-node": "ep", "esno": "ep", "qjs": "e",
}

// interpreter handles the remaining language runtimes: an inline-code option
// in any short cluster (perl -pe, ruby -ne) is inline code, an operand is a
// script file, and input from a pipe or here-doc is inline code.
func (s *bashScan) interpreter(name string, argv []shWord, in bashStdin) bashVerdict {
	args := argv[1:]
	if versionOnly(args) {
		return localVerdict()
	}
	inline := actionVerdict(externalBashAction("process.interpreter.inline", name))
	letters := interpreterInline[name]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.literal() {
			return opaqueVerdict(display(argv), "its arguments are not literal words")
		}
		t := a.text
		switch {
		case t == "--eval" || strings.HasPrefix(t, "--eval=") || t == "--print" ||
			strings.HasPrefix(t, "--print=") || t == "--command" || t == "--expression":
			return inline
		case t == "-" || t == "--":
			return s.stdinCode(name, argv, in, inline)
		case strings.HasPrefix(t, "--"):
		case strings.HasPrefix(t, "-") && len(t) > 1:
			if letters != "" && strings.ContainsAny(t[1:2], letters) {
				return inline
			}
			if name == "perl" || name == "ruby" {
				// perl -pi -e, ruby -ne: the code letter may follow others.
				for _, ch := range t[1:] {
					if strings.ContainsRune(letters, ch) {
						return inline
					}
					if strings.ContainsRune("IiMmFx0lC", ch) {
						break
					}
				}
			}
			if name == "php" && (t == "-f" || t == "-S" || t == "-t") {
				return opaqueVerdict(display(argv), "it runs a script file or a server")
			}
			if name == "ruby" && t == "-S" {
				return opaqueVerdict(display(argv), "it runs a program from the search path")
			}
		default:
			return opaqueVerdict(name+" "+t, "it runs a script file")
		}
	}
	return s.stdinCode(name, argv, in, inline)
}

// runtimeVerdict covers java: a jar, class or source file runs a program.
func runtimeVerdict(name string, args []shWord, in bashStdin, _ map[string]bool) bashVerdict {
	if versionOnly(args) || (len(args) == 1 && (args[0].text == "-version" || args[0].text == "--version")) {
		return localVerdict()
	}
	return opaqueVerdict(name, "it runs a program")
}

// awk reads its program: system(), a pipe or an extension runs commands,
// and print or printf redirected to a file writes it. A literal file name is
// checked like any other write; a name computed at run time is unknown.
func (s *bashScan) awk(argv []shWord) bashVerdict {
	// gawk's -o, -p and -d (and their long forms) take a file only when it
	// is attached, so they never consume the next word.
	hits, operands := scanOptions(argv[1:], "FvfeEilW", setOf("--file", "--source", "--assign",
		"--field-separator", "--exec", "--include", "--load"))
	if optSeen(hits, "-f", "--file", "-E", "--exec", "-i", "--include", "-l", "--load") {
		return opaqueVerdict(display(argv), "it runs an awk program file")
	}
	for _, h := range hits {
		if optNameMatches(h.name, "--dump-variables") || optNameMatches(h.name, "--profile") ||
			optNameMatches(h.name, "--pretty-print") || h.name == "-o" || h.name == "-p" || h.name == "-d" {
			if h.hasValue {
				s.writes = append(s.writes, h.value)
			} else {
				s.writes = append(s.writes, shWord{text: "awkvars.out"}, shWord{text: "awkprof.out"})
			}
		}
	}
	programs := optValues(hits, "-e", "--source")
	if len(programs) == 0 {
		if len(operands) == 0 {
			return localVerdict()
		}
		programs = operands[:1]
	}
	for _, program := range programs {
		if !program.literal() {
			return opaqueVerdict(display(argv), "its program is not a literal")
		}
		text := program.text
		if strings.Contains(text, "system") || strings.Contains(text, "|") || strings.Contains(text, "@load") ||
			strings.Contains(text, "@include") {
			return unknownVerdict(display(argv), "its program runs commands", "")
		}
		targets, ok := awkOutputFiles(text)
		if !ok {
			return unknownVerdict(display(argv), "its program writes files it names at run time", "")
		}
		for _, t := range targets {
			s.writes = append(s.writes, shWord{text: t})
		}
	}
	return localVerdict()
}

// awkOutputFiles returns the files an awk program's print and printf
// statements redirect to. It reports false when a target is not a string
// literal. A '>' inside parentheses or a string is a comparison or text.
func awkOutputFiles(program string) ([]string, bool) {
	var files []string
	n := len(program)
	inPrint := false
	depth := 0
	ident := func(c byte) bool {
		return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	for i := 0; i < n; i++ {
		c := program[i]
		switch {
		case c == '"':
			// Skip a string literal.
			for i++; i < n && program[i] != '"'; i++ {
				if program[i] == '\\' {
					i++
				}
			}
		case c == ';' || c == '\n' || c == '{' || c == '}':
			inPrint, depth = false, 0
		case c == '(':
			depth++
		case c == ')':
			depth--
		case strings.HasPrefix(program[i:], "print") && (i == 0 || !ident(program[i-1])):
			word := 5
			if strings.HasPrefix(program[i:], "printf") {
				word = 6
			}
			if i+word >= n || !ident(program[i+word]) {
				inPrint, depth = true, 0
				i += word - 1
			}
		case c == '>' && inPrint && depth <= 0:
			i++
			if i < n && program[i] == '>' {
				i++
			}
			for i < n && (program[i] == ' ' || program[i] == '\t') {
				i++
			}
			if i >= n || program[i] != '"' {
				return nil, false
			}
			end := strings.IndexByte(program[i+1:], '"')
			if end < 0 {
				return nil, false
			}
			name := program[i+1 : i+1+end]
			if strings.Contains(name, "\\") {
				return nil, false
			}
			files = append(files, name)
			i += end + 1
			// A concatenated name ("out" $1) is computed at run time.
			rest := strings.TrimLeft(program[i+1:], " \t")
			if rest != "" && !strings.ContainsRune(";}\n", rune(rest[0])) {
				return nil, false
			}
		default:
		}
	}
	return files, true
}

func (s *bashScan) sed(argv []shWord) bashVerdict {
	args := argv[1:]
	var scripts []shWord
	var operands []shWord
	inPlace := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		switch {
		case t == "-e" || t == "--expression":
			if i+1 < len(args) {
				i++
				scripts = append(scripts, args[i])
			}
		case strings.HasPrefix(t, "--expression="):
			scripts = append(scripts, shWord{text: t[len("--expression="):], dynamic: a.dynamic})
		case t == "-f" || t == "--file" || strings.HasPrefix(t, "--file="):
			return opaqueVerdict(display(argv), "it runs a sed script file")
		case t == "-l" || t == "--line-length":
			i++
		case strings.HasPrefix(t, "--in-place") || t == "-I":
			inPlace = true
		case strings.HasPrefix(t, "-") && len(t) > 1 && !strings.HasPrefix(t, "--"):
			cluster := t[1:]
			for j := 0; j < len(cluster); j++ {
				switch cluster[j] {
				case 'i', 'I':
					inPlace = true
					j = len(cluster)
				case 'e':
					if j+1 < len(cluster) {
						scripts = append(scripts, shWord{text: cluster[j+1:], dynamic: a.dynamic})
					} else if i+1 < len(args) {
						i++
						scripts = append(scripts, args[i])
					}
					j = len(cluster)
				case 'f':
					return opaqueVerdict(display(argv), "it runs a sed script file")
				case 'l':
					if j == len(cluster)-1 {
						i++
					}
					j = len(cluster)
				default:
				}
			}
		case strings.HasPrefix(t, "--"):
		default:
			operands = append(operands, a)
		}
	}
	if len(scripts) == 0 && len(operands) > 0 {
		scripts = append(scripts, operands[0])
		operands = operands[1:]
	}
	for _, script := range scripts {
		if !script.literal() {
			return opaqueVerdict(display(argv), "its script is not a literal")
		}
		if !sedScriptSafe(script.text) {
			return unknownVerdict(display(argv), "its script runs commands or writes files", "")
		}
	}
	if inPlace {
		s.writes = append(s.writes, operands...)
	}
	return localVerdict()
}

// sedScriptSafe reports a sed script without the e command or flag (run a
// command) and without the w command or flag (write a file).
func sedScriptSafe(script string) bool {
	i := 0
	n := len(script)
	skipSpace := func() {
		for i < n && (script[i] == ' ' || script[i] == '\t' || script[i] == ';' || script[i] == '\n') {
			i++
		}
	}
	skipRegex := func() bool {
		if i >= n {
			return false
		}
		delim := script[i]
		if delim == '\\' {
			i++
			if i >= n {
				return false
			}
			delim = script[i]
		}
		i++
		for i < n && script[i] != delim {
			if script[i] == '\\' {
				i++
			}
			i++
		}
		if i >= n {
			return false
		}
		i++
		return true
	}
	skipAddress := func() bool {
		for k := 0; k < 2; k++ {
			switch {
			case i < n && (script[i] >= '0' && script[i] <= '9'):
				for i < n && ((script[i] >= '0' && script[i] <= '9') || script[i] == '~') {
					i++
				}
			case i < n && script[i] == '$':
				i++
			case i < n && (script[i] == '/' || script[i] == '\\'):
				if !skipRegex() {
					return false
				}
				for i < n && (script[i] == 'I' || script[i] == 'M') {
					i++
				}
			default:
			}
			if i < n && script[i] == ',' {
				i++
				continue
			}
			break
		}
		for i < n && (script[i] == ' ' || script[i] == '!') {
			i++
		}
		return true
	}
	for {
		skipSpace()
		if i >= n {
			return true
		}
		if !skipAddress() {
			return false
		}
		if i >= n {
			return true
		}
		cmd := script[i]
		i++
		switch cmd {
		case '{', '}':
		case 'e', 'w', 'W':
			return false
		case 's', 'y':
			i--
			i++
			if !skipRegex() {
				return false
			}
			// skipRegex consumed the pattern and its closing delimiter;
			// the replacement runs to the next unescaped delimiter.
			delim := script[i-1]
			for i < n && script[i] != delim {
				if script[i] == '\\' {
					i++
				}
				i++
			}
			if i >= n {
				return false
			}
			i++
			if cmd == 's' {
				for i < n && script[i] != ';' && script[i] != '\n' && script[i] != '}' {
					if script[i] == 'e' || script[i] == 'w' {
						return false
					}
					i++
				}
			}
		case 'a', 'i', 'c', 'r', 'R', 'b', 't', 'T', ':', 'q', 'Q', 'l', 'L':
			for i < n && script[i] != '\n' && (cmd == 'a' || cmd == 'i' || cmd == 'c' || script[i] != ';') {
				i++
			}
		default:
		}
	}
}

// sqliteSafeDotCommands are sqlite3 dot-commands that only print.
var sqliteSafeDotCommands = setOf(".tables", ".table", ".schema", ".headers", ".header", ".mode",
	".indexes", ".indices", ".dump", ".help", ".databases", ".show", ".width", ".nullvalue",
	".separator", ".timer", ".changes", ".eqp", ".stats", ".fullschema", ".dbinfo", ".print",
	".bail", ".echo", ".quit", ".exit", ".explain")

// sqliteRiskyWords open, write or attach other files, or run commands.
var sqliteRiskyWords = []string{"load_extension", "edit(", "writefile", "readfile", "attach", "vacuum",
	"fts3_tokenizer", "|"}

// sqliteReadOnly reports SQL whose every statement only reads.
func sqliteReadOnly(sql string) bool {
	// Dot-command lines were checked on their own.
	var lines []string
	for _, line := range strings.Split(sql, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), ".") {
			lines = append(lines, line)
		}
	}
	for _, stmt := range strings.Split(strings.Join(lines, "\n"), ";") {
		words := strings.Fields(strings.ToLower(stmt))
		if len(words) == 0 {
			continue
		}
		switch {
		case words[0] == "select" || words[0] == "explain" || words[0] == "with" || words[0] == "values":
		case words[0] == "pragma":
			if strings.Contains(stmt, "=") {
				return false
			}
		default:
			return false
		}
		for _, w := range words {
			switch strings.Trim(w, "(),") {
			case "insert", "update", "delete", "replace", "create", "drop", "alter", "reindex":
				return false
			default:
			}
		}
	}
	return true
}

// sqlite reads sqlite3's SQL: a dot-command outside the print-only set or a
// function that touches other files makes it unknown. The database file is
// written unless it is opened read-only or every statement only reads.
func (s *bashScan) sqlite(argv []shWord, in bashStdin) bashVerdict {
	args := argv[1:]
	risky := func(text string) bool {
		lower := strings.ToLower(text)
		for _, marker := range sqliteRiskyWords {
			if strings.Contains(lower, marker) {
				return true
			}
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, ".") {
				if !sqliteSafeDotCommands[strings.ToLower(strings.Fields(line)[0])] {
					return true
				}
			}
		}
		return false
	}
	var operands []shWord
	readOnly := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.literal() {
			return unknownVerdict(display(argv), "its arguments are not literal words", "")
		}
		t := a.text
		switch {
		case strings.HasPrefix(t, "-cmd") || strings.HasPrefix(t, "--cmd") || t == "-init" || t == "--init" ||
			strings.HasPrefix(t, "-A") || t == "--archive" || t == "-zip" || t == "--zip":
			return unknownVerdict(display(argv), "it can run commands or write files", "")
		case t == "-readonly" || t == "--readonly" || t == "-safe" || t == "--safe":
			readOnly = true
		case t == "-separator" || t == "-newline" || t == "-nullvalue" || t == "-vfs" || t == "-mmap" ||
			t == "-maxsize" || t == "-heap" || t == "--separator" || t == "--newline" || t == "--nullvalue":
			i++
		case t == "-lookaside" || t == "-pagecache":
			i += 2
		case strings.HasPrefix(t, "-"):
		default:
			operands = append(operands, a)
		}
	}
	sql := ""
	for i, op := range operands {
		if i > 0 {
			sql += op.text + "\n"
		}
	}
	if risky(sql) {
		return unknownVerdict(display(argv), "it can run commands or write files", "")
	}
	if len(operands) <= 1 && in.present {
		if !in.known || risky(in.text) {
			return unknownVerdict(display(argv), "it reads SQL from input that can run commands", "")
		}
		sql = in.text
	}
	if len(operands) > 0 && !readOnly && operands[0].text != ":memory:" && (sql == "" || !sqliteReadOnly(sql)) {
		s.writes = append(s.writes, operands[0])
	}
	return localVerdict()
}

// tarValueLetters are tar's short options that take a value.
const tarValueLetters = "fCbTXIFHLNVKgs"

// tarValueLongs are tar's long options that take a value.
var tarValueLongs = setOf("--file", "--directory", "--blocking-factor", "--files-from", "--exclude-from",
	"--use-compress-program", "--info-script", "--new-volume-script", "--format", "--tape-length",
	"--newer", "--label", "--starting-file", "--listed-incremental", "--to-command", "--rsh-command",
	"--checkpoint-action", "--transform", "--xform", "--exclude", "--owner", "--group", "--mode", "--mtime",
	"--strip-components", "--options", "--index-file", "--volno-file", "--backup", "--suffix")

// tar reads tar's options in every form: the old first word without a dash
// ("xCf dir a.tar"), clusters ("-xC/dir"), and options after the operands.
// It records the archive it creates and the folders it extracts into. An
// extraction that keeps absolute names or rewrites them can write anywhere.
func (s *bashScan) tar(argv []shWord) bashVerdict {
	args := argv[1:]
	var hits []optHit
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0].text, "-") && args[0].literal() &&
		!strings.ContainsAny(args[0].text, "/.") {
		// Old style: each value letter takes the next word in turn.
		next := 1
		for _, c := range args[0].text {
			h := optHit{name: "-" + string(c)}
			if strings.ContainsRune(tarValueLetters, c) && next < len(args) {
				h.value, h.hasValue = args[next], true
				next++
			}
			hits = append(hits, h)
		}
		rest = args[next:]
	}
	more, _ := scanOptions(rest, tarValueLetters, tarValueLongs)
	hits = append(hits, more...)
	if optSeen(hits, "--to-command", "--checkpoint-action", "--use-compress-program", "--rsh-command",
		"--info-script", "--new-volume-script", "-I", "-F") {
		return opaqueVerdict(display(argv), "it runs a program it was given")
	}
	create := optSeen(hits, "-c", "-r", "-u", "--create", "--append", "--update")
	extract := optSeen(hits, "-x", "--extract", "--get")
	for _, file := range optValues(hits, "-f", "--file") {
		if strings.Contains(file.text, ":") && !strings.HasPrefix(file.text, "/") {
			return opaqueVerdict(display(argv), "it reaches a remote archive")
		}
		if create {
			s.writes = append(s.writes, file)
		}
	}
	dirs := optValues(hits, "-C", "--directory")
	s.cds = append(s.cds, dirs...)
	if extract {
		if optSeen(hits, "-P", "--absolute-names", "-s", "--transform", "--xform") {
			return unknownVerdict(display(argv), "it extracts with absolute or rewritten names, which can land anywhere", "")
		}
		if len(dirs) == 0 {
			dirs = []shWord{{text: "."}}
		}
		s.writes = append(s.writes, dirs...)
		// The archive may hold symlinks, so a later write under the
		// extraction folder could follow one outside the workspace.
		s.extractDirs = append(s.extractDirs, dirs...)
	}
	if create && optSeen(hits, "-h", "--dereference") {
		// Creating with -h dereferences a workspace symlink, so a symlink
		// into a protected folder is read through it.
		_, operands := scanOptions(rest, tarValueLetters, tarValueLongs)
		s.followReadRoots = append(s.followReadRoots, operands...)
	}
	return localVerdict()
}
