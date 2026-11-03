package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Keep this audit exhaustive: a new direct model call must explicitly choose
// background priority unless it is the attended reply to an incoming chat.
func TestNonChatModelCallsUseBackgroundPriority(t *testing.T) {
	attended := map[string]map[string]bool{
		// The owner-aware core still serves attended user chat; executePrompt
		// delegates here without changing foreground priority.
		"streaming.go":      {"executePromptOwned": true},
		"daemon.go":         {"handleSlackEvent": true, "handleRCEvent": true},
		"telegram_local.go": {"runAgent": true},
		"feishu_local.go":   {"runAgent": true},
		"slack_local.go":    {"runAgent": true},
		"discord_local.go":  {"runAgent": true},
		"wechat_monitor.go": {"handleMessage": true},
	}
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || attended[path][fn.Name.Name] {
				continue
			}
			ast.Inspect(fn, func(node ast.Node) bool {
				literal, ok := node.(*ast.CompositeLit)
				if !ok {
					return true
				}
				typ, ok := literal.Type.(*ast.SelectorExpr)
				if !ok || typ.Sel.Name != "TaskInput" {
					return true
				}
				checked++
				for _, element := range literal.Elts {
					field, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := field.Key.(*ast.Ident)
					if !ok || key.Name != "Priority" {
						continue
					}
					value, ok := field.Value.(*ast.SelectorExpr)
					if ok && value.Sel.Name == "PriorityBackground" {
						return true
					}
				}
				t.Errorf("%s: %s must set agent.PriorityBackground", fset.Position(literal.Pos()), fn.Name.Name)
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("no background model call sites audited")
	}
}
