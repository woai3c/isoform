package tools

import (
	"strings"
)

// This file holds the rules of system-setting tools, compilers and build
// runners: the commands that read the system unless given a verb that
// changes it, or that run plugins, linkers and tasks named on the command
// line.

// systemSettingVerdict is local for the read-only verbs of diskutil,
// networksetup, pmset, scutil, spctl, csrutil, mdutil and log, and unknown
// (approved per exact command) for everything else they do.
func systemSettingVerdict(name string, argv []shWord) bashVerdict {
	args := argv[1:]
	change := func(what string) bashVerdict {
		return unknownVerdict(display(argv), "it "+what, "")
	}
	if anyDynamic(args) {
		return change("changes system settings with a value built at run time")
	}
	_, operands := scanOptions(args, "", nil)
	first, second := "", ""
	if len(operands) > 0 {
		first = strings.ToLower(operands[0].text)
	}
	if len(operands) > 1 {
		second = strings.ToLower(operands[1].text)
	}
	switch name {
	case "diskutil":
		switch first {
		case "", "list", "info", "information", "activity", "listfilesystems", "help":
			return localVerdict()
		case "apfs", "cs", "corestorage", "ar", "appleraid", "image":
			switch second {
			case "", "list", "listcryptousers", "listsnapshots", "listvolumegroups", "info", "listusers":
				return localVerdict()
			default:
			}
		default:
		}
		return change("changes disks or volumes")
	case "networksetup":
		for _, a := range args {
			lower := strings.ToLower(a.text)
			if !strings.HasPrefix(lower, "-") {
				continue
			}
			read := false
			for _, prefix := range []string{"-get", "-list", "-print", "-help", "-version", "-show", "-isbluetooth"} {
				if strings.HasPrefix(lower, prefix) {
					read = true
				}
			}
			if !read {
				return change("changes network settings")
			}
		}
		return localVerdict()
	case "pmset":
		if len(args) == 0 || args[0].text == "-g" {
			return localVerdict()
		}
		return change("changes power settings or power schedules")
	case "scutil":
		if len(args) == 0 {
			return change("reads commands that can change system settings")
		}
		switch args[0].text {
		case "--get", "--dns", "--proxy", "--nwi", "-r", "-W", "-w":
			return localVerdict()
		case "--nc":
			switch second {
			case "list", "status", "show", "help":
				return localVerdict()
			default:
			}
		default:
		}
		return change("changes system settings")
	case "spctl":
		if first == "kext-consent" && (second == "status" || second == "list") {
			return localVerdict()
		}
		allowed := setOf("--status", "--assess", "-a", "-t", "--type", "-v", "-vv", "-vvv", "--verbose",
			"--raw", "--context", "-c", "--list", "--ignore-cache", "--no-cache", "-R", "--requirement")
		for _, a := range args {
			if strings.HasPrefix(a.text, "-") && !allowed[a.text] {
				return change("changes app security settings")
			}
		}
		if first == "kext-consent" {
			return change("changes app security settings")
		}
		return localVerdict()
	case "csrutil":
		switch {
		case first == "" || first == "status" || first == "report":
			return localVerdict()
		case first == "authenticated-root" && second == "status":
			return localVerdict()
		default:
		}
		return change("changes System Integrity Protection")
	case "mdutil":
		for _, a := range args {
			t := a.text
			if len(t) > 1 && t[0] == '-' && strings.ContainsAny(t[1:], "idEpX") {
				return change("changes or erases the Spotlight index")
			}
		}
		return localVerdict()
	case "log":
		switch first {
		case "", "show", "stream", "stats", "help", "collect":
			return localVerdict()
		default:
		}
		return change("changes system logging")
	default:
	}
	return change("changes system settings")
}

// buildRunnerPublish classifies a build-runner task or option that publishes,
// deploys, uploads or pushes as external.
func buildRunnerPublish(name string, args []shWord) (bashVerdict, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.literal() {
			return unknownVerdict(name, "its tasks are not literal words", ""), true
		}
		lower := strings.ToLower(a.text)
		if lower == "--publish=never" {
			continue
		}
		if (lower == "--publish" || lower == "-p") && i+1 < len(args) && args[i+1].text == "never" {
			i++
			continue
		}
		for _, w := range bashPublishWords {
			if strings.Contains(lower, w) {
				return actionVerdict(externalBashAction("package.publish", name+" "+a.text)), true
			}
		}
	}
	_, operands := splitOptions(args, nil)
	if len(operands) > 0 {
		sub := operands[0].text
		if (name == "lerna" && sub == "version") || (name == "nx" && sub == "release") {
			return actionVerdict(externalBashAction("package.publish", name+" "+sub)), true
		}
	}
	return bashVerdict{}, false
}

