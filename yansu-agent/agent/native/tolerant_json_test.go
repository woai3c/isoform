package native

import (
	"reflect"
	"strings"
	"testing"
)

type parseCase struct {
	name       string
	input      string
	wantStage  ParseStage
	wantValue  any
	wantPrefix string // partial match on result map keys/values (substring check on rendered JSON)
	schema     map[string]string
}

func TestParse_S1Strict(t *testing.T) {
	cases := []parseCase{
		{name: "plain object", input: `{"path": "/tmp/foo"}`, wantStage: StageS1, wantValue: map[string]any{"path": "/tmp/foo"}},
		{name: "array", input: `[1, 2, 3]`, wantStage: StageS1, wantValue: []any{1.0, 2.0, 3.0}},
		{name: "nested", input: `{"a": {"b": [1]}}`, wantStage: StageS1, wantValue: map[string]any{"a": map[string]any{"b": []any{1.0}}}},
		{name: "escaped quote", input: `{"msg": "he said \"hi\""}`, wantStage: StageS1, wantValue: map[string]any{"msg": `he said "hi"`}},
		{name: "unicode", input: `{"emoji": "☃"}`, wantStage: StageS1, wantValue: map[string]any{"emoji": "☃"}},
		{name: "integer no frac", input: `{"n": 42}`, wantStage: StageS1, wantValue: map[string]any{"n": 42.0}},
		{name: "bool null", input: `{"a": true, "b": false, "c": null}`, wantStage: StageS1, wantValue: map[string]any{"a": true, "b": false, "c": nil}},
	}
	runCases(t, cases)
}

func TestParse_S2MarkdownFences(t *testing.T) {
	cases := []parseCase{
		{name: "fence json", input: "```json\n{\"a\":1}\n```", wantStage: StageS2, wantValue: map[string]any{"a": 1.0}},
		{name: "fence no lang", input: "```\n{\"a\":1}\n```", wantStage: StageS2, wantValue: map[string]any{"a": 1.0}},
		{name: "fence uppercase", input: "```JSON\n{\"a\":1}\n```", wantStage: StageS2, wantValue: map[string]any{"a": 1.0}},
		{name: "fence with surrounding whitespace", input: "   ```json\n{\"a\":1}\n```   ", wantStage: StageS2, wantValue: map[string]any{"a": 1.0}},
	}
	runCases(t, cases)
}

