package handoff

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeStoreFixture(t *testing.T, s *Store, name, body string) string {
	t.Helper()
	path := filepath.Join(s.Dir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestJSONStorePersistsVersionAndStructuredResult(t *testing.T) {
	s := newTestStore(t)
	e := Entry{
		ID: "result", Status: StatusNeedsReview, AttemptID: "attempt-1", LastActivityAt: 123,
		Body: "# Success criteria\n- Report is saved\n",
		Result: &Result{
			Status:   StatusCompleted,
			Criteria: []ResultCriterion{{Text: "Report is saved", Met: true, Evidence: []int{0, 1, 2}}},
			Evidence: []ResultEvidence{{Path: "report.md"}, {URL: "https://example.com/report"}, {ArtifactID: "artifact-1"}},
		},
	}
	saved, err := s.Save(e)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), "result.json"))
	if err != nil || !json.Valid(raw) {
		t.Fatalf("JSON file: %s, %v", raw, err)
	}
	var disk Entry
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.SchemaVersion != SchemaVersion || !reflect.DeepEqual(disk.Result, e.Result) ||
		disk.AttemptID != e.AttemptID || disk.LastActivityAt != e.LastActivityAt {
		t.Fatalf("persisted entry = %+v", disk)
	}
	got, err := s.Get(e.ID)
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(got.Criteria, []string{"Report is saved"}) {
		t.Fatalf("brief criteria = %#v", got.Criteria)
	}
	info, err := os.Stat(filepath.Join(s.Dir(), "result.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("JSON permissions = %v, %v", info, err)
	}
}

func TestJSONReaderToleratesMissingAndUnknownFields(t *testing.T) {
	s := newTestStore(t)
	writeStoreFixture(t, s, "old.json", `{"id":"wrong-id","title":"Older brief","createdAt":123,"newOptional":{"enabled":true},"body":"# Acceptance criteria\n- Deliver the report\n"}`)
	e, err := s.Get("old")
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "old" || e.SchemaVersion != 0 || e.Status != StatusActive || e.UpdatedAt != 123 || e.LastActivityAt != 123 ||
		!reflect.DeepEqual(e.Criteria, []string{"Deliver the report"}) {
		t.Fatalf("tolerant read = %+v", e)
	}
	if _, err := s.UpdateStatus(e.ID, StatusInProgress); err != nil {
		t.Fatal(err)
	}
	e, _ = s.Get("old")
	if e.SchemaVersion != SchemaVersion {
		t.Fatalf("old JSON did not upgrade on mutation: %+v", e)
	}
}

