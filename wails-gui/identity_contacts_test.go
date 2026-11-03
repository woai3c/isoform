package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

// newContactsTestStore opens a real principals store in a temp directory.
func newContactsTestStore(t *testing.T) *storage.Store {
	t.Helper()
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// fakeContactSource stands in for the address book or a vCard file.
type fakeContactSource struct {
	kind   string
	list   []systemContact
	err    error
	calls  int
	onRead func()
}

func (f *fakeContactSource) source() string { return f.kind }

func (f *fakeContactSource) contacts(ctx context.Context) ([]systemContact, error) {
	f.calls++
	if f.onRead != nil {
		f.onRead()
	}
	return f.list, f.err
}

func contactsTestImporter(t *testing.T) (contactsImporter, string) {
	t.Helper()
	consentPath := filepath.Join(t.TempDir(), contactsConsentFileName)
	return contactsImporter{store: newContactsTestStore(t), consentPath: consentPath}, consentPath
}

func principalForHandle(t *testing.T, st *storage.Store, app, handle string) *storage.PrincipalRow {
	t.Helper()
	p, err := st.ResolveByAppHandle(context.Background(), app, handle)
	if err != nil {
		t.Fatalf("ResolveByAppHandle(%s, %s): %v", app, handle, err)
	}
	return p
}

func mustCreateManualContact(t *testing.T, st *storage.Store, name string) storage.PrincipalRow {
	t.Helper()
	p, err := st.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind:        storage.PrincipalKindContact,
		DisplayName: name,
		CreatedFrom: "manual",
	})
	if err != nil {
		t.Fatalf("CreatePrincipal(%s): %v", name, err)
	}
	return p
}

func mustBind(t *testing.T, st *storage.Store, principalID, app, handle string) {
	t.Helper()
	if _, err := st.BindAppHandle(context.Background(), principalID, app, handle, handle); err != nil {
		t.Fatalf("BindAppHandle(%s, %s): %v", app, handle, err)
	}
}

func countPrincipals(t *testing.T, st *storage.Store) int {
	t.Helper()
	rows, err := st.ListPrincipals(context.Background())
	if err != nil {
		t.Fatalf("ListPrincipals: %v", err)
	}
	return len(rows)
}

func TestContactsImportRequiresConsent(t *testing.T) {
	im, consentPath := contactsTestImporter(t)
	for _, version := range []int{0, contactsConsentVersion + 1} {
		src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
			{ID: "ABC", GivenName: "Ana", FamilyName: "Lee", Phones: []string{"+14155550100"}},
		}}
		_, err := im.importContacts(context.Background(), src, nil, version)
		if !errors.Is(err, errContactsConsentRequired) {
			t.Fatalf("consent version %d: err = %v, want errContactsConsentRequired", version, err)
		}
		if src.calls != 0 {
			t.Fatalf("consent version %d: source was read %d times", version, src.calls)
		}
	}
	if _, err := os.Stat(consentPath); !os.IsNotExist(err) {
		t.Fatalf("consent file written without consent: %v", err)
	}
	if got := countPrincipals(t, im.store); got != 0 {
		t.Fatalf("principals created without consent: %d", got)
	}
}

