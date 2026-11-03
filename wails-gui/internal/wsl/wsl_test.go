//go:build windows

package wsl

import (
	"testing"
)

func TestToLinuxPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"drive letter", `D:\project`, "/mnt/d/project"},
		{"drive letter with slash", "D:/project/src", "/mnt/d/project/src"},
		{"c drive", `C:\Users\bin`, "/mnt/c/Users/bin"},
		{"drive letter only", "D:", "/mnt/d/"},
		{"already linux", "/home/user/project", "/home/user/project"},
		{"already linux root", "/", "/"},
		{"wsl unc path", `\\wsl$\Ubuntu\home\user`, "/home/user"},
		{"wsl localhost unc", `\\wsl.localhost\Ubuntu\home\user`, "/home/user"},
		{"path with spaces", `D:\my project\src`, "/mnt/d/my project/src"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToLinuxPath(tt.input)
			if got != tt.expected {
				t.Errorf("ToLinuxPath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestToWindowsPath(t *testing.T) {
	tests := []struct {
		name     string
		distro   string
		path     string
		expected string
	}{
		{"home dir", "Ubuntu", "/home/user", `\\wsl$\Ubuntu\home\user`},
		{"root", "Ubuntu-22.04", "/", `\\wsl$\Ubuntu-22.04\`},
		{"default distro", "", "/home/user", `\\wsl$\Ubuntu\home\user`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToWindowsPath(tt.distro, tt.path)
			if got != tt.expected {
				t.Errorf("ToWindowsPath(%q, %q) = %q, want %q", tt.distro, tt.path, got, tt.expected)
			}
		})
	}
}

func TestIsWSLPath(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{`\\wsl$\Ubuntu\home`, true},
		{`\\wsl.localhost\Ubuntu\home`, true},
		{`D:\project`, false},
		{"/home/user", false},
		{"C:/Users", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := IsWSLPath(tt.input)
			if got != tt.expected {
				t.Errorf("IsWSLPath(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseListVerbose(t *testing.T) {
	input := "  NAME            STATE           VERSION\r\n* Ubuntu-22.04    Running         2\r\n  Debian          Stopped         2\r\n"

	distros, err := parseListVerbose(input)
	if err != nil {
		t.Fatalf("parseListVerbose: %v", err)
	}

	if len(distros) != 2 {
		t.Fatalf("expected 2 distros, got %d", len(distros))
	}

	if distros[0].Name != "Ubuntu-22.04" || !distros[0].IsDefault || distros[0].State != "Running" || distros[0].Version != 2 {
		t.Errorf("distro[0] = %+v", distros[0])
	}

	if distros[1].Name != "Debian" || distros[1].IsDefault || distros[1].State != "Stopped" || distros[1].Version != 2 {
		t.Errorf("distro[1] = %+v", distros[1])
	}
}
