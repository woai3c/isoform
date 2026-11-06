package main

import (
	"testing"
	"wails-gui/storage"
)

func TestPeopleSelfSenderGuessesRequireConfirmationAndHonorRejection(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	self, err := a.store.EnsureSelf(t.Context(), "Bo", []string{"alexm"})
	if err != nil {
		t.Fatal(err)
	}
	sender, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindAnonymous, DisplayName: "Bo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.BindAppHandle(t.Context(), sender.ID, "slack", "sender-id", "Bo"); err != nil {
		t.Fatal(err)
	}
	discord, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindAnonymous, DisplayName: "alexm"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.BindAppHandle(t.Context(), discord.ID, "discord", "discord-id", "alexm"); err != nil {
		t.Fatal(err)
	}
	rows, err := a.listPrincipalsWithStats(newPeopleReadGateCache())
	if err != nil {
		t.Fatal(err)
	}
	got := a.peopleSelfSenderGuesses(t.Context(), rows, newPeopleReadGateCache())
	if len(got) != 2 || got[0].SelfID != self.ID || got[0].PersonID != discord.ID || got[0].App != "discord" || got[1].PersonID != sender.ID || got[1].App != "slack" {
		t.Fatalf("guesses = %+v", got)
	}
	row, err := a.store.GetPrincipal(t.Context(), sender.ID)
	if err != nil || row.Kind != storage.PrincipalKindAnonymous {
		t.Fatal("a guess must not merge or bind the sender")
	}
	if err := a.NotSamePeople([]string{self.ID, sender.ID}); err != nil {
		t.Fatal(err)
	}
	if got := a.peopleSelfSenderGuesses(t.Context(), rows, newPeopleReadGateCache()); len(got) != 1 || got[0].PersonID != discord.ID {
		t.Fatalf("rejected guess returned: %+v", got)
	}
}
