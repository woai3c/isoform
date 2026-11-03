package openanswer

import (
	"strings"
	"testing"
)

func TestBlockFilterEveryChunkBoundary(t *testing.T) {
	input := `Answer.<open_answer>{"question":"When?"}</open_answer>Done.`
	for split := 0; split <= len(input); split++ {
		var filter BlockFilter
		a, first := filter.Feed(input[:split])
		b, second := filter.Feed(input[split:])
		visible := a + b + filter.Finish()
		blocks := append(first, second...)
		if visible != "Answer.Done." || len(blocks) != 1 || blocks[0] != `{"question":"When?"}` {
			t.Fatalf("split %d: visible %q blocks %q", split, visible, blocks)
		}
	}
}

func TestBlockFilterUnfinishedAndOversized(t *testing.T) {
	var filter BlockFilter
	visible, blocks := filter.Feed("Visible<open_answer>secret")
	if visible != "Visible" || len(blocks) != 0 || filter.Finish() != "" {
		t.Fatal("unfinished block leaked")
	}
	visible, blocks = filter.Feed("<open_answer>" + strings.Repeat("x", maxBlockBytes+1) + "</open_answer>after")
	if visible != "after" || len(blocks) != 0 {
		t.Fatalf("oversized block leaked: %q %v", visible, blocks)
	}
	visible, _ = filter.Feed("Visible<open_ans")
	if visible != "Visible" || filter.Finish() != "" {
		t.Fatal("partial opening delimiter leaked")
	}
}

func TestBlockFilterSingleByteChunks(t *testing.T) {
	input := "before<open_answer>first</open_answer>middle<open_answer>second</open_answer>after"
	var filter BlockFilter
	var visible strings.Builder
	var blocks []string
	for i := range len(input) {
		text, found := filter.Feed(input[i : i+1])
		visible.WriteString(text)
		blocks = append(blocks, found...)
	}
	visible.WriteString(filter.Finish())
	if visible.String() != "beforemiddleafter" || len(blocks) != 2 || blocks[0] != "first" || blocks[1] != "second" {
		t.Fatalf("visible %q blocks %q", visible.String(), blocks)
	}
}

func TestBlockFilterDiscardsEveryUnfinishedOpeningDelimiter(t *testing.T) {
	for n := 1; n < len(blockOpen); n++ {
		prefix := blockOpen[:n]
		for split := 0; split <= len(prefix); split++ {
			var filter BlockFilter
			first, _ := filter.Feed("Visible" + prefix[:split])
			second, _ := filter.Feed(prefix[split:])
			if got := first + second + filter.Finish(); got != "Visible" {
				t.Fatalf("prefix %q, split %d: visible %q", prefix, split, got)
			}
		}
		var filter BlockFilter
		filter.Feed("Visible<open_answer>{}</open_answer>" + prefix)
		if filter.Complete() || filter.CandidateComplete() {
			t.Fatalf("prefix %q: unfinished second delimiter accepted", prefix)
		}
	}
}

func TestBlockFilterCandidateMustEndAnswer(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        bool
	}{
		{"answer then block", "Not yet. <open_answer>{}</open_answer> \n", true},
		{"empty answer", " \n<open_answer>{}</open_answer>", false},
		{"answer after block", "<open_answer>{}</open_answer>Not yet.", false},
		{"text after block", "Not yet. <open_answer>{}</open_answer>More.", false},
		{"partial delimiter after block", "Not yet. <open_answer>{}</open_answer><", false},
		{"second block", "Not yet. <open_answer>{}</open_answer><open_answer>{}</open_answer>", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for split := 0; split <= len(tc.input); split++ {
				var filter BlockFilter
				filter.Feed(tc.input[:split])
				filter.Feed(tc.input[split:])
				if got := filter.CandidateComplete(); got != tc.want {
					t.Fatalf("split %d: candidate complete = %v, want %v", split, got, tc.want)
				}
			}
		})
	}
}

func TestBlockFilterNestedBlocksNeverLeak(t *testing.T) {
	input := "Answer.<open_answer>outer<open_answer>inner</open_answer>private</open_answer>Visible."
	for split := 0; split <= len(input); split++ {
		var filter BlockFilter
		first, blocks1 := filter.Feed(input[:split])
		second, blocks2 := filter.Feed(input[split:])
		if got := first + second + filter.Finish(); got != "Answer.Visible." {
			t.Fatalf("split %d: visible %q", split, got)
		}
		if len(blocks1)+len(blocks2) != 0 {
			t.Fatalf("split %d: nested block was retained", split)
		}
	}

	var filter BlockFilter
	var visible strings.Builder
	for i := range len(input) {
		text, blocks := filter.Feed(input[i : i+1])
		visible.WriteString(text)
		if len(blocks) != 0 {
			t.Fatal("nested block was retained")
		}
	}
	visible.WriteString(filter.Finish())
	if visible.String() != "Answer.Visible." {
		t.Fatalf("single-byte chunks: visible %q", visible.String())
	}

	var unfinished BlockFilter
	text, blocks := unfinished.Feed("Answer.<open_answer>outer<open_answer>inner</open_answer>private")
	if text+unfinished.Finish() != "Answer." || len(blocks) != 0 {
		t.Fatal("unfinished outer block leaked")
	}
}

func TestBlockFilterSupportsOpenAnswerChangeTag(t *testing.T) {
	input := `<open_answer_change>{"id":"answer-1","action":"cancel"}</open_answer_change>`
	for split := 0; split <= len(input); split++ {
		filter := NewBlockFilter("open_answer_change")
		first, blocks1 := filter.Feed(input[:split])
		second, blocks2 := filter.Feed(input[split:])
		if got := first + second + filter.Finish(); got != "" {
			t.Fatalf("split %d: visible %q", split, got)
		}
		blocks := append(blocks1, blocks2...)
		if len(blocks) != 1 || blocks[0] != `{"id":"answer-1","action":"cancel"}` {
			t.Fatalf("split %d: blocks %q", split, blocks)
		}
	}
}
