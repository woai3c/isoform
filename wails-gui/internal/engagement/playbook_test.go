package engagement

import (
	"strings"
	"testing"
	"time"
)

func TestRequestedChannelsKeepOnlyWhatCanBeReached(t *testing.T) {
	all := Moment{Kind: KindPlaybookResult, Channels: ChoosableChannels()}
	cases := []struct {
		name     string
		moment   Moment
		presence Presence
		want     string
	}{
		{"nothing set up still reaches the Mac", all, Presence{}, "desktop"},
		{"every channel set up", all, Presence{PhoneRegistered: true, SMSActive: true, EmailActive: true}, "desktop,push,text,email"},
		{"a failed push is skipped", all, Presence{PhoneRegistered: true, PushFailed: true, TextAvailable: true}, "desktop,text"},
		{"only the chosen ones", Moment{Channels: []Channel{ChannelText, ChannelEmail}}, Presence{SMSActive: true, EmailActive: true, PhoneRegistered: true}, "text,email"},
		{"none chosen", Moment{}, Presence{PhoneRegistered: true}, ""},
	}
	for _, tc := range cases {
		got := RequestedChannels(tc.moment, tc.presence)
		parts := make([]string, 0, len(got))
		for _, c := range got {
			parts = append(parts, string(c))
		}
		if joined := strings.Join(parts, ","); joined != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, joined, tc.want)
		}
	}
}

func TestParseChannelsDropsUnknownAndRepeats(t *testing.T) {
	got := ParseChannels([]string{"email", "fax", "desktop", "email", "none"})
	if len(got) != 2 || got[0] != ChannelDesktop || got[1] != ChannelEmail {
		t.Fatalf("ParseChannels = %v", got)
	}
}

func TestPlaybookResultIsNotHeldByTheBudget(t *testing.T) {
	prefs := DefaultPrefs()
	prefs.DailyInterruptBudget = 1
	state := NewPaceState()
	now := at(10, 0)
	finished := Moment{Kind: KindWorkFinished, DedupeKey: "work_finished:a", CreatedAt: now}
	v := Pace(finished, mustSpec(t, KindWorkFinished), prefs, state, now)
	if v.Mode != ModeInterrupt {
		t.Fatalf("first active moment: %+v", v)
	}
	state.Record(finished, v, now)

	result := Moment{Kind: KindPlaybookResult, DedupeKey: "playbook_result:run-1", CreatedAt: now}
	v = Pace(result, mustSpec(t, KindPlaybookResult), prefs, state, now)
	if v.Mode != ModeInterrupt || v.Interruption != InterruptionActive {
		t.Fatalf("a playbook result over the budget must still go out: %+v", v)
	}
	state.Record(result, v, now)
	if state.InterruptsToday != 1 {
		t.Fatalf("a playbook result must not use the budget: %d", state.InterruptsToday)
	}

	v = Pace(result, mustSpec(t, KindPlaybookResult), prefs, state, now.Add(time.Minute))
	if v.Mode != ModeSuppress {
		t.Fatalf("the same run's result goes out once: %+v", v)
	}
}

func TestPlaybookResultWaitsOutQuietHours(t *testing.T) {
	prefs := DefaultPrefs()
	night := at(23, 0)
	v := Pace(Moment{Kind: KindPlaybookResult, DedupeKey: "playbook_result:run-2"}, mustSpec(t, KindPlaybookResult), prefs, NewPaceState(), night)
	if v.Mode != ModeHold || v.HoldUntil.Hour() != 8 {
		t.Fatalf("a result at night waits for the morning: %+v", v)
	}
}

func TestPlaybookCopy(t *testing.T) {
	for _, lang := range Languages {
		set, ok := Copy(KindPlaybookResult, lang)
		if !ok || set.EmailSubject == "" || set.EmailBody == "" {
			t.Fatalf("%s: playbook result needs email copy: %+v", lang, set)
		}
		body := Render(set.EmailBody, map[string]string{"title": "PostHog morning check", "detail": "Downloads 1,240", "time": "08:55"})
		if strings.Contains(body, "{") || !strings.Contains(body, "Downloads 1,240") || !strings.Contains(body, "\n\n") {
			t.Fatalf("%s: email body %q", lang, body)
		}
		text, ok := TextMessage(KindPlaybookResult, lang, map[string]string{"title": "PostHog morning check", "result": "Downloads 1,240 (+8%), D7 retention 31%, time in app 6m"})
		if !ok || !strings.HasPrefix(text, "Isoform") || len([]rune(text)) > TextLimit {
			t.Fatalf("%s: text %q", lang, text)
		}
	}
	if got := PlaybookFailedResult("en", "PostHog asked to sign in."); got != "did not finish. PostHog asked to sign in." {
		t.Fatalf("failed result = %q", got)
	}
	if got := PlaybookFailedResult("zh-CN", ""); got != "没有完成。在电脑上打开“操作手册”查看原因。" {
		t.Fatalf("failed result zh = %q", got)
	}
}
