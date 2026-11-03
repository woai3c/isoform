package tools

import (
	"regexp"
	"strconv"
	"strings"
)

var inlineRemovalCall = regexp.MustCompile(`(?i)\b(rmtree|remove_tree|rm_rf|rm_r|rm|rmdir|unlink|remove|rename|move|rmsync|rmdirsync|unlinksync|renamesync)\s*(\(|[\t ]+[$A-Za-z_])`)
var inlineRemovalName = regexp.MustCompile(`(?i)\b(rmtree|remove_tree|rm_rf|rm_r|rm|rmdir|unlink|remove|rename|move|rmsync|rmdirsync|unlinksync|renamesync|removesync)\b`)
var inlineExecutionCall = regexp.MustCompile(`(?i)\b(system|exec|eval|popen|execsync|execfile|execfilesync|spawn|spawnsync|run|call|check_call|check_output)\s*\(|\bdo\s+shell\s+script\b`)
var inlineHomeReference = regexp.MustCompile(`(?i)\bHOME\b|\bhomedir\s*\(|\b(Path|Dir)\.home\b`)

// This is a refusal check, not a language evaluator. A removal API name and a
// protected reference anywhere in an inline program are enough: aliases and
// getattr can separate the source from the eventual call. Never execute code
// to discover the value of a destructive source.
func (s *bashScan) inlineDestructiveMention(roots, boundaries []string, depth int) string {
	for _, command := range s.cmds {
		if len(command.argv) == 0 {
			continue
		}
		language, _, _ := commandName(command.argv[0])
		programs := inlinePrograms(command.argv)
		if len(programs) == 0 && inlineInterpreter(language) && command.stdin.present {
			in := command.stdin
			if in.known || in.from == "a here-string" || in.from == "a here-document" {
				programs = append(programs, shWord{text: in.text, dynamic: !in.known})
			}
		}
		var code strings.Builder
		for _, program := range programs {
			if program.dynamic {
				return "dynamic inline program"
			}
			code.WriteString(program.text)
			code.WriteByte('\n')
		}
		if hit := s.inlineProgramDestructive(code.String(), language, command.argv[1:], roots, boundaries, depth); hit != "" {
			return hit
		}
	}
	return ""
}

func (s *bashScan) inlineProgramDestructive(code, language string, arguments []shWord, roots, boundaries []string, depth int) string {
	if depth > bashMaxDepth {
		return "nested inline program"
	}
	outside, literals, uncommented := inlineCodeLiterals(code, language)
	if inlineRemovalName.MatchString(uncommented) {
		if hit := s.inlineProtectedReference(uncommented, literals, roots, boundaries); hit != "" {
			return hit
		}
	}
	destructive := inlineRemovalCall.MatchString(outside)
	executes := inlineExecutionCall.MatchString(outside)
	if executes {
		for _, literal := range literals {
			if hit := s.inlineProgramDestructive(literal, language, nil, roots, boundaries, depth+1); hit != "" {
				return hit
			}
			nested := analyzeBash(literal, s.workDir)
			for _, command := range nested.cmds {
				if len(command.argv) > 0 {
					name, _, _ := commandName(command.argv[0])
					switch name {
					case "rm", "grm", "mv", "gmv", "rmdir", "grmdir", "unlink", "srm", "trash":
						destructive = true
					default:
					}
				}
			}
			if hit := nested.destructiveAncestorMentionDepth(roots, depth+1); hit != "" {
				return hit
			}
		}
	}
	if !destructive {
		return ""
	}
	// After the whole-program protected-reference check, a literal first
	// argument proves the destructive source of a direct removal/move call.
	// Computed sources also require inspecting arguments supplied to the program.
	sources, literalSources := inlineLiteralRemovalSources(outside, uncommented)
	if literalSources && !executes {
		literals = sources
	} else if inlineHomeReference.MatchString(uncommented) {
		return "HOME reference in destructive inline program"
	}
	// argv can supply the removal target (for example shutil.rmtree(sys.argv[1])).
	// An unresolved operand is not evidence that it stays inside the workspace.
	if !literalSources || executes {
		for _, argument := range arguments {
			if argument.dynamic {
				return "dynamic argument to destructive inline program"
			}
			literals = append(literals, argument.text)
		}
	}
	for _, literal := range literals {
		if literal == "" {
			continue
		}
		for _, candidate := range s.destructiveSourceCandidates(shWord{text: literal}) {
			if protectedDataHit(candidate, roots, true) || bundledWriteHit(candidate, roots) || sensitiveFileHit(candidate, s.home) {
				return literal
			}
			for _, boundary := range boundaries {
				if boundary != "" && destructiveSourceContains(candidate, boundary, false) {
					return literal
				}
			}
		}
	}
	return ""
}