// mvn runs the goals it is given: an exec goal runs any program, and a
// deploy, release or upload goal publishes.
func (s *bashScan) mvn(argv []shWord) bashVerdict {
	args := argv[1:]
	if versionOnly(args) {
		return localVerdict()
	}
	for i, a := range args {
		if !a.literal() {
			return unknownVerdict(display(argv), "its goals are not literal words", "")
		}
		t := a.text
		if (t == "-l" || t == "--log-file") && i+1 < len(args) {
			s.writes = append(s.writes, args[i+1])
		}
		if strings.HasPrefix(t, "-") {
			continue
		}
		lower := strings.ToLower(t)
		if strings.HasPrefix(lower, "exec:") || strings.Contains(lower, ":exec") {
			return opaqueVerdict(display(argv), "it runs a program it was given")
		}
		for _, w := range append([]string{"release"}, bashPublishWords...) {
			if strings.Contains(lower, w) {
				return actionVerdict(externalBashAction("package.publish", "mvn "+t))
			}
		}
	}
	return localVerdict()
}

// buf builds and lints locally; push publishes, curl sends requests, and the
// registry commands sign in or change the Buf registry.
func (s *bashScan) buf(argv []shWord) bashVerdict {
	args := argv[1:]
	if versionOnly(args) {
		return localVerdict()
	}
	_, rest := splitOptions(args, setOf("--config", "--log-format", "--timeout"))
	if len(rest) == 0 {
		return localVerdict()
	}
	sub := rest[0].text
	switch sub {
	case "push":
		return actionVerdict(externalBashAction("package.publish", "buf push"))
	case "curl":
		return actionVerdict(externalBashAction("network.request.write", "buf curl"))
	case "registry", "beta", "alpha", "login", "logout", "plugin":
		return unknownVerdict("buf "+sub, "it signs in to or changes the Buf registry", "buf "+sub)
	case "export", "generate":
		hits, _ := scanOptions(rest[1:], "o", setOf("--output", "--template", "--path", "--exclude-path", "--config", "--type"))
		s.writes = append(s.writes, optValues(hits, "-o", "--output")...)
	default:
	}
	return localVerdict()
}

// protoc runs plugins: one named by path is any program. The --*_out
// options name the folders it writes ("--go_out=opt:dir").
func (s *bashScan) protoc(argv []shWord) bashVerdict {
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		switch {
		case strings.HasPrefix(t, "--plugin"):
			return opaqueVerdict(display(argv), "it runs a protoc plugin it was given")
		case strings.HasPrefix(t, "@"):
			return unknownVerdict(display(argv), "it reads its options from a file", "")
		case t == "-o" && i+1 < len(args):
			i++
			s.writes = append(s.writes, args[i])
		case strings.HasPrefix(t, "-o") && !strings.HasPrefix(t, "--"):
			s.writes = append(s.writes, tailWord(a, 2))
		case strings.HasPrefix(t, "--"):
			name, value, hasValue := strings.Cut(t, "=")
			if !strings.HasSuffix(name, "_out") && name != "--descriptor_set_out" && name != "--dependency_out" {
				continue
			}
			out := tailWord(a, len(name)+1)
			if !hasValue {
				if i+1 >= len(args) {
					continue
				}
				i++
				out, value = args[i], args[i].text
			}
			if colon := strings.LastIndexByte(value, ':'); colon >= 0 {
				out = shWord{text: value[colon+1:], dynamic: out.dynamic, pattern: out.pattern}
			}
			s.writes = append(s.writes, out)
		default:
		}
	}
	return localVerdict()
}

