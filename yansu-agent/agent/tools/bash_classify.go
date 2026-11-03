package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"yansu-agent/agent/authority"
)

// This file is the Bash command scanner behind BashTool.DescribeAuthority and
// the unattended deny list. It parses a command with a real shell parser
// (mvdan.cc/sh/v3) and walks the whole syntax tree with syntax.Walk, so every
// command that will run is seen: commands in pipelines, lists, loops, case
// subjects, [[ ]] tests, function bodies, command and process substitutions,
// and substitutions inside parameter expansions. Wrappers (env, sudo, xargs,
// find -exec, timeout, nohup, watch, ...) are unwrapped, and code handed to a
// shell (sh -c, eval, env -S, a here-document, a pipe or process substitution
// into sh) is parsed as shell in turn.
//
// The default is inverted: a command is local only when it is on the read and
// build allowlist (bash_rules.go) and every file it writes is inside the
// workspace or a temp folder. Anything else is unknown, which asks the user in
// an attended chat and is denied in an unattended run. A command whose name or
// script is not a literal, that runs a script file, or that cannot be parsed
// is unknown too, and is marked opaque so the deny list fails closed on it.

const bashMaxDepth = 8

type bashVerdictKind int

const (
	bashLocal bashVerdictKind = iota
	bashUnknown
	bashAction
)

// bashVerdict is the classification of one command (or one script-level
// construct) found in a Bash command.
type bashVerdict struct {
	kind bashVerdictKind
	// action is the external or privileged action for bashAction.
	action authority.Action
	// subject is the command as shown to the user, e.g. "docker" or "./x.sh".
	subject string
	// reason says why an unknown command needs approval.
	reason string
	// trust is the session-trust key of an unknown command. Empty means the
	// approval covers only this exact command text.
	trust string
	// opaque marks a construct that hides which command will run (a dynamic
	// command name, eval of a variable, a script file, a parse failure). The
	// unattended deny list fails closed on it.
	opaque bool
	// detail is the full command a verdict came from, so two different
	// calls of one program count as two effects.
	detail string
}

// shWord is a shell word after quote removal. A NUL byte stands for each
// dynamic part (a parameter, command, process or arithmetic expansion) so a
// value is never mistaken for a literal, while a literal "--flag=" prefix is
// still visible.
type shWord struct {
	text    string
	dynamic bool
	// pathRoot is trusted scanner provenance for an unmodified path from a
	// bounded find/fd traversal. Shell expansions never acquire this field.
	pathRoot string
	// pattern is set when the word holds an unquoted glob or brace-expansion
	// metacharacter, so the shell may turn it into other words.
	pattern bool
}

func (w shWord) literal() bool { return !w.dynamic && !w.pattern }

// bashCommand is one resolved command, after wrappers are unwrapped.
type bashCommand struct {
	argv  []shWord
	stdin bashStdin
}

// bashStdin describes the standard input of a command that reads code from it
// (a shell or an interpreter without a script argument).
type bashStdin struct {
	present bool
	known   bool
	text    string
	from    string
	// file is the file a `< file` redirect reads, when that is the input.
	file shWord
	// paths is set when the input is a list of paths from a command that
	// lists files under literal folders (find ., git ls-files): the folder
	// prefix every listed path starts with.
	paths string
	// pathRoot is the canonical bound of a path-only, NUL-delimited producer.
	// It is usable by xargs only when xargs also consumes NUL-delimited input.
	pathRoot string
}

type bashScan struct {
	home     string
	workDir  string
	verdicts []bashVerdict
	cmds     []bashCommand
	writes   []shWord
	cds      []shWord
	// dynamicWrappers retain operands that unwrapping cannot safely place.
	// A hidden destructive command must still reach the unconditional guard.
	dynamicWrappers [][]shWord
	// arith is set when the script has an arithmetic or [[ ]] context, and
	// quotedCode when a quoted string holds $( or a backtick. Together they
	// can run code: bash evaluates a[$(cmd)] held in a variable.
	arith      bool
	quotedCode bool
	// stdinRedirected is set by `exec < file`, which feeds every later
	// command that reads standard input.
	stdinRedirected bool
	parseFailed     bool
	// args are the words of every command and assignment, for the
	// protected-path check; recursiveNoPath names recursive readers that
	// search the current folder.
	args            []scanArg
	recursiveNoPath []string
	// links are the links this command makes (ln, cp -s, cp -l); a write
	// through one of them reaches whatever it points to.
	links []bashLink
	// programDirs are the installed-tool folders of programs this command
	// runs by path, so a write into one of them is caught.
	programDirs []string
	// extractDirs are folders an archive extraction or recursive copy fills
	// in this command; a later write under one may land on an extracted
	// symlink and reach outside the workspace.
	extractDirs []shWord
	// followReadRoots are literal folders a recursive reader walks while
	// following symlinks (find -L, find -type l -exec, cp -RL, rsync -L,
	// tar -h creating); a link under one that resolves into a protected
	// root or credential is read through.
	followReadRoots []shWord
	findTraversals  []bashFindTraversal
}

// scanArg is a command argument; recursive marks one a command reads every
// file under.
type scanArg struct {
	word      shWord
	recursive bool
}

// analyzeBash scans command as it would run in workDir ("" when unknown).
func analyzeBash(command, workDir string) *bashScan {
	s := &bashScan{workDir: cleanAbs(workDir)}
	s.home, _ = os.UserHomeDir()
	if runtime.GOOS == "windows" {
		command = stripCmdCarets(command)
	}
	if !s.scanScript(command, 0) {
		s.parseFailed = true
		s.opaque(command, "it could not be parsed")
	}
	s.checkWrites()
	if s.gitInternalsWrittenThenGit() {
		s.unknown("a command that writes under .git and then runs git",
			"it changes .git config or hooks and then runs git, which would run them", "")
	}
	if hit := s.extractWriteHit(); hit != "" {
		s.unknown("a command that writes "+hit,
			"it writes under a folder an extraction or recursive copy filled, which may hold a link that leaves the workspace", "")
	}
	if _, ok := s.relativeBases(); !ok && s.workDir != "" {
		s.unknown(command, "it changes to a folder built at run time, so the paths after it are not known", "")
	}
	if s.arith && s.quotedCode {
		s.opaque(command, "it evaluates arithmetic next to quoted code, which bash can run")
	}
	return s
}