func (s *bashScan) inlineProtectedReference(code string, literals, roots, boundaries []string) string {
	if inlineHomeReference.MatchString(code) {
		return "HOME reference in destructive inline program"
	}
	for _, literal := range literals {
		if strings.Contains(literal, "~") {
			return "HOME reference in destructive inline program"
		}
		for _, candidate := range s.destructiveSourceCandidates(shWord{text: literal}) {
			if protectedDataHit(candidate, roots, true) || bundledWriteHit(candidate, roots) || sensitiveFileHit(candidate, s.home) {
				return literal
			}
			for _, boundary := range boundaries {
				if boundary != "" && destructiveSourceContains(candidate, boundary, false) {
					return literal
				}
			}
		}
	}
	return ""
}

func inlineInterpreter(name string) bool {
	if _, ok := interpreterInline[name]; ok {
		return true
	}
	switch name {
	case "python", "python2", "python3", "pypy", "pypy3", "node", "nodejs", "bun", "deno", "osascript":
		return true
	default:
		return strings.HasPrefix(name, "python3.") || strings.HasPrefix(name, "python2.")
	}
}

func inlineLiteralRemovalSources(outside, code string) ([]string, bool) {
	matches := inlineRemovalCall.FindAllStringIndex(outside, -1)
	var sources []string
	for _, match := range matches {
		i := match[0]
		for i < len(code) && (code[i] == '_' || code[i] >= 'a' && code[i] <= 'z' || code[i] >= 'A' && code[i] <= 'Z') {
			i++
		}
		for i < len(code) && strings.ContainsRune(" \t\r\n(", rune(code[i])) {
			i++
		}
		if i == len(code) || (code[i] != '\'' && code[i] != '"') {
			return nil, false
		}
		quote := code[i]
		i++
		var source strings.Builder
		for i < len(code) && code[i] != quote {
			r, _, tail, err := strconv.UnquoteChar(code[i:], quote)
			if err != nil {
				return nil, false
			}
			source.WriteRune(r)
			i += len(code[i:]) - len(tail)
		}
		if i == len(code) {
			return nil, false
		}
		if quote == '"' && (strings.Contains(source.String(), "$") || strings.Contains(source.String(), "#{")) {
			return nil, false
		}
		tail := strings.TrimLeft(code[i+1:], " \t\r")
		if tail != "" && !strings.ContainsRune(",);\n", rune(tail[0])) {
			return nil, false
		}
		sources = append(sources, source.String())
	}
	return sources, len(sources) > 0
}