func TestContactsImportCreatesContactsAndRecordsConsent(t *testing.T) {
	im, consentPath := contactsTestImporter(t)
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "ABC:ABPerson", GivenName: "Ana", FamilyName: "Lee", Organization: "Acme",
			Phones: []string{"+1 (415) 555-0100"}, Emails: []string{"Ana@Example.com"}},
		{ID: "DEF:ABPerson", GivenName: "Ben", FamilyName: "Ito", Emails: []string{"ben@example.com"}},
		{ID: "GHI:ABPerson", GivenName: "Cy"},
	}}
	selected := map[string]bool{"ABC:ABPerson": true, "DEF:ABPerson": true, "GHI:ABPerson": true}

	res, err := im.importContacts(ctx, src, selected, contactsConsentVersion)
	if err != nil {
		t.Fatalf("importContacts: %v", err)
	}
	if res.Imported != 3 || res.Merged != 0 || res.Skipped != 0 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want 3 imported and nothing else", res)
	}

	ana := principalForHandle(t, im.store, "contacts", "ABC:ABPerson")
	if ana == nil {
		t.Fatal("identifier handle not bound to the imported contact")
	}
	if ana.DisplayName != "Ana Lee" || ana.CreatedFrom != contactsSourceSystem || ana.Source != contactsSourceSystem || ana.ExternalRef != "ABC:ABPerson" || ana.Kind != storage.PrincipalKindContact {
		t.Fatalf("imported principal = %+v", ana)
	}
	if p := principalForHandle(t, im.store, "imessage", "+14155550100"); p == nil || p.ID != ana.ID {
		t.Fatalf("phone handle not bound to Ana: %+v", p)
	}
	if p := principalForHandle(t, im.store, "email", "ana@example.com"); p == nil || p.ID != ana.ID {
		t.Fatalf("email handle not lowercased and bound to Ana: %+v", p)
	}

	consent := loadContactsConsent(consentPath)
	if consent.Version != contactsConsentVersion || consent.Source != contactsSourceSystem || consent.AcceptedAt == "" {
		t.Fatalf("consent = %+v", consent)
	}
	info, err := os.Stat(consentPath)
	if err != nil {
		t.Fatalf("stat consent file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("consent file mode = %o, want 600", perm)
	}
	last, err := im.store.GetUIState(ctx, contactsLastImportKey)
	if err != nil || last == "" {
		t.Fatalf("last import time not recorded: %q, %v", last, err)
	}
}

func TestContactsImportMergesIntoExistingPeople(t *testing.T) {
	im, _ := contactsTestImporter(t)
	st := im.store
	dana := mustCreateManualContact(t, st, "Dana")
	mustBind(t, st, dana.ID, "imessage", "+14155550101")
	eli := mustCreateManualContact(t, st, "Eli")
	mustBind(t, st, eli.ID, "email", "eli@example.com")

	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "DANA-ID", GivenName: "Dana", FamilyName: "Park", Phones: []string{"+1 415 555 0101"}},
		{ID: "ELI-ID", GivenName: "Eli", FamilyName: "Stone", Emails: []string{"ELI@example.com"}},
	}}
	res, err := im.importContacts(context.Background(), src, nil, contactsConsentVersion)
	if err != nil {
		t.Fatalf("importContacts: %v", err)
	}
	if res.Merged != 2 || res.Imported != 0 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want 2 merged", res)
	}
	if got := countPrincipals(t, st); got != 2 {
		t.Fatalf("principals = %d, want 2 (no duplicates)", got)
	}
	danaRow, err := st.GetPrincipal(context.Background(), dana.ID)
	if err != nil {
		t.Fatalf("GetPrincipal: %v", err)
	}
	if danaRow.DisplayName != "Dana" || danaRow.CreatedFrom != "manual" || danaRow.Source != "" || danaRow.ExternalRef != "" {
		t.Fatalf("curated principal was changed by the import: %+v", danaRow)
	}
	if p := principalForHandle(t, st, "contacts", "DANA-ID"); p == nil || p.ID != dana.ID {
		t.Fatalf("identifier not bound to the merged principal: %+v", p)
	}
	if p := principalForHandle(t, st, "contacts", "ELI-ID"); p == nil || p.ID != eli.ID {
		t.Fatalf("identifier not bound to the merged email principal: %+v", p)
	}
}

func TestContactsImportReimportUpdatesInsteadOfDuplicating(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	first := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "ABC", GivenName: "Ana", FamilyName: "Lee", Phones: []string{"+14155550100"}},
	}}
	if _, err := im.importContacts(ctx, first, nil, contactsConsentVersion); err != nil {
		t.Fatalf("first import: %v", err)
	}

	second := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "ABC", GivenName: "Ana", FamilyName: "Lee-Ross",
			Phones: []string{"+14155550100"}, Emails: []string{"ana.lee@example.com"}},
	}}
	res, err := im.importContacts(ctx, second, nil, contactsConsentVersion)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res.Merged != 1 || res.Imported != 0 {
		t.Fatalf("re-import result = %+v, want 1 merged", res)
	}
	if got := countPrincipals(t, im.store); got != 1 {
		t.Fatalf("principals = %d after re-import, want 1", got)
	}
	ana := principalForHandle(t, im.store, "contacts", "ABC")
	if ana == nil || ana.DisplayName != "Ana Lee-Ross" {
		t.Fatalf("re-import did not refresh the name: %+v", ana)
	}
	if p := principalForHandle(t, im.store, "email", "ana.lee@example.com"); p == nil || p.ID != ana.ID {
		t.Fatalf("new email not bound on re-import: %+v", p)
	}
}

