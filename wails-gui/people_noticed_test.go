package main

import "testing"

func TestPersonEntityNamesKeepsOnlyPeople(t *testing.T) {
	raw := `[{"type":"person","name":"Ana Rivera"},{"type":"project","name":"Harbor"},{"type":"person","name":"  "},{"type":"person","name":"Ben"}]`
	got := personEntityNames(raw)
	if len(got) != 2 || got[0] != "ana rivera" || got[1] != "ben" {
		t.Fatalf("person names = %v", got)
	}
	if personEntityNames("not json") != nil {
		t.Fatal("bad entities must yield no names")
	}
}

func TestNoticedNamesPersonUsesFirstNameOnlyWhenUnique(t *testing.T) {
	displayNames := map[string]string{"p1": "Dana Park", "p2": "Dana Lin", "p3": "Omar Haddad"}
	counts := peopleFirstNames(displayNames)
	names := map[string]bool{threadNorm("Dana Park"): true}
	if !noticedNamesPerson("dana park", names, "Dana Park", counts) {
		t.Fatal("full name must match its person")
	}
	if noticedNamesPerson("dana", names, "Dana Park", counts) {
		t.Fatal("a shared first name must not match either Dana")
	}
	if !noticedNamesPerson("omar", map[string]bool{}, "Omar Haddad", counts) {
		t.Fatal("a unique first name must match its person")
	}
	if noticedNamesPerson("omar", map[string]bool{}, "Dana Park", counts) {
		t.Fatal("a first name must match only the person who has it")
	}
}