func cleanAbs(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	if canonical, err := filepath.EvalSymlinks(dir); err == nil {
		return canonical
	}
	return filepath.Clean(dir)
}

// stripCmdCarets removes cmd.exe's caret escapes outside double quotes, so
// `git ^push` reads as `git push` before the POSIX parse.
func stripCmdCarets(command string) string {
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == '^' && !inQuote:
			if i+1 < len(command) {
				i++
				b.WriteByte(command[i])
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func (s *bashScan) add(v bashVerdict) { s.verdicts = append(s.verdicts, v) }

func (s *bashScan) unknown(subject, reason, trust string) {
	s.add(bashVerdict{kind: bashUnknown, subject: subject, reason: reason, trust: trust})
}

func (s *bashScan) opaque(subject, reason string) {
	s.add(bashVerdict{kind: bashUnknown, subject: subject, reason: reason, opaque: true})
}

func (s *bashScan) action(action authority.Action) {
	s.add(bashVerdict{kind: bashAction, action: action, subject: action.Operation})
}

// scanScript parses src and classifies every command in it. It reports false
// when src cannot be parsed.
func (s *bashScan) scanScript(src string, depth int) bool {
	if strings.TrimSpace(src) == "" {
		return true
	}
	if depth > bashMaxDepth {
		s.opaque(src, "it nests shells too deeply to check")
		return true
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		return false
	}
	w := &bashWalk{
		s:         s,
		src:       src,
		depth:     depth,
		stmtOf:    map[*syntax.CallExpr]*syntax.Stmt{},
		producer:  map[*syntax.Stmt]*syntax.Stmt{},
		leftOf:    map[*syntax.Stmt]*syntax.Stmt{},
		inherited: map[*syntax.CallExpr]bool{},
	}
	syntax.Walk(file, w.index)
	syntax.Walk(file, w.visit)
	return true
}

// scanNested parses code a command hands to a shell (sh -c, eval, a here-doc).
func (s *bashScan) scanNested(subject, code string, depth int) {
	if !s.scanScript(code, depth+1) {
		s.opaque(subject, "the shell code it runs could not be parsed")
	}
}

type bashWalk struct {
	s     *bashScan
	src   string
	depth int
	// stmtOf maps a call to its statement (for redirects), producer maps the
	// right side of a pipe to its left side, and leftOf maps the left side of
	// a pipe to the statement holding the pipe, so a pipeline's first command
	// inherits the pipeline's own input.
	stmtOf   map[*syntax.CallExpr]*syntax.Stmt
	producer map[*syntax.Stmt]*syntax.Stmt
	leftOf   map[*syntax.Stmt]*syntax.Stmt
	// inherited marks calls inside a compound command, function body or
	// pipeline stage whose input comes from an enclosing redirect or pipe.
	inherited map[*syntax.CallExpr]bool
}

func (w *bashWalk) index(node syntax.Node) bool {
	switch n := node.(type) {
	case *syntax.Stmt:
		switch cmd := n.Cmd.(type) {
		case *syntax.CallExpr:
			w.stmtOf[cmd] = n
		case *syntax.BinaryCmd:
			if cmd.Op == syntax.Pipe || cmd.Op == syntax.PipeAll {
				w.producer[cmd.Y] = cmd.X
				w.leftOf[cmd.X] = n
			}
		case nil:
		default:
			// A compound command fed by a redirect or a pipe passes that
			// input to every command inside it.
			if hasInputRedirect(n) || w.producer[n] != nil {
				w.markInherited(cmd)
			}
		}
	case *syntax.FuncDecl:
		// A function can be called with any input.
		w.markInherited(n.Body)
	}
	return true
}

func (w *bashWalk) markInherited(node syntax.Node) {
	syntax.Walk(node, func(inner syntax.Node) bool {
		if call, ok := inner.(*syntax.CallExpr); ok {
			w.inherited[call] = true
		}
		return true
	})
}

func hasInputRedirect(st *syntax.Stmt) bool {
	for _, r := range st.Redirs {
		if r == nil {
			continue
		}
		switch r.Op {
		case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			if r.N == nil || r.N.Value == "0" {
				return true
			}
		default:
		}
	}
	return false
}

func (w *bashWalk) visit(node syntax.Node) bool {
	s := w.s
	switch n := node.(type) {
	case *syntax.CallExpr:
		w.call(n)
	case *syntax.Assign:
		if n.Value != nil {
			s.args = append(s.args, scanArg{word: s.word(n.Value)})
		}
		if n.Name != nil && !n.Naked && dangerousEnvAssign(n.Name.Value, s.word(n.Value)) {
			s.opaque(n.Name.Value+"=...", "it changes "+n.Name.Value+", which decides what later commands run")
		}
		if (n.Name == nil || n.Naked) && n.Value != nil {
			// A quoted or dynamically named export/declare keeps the whole
			// NAME=value inside the value word: export "GIT_PAGER=./x",
			// export $V=./x. Split it out so a dangerous or run-time-built
			// name is still caught.
			val := s.word(n.Value)
			if !strings.HasPrefix(val.text, "-") {
				if name, rhs, ok := strings.Cut(val.text, "="); ok {
					switch {
					case strings.ContainsRune(name, 0):
						s.opaque(strings.ReplaceAll(val.text, "\x00", "..."),
							"it sets a variable whose name is built at run time, which could change what later commands run")
					case dangerousEnvAssign(name, shWord{text: rhs, dynamic: strings.ContainsRune(rhs, 0)}):
						s.opaque(name+"=...", "it changes "+name+", which decides what later commands run")
					default:
					}
				}
			}
		}
	case *syntax.Redirect:
		w.redirect(n)
	case *syntax.ParamExp:
		if n.Excl || (n.Exp != nil && n.Exp.Op == syntax.OtherParamOps) {
			s.opaque(w.text(n), "an indirect or prompt expansion can run code")
		}
		if n.Exp != nil && n.Param != nil && dangerousEnvName(n.Param.Value) &&
			(n.Exp.Op == syntax.AssignUnset || n.Exp.Op == syntax.AssignUnsetOrNull) {
			s.opaque(w.text(n), "it changes "+n.Param.Value+", which decides what later commands run")
		}
	case *syntax.ArithmExp, *syntax.ArithmCmd, *syntax.LetClause, *syntax.CStyleLoop, *syntax.TestClause:
		s.arith = true
	case *syntax.DeclClause:
		for _, arg := range n.Args {
			if arg == nil || arg.Name != nil || arg.Value == nil {
				continue
			}
			flag := s.word(arg.Value).text
			if strings.HasPrefix(flag, "-") && strings.ContainsAny(flag, "in") {
				// declare -i makes assignments arithmetic; -n makes a name
				// reference that can rename PATH.
				s.arith = true
				if strings.Contains(flag, "n") {
					s.opaque(n.Variant.Value+" "+flag, "a name reference can change any variable")
				}
			}
		}
	case *syntax.SglQuoted:
		if strings.Contains(n.Value, "$(") || strings.Contains(n.Value, "`") {
			s.quotedCode = true
		}
	case *syntax.Lit:
		if strings.Contains(n.Value, "$(") || strings.Contains(n.Value, "`") {
			s.quotedCode = true
		}
	}
	return true
}

// text returns the source text of a node.
func (w *bashWalk) text(node syntax.Node) string {
	start, end := int(node.Pos().Offset()), int(node.End().Offset())
	if start < 0 || end > len(w.src) || start > end {
		return ""
	}
	return w.src[start:end]
}

func (w *bashWalk) call(c *syntax.CallExpr) {
	if len(c.Args) == 0 {
		return
	}
	argv := make([]shWord, len(c.Args))
	for i, arg := range c.Args {
		argv[i] = w.s.word(arg)
	}
	// `exec < file` feeds every later command that reads input.
	if len(argv) == 1 && argv[0].text == "exec" {
		if st := w.stmtOf[c]; st != nil && hasInputRedirect(st) {
			w.s.stdinRedirected = true
		}
	}
	w.s.run(argv, w.stdinFor(c), w.depth)
}

// stdinFor describes the input of a call: its own here-doc, here-string or
// redirect, the command piped into it, or input inherited from an enclosing
// redirect or pipe.
func (w *bashWalk) stdinFor(c *syntax.CallExpr) bashStdin {
	st := w.stmtOf[c]
	if st != nil {
		for _, r := range st.Redirs {
			if r == nil || (r.N != nil && r.N.Value != "0") {
				continue
			}
			switch r.Op {
			case syntax.Hdoc, syntax.DashHdoc:
				if r.Hdoc == nil {
					return bashStdin{present: true, known: true, from: "a here-document"}
				}
				return bashStdin{present: true, known: true, text: w.hdocBody(r), from: "a here-document"}
			case syntax.WordHdoc:
				word := w.s.word(r.Word)
				return bashStdin{present: true, known: word.literal(), text: word.text, from: "a here-string"}
			case syntax.RdrIn:
				return bashStdin{present: true, from: "a file", file: w.s.word(r.Word)}
			case syntax.RdrInOut, syntax.DplIn:
				return bashStdin{present: true, from: "a file"}
			default:
			}
		}
		if p := w.producerOf(st); p != nil {
			return w.pipeInput(p)
		}
	}
	if w.inherited[c] || w.s.stdinRedirected {
		return bashStdin{present: true, from: "an enclosing redirect or pipe"}
	}
	return bashStdin{}
}

// hdocBody returns a here-document's body without its closing delimiter line.
func (w *bashWalk) hdocBody(r *syntax.Redirect) string {
	body := w.text(r.Hdoc)
	delim := w.s.word(r.Word).text
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if n := len(lines); n > 0 && strings.TrimLeft(lines[n-1], "\t") == delim {
		lines = lines[:n-1]
	}
	return strings.Join(lines, "\n")
}

func (w *bashWalk) producerOf(st *syntax.Stmt) *syntax.Stmt {
	for hop := 0; st != nil && hop < 64; hop++ {
		if p, ok := w.producer[st]; ok {
			return p
		}
		st = w.leftOf[st]
	}
	return nil
}

// pipeInput describes what a pipe stage writes: the literal text of echo or
// printf, a cat of a here-document, or unknown content.
func (w *bashWalk) pipeInput(st *syntax.Stmt) bashStdin {
	in := bashStdin{present: true, from: "a pipe"}
	call := effectiveCall(st)
	if call == nil || len(call.Args) == 0 {
		return in
	}
	argv := make([]shWord, len(call.Args))
	for i, arg := range call.Args {
		argv[i] = w.s.word(arg)
	}
	name, hasDir, ok := commandName(argv[0])
	if !ok || hasDir {
		return in
	}
	in.paths = w.s.listedPathPrefix(name, argv)
	if traversalNULPaths(name, argv) {
		in.pathRoot = w.s.traversalPathRoot(traversalRoots(name, argv))
	}
	switch name {
	case "echo", "printf":
		parts := make([]string, 0, len(argv)-1)
		for _, arg := range argv[1:] {
			// A printf format or an echo -e escape rewrites the text, so
			// what reaches the shell is not the literal words.
			if !arg.literal() || (name == "printf" && strings.ContainsAny(arg.text, "%\\")) ||
				(name == "echo" && strings.Contains(arg.text, "\\")) {
				return in
			}
			// echo's own leading -n, -e and -E options are not output.
			if name == "echo" && len(parts) == 0 && len(arg.text) > 1 &&
				strings.HasPrefix(arg.text, "-") && strings.Trim(arg.text[1:], "neE") == "" {
				continue
			}
			parts = append(parts, arg.text)
		}
		in.known = true
		in.text = strings.Join(parts, " ")
	case "cat":
		if len(argv) == 1 {
			if hd := w.stmtOf[call]; hd != nil {
				for _, r := range hd.Redirs {
					if r != nil && (r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc) && r.Hdoc != nil {
						in.known = true
						in.text = w.hdocBody(r)
					}
				}
			}
		}
	default:
	}
	return in
}

// listedPathPrefix returns the folder bound used to check generated paths:
// canonical find/fd roots (or a broad guard bound for unknown roots), and the
// current folder for git ls-files and grep/rg file-list modes. It preserves
// symlink/.. semantics and returns "" for other producers.
func (s *bashScan) listedPathPrefix(name string, argv []shWord) string {
	args := argv[1:]
	switch name {
	case "find", "gfind", "fd", "fdfind":
		if root := s.traversalGuardRoot(traversalRoots(name, argv)); root != "" {
			return strings.TrimRight(root, string(filepath.Separator)) + string(filepath.Separator)
		}
		return ""
	}
	for _, a := range args {
		if !a.literal() || strings.Contains(a.text, "..") {
			return ""
		}
	}
	switch name {
	case "git":
		sub, _, verdict := gitSubcommand(argv)
		if verdict == nil && (sub == "ls-files" || sub == "diff" || sub == "ls-tree") {
			return "./"
		}
	case "grep", "egrep", "fgrep", "rg":
		for _, a := range args {
			if filepath.IsAbs(a.text) {
				return ""
			}
		}
		for _, a := range args {
			t := a.text
			if t == "--files" || t == "--files-with-matches" || t == "-l" ||
				(len(t) > 1 && t[0] == '-' && t[1] != '-' && strings.IndexByte(t, 'l') > 0) {
				return "./"
			}
		}
	default:
	}
	return ""
}

func effectiveCall(st *syntax.Stmt) *syntax.CallExpr {
	for hop := 0; st != nil && hop < 64; hop++ {
		switch cmd := st.Cmd.(type) {
		case *syntax.CallExpr:
			return cmd
		case *syntax.BinaryCmd:
			if cmd.Op != syntax.Pipe && cmd.Op != syntax.PipeAll {
				return nil
			}
			st = cmd.Y
		default:
			return nil
		}
	}
	return nil
}

// redirect records write targets and flags network redirects.
func (w *bashWalk) redirect(r *syntax.Redirect) {
	if r == nil || r.Word == nil {
		return
	}
	switch r.Op {
	case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return
	default:
	}
	target := w.s.word(r.Word)
	lower := strings.ToLower(target.text)
	if strings.HasPrefix(lower, "/dev/tcp/") || strings.HasPrefix(lower, "/dev/udp/") ||
		(target.dynamic && strings.HasPrefix(lower, "/dev/")) {
		w.s.action(externalBashAction("network.socket.open", "/dev/tcp"))
		return
	}
	switch r.Op {
	case syntax.RdrIn:
		w.s.args = append(w.s.args, scanArg{word: target})
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.ClbOut, syntax.RdrInOut:
		w.s.writes = append(w.s.writes, target)
	case syntax.DplOut, syntax.DplIn:
		// >&2 and <&0 duplicate a descriptor; >&file writes a file.
		if !isDescriptor(target.text) {
			w.s.writes = append(w.s.writes, target)
		}
	default:
	}
}

func isDescriptor(text string) bool {
	if text == "-" {
		return true
	}
	text = strings.TrimSuffix(text, "-")
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// word turns a parsed word into its text after quote removal.
func (s *bashScan) word(w *syntax.Word) shWord {
	var out shWord
	if w == nil {
		return out
	}
	var b strings.Builder
	s.writeParts(&b, &out, w.Parts, false)
	out.text = b.String()
	return out
}

func (s *bashScan) writeParts(b *strings.Builder, out *shWord, parts []syntax.WordPart, quoted bool) {
	for i, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			value := p.Value
			if quoted {
				b.WriteString(unescapeDoubleQuoted(value))
				continue
			}
			if i == 0 && b.Len() == 0 && strings.HasPrefix(value, "~") {
				rest := value[1:]
				switch {
				case rest == "" || strings.HasPrefix(rest, "/"):
					if s.home == "" {
						out.dynamic = true
						b.WriteByte(0)
					} else {
						b.WriteString(s.home)
					}
					value = rest
				default:
					// ~user is another account's home folder. Resolve it when
					// the user exists so the remainder stays a literal path the
					// name and root checks can see; otherwise the word is
					// dynamic. Without this, bash's own ~user expansion would
					// reach a path this scanner never classified.
					if resolved, ok := resolveUserTilde("~" + rest); ok {
						b.WriteString(resolved)
					} else {
						out.dynamic = true
						b.WriteByte(0)
					}
					value = ""
				}
			}
			if isGlobPattern(value) {
				out.pattern = true
			}
			b.WriteString(unescapeUnquoted(value))
		case *syntax.SglQuoted:
			if p.Dollar {
				decoded, ok := decodeANSIC(p.Value)
				if !ok {
					out.dynamic = true
					b.WriteByte(0)
					continue
				}
				b.WriteString(decoded)
				continue
			}
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			s.writeParts(b, out, p.Parts, true)
		case *syntax.ParamExp:
			if s.home != "" && isPlainParam(p, "HOME") {
				b.WriteString(s.home)
				continue
			}
			out.dynamic = true
			b.WriteByte(0)
		case *syntax.ExtGlob:
			out.pattern = true
			if p.Pattern != nil {
				b.WriteString(p.Pattern.Value)
			}
		case *syntax.ProcSubst:
			// <(cmd) and >(cmd) become a /dev/fd path.
			out.dynamic = true
			b.WriteString("/dev/fd/")
			b.WriteByte(0)
		default:
			// Command, process and arithmetic substitutions.
			out.dynamic = true
			b.WriteByte(0)
		}
	}
}

// isGlobPattern reports unquoted text the shell would expand: * or ?, a
// bracket expression [...], or a brace expansion {a,b} or {1..3}.
func isGlobPattern(value string) bool {
	if strings.ContainsAny(value, "*?") {
		return true
	}
	if open := strings.IndexByte(value, '['); open >= 0 && strings.IndexByte(value[open+1:], ']') >= 0 {
		return true
	}
	if open := strings.IndexByte(value, '{'); open >= 0 {
		inner := value[open+1:]
		if end := strings.IndexByte(inner, '}'); end >= 0 {
			inner = inner[:end]
			return strings.Contains(inner, ",") || strings.Contains(inner, "..")
		}
	}
	return false
}

func isPlainParam(p *syntax.ParamExp, name string) bool {
	return p.Param != nil && p.Param.Value == name && !p.Excl && !p.Length && !p.Width &&
		p.Index == nil && p.Slice == nil && p.Repl == nil && p.Exp == nil && p.Names == 0
}

// unescapeUnquoted removes backslash escapes outside quotes: `\c\u\r\l` is curl.
func unescapeUnquoted(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			i++
			if value[i] == '\n' {
				continue
			}
		}
		b.WriteByte(value[i])
	}
	return b.String()
}

