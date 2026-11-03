package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// This shared file is compiled on Linux and other platforms which have no
// menuBarApp global. Language must enter through an explicit argument.
func TestObservingMenuSharedCodeDoesNotReadPlatformGlobal(t *testing.T) {
	files := token.NewFileSet()
	source, err := parser.ParseFile(files, "observation_menu.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(source, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == "menuBarApp" && identifier.Obj == nil {
			t.Errorf("%s: shared observation menu code reads a platform-only global", files.Position(identifier.Pos()))
		}
		return true
	})
}
