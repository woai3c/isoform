package daemon

import "testing"

func TestIsPassiveChannelEvent(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  bool
	}{
		{
			name:  "slack channel message without mention is passive",
			event: Event{Platform: "slack", EventType: "message", ChatType: "group"},
			want:  true,
		},
		{
			name:  "slack channel message with empty chat type is passive",
			event: Event{Platform: "slack", EventType: "message", ChatType: ""},
			want:  true,
		},
		{
			name:  "slack DM is interactive",
			event: Event{Platform: "slack", EventType: "message", ChatType: "p2p"},
			want:  false,
		},
		{
			name:  "slack app_mention in channel is interactive",
			event: Event{Platform: "slack", EventType: "app_mention", ChatType: "group"},
			want:  false,
		},
		{
			name:  "older backend without event_type is interactive",
			event: Event{Platform: "slack", EventType: "", ChatType: "group"},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPassiveChannelEvent(tt.event); got != tt.want {
				t.Errorf("IsPassiveChannelEvent(%+v) = %v, want %v", tt.event, got, tt.want)
			}
		})
	}
}
