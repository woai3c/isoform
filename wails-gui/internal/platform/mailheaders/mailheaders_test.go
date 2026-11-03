package mailheaders

import (
	"strings"
	"testing"
	"time"
)

func TestParseMapsTheScriptOutput(t *testing.T) {
	out := []byte(`{"running":true,
		"me":["Me@Example.com","me@example.com","work@corp.example"],
		"inbox":[
			{"id":"<a1@mail.example.com>","from":"Sam Lee <Sam@Example.com>","subject":"Re:  the   Q3 deck","at":1760400000000,
			 "read":false,"flagged":true,"replied":false,"to":["me@example.com","Ann <ann@example.com>"],"cc":["bob@example.com"]},
			{"id":"","from":"x@example.com","subject":"no id","at":1760400000000},
			{"id":"<a2@x>","from":"no date <y@example.com>","subject":"no date","at":0},
			{"id":"<a3@x>","from":"garbage","subject":"Hello","at":1760400001000,"to":["not an address"]}
		],
		"sent":[{"id":"<s1@x>","from":"Me <me@example.com>","subject":"Re: the Q3 deck","at":1760500000000,"to":["sam@example.com"],"cc":null}]
	}`)
	snap, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Running || len(snap.Me) != 2 || snap.Me[0] != "me@example.com" {
		t.Fatalf("me = %v", snap.Me)
	}
	if len(snap.Inbox) != 2 || len(snap.Sent) != 1 {
		t.Fatalf("inbox %d sent %d", len(snap.Inbox), len(snap.Sent))
	}
	if snap.Dropped != 2 {
		t.Fatalf("dropped = %d, want 2", snap.Dropped)
	}
	first := snap.Inbox[0]
	if first.MessageID != "<a1@mail.example.com>" || first.FromName != "Sam Lee" || first.FromEmail != "sam@example.com" {
		t.Fatalf("first = %+v", first)
	}
	if first.Subject != "Re: the Q3 deck" || first.Read || !first.Flagged || first.Replied {
		t.Fatalf("first = %+v", first)
	}
	if !first.At.Equal(time.UnixMilli(1760400000000)) {
		t.Fatalf("at = %v", first.At)
	}
	if strings.Join(first.To, ",") != "me@example.com,ann@example.com" || strings.Join(first.CC, ",") != "bob@example.com" {
		t.Fatalf("recipients = %v %v", first.To, first.CC)
	}
	if bad := snap.Inbox[1]; bad.FromEmail != "" || len(bad.To) != 0 {
		t.Fatalf("an unparseable sender keeps no address: %+v", bad)
	}
}

func TestParseNotRunningAndErrors(t *testing.T) {
	snap, err := Parse([]byte(`{"running":false}`))
	if err != nil || snap.Running || snap.Inbox != nil {
		t.Fatalf("snap = %+v, err = %v", snap, err)
	}
	if _, err := Parse([]byte("")); err == nil {
		t.Fatal("empty output must fail")
	}
	if _, err := Parse([]byte("execution error: Not authorized to send Apple events to Mail. (-1743)")); err == nil {
		t.Fatal("an error message must fail")
	}
}

