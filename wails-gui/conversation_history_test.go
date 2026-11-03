package main

import "testing"

func TestLastUserTurnHasImagePlaceholder(t *testing.T) {
	tests := []struct {
		name  string
		items []conversationItem
		want  bool
	}{
		{
			name:  "empty",
			items: nil,
			want:  false,
		},
		{
			name: "only text turns",
			items: []conversationItem{
				{Kind: "user", Content: "hello"},
				{Kind: "assistant", Content: "hi"},
			},
			want: false,
		},
		{
			name: "most recent user turn is the image",
			items: []conversationItem{
				{Kind: "user", Content: "[user sent 1 image(s)]"},
				{Kind: "assistant", Content: "nice bridge"},
			},
			want: true,
		},
		{
			name: "image turn is older than the most recent user turn",
			items: []conversationItem{
				{Kind: "user", Content: "[user sent 1 image(s)]"},
				{Kind: "assistant", Content: "nice bridge"},
				{Kind: "user", Content: "thanks, what about it"},
				{Kind: "assistant", Content: "..."},
			},
			want: false,
		},
		{
			name: "most recent user turn has text plus placeholder",
			items: []conversationItem{
				{Kind: "user", Content: "look at this [user sent 2 image(s)]"},
			},
			want: true,
		},
		{
			name: "placeholder only on assistant turn does not count",
			items: []conversationItem{
				// Assistants never send images — if the marker somehow lands
				// in an assistant turn it must not count as a user image.
				{Kind: "assistant", Content: "I saw the [user sent 1 image(s)]"},
				{Kind: "user", Content: "tell me more"},
			},
			want: false,
		},
		{
			name: "marker substrings present but format does not match",
			items: []conversationItem{
				// Both substrings "[user sent " and "image(s)]" appear, but
				// not in the exact "[user sent N image(s)]" shape — the regex
				// should reject this where the old substring check did not.
				{Kind: "user", Content: "show me [user sent the image(s)] from Bob"},
			},
			want: false,
		},
		{
			name: "placeholder split across two user turns does not count",
			items: []conversationItem{
				// Each turn is matched independently; partial markers split
				// across turns must not trigger.
				{Kind: "user", Content: "[user sent 1"},
				{Kind: "assistant", Content: "..."},
				{Kind: "user", Content: "image(s)]"},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastUserTurnHasImagePlaceholder(tc.items); got != tc.want {
				t.Fatalf("lastUserTurnHasImagePlaceholder() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShouldAnchorVisualContext(t *testing.T) {
	historyImageThenAssistant := []conversationItem{
		{Kind: "user", Content: "[user sent 1 image(s)]"},
		{Kind: "assistant", Content: "looks like a bridge"},
	}
	historyTextOnly := []conversationItem{
		{Kind: "user", Content: "hello"},
		{Kind: "assistant", Content: "hi"},
	}
	historyImageThenLaterText := []conversationItem{
		{Kind: "user", Content: "[user sent 1 image(s)]"},
		{Kind: "assistant", Content: "looks like a bridge"},
		{Kind: "user", Content: "thanks"},
		{Kind: "assistant", Content: "you're welcome"},
	}

	tests := []struct {
		name              string
		items             []conversationItem
		newImageAttempted bool
		userText          string
		want              bool
	}{
		{
			name:              "text follow-up directly after image",
			items:             historyImageThenAssistant,
			newImageAttempted: false,
			userText:          "what brand is that?",
			want:              true,
		},
		{
			name:              "new image attempt suppresses anchor",
			items:             historyImageThenAssistant,
			newImageAttempted: true,
			userText:          "look at this",
			want:              false,
		},
		{
			name:              "empty user text suppresses anchor",
			items:             historyImageThenAssistant,
			newImageAttempted: false,
			userText:          "",
			want:              false,
		},
		{
			name:              "whitespace-only user text suppresses anchor",
			items:             historyImageThenAssistant,
			newImageAttempted: false,
			userText:          "   \n  ",
			want:              false,
		},
		{
			name:              "no image anywhere in history",
			items:             historyTextOnly,
			newImageAttempted: false,
			userText:          "tell me more",
			want:              false,
		},
		{
			name:              "image is older than the most recent user turn",
			items:             historyImageThenLaterText,
			newImageAttempted: false,
			userText:          "say more",
			want:              false,
		},
		{
			name:              "empty history",
			items:             nil,
			newImageAttempted: false,
			userText:          "first message",
			want:              false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldAnchorVisualContext(tc.items, tc.newImageAttempted, tc.userText)
			if got != tc.want {
				t.Fatalf("shouldAnchorVisualContext(newImageAttempted=%v, userText=%q) = %v, want %v",
					tc.newImageAttempted, tc.userText, got, tc.want)
			}
		})
	}
}