func TestContactsImportFoldsChatBucketIntoContact(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	bucket, err := im.store.EnsureAnonymousContact(ctx, "imessage", "+14155550102", "")
	if err != nil {
		t.Fatalf("EnsureAnonymousContact: %v", err)
	}

	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "FAY", GivenName: "Fay", FamilyName: "Wu", Phones: []string{"+1 415 555 0102"}},
	}}
	res, err := im.importContacts(ctx, src, nil, contactsConsentVersion)
	if err != nil {
		t.Fatalf("importContacts: %v", err)
	}
	if res.Imported != 1 || res.Merged != 0 {
		t.Fatalf("result = %+v, want 1 imported", res)
	}
	if _, err := im.store.GetPrincipal(ctx, bucket.ID); err == nil {
		t.Fatal("anonymous bucket survived the fold into the imported contact")
	}
	fay := principalForHandle(t, im.store, "imessage", "+14155550102")
	if fay == nil || fay.DisplayName != "Fay Wu" || fay.CreatedFrom != contactsSourceSystem {
		t.Fatalf("chat handle not owned by the imported contact: %+v", fay)
	}
	if got := countPrincipals(t, im.store); got != 1 {
		t.Fatalf("principals = %d, want 1", got)
	}
}

func TestContactsImportSkipsOwnSelfCard(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	self, err := im.store.EnsureSelf(ctx, "Me", nil)
	if err != nil {
		t.Fatalf("EnsureSelf: %v", err)
	}
	mustBind(t, im.store, self.ID, "imessage", "+14155550103")

	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "ME", GivenName: "Me", FamilyName: "Myself", Phones: []string{"+1-415-555-0103"}},
	}}
	res, err := im.importContacts(ctx, src, nil, contactsConsentVersion)
	if err != nil {
		t.Fatalf("importContacts: %v", err)
	}
	if res.Skipped != 1 || res.Imported != 0 || res.Merged != 0 {
		t.Fatalf("result = %+v, want the self card skipped", res)
	}
	row, err := im.store.GetPrincipal(ctx, self.ID)
	if err != nil || row.Kind != storage.PrincipalKindSelf {
		t.Fatalf("self principal changed: %+v, %v", row, err)
	}
	if p := principalForHandle(t, im.store, "contacts", "ME"); p != nil {
		t.Fatalf("self principal was bound to the contact identifier: %+v", p)
	}
}

func TestContactsImportLeavesHandlesOwnedByOthers(t *testing.T) {
	im, _ := contactsTestImporter(t)
	st := im.store
	ctx := context.Background()
	gus := mustCreateManualContact(t, st, "Gus")
	mustBind(t, st, gus.ID, "email", "gus@example.com")
	hal := mustCreateManualContact(t, st, "Hal")
	mustBind(t, st, hal.ID, "imessage", "+14155550104")

	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "HAL", GivenName: "Hal", FamilyName: "Ng",
			Phones: []string{"+14155550104"}, Emails: []string{"gus@example.com"}},
	}}
	res, err := im.importContacts(ctx, src, nil, contactsConsentVersion)
	if err != nil {
		t.Fatalf("importContacts: %v", err)
	}
	if res.Merged != 1 {
		t.Fatalf("result = %+v, want 1 merged", res)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "email address") {
		t.Fatalf("errors = %v, want one email conflict", res.Errors)
	}
	if p := principalForHandle(t, st, "email", "gus@example.com"); p == nil || p.ID != gus.ID {
		t.Fatalf("email moved away from its owner: %+v", p)
	}
	if p := principalForHandle(t, st, "contacts", "HAL"); p == nil || p.ID != hal.ID {
		t.Fatalf("identifier not bound to the matched principal: %+v", p)
	}
}

