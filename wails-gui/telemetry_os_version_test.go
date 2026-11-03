package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// A Windows GUI build must not launch a shell to inspect the OS version. Keep
// this check portable so Linux CI catches regressions that would flash a console.
func TestTelemetryWindowsOSVersionDoesNotStartProcess(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "telemetry_product.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundWindows := false
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "telemetryOSVersion" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			branch, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expression := range branch.List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				platform, _ := strconv.Unquote(literal.Value)
				if platform != "windows" {
					continue
				}
				foundWindows = true
				ast.Inspect(branch, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if ok && (selector.Sel.Name == "Command" || selector.Sel.Name == "CommandContext") {
						t.Error("Windows OS version must use the native version API without starting a child process")
					}
					return true
				})
			}
			return true
		})
	}
	if !foundWindows {
		t.Fatal("Windows OS version branch was not checked")
	}
}