func TestParseCompletenessRequiresBothMailboxArrays(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{"complete", `{"running":true,"complete":true,"inbox":[],"sent":[]}`, true},
		{"script reports incomplete", `{"running":true,"complete":false,"inbox":[],"sent":[]}`, false},
		{"missing completeness", `{"running":true,"inbox":[],"sent":[]}`, false},
		{"missing inbox", `{"running":true,"complete":true,"sent":[]}`, false},
		{"null sent", `{"running":true,"complete":true,"inbox":[],"sent":null}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := Parse([]byte(tc.raw))
			if err != nil || snap.Complete != tc.want {
				t.Fatalf("Complete = %v, err = %v; want %v", snap.Complete, err, tc.want)
			}
		})
	}
}

func TestParseCompletenessRequiresValidAddresses(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{"valid", `{"running":true,"complete":true,"me":["me@example.com"],"inbox":[{"id":"i","at":1,"from":"Priya <priya@example.com>","to":["me@example.com"],"cc":[]}],"sent":[{"id":"s","at":2,"to":["priya@example.com"],"cc":[]}]}`, true},
		{"invalid account", `{"running":true,"complete":true,"me":["bad"],"inbox":[],"sent":[]}`, false},
		{"invalid inbox sender", `{"running":true,"complete":true,"inbox":[{"id":"i","at":1,"from":"bad","to":[],"cc":[]}],"sent":[]}`, false},
		{"invalid recipient", `{"running":true,"complete":true,"me":["me@example.com"],"inbox":[{"id":"i","at":1,"from":"priya@example.com","to":["me@example.com","bad"],"cc":[]}],"sent":[]}`, false},
		{"missing recipients", `{"running":true,"complete":true,"me":["me@example.com"],"inbox":[{"id":"i","at":1,"from":"priya@example.com","to":[]}],"sent":[]}`, false},
		{"fallback sender missing", `{"running":true,"complete":true,"inbox":[],"sent":[{"id":"s","at":1,"to":[],"cc":[]}]}`, false},
		{"fallback sender valid", `{"running":true,"complete":true,"inbox":[],"sent":[{"id":"s","at":1,"from":"me@example.com","to":[],"cc":[]}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := Parse([]byte(tc.raw))
			if err != nil || snap.Complete != tc.want {
				t.Fatalf("Complete = %v, err = %v; want %v", snap.Complete, err, tc.want)
			}
		})
	}
}

func TestParseKeepsUsableHeadersFromIncompleteAddresses(t *testing.T) {
	snap, err := Parse([]byte(`{"running":true,"complete":true,"me":["me@example.com"],"inbox":[{"id":"i","at":1,"from":"priya@example.com","to":["me@example.com","invalid"],"cc":[]}],"sent":[]}`))
	if err != nil || snap.Complete || len(snap.Inbox) != 1 || len(snap.Inbox[0].To) != 1 || snap.Inbox[0].To[0] != "me@example.com" {
		t.Fatalf("snapshot = %+v, err = %v", snap, err)
	}
}