func TestContactsImportOnlySelectedContacts(t *testing.T) {
	im, _ := contactsTestImporter(t)
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "A", FullName: "Ada", Phones: []string{"+14155550111"}},
		{ID: "B", FullName: "Bo", Phones: []string{"+14155550112"}},
		{ID: "C", FullName: "Cal", Phones: []string{"+14155550113"}},
	}}
	res, err := im.importContacts(context.Background(), src, map[string]bool{"B": true}, contactsConsentVersion)
	if err != nil {
		t.Fatalf("importContacts: %v", err)
	}
	if res.Imported != 1 || res.Skipped != 0 {
		t.Fatalf("result = %+v, want exactly the selected contact", res)
	}
	if got := countPrincipals(t, im.store); got != 1 {
		t.Fatalf("principals = %d, want 1", got)
	}
	if principalForHandle(t, im.store, "contacts", "A") != nil || principalForHandle(t, im.store, "contacts", "C") != nil {
		t.Fatal("unselected contacts were imported")
	}
}

func TestContactsImportCountsDuplicateIdentifiersAsSkipped(t *testing.T) {
	im, _ := contactsTestImporter(t)
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "X", FullName: "Xia", Phones: []string{"+14155550121"}},
		{ID: "X", FullName: "Xia", Phones: []string{"+14155550121"}},
	}}
	res, err := im.importContacts(context.Background(), src, nil, contactsConsentVersion)
	if err != nil {
		t.Fatalf("importContacts: %v", err)
	}
	if res.Imported != 1 || res.Skipped != 1 {
		t.Fatalf("result = %+v, want 1 imported and 1 skipped", res)
	}
}

func TestContactsRemoveImportedKeepsExistingPeople(t *testing.T) {
	im, _ := contactsTestImporter(t)
	st := im.store
	ctx := context.Background()

	system := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "ANA", FullName: "Ana Lee", Phones: []string{"+14155550131"}},
		{ID: "BEN", FullName: "Ben Ito", Emails: []string{"ben@example.com"}},
	}}
	if _, err := im.importContacts(ctx, system, nil, contactsConsentVersion); err != nil {
		t.Fatalf("system import: %v", err)
	}
	vcard := &fakeContactSource{kind: contactsSourceVCard, list: []systemContact{
		{ID: "vcard-1", FullName: "Cy Vance", Emails: []string{"cy@example.com"}},
	}}
	if _, err := im.importContacts(ctx, vcard, nil, contactsConsentVersion); err != nil {
		t.Fatalf("vcard import: %v", err)
	}
	dana := mustCreateManualContact(t, st, "Dana")
	mustBind(t, st, dana.ID, "imessage", "+14155550132")
	merge := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "DANA", FullName: "Dana Park", Phones: []string{"+14155550132"}},
	}}
	if res, err := im.importContacts(ctx, merge, nil, contactsConsentVersion); err != nil || res.Merged != 1 {
		t.Fatalf("merge into existing: %+v, %v", res, err)
	}

	res, err := removeImportedContacts(ctx, st)
	if err != nil {
		t.Fatalf("removeImportedContacts: %v", err)
	}
	if res.Removed != 2 || len(res.Errors) != 0 {
		t.Fatalf("remove result = %+v, want 2 removed", res)
	}
	if got := countPrincipals(t, st); got != 2 {
		t.Fatalf("principals = %d after removal, want Dana and the vCard contact", got)
	}
	danaRow, err := st.GetPrincipal(ctx, dana.ID)
	if err != nil || danaRow.DisplayName != "Dana" {
		t.Fatalf("curated principal removed: %+v, %v", danaRow, err)
	}
	if p := principalForHandle(t, st, "contacts", "ANA"); p != nil {
		t.Fatalf("imported contact still resolves after removal: %+v", p)
	}
	if p := principalForHandle(t, st, "vcard", "vcard-1"); p == nil {
		t.Fatal("vCard contact was removed by system contact removal")
	}
}

