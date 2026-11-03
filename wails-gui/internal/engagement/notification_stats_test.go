package engagement

import "testing"

func TestNotificationStatsTag(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		kind                       Kind
		sent, openedOrActed, acted int
		quiet                      bool
		want                       NotificationTag
	}{
		{"zero", KindBriefReady, 0, 0, 0, false, NotificationTagNone},
		{"rare lower", KindBriefReady, 1, 0, 0, false, NotificationTagRare},
		{"rare upper", KindBriefReady, 4, 0, 0, false, NotificationTagRare},
		{"ignored lower", KindBriefReady, 5, 0, 0, false, NotificationTagIgnored},
		{"ignored below ten percent", KindBriefReady, 11, 1, 0, false, NotificationTagIgnored},
		{"opened at ten percent", KindBriefReady, 10, 1, 0, false, NotificationTagNone},
		{"acted at ten percent", KindBriefReady, 10, 1, 1, false, NotificationTagNone},
		{"opened then acted counted once", KindBriefReady, 11, 1, 1, false, NotificationTagIgnored},
		{"below noisy", KindBriefReady, 19, 0, 0, false, NotificationTagIgnored},
		{"noisy lower", KindBriefReady, 20, 0, 0, false, NotificationTagNoisy},
		{"noisy upper", KindBriefReady, 21, 0, 0, false, NotificationTagNoisy},
		{"noisy opened", KindBriefReady, 20, 1, 0, false, NotificationTagIgnored},
		{"noisy acted", KindBriefReady, 20, 1, 1, false, NotificationTagIgnored},
		{"needs lower", KindDecisionDue, 1, 0, 0, false, NotificationTagNeedsYou},
		{"needs below half", KindDecisionDue, 10, 4, 4, false, NotificationTagNeedsYou},
		{"needs at half", KindDecisionDue, 10, 5, 5, false, NotificationTagNone},
		{"needs above half", KindDecisionDue, 10, 6, 6, false, NotificationTagNone},
		{"needs zero", KindDecisionDue, 0, 0, 0, false, NotificationTagNone},
		{"needs noisy priority", KindDecisionDue, 20, 0, 0, false, NotificationTagNeedsYou},
		{"needs ignored priority", KindDecisionDue, 5, 0, 0, false, NotificationTagNeedsYou},
		{"quiet zero", KindBriefReady, 0, 0, 0, true, NotificationTagQuiet},
		{"quiet noisy", KindBriefReady, 20, 0, 0, true, NotificationTagQuiet},
		{"quiet ignored", KindBriefReady, 5, 0, 0, true, NotificationTagQuiet},
		{"quiet rare", KindBriefReady, 1, 0, 0, true, NotificationTagQuiet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NotificationStatsTag(tc.kind, tc.sent, tc.openedOrActed, tc.acted, tc.quiet); got != tc.want {
				t.Fatalf("tag = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNotificationCanQuiet(t *testing.T) {
	for _, kind := range AllKinds() {
		needs := kind == KindDecisionDue || kind == KindCommitmentSlipping || kind == KindWaitingOnReply || kind == KindWorkBlocked
		if NotificationNeedsYou(kind) != needs {
			t.Fatalf("needs-you policy for %s", kind)
		}
		if got := QuietOnTodayAllowed(kind); got != (kind != KindDecisionDue) {
			t.Fatalf("explicit quiet choice for %s = %v", kind, got)
		}
		if got := QuietSuggestionAllowed(kind); got != (!needs && kind != KindReminder) {
			t.Fatalf("quiet suggestion policy for %s = %v", kind, got)
		}
		if needs && NotificationStatsTag(kind, 1, 0, 0, false) != NotificationTagNeedsYou {
			t.Fatalf("needs-you tag for %s", kind)
		}
	}
	if QuietOnTodayAllowed("unknown") {
		t.Fatal("unknown kinds cannot be quieted")
	}
}

func TestNotificationSampleCopyMatchesActualGenericPushVariants(t *testing.T) {
	for _, lang := range Languages {
		for _, kind := range AllKinds() {
			for _, thread := range []bool{false, true} {
				params := map[string]string{}
				if thread {
					params["_activity_thread"] = "private-thread-id"
				}
				actual, ok := Copy(kind, lang, params)
				key := NotificationSampleCopyKey(kind, thread)
				sample, found := NotificationSampleCopy(key, lang)
				if !ok || !found || sample.PushBody != actual.PushBody {
					t.Fatalf("%s/%s thread=%v key=%s generic copy mismatch", kind, lang, thread, key)
				}
			}
		}
	}
	if _, ok := NotificationSampleCopy("unknown", "en"); ok {
		t.Fatal("unknown sample key")
	}
}