// unescapeDoubleQuoted removes the escapes bash honours inside double quotes.
func unescapeDoubleQuoted(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			switch value[i+1] {
			case '$', '`', '"', '\\':
				i++
			case '\n':
				i++
				continue
			default:
			}
		}
		b.WriteByte(value[i])
	}
	return b.String()
}

// decodeANSIC decodes a $'...' string. It reports false for escapes it does
// not model, so the word is treated as dynamic.
func decodeANSIC(value string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c != '\\' || i+1 >= len(value) {
			b.WriteByte(c)
			continue
		}
		i++
		switch value[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case 'e', 'E':
			b.WriteByte(27)
		case 'f':
			b.WriteByte(12)
		case 'v':
			b.WriteByte(11)
		case '\\', '\'', '"', '?':
			b.WriteByte(value[i])
		case 'x':
			n, used := parseRadix(value[i+1:], 16, 2)
			if used == 0 {
				return "", false
			}
			b.WriteByte(byte(n))
			i += used
		case '0', '1', '2', '3', '4', '5', '6', '7':
			n, used := parseRadix(value[i:], 8, 3)
			b.WriteByte(byte(n))
			i += used - 1
		default:
			return "", false
		}
	}
	return b.String(), true
}

func parseRadix(s string, base, max int) (int, int) {
	n, used := 0, 0
	for used < max && used < len(s) {
		c := s[used]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return n, used
		}
		if d >= base {
			return n, used
		}
		n = n*base + d
		used++
	}
	return n, used
}