func TestContactsAccessStatusReportsConsentAndImports(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st := newContactsTestStore(t)
	app := &App{store: st}
	ctx := context.Background()

	before, err := app.contactsAccessStatus(ctx)
	if err != nil {
		t.Fatalf("status before import: %v", err)
	}
	if before.ConsentAccepted || before.ImportedCount != 0 || before.LastImportAt != "" {
		t.Fatalf("status before import = %+v", before)
	}

	im := contactsImporter{store: st, consentPath: filepath.Join(home, configDirName, contactsConsentFileName)}
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "P1", FullName: "Pat One", Phones: []string{"+14155550141"}},
		{ID: "P2", FullName: "Pat Two", Phones: []string{"+14155550142"}},
	}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatalf("import: %v", err)
	}

	after, err := app.contactsAccessStatus(ctx)
	if err != nil {
		t.Fatalf("status after import: %v", err)
	}
	if !after.ConsentAccepted || after.ConsentAcceptedAt == "" {
		t.Fatalf("consent not reported: %+v", after)
	}
	if after.ImportedCount != 2 || after.LastImportAt == "" {
		t.Fatalf("counts not reported: %+v", after)
	}
	if after.Platform != goruntime.GOOS || !after.VCardSupported {
		t.Fatalf("platform fields = %+v", after)
	}
	if after.Supported != systemContactsSupported() {
		t.Fatalf("Supported = %v, want %v", after.Supported, systemContactsSupported())
	}
}

func TestContactsVCardImportThroughSource(t *testing.T) {
	im, consentPath := contactsTestImporter(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "team.vcf")
	writeTestFile(t, path, "BEGIN:VCARD\nVERSION:3.0\nUID:ana-uid\nFN:Ana Lee\nTEL:+14155550151\nEND:VCARD\n"+
		"BEGIN:VCARD\nVERSION:3.0\nFN:Ben Ito\nEMAIL:ben@example.com\nEND:VCARD\n")

	res, err := im.importContacts(ctx, vcardFileSource{path: path}, nil, contactsConsentVersion)
	if err != nil {
		t.Fatalf("vcard import: %v", err)
	}
	if res.Imported != 2 {
		t.Fatalf("result = %+v, want 2 imported", res)
	}
	ana := principalForHandle(t, im.store, "vcard", "ana-uid")
	if ana == nil || ana.CreatedFrom != contactsSourceVCard || ana.DisplayName != "Ana Lee" {
		t.Fatalf("vCard principal = %+v", ana)
	}
	if consent := loadContactsConsent(consentPath); consent.Source != contactsSourceVCard {
		t.Fatalf("consent source = %q, want vcard", consent.Source)
	}

	again, err := im.importContacts(ctx, vcardFileSource{path: path}, nil, contactsConsentVersion)
	if err != nil {
		t.Fatalf("second vcard import: %v", err)
	}
	if again.Merged != 2 || again.Imported != 0 {
		t.Fatalf("re-import = %+v, want 2 merged", again)
	}
	if got := countPrincipals(t, im.store); got != 2 {
		t.Fatalf("principals = %d after re-import, want 2", got)
	}
}

func TestContactsVCardRejectsOtherFileTypes(t *testing.T) {
	im, consentPath := contactsTestImporter(t)
	path := filepath.Join(t.TempDir(), "contacts.txt")
	writeTestFile(t, path, "BEGIN:VCARD\nFN:Ana\nEND:VCARD\n")

	_, err := im.importContacts(context.Background(), vcardFileSource{path: path}, nil, contactsConsentVersion)
	if !errors.Is(err, errVCardExtension) {
		t.Fatalf("err = %v, want errVCardExtension", err)
	}
	if !hasCurrentContactsConsent(consentPath) {
		t.Fatal("explicit consent should be retained when the subsequent import fails")
	}
}

func TestContactsVCardCheckPath(t *testing.T) {
	cases := map[string]bool{
		"a.vcf": true, "A.VCF": true, "a.vcard": true,
		"a.txt": false, "a": false, "a.vcf.zip": false,
	}
	for path, want := range cases {
		err := checkVCardPath(path)
		if got := err == nil; got != want {
			t.Errorf("checkVCardPath(%q) ok = %v, want %v", path, got, want)
		}
	}
}