func TestParse_S3GeneralRepair(t *testing.T) {
	cases := []parseCase{
		{name: "trailing comma object", input: `{"a": 1, "b": 2,}`, wantStage: StageS3, wantValue: map[string]any{"a": 1.0, "b": 2.0}},
		{name: "trailing comma array", input: `[1, 2, 3,]`, wantStage: StageS3, wantValue: []any{1.0, 2.0, 3.0}},
		{name: "nested trailing", input: `{"items": [1, 2,],}`, wantStage: StageS3, wantValue: map[string]any{"items": []any{1.0, 2.0}}},
		{name: "smart quotes", input: `{“path”: “/tmp”}`, wantStage: StageS3, wantValue: map[string]any{"path": "/tmp"}},
		{name: "bare key", input: `{path: "/tmp"}`, wantStage: StageS3, wantValue: map[string]any{"path": "/tmp"}},
		{name: "multi bare keys", input: `{path: "/tmp", mode: 420}`, wantStage: StageS3, wantValue: map[string]any{"path": "/tmp", "mode": 420.0}},
		{name: "bare key with dash", input: `{work-dir: "/tmp"}`, wantStage: StageS3, wantValue: map[string]any{"work-dir": "/tmp"}},
		{name: "single quoted string", input: `{"path": '/tmp/foo'}`, wantStage: StageS3, wantValue: map[string]any{"path": "/tmp/foo"}},
		{name: "python true", input: `{"flag": True}`, wantStage: StageS3, wantValue: map[string]any{"flag": true}},
		{name: "python false", input: `{"flag": False}`, wantStage: StageS3, wantValue: map[string]any{"flag": false}},
		{name: "python none", input: `{"value": None}`, wantStage: StageS3, wantValue: map[string]any{"value": nil}},
		{name: "line comment", input: `{"a": 1 // inline}` + "\n", wantStage: StageS3, wantValue: map[string]any{"a": 1.0}},
		{name: "block comment", input: `{/* hello */ "a": 1}`, wantStage: StageS3, wantValue: map[string]any{"a": 1.0}},
		{name: "unclosed object", input: `{"a": 1`, wantStage: StageS3, wantValue: map[string]any{"a": 1.0}},
		{name: "unclosed array", input: `{"arr": [1, 2`, wantStage: StageS3, wantValue: map[string]any{"arr": []any{1.0, 2.0}}},
		{name: "fence + trailing comma", input: "```json\n{\"a\":1,}\n```", wantStage: StageS3, wantValue: map[string]any{"a": 1.0}},
		{name: "bare key + smart quote value", input: `{path: “/tmp”}`, wantStage: StageS3, wantValue: map[string]any{"path": "/tmp"}},
		{name: "unterminated string", input: `{"msg": "hello`, wantStage: StageS3, wantValue: map[string]any{"msg": "hello"}},
		{name: "nested unclosed", input: `{"outer": {"inner": "x"`, wantStage: StageS3, wantValue: map[string]any{"outer": map[string]any{"inner": "x"}}},
		{name: "bare keys nested", input: `{outer: {inner: "x"}}`, wantStage: StageS3, wantValue: map[string]any{"outer": map[string]any{"inner": "x"}}},
		{name: "single quoted inside mixed", input: `{'path': 'a', 'mode': 420}`, wantStage: StageS3, wantValue: map[string]any{"path": "a", "mode": 420.0}},
		{name: "trailing comma in deeply nested", input: `{"a": {"b": {"c": 1,},},}`, wantStage: StageS3, wantValue: map[string]any{"a": map[string]any{"b": map[string]any{"c": 1.0}}}},
		{name: "comma in string not stripped", input: `{"s": "a, b, c,"}`, wantStage: StageS1, wantValue: map[string]any{"s": "a, b, c,"}},
		{name: "comment in string preserved", input: `{"s": "// not a comment"}`, wantStage: StageS1, wantValue: map[string]any{"s": "// not a comment"}},
		{name: "empty object trailing comma", input: `{,}`, wantStage: StageS3, wantValue: map[string]any{}},
		{name: "missing closing brace with comment", input: `{"a": 1 // oops` + "\n", wantStage: StageS3, wantValue: map[string]any{"a": 1.0}},
		{name: "unquoted python style", input: `{path: '/x', ok: True, items: [1, 2,]}`, wantStage: StageS3, wantValue: map[string]any{"path": "/x", "ok": true, "items": []any{1.0, 2.0}}},
	}
	runCases(t, cases)
}

func TestParse_S4Truncation(t *testing.T) {
	cases := []parseCase{
		{
			name:      "truncated mid value",
			input:     `{"path": "/tmp", "content": "hello wor`,
			wantStage: StageS4,
		},
		{
			name:      "truncated after comma",
			input:     `{"a": 1, "b": 2, "c`,
			wantStage: StageS4,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := Parse(c.input, ParseOptions{})
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			// Either S3 or S4 is acceptable — both recover the value.
			if res.Stage != StageS3 && res.Stage != StageS4 {
				t.Errorf("expected S3 or S4, got %s", res.Stage)
			}
			if _, ok := res.Value.(map[string]any); !ok {
				t.Errorf("expected map, got %T", res.Value)
			}
		})
	}
}

func TestParse_S5HeuristicExtraction(t *testing.T) {
	// Purposefully unrecoverable JSON: balanced brackets but invalid
	// scalar in a strict parse context. S3/S4 will fail; S5 pulls the
	// two known fields out by regex.
	schema := map[string]string{"path": "string", "count": "number", "enabled": "boolean"}
	input := `garbage prefix {"path": "/tmp/foo", "count": 42, "enabled": true, "unparseable": <<<broken>>>} trailing garbage`

	res, err := Parse(input, ParseOptions{Schema: schema})
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if res.Stage != StageS5 && res.Stage != StageS3 && res.Stage != StageS4 {
		t.Fatalf("expected S3/S4/S5, got %s", res.Stage)
	}
	m, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", res.Value)
	}
	if m["path"] != "/tmp/foo" {
		t.Errorf("path = %v, want /tmp/foo", m["path"])
	}
	if m["count"] != 42.0 {
		t.Errorf("count = %v, want 42", m["count"])
	}
	if m["enabled"] != true {
		t.Errorf("enabled = %v, want true", m["enabled"])
	}
}

