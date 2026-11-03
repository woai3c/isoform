package main

import (
	"testing"

	"wails-gui/storage"
)

func TestPrincipalDTOIncludesImportProvenance(t *testing.T) {
	dto := principalToDTO(storage.PrincipalRow{
		ID: "imported", Kind: storage.PrincipalKindContact, DisplayName: "Imported contact",
		CreatedFrom: "system_contacts", Source: "system_contacts", ExternalRef: "address-book-1",
	})
	if dto.Source != "system_contacts" || dto.ExternalRef != "address-book-1" || dto.CreatedFrom != "system_contacts" {
		t.Fatalf("DTO lost import provenance: %+v", dto)
	}
}

func TestAppUpdatePrincipalPreservesStoredImportProvenance(t *testing.T) {
	a := newBindingsTestApp(t)
	imported, err := createContactRecordWithProvenance(bindingsCtx, a.store, "system_contacts", "system_contacts", "address-book-1", "Imported contact", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if imported.Source != "system_contacts" || imported.ExternalRef != "address-book-1" {
		t.Fatalf("create lost import provenance: %+v", imported)
	}
	// UI edits may carry old or altered DTO fields; provenance comes from storage.
	imported.DisplayName = "Edited contact"
	imported.Source = "vcard"
	imported.ExternalRef = "replacement-ref"
	imported.CreatedFrom = "manual"
	updated, err := a.UpdatePrincipal(imported)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Edited contact" || updated.CreatedFrom != "system_contacts" || updated.Source != "system_contacts" || updated.ExternalRef != "address-book-1" {
		t.Fatalf("update changed provenance or lost edit: %+v", updated)
	}
}