func TestContactsVCardParse(t *testing.T) {
	data := strings.Join([]string{
		"BEGIN:VCARD",
		"VERSION:3.0",
		"N:Lee;Ana;;;",
		"FN:Ana Lee",
		`ORG:Acme\, Inc.;Sales`,
		"TEL;TYPE=CELL:+1 415",
		" 555 0100",
		"TEL;TYPE=WORK:tel:+1-415-555-0199",
		"EMAIL;TYPE=INTERNET:Ana@Example.com",
		"EMAIL:ana.home@example.com",
		"UID:urn:uuid:1234",
		`NOTE:Met at the offsite\; follow up`,
		"PHOTO;ENCODING=b;TYPE=JPEG:/9j/4AAQSkZJRgABAQ",
		" AAAA",
		"END:VCARD",
		"BEGIN:VCARD",
		"VERSION:2.1",
		"N;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:Caf=C3=A9;Ren=C3=A9",
		"TEL;CELL:+33 6 39 98 01 00",
		"EMAIL;INTERNET:rene@example.com",
		"END:VCARD",
		"begin:vcard",
		"FN:Solo",
		"end:vcard",
		"",
	}, "\r\n")

	cards := parseVCards([]byte(data))
	if len(cards) != 3 {
		t.Fatalf("parsed %d cards, want 3", len(cards))
	}

	ana := cards[0]
	if ana.ID != "urn:uuid:1234" || ana.GivenName != "Ana" || ana.FamilyName != "Lee" || ana.FullName != "Ana Lee" {
		t.Errorf("card 1 names = %+v", ana)
	}
	if ana.Organization != "Acme, Inc." {
		t.Errorf("card 1 organization = %q", ana.Organization)
	}
	if want := []string{"+1 415555 0100", "+1-415-555-0199"}; !sameContactStrings(ana.Phones, want) {
		t.Errorf("card 1 phones = %q, want %q", ana.Phones, want)
	}
	if want := []string{"Ana@Example.com", "ana.home@example.com"}; !sameContactStrings(ana.Emails, want) {
		t.Errorf("card 1 emails = %q, want %q", ana.Emails, want)
	}

	rene := cards[1]
	if !strings.HasPrefix(rene.ID, "vcard-") || len(rene.ID) != len("vcard-")+16 {
		t.Errorf("card 2 id = %q, want a content hash", rene.ID)
	}
	if rene.FamilyName != "Café" || rene.GivenName != "René" {
		t.Errorf("card 2 quoted-printable names = %q %q", rene.GivenName, rene.FamilyName)
	}
	if got := contactDisplayName(rene); got != "René Café" {
		t.Errorf("card 2 display name = %q", got)
	}
	if want := []string{"+33 6 39 98 01 00"}; !sameContactStrings(rene.Phones, want) {
		t.Errorf("card 2 phones = %q", rene.Phones)
	}

	if cards[2].FullName != "Solo" {
		t.Errorf("lowercase card = %+v", cards[2])
	}
}

func TestContactsNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"+1 (415) 555-0100":  "+14155550100",
		"0044 20 7946 0100":  "+442079460100",
		"+86 000-0013-8000":  "+8600000138000",
		"(415) 555-0100":     "4155550100",
		"5550":               "",
		"":                   "",
		"  +1 415.555.0100 ": "+14155550100",
	}
	for raw, want := range cases {
		if got := normalizeContactPhone(raw); got != want {
			t.Errorf("normalizeContactPhone(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestContactsNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		" Ana@Example.COM ":     "ana@example.com",
		"not an email":          "",
		"@example.com":          "",
		"ana@":                  "",
		"ana smith@example.com": "",
	}
	for raw, want := range cases {
		if got := normalizeContactEmail(raw); got != want {
			t.Errorf("normalizeContactEmail(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestContactsDisplayNameFallbacks(t *testing.T) {
	cases := []struct {
		name string
		in   systemContact
		want string
	}{
		{"full name wins", systemContact{FullName: "Ana L.", GivenName: "Ana", FamilyName: "Lee"}, "Ana L."},
		{"given and family", systemContact{GivenName: "Ana", FamilyName: "Lee", Organization: "Acme"}, "Ana Lee"},
		{"organization", systemContact{Organization: "Acme"}, "Acme"},
		{"first non-empty phone", systemContact{Phones: []string{" ", "+14155550100"}}, "+14155550100"},
		{"email", systemContact{Emails: []string{"ana@example.com"}}, "ana@example.com"},
		{"nothing", systemContact{}, ""},
	}
	for _, tc := range cases {
		if got := contactDisplayName(tc.in); got != tc.want {
			t.Errorf("%s: contactDisplayName = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sameContactStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestContactsPickerRequiresConsentBeforeReading(t *testing.T) {
	im, path := contactsTestImporter(t)
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "a", FullName: "Ada"}}}
	for _, raw := range []string{"", `{"version":1,"acceptedAt":"2026-10-17T00:00:00Z"}`, `{"version":2,"acceptedAt":""}`} {
		if raw != "" {
			writeTestFile(t, path, raw)
		}
		if _, err := im.listContacts(context.Background(), src); !errors.Is(err, errContactsConsentRequired) {
			t.Fatalf("list with invalid consent %q: %v", raw, err)
		}
	}
	if src.calls != 0 || countPrincipals(t, im.store) != 0 {
		t.Fatal("picker read or persisted contacts before consent")
	}
}

func TestContactsPickerMapsDTOsWithoutPersisting(t *testing.T) {
	im, path := contactsTestImporter(t)
	ctx := context.Background()
	person := mustCreateManualContact(t, im.store, "Existing")
	mustBind(t, im.store, person.ID, "contacts", "z")
	if err := recordContactsConsent(path, "people", time.Now()); err != nil {
		t.Fatal(err)
	}
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "z", GivenName: " Zoe ", FamilyName: " Lee ", Phones: []string{" +1 415 555 0100 ", ""}},
		{ID: " a ", FullName: " Ada ", Emails: []string{" ada@example.com ", " "}},
		{ID: "a", FullName: "Duplicate identifier"},
		{ID: "", FullName: "No identifier"},
		{ID: "empty"},
	}}
	got, err := im.listContacts(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	want := []SystemContactView{
		{ID: "a", DisplayName: "Ada", Phones: []string{}, Emails: []string{"ada@example.com"}},
		{ID: "z", DisplayName: "Zoe Lee", Phones: []string{"+1 415 555 0100"}, Emails: []string{}, Imported: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("picker DTOs = %#v, want %#v", got, want)
	}
	if countPrincipals(t, im.store) != 1 || principalForHandle(t, im.store, "contacts", "a") != nil {
		t.Fatal("listing persisted an unselected person")
	}
	if last, err := im.store.GetUIState(ctx, contactsLastImportKey); err != nil || last != "" {
		t.Fatalf("listing changed import metadata: %q, %v", last, err)
	}
}

func TestContactsImportConsentIsDurableBeforeSourceRead(t *testing.T) {
	im, path := contactsTestImporter(t)
	src := &fakeContactSource{kind: contactsSourceSystem, onRead: func() {
		if !hasCurrentContactsConsent(path) {
			t.Fatal("source read before consent was stored")
		}
	}}
	if _, err := im.importContacts(context.Background(), src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	first := loadContactsConsent(path).AcceptedAt
	if err := recordContactsConsent(path, "people", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if loadContactsConsent(path).AcceptedAt != first {
		t.Fatal("later consent overwrote the first acceptance time")
	}
	im.consentPath = filepath.Join(path, "cannot-write-under-file")
	if _, err := im.importContacts(context.Background(), src, nil, contactsConsentVersion); err == nil {
		t.Fatal("import succeeded when consent could not be stored")
	}
	if src.calls != 1 {
		t.Fatal("source read after consent persistence failed")
	}
}

func TestContactsEmptySelectionDoesNotReadOrPersist(t *testing.T) {
	im, path := contactsTestImporter(t)
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "a", FullName: "Ada"}}}
	res, err := im.importContacts(context.Background(), src, map[string]bool{}, contactsConsentVersion)
	if err != nil || res.Imported != 0 || src.calls != 0 || countPrincipals(t, im.store) != 0 {
		t.Fatalf("empty selection had side effects: %+v, %v, calls=%d", res, err, src.calls)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty selection wrote consent: %v", err)
	}
}

func TestContactsConsentBindingsGateNativeAccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(agentHomeEnv, "")
	app := &App{store: newContactsTestStore(t)}
	if _, err := app.RequestContactsAccess(); !errors.Is(err, errContactsConsentRequired) {
		t.Fatalf("request without consent: %v", err)
	}
	if _, err := app.ListSystemContacts(); !errors.Is(err, errContactsConsentRequired) {
		t.Fatalf("list without consent: %v", err)
	}
	if _, err := app.AcceptContactsConsent(contactsConsentVersion - 1); !errors.Is(err, errContactsConsentRequired) {
		t.Fatalf("accepted obsolete consent: %v", err)
	}
	status, err := app.AcceptContactsConsent(contactsConsentVersion)
	if err != nil || !status.ConsentAccepted || status.ConsentAcceptedAt == "" {
		t.Fatalf("accept current consent: %+v, %v", status, err)
	}
	if countPrincipals(t, app.store) != 0 {
		t.Fatal("consent action imported contacts")
	}
}

func TestContactsDuplicateHandlesSharePrincipalWithoutResearch(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "a", FullName: "Ada", Emails: []string{"Ada@example.com"}},
		{ID: "b", FullName: "Ada", Emails: []string{" ada@example.com "}},
	}}
	res, err := im.importContacts(ctx, src, nil, contactsConsentVersion)
	if err != nil || res.Imported != 1 || res.Merged != 1 || len(res.Errors) != 0 {
		t.Fatalf("duplicate import: %+v, %v", res, err)
	}
	a := principalForHandle(t, im.store, "contacts", "a")
	b := principalForHandle(t, im.store, "contacts", "b")
	if a == nil || b == nil || a.ID != b.ID || a.ExternalRef != "a" || a.Source != contactsSourceSystem {
		t.Fatalf("duplicate provenance: %+v, %+v", a, b)
	}
	research, err := im.store.GetPrincipalResearch(ctx, a.ID)
	if err != nil || research.Status != storage.PrincipalResearchStatusIdle || research.CreatedAt != 0 {
		t.Fatalf("import started person research: %+v, %v", research, err)
	}
}