func TestLegacyMarkdownMigratesOnlyOnMutation(t *testing.T) {
	s := newTestStore(t)
	legacy := "---\nschemaVersion: 2\nid: legacy\nstatus: active\ntitle: Report\nsource: mobile\nchatSessionId: chat-1\ncreatedAt: 100\nupdatedAt: 200\n---\n\n# Acceptance criteria\n- report.md exists\n"
	path := writeStoreFixture(t, s, "legacy.md", legacy)
	e, err := s.Get("legacy")
	if err != nil || e.SchemaVersion != 2 || e.ChatSessionID != "chat-1" || e.LastActivityAt != 200 {
		t.Fatalf("legacy read = %+v, %v", e, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "legacy.json")); !os.IsNotExist(err) {
		t.Fatalf("read unexpectedly migrated file: %v", err)
	}
	if len(e.Criteria) != 1 || e.Criteria[0] != "report.md exists" {
		t.Fatalf("legacy criteria = %#v", e.Criteria)
	}
	if _, err := s.UpdateStatus("legacy", StatusInProgress); err != nil {
		t.Fatal(err)
	}
	all, err := s.List()
	if err != nil || len(all) != 1 || all[0].Status != StatusInProgress || all[0].SchemaVersion != SchemaVersion {
		t.Fatalf("JSON precedence/list dedup = %+v, %v", all, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != legacy {
		t.Fatalf("migration changed legacy recovery copy: %s, %v", raw, err)
	}
	if err := s.Delete("legacy"); err != nil {
		t.Fatal(err)
	}
	if all, err := s.List(); err != nil || len(all) != 0 {
		t.Fatalf("delete resurrected legacy record: %+v, %v", all, err)
	}
}

func TestFutureSchemaIsReadableButCannotBeDowngraded(t *testing.T) {
	for _, fixture := range []struct{ name, content string }{
		{"future.json", `{"schemaVersion":999,"status":"active","createdAt":1,"newField":"keep"}`},
		{"future.md", "---\nschemaVersion: 999\nstatus: active\ncreatedAt: 1\n---\nfuture body\n"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			s := newTestStore(t)
			path := writeStoreFixture(t, s, fixture.name, fixture.content)
			e, err := s.Get("future")
			if err != nil || e.SchemaVersion != 999 {
				t.Fatalf("future read = %+v, %v", e, err)
			}
			if _, err := s.UpdateStatus("future", StatusInProgress); err == nil {
				t.Fatal("future schema was mutated")
			}
			if _, err := s.Save(Entry{ID: "future", Status: StatusActive}); err == nil {
				t.Fatal("future schema was overwritten from an old snapshot")
			}
			if expired, err := s.MarkStale(24 * time.Hour); err != nil || len(expired) != 0 {
				t.Fatalf("future schema affected sweep: %+v, %v", expired, err)
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != fixture.content {
				t.Fatalf("future record changed: %s, %v", actual, err)
			}
		})
	}
}

func TestMalformedJSONDoesNotResurrectLegacyStateOrBlockOtherEntries(t *testing.T) {
	s := newTestStore(t)
	writeStoreFixture(t, s, "broken.md", "---\nstatus: active\n---\nold brief\n")
	path := writeStoreFixture(t, s, "broken.json", `{"status":`)
	writeStoreFixture(t, s, "healthy.json", `{"status":"blocked","title":"Needs input"}`)
	if _, err := s.Get("broken"); err == nil {
		t.Fatal("broken JSON fell back to the old active markdown record")
	}
	if _, err := s.Save(Entry{ID: "broken", Status: StatusActive}); err == nil {
		t.Fatal("broken record was silently overwritten")
	}
	all, err := s.List()
	if err != nil || len(all) != 1 || all[0].ID != "healthy" {
		t.Fatalf("tolerant list = %+v, %v", all, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, []byte(`{"status":`)) {
		t.Fatalf("malformed file was changed: %s, %v", raw, err)
	}
}

func TestUnknownStatusIsReadableButNotActionable(t *testing.T) {
	s := newTestStore(t)
	writeStoreFixture(t, s, "unknown.json", `{"status":"awaiting_new_state","createdAt":1}`)
	e, err := s.Get("unknown")
	if err != nil || e.Status != "awaiting_new_state" {
		t.Fatalf("unknown state was lost: %+v, %v", e, err)
	}
	if chips, err := s.ActiveForChips(0); err != nil || len(chips) != 0 {
		t.Fatalf("unknown state became actionable: %+v, %v", chips, err)
	}
	if _, err := s.UpdateStatus("unknown", StatusInProgress); err == nil {
		t.Fatal("unknown state was restarted")
	}
}

func TestStoreRejectsIDsOutsideItsDirectory(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"", "../outside", "/outside", "a/b", "a\\b", ".hidden", " id ", "a\x00b"} {
		t.Run(id, func(t *testing.T) {
			if _, err := s.Save(Entry{ID: id, Status: StatusActive}); err == nil {
				t.Fatal("Save accepted an unsafe ID")
			}
			if _, err := s.Get(id); err == nil {
				t.Fatal("Get accepted an unsafe ID")
			}
			if err := s.Delete(id); err == nil {
				t.Fatal("Delete accepted an unsafe ID")
			}
		})
	}
}
