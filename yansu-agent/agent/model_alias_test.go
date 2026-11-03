package agent

import "testing"

func TestResolveModel(t *testing.T) {
	r := &NativeAgentRunner{
		modelAliases: map[string]string{
			"opus":   "claude-opus-4-6",
			"sonnet": "claude-sonnet-4-6",
			"haiku":  "claude-haiku-4-5-20251001",
			"fable":  "claude-fable-5",
		},
	}
	cases := []struct {
		in, want string
	}{
		{"opus", "claude-opus-4-6"},
		{"sonnet", "claude-sonnet-4-6"},
		{"haiku", "claude-haiku-4-5-20251001"},
		{"fable", "claude-fable-5"},
		{"claude-opus-4-6", "claude-opus-4-6"}, // full id passes through
		{"claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"unknown-model", "unknown-model"}, // unknown passes through
		{"", ""},                           // empty passes through
	}
	for _, c := range cases {
		got := r.resolveModel(c.in)
		if got != c.want {
			t.Errorf("resolveModel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveModel_NilAliases(t *testing.T) {
	r := &NativeAgentRunner{modelAliases: nil}
	if got := r.resolveModel("opus"); got != "opus" {
		t.Errorf("nil aliases should pass through, got %q", got)
	}
}

func TestResolveModel_EmptyValueFallsThrough(t *testing.T) {
	// An alias intentionally mapped to empty string should NOT silently
	// blank the model — pass the alias through so the caller sees a
	// meaningful upstream error instead of an empty request.
	r := &NativeAgentRunner{modelAliases: map[string]string{"opus": ""}}
	if got := r.resolveModel("opus"); got != "opus" {
		t.Errorf("empty alias value should pass through, got %q", got)
	}
}