func TestParseSender(t *testing.T) {
	cases := []struct{ in, name, email string }{
		{"Sam Lee <sam@example.com>", "Sam Lee", "sam@example.com"},
		{`"Lee, Sam" <Sam@Example.COM>`, "Lee, Sam", "sam@example.com"},
		{"sam@example.com", "", "sam@example.com"},
		{"李雷 <li@example.com>", "李雷", "li@example.com"},
		{"Broken <not-an-address>", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		name, email := ParseSender(c.in)
		if name != c.name || email != c.email {
			t.Errorf("ParseSender(%q) = %q %q, want %q %q", c.in, name, email, c.name, c.email)
		}
	}
}

func TestNormalizeSubject(t *testing.T) {
	cases := map[string]string{
		"Re: Q3 deck":                "Q3 deck",
		"RE: Fwd: re:  Q3   deck ":   "Q3 deck",
		"AW: Angebot":                "Angebot",
		"回复：季度报告":                    "季度报告",
		"Re[2]: Budget":              "Budget",
		"[team-list] Re: Offsite":    "Offsite",
		"Regarding the plan":         "Regarding the plan",
		"Fwd:":                       "",
		"Request: access to the doc": "Request: access to the doc",
	}
	for in, want := range cases {
		if got := NormalizeSubject(in); got != want {
			t.Errorf("NormalizeSubject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScriptReadsOnlyHeadersAndNeverOpensMail(t *testing.T) {
	script := Script(7, 4242)
	if !strings.Contains(script, "Application(4242)") {
		t.Fatal("the script must address the running Mail by its process id")
	}
	for _, byName := range []string{BundleID, `Application("Mail")`, `Application('Mail')`} {
		if strings.Contains(script, byName) {
			t.Fatalf("addressing Mail by %q would start it again after it quit", byName)
		}
	}
	running := strings.Index(script, "mail.running()")
	firstRead := strings.Index(script, "mail.accounts")
	if running < 0 || firstRead < 0 || running > firstRead {
		t.Fatal("the script must check that Mail runs before asking it anything")
	}
	if strings.Count(script, "mail.running()") < 2 || !strings.Contains(script, "out.inbox === null") ||
		!strings.Contains(script, "out.sent === null") {
		t.Fatal("the script must check again before each mailbox and stop when Mail quit")
	}
	for _, forbidden := range []string{"content", "source", "allHeaders", "activate", "launch", "open(", "mailAttachments"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("the script must not use %q", forbidden)
		}
	}
	if !strings.Contains(script, "7 * 86400000") {
		t.Fatal("the window is seven days")
	}
	// The sent mailbox asks for no state it never uses.
	for _, perReceived := range []string{"received ? msgs.readStatus()", "received ? msgs.flaggedStatus()", "received ? msgs.wasRepliedTo()"} {
		if !strings.Contains(script, perReceived) {
			t.Fatalf("missing %q", perReceived)
		}
	}
	if none := Script(7, 0); strings.Contains(none, "Application(") {
		t.Fatalf("with no process there is nothing to address: %s", none)
	}
}

func TestAutomated(t *testing.T) {
	automated := []struct{ subject, from string }{
		{"Accepted: Budget review", "sam@example.com"},
		{"Declined: Budget review @ Wed Oct 15, 2026", "sam@example.com"},
		{"Tentative: Budget review", "sam@example.com"},
		{"Tentatively Accepted: Budget review", "sam@example.com"},
		{"Invitation: Offsite @ Fri", "sam@example.com"},
		{"Updated invitation with note: Offsite", "sam@example.com"},
		{"Canceled event: Offsite", "sam@example.com"},
		{"Automatic reply: Q3 deck", "sam@example.com"},
		{"Re: Automatic reply: Q3 deck", "sam@example.com"},
		{"Out of Office", "sam@example.com"},
		{"Out of office until Monday", "sam@example.com"},
		{"Auto: Q3 deck", "sam@example.com"},
		{"Read: Q3 deck", "sam@example.com"},
		{"Read receipt", "sam@example.com"},
		{"Undeliverable: Q3 deck", "sam@example.com"},
		{"Delivery Status Notification (Failure)", "sam@example.com"},
		{"已接受: 预算评审", "li@example.com"},
		{"已拒绝：预算评审", "li@example.com"},
		{"邀请：团建", "li@example.com"},
		{"自动回复: 季度报告", "li@example.com"},
		{"自动答复：季度报告", "li@example.com"},
		{"Quick question", "noreply@example.com"},
		{"Quick question", "no-reply@example.com"},
		{"Quick question", "do_not_reply@example.com"},
		{"Quick question", "calendar-notification@google.com"},
		{"Quick question", "notifications@github.com"},
		{"Quick question", "MAILER-DAEMON@example.com"},
		{"Quick question", "postmaster@example.com"},
	}
	for _, c := range automated {
		if !Automated(c.subject, c.from) {
			t.Errorf("Automated(%q, %q) = false", c.subject, c.from)
		}
	}
	personal := []struct{ subject, from string }{
		{"Q3 deck", "sam@example.com"},
		{"Re: Budget review", "sam@example.com"},
		{"Invitation", "sam@example.com"},
		{"Reading list", "sam@example.com"},
		{"Autumn offsite", "sam@example.com"},
		{"Accepting the offer?", "sam@example.com"},
		{"Delivered the draft yet?", "sam@example.com"},
		{"季度报告", "li@example.com"},
		{"Quick question", "replyguy@example.com"},
		{"Quick question", "noel@example.com"},
		{"Quick question", ""},
	}
	for _, c := range personal {
		if Automated(c.subject, c.from) {
			t.Errorf("Automated(%q, %q) = true", c.subject, c.from)
		}
	}
}