// Collect actual -c/-e/... program operands, including attached options and
// repeated Perl/Ruby/AppleScript expressions. Options that consume a value
// must not turn a module name such as -MFile::Path into an inline expression.
func inlinePrograms(argv []shWord) []shWord {
	if len(argv) == 0 {
		return nil
	}
	name, _, ok := commandName(argv[0])
	if !ok {
		return nil
	}
	letters, values, optional := interpreterInline[name], "", ""
	switch name {
	case "deno":
		_, rest := splitOptions(argv[1:], nil)
		if len(rest) > 0 && rest[0].text == "eval" {
			_, operands := splitOptions(rest[1:], setOf("--ext"))
			if len(operands) > 0 {
				return operands[:1]
			}
		}
		return nil
	case "python", "python2", "python3", "pypy", "pypy3":
		letters, values = "c", "WXQm"
	case "node", "nodejs", "bun":
		letters, values = "ep", "r"
	case "perl":
		values, optional = "MIm", "iFxCD"
	case "ruby":
		values = "IrCEF"
	case "osascript":
		letters, values = "e", "ls"
	case "sh", "bash", "zsh", "dash", "ksh", "fish":
		// The shell scanner already parses their code and preserves command
		// operands. Do not reclassify quoted shell examples as language calls.
		return nil
	default:
		if strings.HasPrefix(name, "python3.") || strings.HasPrefix(name, "python2.") {
			letters, values = "c", "WXQm"
		}
	}
	if letters == "" {
		return nil
	}
	var programs []shWord
	for i := 1; i < len(argv); i++ {
		word := argv[i]
		t := word.text
		if t == "--" || t == "-" || !strings.HasPrefix(t, "-") {
			break
		}
		if strings.HasPrefix(t, "--") {
			flag, attached, hasValue := strings.Cut(t, "=")
			switch flag {
			case "--eval", "--print", "--command", "--expression", "--execute":
				if hasValue {
					programs = append(programs, shWord{text: attached, dynamic: word.dynamic})
				} else if i+1 < len(argv) {
					i++
					programs = append(programs, argv[i])
				}
			case "--require", "--import", "--loader", "--experimental-loader", "--input-type", "--conditions", "--inspect-port", "--title":
				if !hasValue && i+1 < len(argv) {
					i++
				}
			default:
			}
			continue
		}
		for j := 1; j < len(t); j++ {
			ch := rune(t[j])
			if name == "perl" && (ch == 'l' || ch == '0') {
				// Optional numeric suffixes do not consume a following flag:
				// -le and -0pe still contain an inline -e program.
				for j+1 < len(t) && t[j+1] >= '0' && t[j+1] <= '7' {
					j++
				}
				continue
			}
			if strings.ContainsRune(letters, ch) {
				if j+1 < len(t) {
					programs = append(programs, shWord{text: t[j+1:], dynamic: word.dynamic})
				} else if i+1 < len(argv) {
					i++
					programs = append(programs, argv[i])
				}
				break
			}
			if strings.ContainsRune(values, ch) {
				if j+1 == len(t) {
					i++
				}
				break
			}
			if strings.ContainsRune(optional, ch) {
				break
			}
		}
	}
	return programs
}

func inlineCodeLiterals(code, language string) (string, []string, string) {
	var outside, uncommented strings.Builder
	var literals []string
	for i := 0; i < len(code); {
		start := i
		ch := code[i]
		lineComment := ch == '#'
		blockEnd := ""
		switch language {
		case "node", "nodejs", "bun", "deno":
			lineComment = strings.HasPrefix(code[i:], "//")
			if strings.HasPrefix(code[i:], "/*") {
				blockEnd = "*/"
			}
		case "osascript":
			lineComment = strings.HasPrefix(code[i:], "--")
			if strings.HasPrefix(code[i:], "(*") {
				blockEnd = "*)"
			}
		default:
		}
		if lineComment || blockEnd != "" {
			if blockEnd != "" {
				end := strings.Index(code[i+2:], blockEnd)
				if end < 0 {
					i = len(code)
				} else {
					i += end + 4
				}
			} else {
				for i < len(code) && code[i] != '\n' {
					i++
				}
			}
			outside.WriteByte(' ')
			uncommented.WriteByte(' ')
			continue
		}
		switch ch {
		case '\'', '"', '`':
			i++
			var value strings.Builder
			for i < len(code) && code[i] != ch {
				r, _, tail, err := strconv.UnquoteChar(code[i:], ch)
				if err != nil {
					value.WriteByte(code[i])
					i++
					continue
				}
				value.WriteRune(r)
				i += len(code[i:]) - len(tail)
			}
			if i < len(code) {
				i++
			}
			literals = append(literals, value.String())
			// Preserve offsets so direct call operands can be inspected without
			// mistaking destination literals for destructive sources.
			outside.WriteByte('_')
			outside.WriteString(strings.Repeat(" ", i-start-1))
		default:
			outside.WriteByte(ch)
			i++
		}
		uncommented.WriteString(code[start:i])
	}
	return outside.String(), literals, uncommented.String()
}
