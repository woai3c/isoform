package main

import (
	"reflect"
	"testing"
)

func TestExpandCommaSeparated(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "single comma string",
			in:   []string{"-y,@x/y,/tmp"},
			want: []string{"-y", "@x/y", "/tmp"},
		},
		{
			name: "repeated flag values",
			in:   []string{"-y", "@x/y", "/tmp"},
			want: []string{"-y", "@x/y", "/tmp"},
		},
		{
			name: "mixed and trims whitespace",
			in:   []string{"-y , @x/y", " /tmp "},
			want: []string{"-y", "@x/y", "/tmp"},
		},
		{
			name: "empty fragments dropped",
			in:   []string{",", "", "-y,,/tmp"},
			want: []string{"-y", "/tmp"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandCommaSeparated(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("expandCommaSeparated(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseMcpEnvFlags(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "single pair",
			in:   []string{"FOO=bar"},
			want: map[string]string{"FOO": "bar"},
		},
		{
			name: "multiple via comma",
			in:   []string{"FOO=bar,BAZ=qux"},
			want: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name: "value containing comma split point lost",
			// Documenting current behaviour: commas are pair separators,
			// so values with embedded commas need separate --env flags.
			in:      []string{"FOO=hello,world"},
			want:    map[string]string{"FOO": "hello", "world": ""},
			wantErr: true, // "world" has no '=', so it's rejected.
		},
		{
			name:    "missing equals",
			in:      []string{"FOO"},
			wantErr: true,
		},
		{
			name:    "leading equals",
			in:      []string{"=bar"},
			wantErr: true,
		},
		{
			name: "trailing whitespace tolerated on key",
			in:   []string{" FOO =bar"},
			want: map[string]string{"FOO": "bar"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMcpEnvFlags(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseMcpEnvFlags(%v) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMcpEnvFlags(%v) error: %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseMcpEnvFlags(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