// compiler handles the C compilers and the linker: a plugin, a spec file, a
// helper-program prefix or options read from a file make them run code this
// check cannot see. -o comes from the output table; a linker -o passed
// through -Wl is read here.
func (s *bashScan) compiler(argv []shWord) bashVerdict {
	for _, a := range argv[1:] {
		t := a.text
		switch {
		case strings.HasPrefix(t, "@"):
			return unknownVerdict(display(argv), "it reads its options from a file", "")
		case strings.HasPrefix(t, "-fplugin"), strings.HasPrefix(t, "-fpass-plugin"), strings.HasPrefix(t, "-specs"),
			strings.HasPrefix(t, "--specs"), t == "-wrapper", t == "-load", t == "-Xclang", t == "-Xlinker",
			strings.HasPrefix(t, "-B"), strings.HasPrefix(t, "--ld-path"), strings.HasPrefix(t, "-ccc-"),
			strings.HasPrefix(t, "-fuse-ld=") && strings.ContainsAny(t[len("-fuse-ld="):], `/\`):
			return opaqueVerdict(display(argv), "it loads a compiler plugin or runs another program")
		case strings.HasPrefix(t, "-Wl,"):
			parts := strings.Split(t[len("-Wl,"):], ",")
			for j, p := range parts {
				switch {
				case strings.HasPrefix(p, "@"):
					return unknownVerdict(display(argv), "it reads linker options from a file", "")
				case (p == "-o" || p == "-map") && j+1 < len(parts):
					s.writes = append(s.writes, shWord{text: parts[j+1], dynamic: a.dynamic})
				case strings.HasPrefix(p, "-o") && len(p) > 2:
					s.writes = append(s.writes, shWord{text: p[2:], dynamic: a.dynamic})
				default:
				}
			}
		default:
		}
	}
	return localVerdict()
}

// rustc: a linker or link argument runs another program, an unstable -Z
// option can do anything, and --emit names the files it writes.
func (s *bashScan) rustc(argv []shWord) bashVerdict {
	args := argv[1:]
	for i, a := range args {
		t := a.text
		if strings.HasPrefix(t, "@") {
			return unknownVerdict(display(argv), "it reads its options from a file", "")
		}
		if t == "-Z" || (strings.HasPrefix(t, "-Z") && !strings.HasPrefix(t, "--")) {
			return unknownVerdict(display(argv), "it uses an unstable compiler option", "")
		}
		codegen, isCodegen := "", false
		switch {
		case (t == "-C" || t == "--codegen") && i+1 < len(args):
			codegen, isCodegen = args[i+1].text, true
		case strings.HasPrefix(t, "--codegen="):
			codegen, isCodegen = t[len("--codegen="):], true
		case strings.HasPrefix(t, "-C") && !strings.HasPrefix(t, "--"):
			codegen, isCodegen = t[2:], true
		default:
		}
		if isCodegen {
			key, value, _ := strings.Cut(codegen, "=")
			switch key {
			case "linker", "link-arg", "link-args", "llvm-args", "passes":
				return opaqueVerdict(display(argv), "it runs a linker or passes it options")
			case "incremental", "profile-generate":
				s.writes = append(s.writes, shWord{text: value, dynamic: a.dynamic})
			default:
			}
		}
		emit := ""
		switch {
		case t == "--emit" && i+1 < len(args):
			emit = args[i+1].text
		case strings.HasPrefix(t, "--emit="):
			emit = t[len("--emit="):]
		default:
		}
		for _, kind := range strings.Split(emit, ",") {
			if _, path, ok := strings.Cut(kind, "="); ok {
				s.writes = append(s.writes, shWord{text: path, dynamic: a.dynamic})
			}
		}
	}
	return localVerdict()
}

// opensslVerdict: the network subcommands are external, and an engine,
// provider or configuration file loads code. -out and -keyout come from the
// output table.
func opensslVerdict(argv []shWord) bashVerdict {
	args := argv[1:]
	_, rest := splitOptions(args, nil)
	if len(rest) > 0 {
		switch rest[0].text {
		case "s_client", "s_server", "s_time", "ocsp":
			return actionVerdict(externalBashAction("network.socket.open", "openssl "+rest[0].text))
		case "ca", "engine":
			return unknownVerdict("openssl "+rest[0].text, "it runs a certificate authority or loads an engine", "")
		default:
		}
	}
	for _, a := range args {
		switch a.text {
		case "-engine", "-provider", "-provider-path", "-config", "-ssl_config", "-keylogfile":
			return unknownVerdict(display(argv), "it loads an engine, provider or configuration that can run code", "")
		default:
		}
	}
	return localVerdict()
}
