//go:build !darwin

package main

func runLoginItemCommand(args []string) (exitCode int, handled bool) {
	_, handled = loginItemCommandAction(args)
	return 2, handled
}