func TestParse_MaxStageCap(t *testing.T) {
	input := `{"a": 1,}`
	_, err := Parse(input, ParseOptions{MaxStage: StageS1})
	if err == nil {
		t.Fatal("expected failure when capped at S1")
	}
	res, err := Parse(input, ParseOptions{MaxStage: StageS3})
	if err != nil {
		t.Fatalf("expected S3 success, got %v", err)
	}
	if res.Stage != StageS3 {
		t.Errorf("stage = %s, want S3", res.Stage)
	}
}

func TestParse_EmptyInput(t *testing.T) {
	if _, err := Parse("", ParseOptions{}); err == nil {
		t.Error("expected error on empty input")
	}
	if _, err := Parse("   \n\t ", ParseOptions{}); err == nil {
		t.Error("expected error on whitespace-only input")
	}
}

func TestParse_WarningSurface(t *testing.T) {
	res, err := Parse("```json\n{\"a\":1}\n```", ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(res.Warning, "markdown") {
		t.Errorf("expected warning mentioning markdown, got %q", res.Warning)
	}

	res, err = Parse(`{"a": 1,}`, ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(res.Warning, "malformed") && !strings.Contains(res.Warning, "repaired") {
		t.Errorf("expected S3 repair warning, got %q", res.Warning)
	}
}

func TestStripMarkdownFence(t *testing.T) {
	cases := map[string]string{
		"```json\nhello\n```":     "hello",
		"```\nhi\n```":            "hi",
		"plain":                   "plain",
		"   ```JSON\nok\n```   ":  "ok",
		"```json\n{\"a\":1}\n```": `{"a":1}`,
	}
	for in, want := range cases {
		if got := stripMarkdownFence(in); got != want {
			t.Errorf("stripMarkdownFence(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDropTrailingCommas(t *testing.T) {
	cases := map[string]string{
		`{"a":1,}`:             `{"a":1}`,
		`[1,2,3,]`:             `[1,2,3]`,
		`[{"a":1,},{"b":2,},]`: `[{"a":1},{"b":2}]`,
		`{"s":"a, b,"}`:        `{"s":"a, b,"}`, // string contents preserved
	}
	for in, want := range cases {
		if got := dropTrailingCommas(in); got != want {
			t.Errorf("dropTrailingCommas(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBalanceBrackets(t *testing.T) {
	cases := map[string]string{
		`{"a":1`:            `{"a":1}`,
		`[1,2`:              `[1,2]`,
		`{"a":[1,2`:         `{"a":[1,2]}`,
		`{"a":{"b":[1`:      `{"a":{"b":[1]}}`,
		`"s with [ inside"`: `"s with [ inside"`,
	}
	for in, want := range cases {
		if got := balanceBrackets(in); got != want {
			t.Errorf("balanceBrackets(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReplacePythonLiterals(t *testing.T) {
	cases := map[string]string{
		`{"x": True}`:         `{"x": true}`,
		`{"x": False}`:        `{"x": false}`,
		`{"x": None}`:         `{"x": null}`,
		`{"label":"Truelie"}`: `{"label":"Truelie"}`, // inside string untouched by regex — exercises the boundary test
		`[True, False, None]`: `[true, false, null]`,
	}
	for in, want := range cases {
		if got := replacePythonLiterals(in); got != want {
			t.Errorf("replacePythonLiterals(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteBareKeys(t *testing.T) {
	cases := map[string]string{
		`{a: 1}`:                `{"a": 1}`,
		`{a: 1, b: 2}`:          `{"a": 1, "b": 2}`,
		`{"s": "a: b"}`:         `{"s": "a: b"}`, // inside string untouched
		`{"x": {nested: true}}`: `{"x": {"nested": true}}`,
	}
	for in, want := range cases {
		if got := quoteBareKeys(in); got != want {
			t.Errorf("quoteBareKeys(%q) = %q, want %q", in, got, want)
		}
	}
}

func runCases(t *testing.T, cases []parseCase) {
	t.Helper()
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			res, err := Parse(c.input, ParseOptions{Schema: c.schema})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if c.wantStage != "" && res.Stage != c.wantStage {
				t.Errorf("stage = %s, want %s", res.Stage, c.wantStage)
			}
			if c.wantValue != nil && !reflect.DeepEqual(res.Value, c.wantValue) {
				t.Errorf("value = %#v, want %#v", res.Value, c.wantValue)
			}
		})
	}
}