// commandName normalizes a command word: the basename after the last / or \,
// lowercased (macOS and Windows file systems ignore case) and without a
// Windows executable suffix. ok is false when the name is not a literal.
func commandName(w shWord) (name string, hasDir bool, ok bool) {
	if !w.literal() || w.text == "" {
		return "", false, false
	}
	text := w.text
	if i := strings.LastIndexAny(text, `/\`); i >= 0 {
		hasDir = true
		text = text[i+1:]
	}
	text = strings.ToLower(text)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
		if strings.HasSuffix(text, ext) && len(text) > len(ext) {
			text = strings.TrimSuffix(text, ext)
			break
		}
	}
	return text, hasDir, text != ""
}

// display joins argv for messages, showing dynamic parts as "...".
func display(argv []shWord) string {
	parts := make([]string, 0, len(argv))
	for _, w := range argv {
		parts = append(parts, strings.ReplaceAll(w.text, "\x00", "..."))
	}
	return strings.Join(parts, " ")
}

// run unwraps wrappers and classifies the command that really runs.
func (s *bashScan) run(argv []shWord, in bashStdin, depth int) {
	for _, w := range argv[min(1, len(argv)):] {
		s.args = append(s.args, scanArg{word: w})
	}
	for hop := 0; hop < 32 && len(argv) > 0; hop++ {
		name, hasDir, ok := commandName(argv[0])
		if !ok {
			s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
			s.opaque(display(argv), "its command name is not a literal word")
			return
		}
		s.recordDynamicWrapper(name, argv)
		if hasDir && !s.trustedBinPath(argv[0].text) {
			// A program outside the installed-tool folders may be a file the
			// run wrote. A named external command still counts as external.
			s.cmds = append(s.cmds, bashCommand{argv: argv, stdin: in})
			if bashNamedExternal[name] {
				if v := s.rule(name, argv, in, depth); v.kind == bashAction {
					v.detail = display(argv)
					s.add(v)
				}
			}
			// An installed program outside the folders a run can write
			// without asking gets one approval per chat; a file in the work
			// or temp folder is approved per exact command.
			trust := ""
			if s.installedProgram(argv[0].text) {
				trust = "run:" + argv[0].text
			}
			s.add(bashVerdict{kind: bashUnknown, subject: display(argv), reason: "it runs a file", trust: trust, opaque: true})
			return
		}
		rest, next, handled := s.unwrap(name, argv, in, depth)
		if handled {
			return
		}
		if rest == nil {
			s.classify(name, argv, in, depth)
			return
		}
		argv, in = rest, next
	}
	if len(argv) > 0 {
		s.opaque(display(argv), "it nests wrappers too deeply to check")
	}
}

// bashNamedExternal are command names classified external by name even when
// called by path, so /tmp/x/git push is still a push.
var bashNamedExternal = map[string]bool{
	"git": true, "gh": true, "curl": true, "wget": true, "ssh": true, "scp": true,
	"sftp": true, "rsync": true, "nc": true, "ncat": true, "netcat": true,
	"socat": true, "telnet": true, "opencli": true, "osascript": true,
	"yansu": true, "npm": true, "pnpm": true, "yarn": true, "docker": true,
}

// trustedBinPath reports whether a command path is in a folder of installed
// tools, matched exactly: the system folders, Homebrew, and the user's Go,
// Cargo and Isoform tool folders. A folder inside the work folder or a temp
// folder is never trusted, since a run writes there without asking, and
// neither is a project's node_modules/.bin or virtualenv. checkWrites catches
// a command that writes into the folder of a program it runs.
func (s *bashScan) trustedBinPath(path string) bool {
	path = strings.ReplaceAll(path, `\`, "/")
	if !filepath.IsAbs(path) || strings.Contains(path, "..") {
		return false
	}
	dir := filepath.Dir(filepath.Clean(path))
	trusted := false
	switch dir {
	case "/bin", "/usr/bin", "/sbin", "/usr/sbin", "/usr/local/bin", "/opt/homebrew/bin",
		"/opt/local/bin", "/usr/local/go/bin", "/opt/homebrew/opt/python/libexec/bin":
		trusted = true
	default:
	}
	if s.home != "" {
		for _, sub := range []string{"go/bin", ".cargo/bin", ".yansu-agent/bin", ".isoform-agent/bin"} {
			if dir == filepath.Join(s.home, sub) {
				trusted = true
			}
		}
	}
	if home := strings.TrimSpace(os.Getenv("YANSU_AGENT_HOME")); home != "" && dir == filepath.Join(home, "bin") {
		trusted = true
	}
	if !trusted {
		return false
	}
	canonical, err := canonicalPathLoose(dir)
	if err != nil {
		return false
	}
	if s.workDir != "" && pathWithin(canonical, s.workDir) {
		return false
	}
	for _, root := range tempRoots() {
		if pathWithin(canonical, root) {
			return false
		}
	}
	s.programDirs = append(s.programDirs, canonical)
	return true
}

// installedProgram reports an absolute program path outside the work folder
// and the temp folders, the only places a run can write without asking.
func (s *bashScan) installedProgram(path string) bool {
	if !filepath.IsAbs(path) || s.workDir == "" {
		return false
	}
	canonical, err := canonicalPathLoose(path)
	if err != nil || pathWithin(canonical, s.workDir) {
		return false
	}
	for _, root := range tempRoots() {
		if pathWithin(canonical, root) {
			return false
		}
	}
	return true
}

func literalTexts(argv []shWord) ([]string, bool) {
	out := make([]string, len(argv))
	for i, w := range argv {
		if !w.literal() {
			return nil, false
		}
		out[i] = w.text
	}
	return out, true
}

// writeAllowed reports whether a write target is inside the workspace or a
// temp folder. Without a known workspace every literal target is allowed.
func (s *bashScan) writeAllowed(target shWord) bool {
	text := target.text
	if target.dynamic {
		return false
	}
	if target.pattern {
		// A glob cannot leave its literal folder: check that folder.
		cut := strings.IndexAny(text, "*?[{")
		if cut >= 0 {
			text = text[:cut]
		}
		if i := strings.LastIndex(text, "/"); i >= 0 {
			text = text[:i+1]
		} else {
			text = "."
		}
	}
	switch text {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty", "-":
		return true
	default:
	}
	if strings.HasPrefix(text, "/dev/fd/") {
		return true
	}
	if s.workDir == "" {
		return true
	}
	if filepath.IsAbs(text) {
		return s.allowedWritePath(text)
	}
	bases, ok := s.relativeBases()
	if !ok {
		return false
	}
	for _, base := range bases {
		if !s.allowedWritePath(joinPathUnclean(base, text)) {
			return false
		}
	}
	return true
}

// relativeBases are the folders a relative path may be relative to: the work
// folder and every literal cd target, resolved in order. A cd to a non-literal
// folder makes relative writes unknowable. Keep earlier bases as possibilities
// because this scan also visits conditional commands and subshells.
func (s *bashScan) relativeBases() ([]string, bool) {
	bases := []string{s.workDir}
	current := s.workDir
	for _, cd := range s.cds {
		if !cd.literal() {
			return nil, false
		}
		dir := s.expandLeadingTilde(cd.text)
		if !filepath.IsAbs(dir) {
			dir = joinPathUnclean(current, dir)
		}
		resolved, err := canonicalPathLoose(dir)
		if err != nil {
			return nil, false
		}
		current = resolved
		bases = append(bases, current)
	}
	return bases, true
}

func (s *bashScan) allowedWritePath(path string) bool {
	canonical, err := canonicalPathLoose(path)
	if err != nil {
		return false
	}
	roots := []string{s.workDir}
	roots = append(roots, tempRoots()...)
	inside := false
	for _, root := range roots {
		if root != "" && pathWithin(canonical, root) {
			inside = true
			break
		}
	}
	if !inside {
		return false
	}
	// Even when the workspace is the home folder, its dotfiles and Library
	// folder hold shell, git and launch configuration that later commands
	// would run.
	if home := cleanAbs(s.home); home != "" && pathWithin(canonical, home) {
		if rel, err := filepath.Rel(home, canonical); err == nil && rel != "." {
			first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
			if (strings.HasPrefix(first, ".") || first == "Library") &&
				!pathWithin(s.workDir, filepath.Join(home, first)) {
				return false
			}
		}
	}
	return true
}

func tempRoots() []string {
	roots := []string{"/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp"}
	if dir := cleanAbs(os.TempDir()); dir != "" {
		roots = append(roots, dir)
	}
	return roots
}

// firmlinkPrefix is the macOS data-volume mount. Everything in the user's
// home is firmlinked between "/System/Volumes/Data/..." and "/...", and the
// two spellings name the same directory, yet EvalSymlinks does not resolve
// the firmlink, so a string comparison misses the "/System/Volumes/Data"
// spelling of a protected path. Stripping the prefix canonicalizes it.
const firmlinkPrefix = "/System/Volumes/Data"

// stripFirmlink rewrites the "/System/Volumes/Data" firmlink spelling of a
// path to its plain "/" spelling on macOS, so the two name one path.
func stripFirmlink(path string) string {
	if runtime.GOOS != "darwin" {
		return path
	}
	if strings.EqualFold(path, firmlinkPrefix) {
		return "/"
	}
	if strings.HasPrefix(strings.ToLower(path), strings.ToLower(firmlinkPrefix)+"/") {
		return path[len(firmlinkPrefix):]
	}
	return path
}

// joinPathUnclean preserves symlink/.. semantics until canonicalPathLoose has
// resolved the filesystem prefix. filepath.Join would erase them too early.
func joinPathUnclean(base, path string) string {
	if base == "" || filepath.IsAbs(path) {
		return path
	}
	if path == "" {
		return base
	}
	return strings.TrimRight(base, string(filepath.Separator)) + string(filepath.Separator) + path
}

// canonicalPathLoose resolves symlinks before simplifying dot-dot components,
// including when a not-yet-created leaf requires resolving an existing prefix.
func canonicalPathLoose(path string) (string, error) {
	raw := stripFirmlink(path)
	existing := raw
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = joinPathUnclean(resolved, missing[i])
			}
			return stripFirmlink(filepath.Clean(resolved)), nil
		}
		// Split, unlike Dir, does not clean an unresolved symlink/.. prefix.
		trimmed := strings.TrimRight(existing, string(filepath.Separator))
		parent, leaf := filepath.Split(trimmed)
		if leaf == "" || parent == "" {
			return stripFirmlink(filepath.Clean(raw)), nil
		}
		missing = append(missing, leaf)
		existing = parent
	}
}

// pathWithin reports whether path is root or inside it.
func pathWithin(path, root string) bool {
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (s *bashScan) checkWrites() {
	seen := map[string]bool{}
	for _, target := range s.expandBraceWords(s.writes) {
		if seen[target.text] {
			continue
		}
		seen[target.text] = true
		shown := strings.ReplaceAll(target.text, "\x00", "...")
		if !s.writeAllowed(target) {
			s.unknown("a command that writes "+shown, "that file is outside the workspace", "")
			continue
		}
		// A glob write into a folder that holds a link this command makes may
		// land on the link and reach whatever it points to.
		if target.pattern && s.folderHoldsLink(target) {
			s.unknown("a command that writes "+shown, "it writes by a glob into a folder holding a link the same command makes", "")
			continue
		}
		for _, path := range s.targetPaths(target) {
			reason := ""
			switch {
			case s.underLink(path):
				reason = "it writes through a link the same command makes"
			case s.underProgramDir(path):
				reason = "it writes into the folder of a program it runs"
			case hardLinked(path):
				reason = "that file has other hard links, which may be outside the workspace"
			default:
			}
			if reason != "" {
				s.unknown("a command that writes "+shown, reason, "")
				break
			}
		}
	}
	for _, link := range s.links {
		if !s.writeAllowed(link.path) {
			s.unknown("a link at "+strings.ReplaceAll(link.path.text, "\x00", "..."), "that place is outside the workspace", "")
		}
	}
}

// targetPaths returns the canonical paths a write target may name: the
// literal folder of a glob, and a relative path under each folder the
// command may be in. It returns nothing for a target built at run time.
func (s *bashScan) targetPaths(target shWord) []string {
	if target.dynamic {
		return nil
	}
	text := target.text
	if target.pattern {
		if cut := strings.IndexAny(text, "*?[{"); cut >= 0 {
			text = text[:cut]
		}
		if i := strings.LastIndex(text, "/"); i >= 0 {
			text = text[:i+1]
		} else {
			text = "."
		}
	}
	var raw []string
	if filepath.IsAbs(text) {
		raw = []string{text}
	} else {
		bases, ok := s.relativeBases()
		if !ok {
			return nil
		}
		for _, base := range bases {
			if base != "" {
				raw = append(raw, joinPathUnclean(base, text))
			}
		}
	}
	var out []string
	for _, r := range raw {
		if canonical, err := canonicalPathLoose(r); err == nil {
			out = append(out, canonical)
		}
	}
	return out
}

// underLink reports a path at or under a link this command makes. The
// comparison is case-insensitive on macOS and Windows, so a write to "./l"
// still reaches a link made as "./L".
func (s *bashScan) underLink(path string) bool {
	for _, link := range s.links {
		for _, linkPath := range s.targetPaths(link.path) {
			if foldedWithin(path, linkPath) {
				return true
			}
		}
	}
	return false
}

// folderHoldsLink reports whether a write target's literal folder holds, or
// is at or under, a link this command makes, so a glob write there may land
// on the link.
func (s *bashScan) folderHoldsLink(target shWord) bool {
	for _, folder := range s.targetPaths(target) {
		for _, link := range s.links {
			for _, linkPath := range s.targetPaths(link.path) {
				if foldedWithin(linkPath, folder) || foldedWithin(folder, linkPath) {
					return true
				}
			}
		}
	}
	return false
}

// expandBraceWords replaces each literal word holding an unquoted brace
// expansion with its members, so every target a brace write names is checked.
// A member with a leading ~ is tilde-expanded, as bash does after brace
// expansion, so "{~/.zprofile,a}" does not read as a folder under the work
// folder.
func (s *bashScan) expandBraceWords(words []shWord) []shWord {
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
			out = append(out, shWord{text: s.expandLeadingTilde(m), pattern: isGlobPattern(m)})
		}
	}
	return out
}

// expandLeadingTilde turns a leading "~/" or bare "~" into the home folder.
func (s *bashScan) expandLeadingTilde(text string) string {
	if s.home == "" || text == "" || text[0] != '~' {
		return text
	}
	if text == "~" {
		return s.home
	}
	if strings.HasPrefix(text, "~/") {
		return s.home + text[1:]
	}
	if resolved, ok := resolveUserTilde(text); ok {
		return resolved
	}
	return text
}

func (s *bashScan) underProgramDir(path string) bool {
	for _, dir := range s.programDirs {
		if pathWithin(path, dir) {
			return true
		}
	}
	return false
}

// hardLinked reports an existing regular file with more than one name: a
// write to it changes every name, which may be outside the workspace.
func hardLinked(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return fileLinkCount(info) > 1
}

// gitInternalsWrittenThenGit reports a command that writes under a
// repository's .git (config, hooks, info, HEAD, refs, modules) and also runs
// a git subcommand: the written config or hook would run on that git command.
func (s *bashScan) gitInternalsWrittenThenGit() bool {
	wrote := false
	for _, w := range s.writes {
		if w.literal() && isGitInternalPath(w.text) {
			wrote = true
			break
		}
	}
	if !wrote {
		return false
	}
	for _, cmd := range s.cmds {
		if len(cmd.argv) == 0 {
			continue
		}
		if name, _, ok := commandName(cmd.argv[0]); ok && name == "git" {
			return true
		}
	}
	return false
}

// isGitInternalPath reports a path under a .git folder whose contents git
// runs or trusts: config, hooks, info, HEAD, refs and submodule configs.
func isGitInternalPath(text string) bool {
	parts := strings.Split(filepath.ToSlash(text), "/")
	for i, p := range parts {
		if p != ".git" || i+1 >= len(parts) {
			continue
		}
		switch parts[i+1] {
		case "config", "hooks", "info", "HEAD", "refs", "modules":
			return true
		default:
		}
	}
	return false
}

// workFolderShadowsModule reports whether the work folder (or a cd target)
// holds a file or package that python -m <module> would run instead of the
// real module: <first>.py or a <first>/ directory for the module's first
// dotted component.
func (s *bashScan) workFolderShadowsModule(module string) bool {
	first := module
	if i := strings.IndexByte(module, '.'); i >= 0 {
		first = module[:i]
	}
	if first == "" || strings.ContainsRune(first, 0) {
		return false
	}
	bases, ok := s.relativeBases()
	if !ok {
		return false
	}
	for _, base := range bases {
		if base == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(base, first+".py")); err == nil && info.Mode().IsRegular() {
			return true
		}
		if info, err := os.Stat(filepath.Join(base, first)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// dangerousEnvName reports variables that decide which program or code a
// later command runs: the search path, startup files, preload libraries,
// interpreter options, git and editor hooks, and the home folder.
func dangerousEnvName(name string) bool {
	upper := strings.ToUpper(name)
	switch upper {
	case "PATH", "BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "PS4", "PROMPT_COMMAND",
		"HOME", "ZDOTDIR", "XDG_CONFIG_HOME", "NODE_OPTIONS", "NODE_PATH", "PYTHONSTARTUP",
		"PYTHONPATH", "PYTHONHOME", "PYTHONUSERBASE", "GOROOT", "GOTOOLDIR", "GOENV",
		"PERL5OPT", "PERL5LIB", "PERLLIB", "RUBYOPT", "RUBYLIB",
		"EDITOR", "VISUAL", "PAGER", "MANPAGER", "LESSOPEN", "LESSCLOSE", "CURL_HOME",
		"WGETRC", "GOFLAGS", "CC", "CXX", "CPP", "LD", "BROWSER", "SSH_ASKPASS",
		"SUDO_ASKPASS", "CDPATH", "GLOBIGNORE", "FPATH", "MAKEFLAGS", "MAKEFILES", "SHELL",
		"YANSU_AGENT_HOME", "RIPGREP_CONFIG_PATH", "ACK_PAGER", "ACK_PAGER_COLOR", "ACK_OPTIONS",
		"ACKRC", "LESS", "LESSKEY", "LESSKEYIN", "TAR_OPTIONS", "UNZIP", "UNZIPOPT", "ZIPOPT",
		"GZIP", "XZ_OPT", "XZ_DEFAULTS", "RSYNC_RSH", "RSYNC_CONNECT_PROG", "CFLAGS", "CXXFLAGS",
		"CPPFLAGS", "LDFLAGS", "RUSTFLAGS", "RUSTC", "RUSTC_WRAPPER", "RUSTDOC", "JAVA_TOOL_OPTIONS",
		"_JAVA_OPTIONS", "JDK_JAVA_OPTIONS", "MAVEN_OPTS", "GRADLE_OPTS", "PYTEST_ADDOPTS",
		"PYTEST_PLUGINS":
		return true
	default:
	}
	for _, prefix := range []string{"LD_", "DYLD_", "GIT_", "NPM_CONFIG_", "BASH_FUNC_", "CGO_", "CARGO_"} {
		if strings.HasPrefix(upper, prefix) && upper != "GIT_TERMINAL_PROMPT" {
			return true
		}
	}
	return false
}

// harmlessPagers are pager settings that only show text.
var harmlessPagers = setOf("", "cat", "less", "more", "true", "less -R", "less -r", "less -FRX",
	"less -RFX", "less -FRSX", "less -S", "less -FX", "less -F -X")

// dangerousEnvAssign is dangerousEnvName for an assignment whose value is
// known: GIT_PAGER and PAGER set to a plain pager (cat, less) are harmless,
// any other value is a program git or man would run.
func dangerousEnvAssign(name string, value shWord) bool {
	switch strings.ToUpper(name) {
	case "GIT_PAGER", "PAGER":
		return !value.literal() || !harmlessPagers[strings.Join(strings.Fields(value.text), " ")]
	default:
	}
	return dangerousEnvName(name)
}

// DeniedBashCommandMatch reports the first denied prefix that any command in a
// Bash script matches, or "". It parses the whole script and unwraps
// wrappers, so `sh -c 'git push'`, `git -C dir push`, a newline before
// `gh api ... -XPOST` and a command substitution are all matched. It fails
// closed: a script that cannot be parsed, or that hides which command will
// run (a dynamic command name, eval of a variable, a script file), returns a
// reason in place of a prefix. prefixes are space-joined command keys, e.g.
// "git push", "gh api".
func DeniedBashCommandMatch(command string, prefixes []string) string {
	if len(prefixes) == 0 {
		return ""
	}
	scan := analyzeBash(command, "")
	if scan.parseFailed {
		return "(a command that could not be parsed)"
	}
	for _, cmd := range scan.cmds {
		key := normalizedCommandKey(cmd.argv)
		if key == "" {
			continue
		}
		for _, p := range prefixes {
			if key == p || strings.HasPrefix(key, p+" ") {
				return p
			}
		}
	}
	for _, v := range scan.verdicts {
		if v.opaque {
			return "(" + v.subject + ": " + v.reason + ")"
		}
	}
	return ""
}

// normalizedCommandKey builds a space-joined key of a command's name plus its
// subcommand words, skipping global options and their values (git -C dir,
// gh -R owner/repo), so it can be prefix-matched against the deny list.
func normalizedCommandKey(argv []shWord) string {
	name, _, ok := commandName(argv[0])
	if !ok {
		return ""
	}
	texts := make([]string, len(argv))
	for i, w := range argv {
		texts[i] = w.text
	}
	parts := []string{name}
	switch name {
	case "git":
		if sub, _, _ := gitSubcommand(argv); sub != "" {
			parts = append(parts, sub)
		}
		return strings.Join(parts, " ")
	case "gh":
		parts = append(parts, cobraPositionals(texts[1:], ghBoolFlags, 2)...)
		return strings.Join(parts, " ")
	default:
	}
	for _, a := range texts[1:] {
		if len(parts) >= 4 || strings.ContainsRune(a, 0) {
			break
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// commandDigest is a short stable id for an exact command, used as the
// session-trust key of approvals that must not generalize.
func commandDigest(command string) string {
	sum := sha256.Sum256([]byte(command))
	return hex.EncodeToString(sum[:8])
}
