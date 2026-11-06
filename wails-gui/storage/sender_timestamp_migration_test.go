package storage

import (
	"path/filepath"
	"testing"
)

func TestSenderTimestampMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []PrincipalRow{
		{ID: "sender", Kind: PrincipalKindAnonymous, DisplayName: "v1t0 11/1/26, 6:43 PM"},
		{ID: "plain", Kind: PrincipalKindAnonymous, DisplayName: "v1t0"},
		{ID: "nbsp", Kind: PrincipalKindAnonymous, DisplayName: "sender Yesterday at 9:11\u202fPM"},
		{ID: "curated", Kind: PrincipalKindContact, DisplayName: "Team 2026-11-01"},
	} {
		if _, err := s.CreatePrincipal(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`PRAGMA user_version=73`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err = New(path)
		if err != nil {
			t.Fatal(err)
		}
		for id, want := range map[string]string{"sender": "v1t0", "plain": "v1t0", "nbsp": "sender", "curated": "Team"} {
			row, err := s.GetPrincipal(t.Context(), id)
			if err != nil || row.DisplayName != want {
				t.Fatalf("%s: %+v %v", id, row, err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