func TestContactsRemovalUsesExplicitSource(t *testing.T) {
	st := newContactsTestStore(t)
	ctx := context.Background()
	legacy, err := st.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Keep", CreatedFrom: contactsSourceSystem})
	if err != nil {
		t.Fatal(err)
	}
	imported, err := st.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Remove", Source: contactsSourceSystem, ExternalRef: "native-id"})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := countImportedPrincipals(ctx, st); err != nil || count != 1 {
		t.Fatalf("removable count=%d, %v", count, err)
	}
	res, err := removeImportedContacts(ctx, st)
	if err != nil || res.Removed != 1 {
		t.Fatalf("removal: %+v, %v", res, err)
	}
	if _, err := st.GetPrincipal(ctx, legacy.ID); err != nil {
		t.Fatal("removed a principal without system source")
	}
	if _, err := st.GetPrincipal(ctx, imported.ID); err == nil {
		t.Fatal("kept a system-source principal")
	}
}

func TestContactsImportMatchesDisplayOnlyHandlesWithoutMatchingNames(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	curated := mustCreateManualContact(t, im.store, "Ada")
	if _, err := im.store.BindAppHandle(ctx, curated.ID, "email", "", "Ada@Example.com"); err != nil {
		t.Fatal(err)
	}
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "a", FullName: "Ada Renamed", Emails: []string{"ada@example.com"}},
		{ID: "b", FullName: "Ada"},
	}}
	res, err := im.importContacts(ctx, src, nil, contactsConsentVersion)
	if err != nil || res.Imported != 1 || res.Merged != 1 {
		t.Fatalf("display-handle dedupe: %+v, %v", res, err)
	}
	if a := principalForHandle(t, im.store, "contacts", "a"); a == nil || a.ID != curated.ID {
		t.Fatalf("did not match display-only email: %+v", a)
	}
	if b := principalForHandle(t, im.store, "contacts", "b"); b == nil || b.ID == curated.ID {
		t.Fatalf("merged an unrelated same-name person: %+v", b)
	}
}
