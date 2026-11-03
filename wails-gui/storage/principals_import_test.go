package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestPrincipalImportProvenanceRoundTrip(t *testing.T) {
	s := newTestStore(t)
	created := mustCreate(t, s, PrincipalRow{
		Kind: PrincipalKindContact, DisplayName: "Imported contact",
		CreatedFrom: "system_contacts", Source: "system_contacts", ExternalRef: "address-book-1",
	})
	if _, err := s.BindAppHandle(ctx, created.ID, "contacts", created.ExternalRef, created.ExternalRef); err != nil {
		t.Fatal(err)
	}
	created.DisplayName = "Updated contact"
	updated, err := s.UpdatePrincipal(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	assertImportProvenance(t, updated, "system_contacts", "address-book-1")

	got, err := s.GetPrincipal(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertImportProvenance(t, got, "system_contacts", "address-book-1")
	for _, kind := range []string{"", PrincipalKindContact} {
		rows, err := s.listPrincipals(ctx, kind)
		if err != nil || len(rows) != 1 {
			t.Fatalf("listPrincipals(%q) = %+v, %v", kind, rows, err)
		}
		assertImportProvenance(t, rows[0], "system_contacts", "address-book-1")
	}
	resolved, err := s.ResolveByAppHandle(ctx, "contacts", "address-book-1")
	if err != nil || resolved == nil {
		t.Fatalf("ResolveByAppHandle = %+v, %v", resolved, err)
	}
	assertImportProvenance(t, *resolved, "system_contacts", "address-book-1")
}

func TestMergePrincipalsPreservesDestinationImportProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, source, destinationSource string
	}{
		{"imported_into_curated", "system_contacts", ""},
		{"curated_into_imported", "", "system_contacts"},
		{"system_into_vcard", "system_contacts", "vcard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			src := mustCreate(t, s, PrincipalRow{
				Kind: PrincipalKindContact, DisplayName: "Source",
				CreatedFrom: "manual", Source: tc.source, ExternalRef: "source-ref",
			})
			externalRef := ""
			if tc.destinationSource != "" {
				externalRef = "destination-ref"
			}
			dst := mustCreate(t, s, PrincipalRow{
				Kind: PrincipalKindContact, DisplayName: "Destination",
				CreatedFrom: "manual", Source: tc.destinationSource, ExternalRef: externalRef,
			})
			if _, err := s.BindAppHandle(ctx, src.ID, "contacts", "source-ref", "source-ref"); err != nil {
				t.Fatal(err)
			}
			merged, err := s.MergePrincipals(ctx, src.ID, dst.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertImportProvenance(t, merged, tc.destinationSource, externalRef)
			if merged.CreatedFrom != "manual" || merged.DisplayName != "Destination" {
				t.Fatalf("merge replaced curated destination fields: %+v", merged)
			}
			owner, err := s.ResolveByAppHandle(ctx, "contacts", "source-ref")
			if err != nil || owner == nil || owner.ID != dst.ID {
				t.Fatalf("source handle did not transfer: %+v, %v", owner, err)
			}
			assertImportProvenance(t, *owner, tc.destinationSource, externalRef)
		})
	}
}

func TestMigrateV61PrincipalImportProvenance(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "contacts.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id, createdFrom, app, handle, wantSource, wantRef string
	}{
		{"system", "system_contacts", "contacts", "system-ref", "system_contacts", "system-ref"},
		{"vcard", "vcard", "vcard", "vcard-ref", "vcard", "vcard-ref"},
		{"missing_handle", "system_contacts", "", "", "system_contacts", ""},
		{"curated", "manual", "contacts", "curated-ref", "", ""},
		{"anonymous", "auto_from_chat", "contacts", "anonymous-ref", "", ""},
	}
	for _, tc := range cases {
		p := mustCreate(t, s, PrincipalRow{ID: tc.id, Kind: PrincipalKindContact, DisplayName: tc.id, CreatedFrom: tc.createdFrom})
		if tc.app != "" {
			if _, err := s.BindAppHandle(ctx, p.ID, tc.app, tc.handle, tc.handle); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Reconstruct the pre-v61 table rather than only lowering the version stamp.
	if _, err := s.db.Exec(`
		ALTER TABLE principals DROP COLUMN source;
		ALTER TABLE principals DROP COLUMN external_ref;
		PRAGMA user_version = 60;
	`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	for _, tc := range cases {
		p, err := migrated.GetPrincipal(ctx, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		assertImportProvenance(t, p, tc.wantSource, tc.wantRef)
		if p.CreatedFrom != tc.createdFrom {
			t.Fatalf("migration rewrote creation history for %s", tc.id)
		}
	}
	if _, err := migrated.db.Exec("UPDATE principals SET external_ref = 'canonical-ref' WHERE id = 'system'"); err != nil {
		t.Fatal(err)
	}
	if err := migrated.migratePrincipalImportProvenance(); err != nil {
		t.Fatalf("replay migration: %v", err)
	}
	p, err := migrated.GetPrincipal(ctx, "system")
	if err != nil {
		t.Fatal(err)
	}
	assertImportProvenance(t, p, "system_contacts", "canonical-ref")
	var version int
	if err := migrated.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version = %d, error %v", version, err)
	}
	var invalidForeignKey any
	if err := migrated.db.QueryRow("PRAGMA foreign_key_check").Scan(&invalidForeignKey); err != sql.ErrNoRows {
		t.Fatalf("foreign keys after migration: %v", err)
	}
}

func assertImportProvenance(t *testing.T, p PrincipalRow, source, externalRef string) {
	t.Helper()
	if p.Source != source || p.ExternalRef != externalRef {
		t.Fatalf("principal %s provenance = (%q, %q), want (%q, %q)", p.ID, p.Source, p.ExternalRef, source, externalRef)
	}
}
