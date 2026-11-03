package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestTodayChatAdmissionFilesDetectSourceAndControlChanges(t *testing.T) {
	for _, change := range []string{"memory edit", "memory delete", "memory rename", "knowledge edit", "forget controls", "semantic controls"} {
		t.Run(change, func(t *testing.T) {
			app := newTodayContextTestApp(t, false)
			writeTodayContextMemory(t, "selected", "# Original title\n\nOriginal facts")
			memory, _ := memoryDir()
			knowledge, _ := knowledgeDir()
			if err := os.WriteFile(filepath.Join(knowledge, "reference.md"), []byte("# Reference title"), 0600); err != nil {
				t.Fatal(err)
			}
			ref := TodayChatContextRef{SourceKind: "memory", SourceID: "selected"}
			before, err := app.readTodayChatAdmissionFiles(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			if err := app.validateTodayChatAdmissionFiles(t.Context(), ref, before); err != nil {
				t.Fatalf("unchanged sources were rejected: %v", err)
			}
			switch change {
			case "memory edit":
				err = os.WriteFile(filepath.Join(memory, "selected.md"), []byte("# Revised title\n\nRevised facts"), 0600)
			case "memory delete":
				err = os.Remove(filepath.Join(memory, "selected.md"))
			case "memory rename":
				err = os.Rename(filepath.Join(memory, "selected.md"), filepath.Join(memory, "renamed.md"))
			case "knowledge edit":
				err = os.WriteFile(filepath.Join(knowledge, "reference.md"), []byte("# Revised reference"), 0600)
			case "forget controls":
				err = os.WriteFile(filepath.Join(memory, memoryTombstonesFile), []byte(`[{"version":2,"keys":["withdrawn"]}]`), 0600)
			case "semantic controls":
				if err = os.MkdirAll(filepath.Join(memory, ".semantic"), 0700); err == nil {
					err = os.WriteFile(filepath.Join(memory, ".semantic", "receipt.json"), []byte(`{"factHashes":["withdrawn"]}`), 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := app.validateTodayChatAdmissionFiles(t.Context(), ref, before); !errors.Is(err, errTodayChatItemUnavailable) {
				t.Fatalf("changed files remained admissible: %v", err)
			}
		})
	}
}

func TestTodayChatAdmissionFilesDetectBriefTopicsAndHandoffChanges(t *testing.T) {
	for _, source := range []string{"brief_item", "find", "handoff", "deliverable"} {
		t.Run(source, func(t *testing.T) {
			app := newTodayContextTestApp(t, false)
			t.Setenv(topicsStateDirEnvVar, filepath.Join(os.Getenv("HOME"), "topics"))
			var err error
			app.handoffStore, err = handoff.NewStore(filepath.Join(os.Getenv("HOME"), "handoffs"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := app.handoffStore.Save(handoff.Entry{ID: "selected", Title: "Original title", Status: handoff.StatusActive}); err != nil {
				t.Fatal(err)
			}
			ref := TodayChatContextRef{SourceKind: source, SourceID: "selected"}
			var briefPath string
			if source == "brief_item" {
				ref.BriefID = "2026-10-17-evening"
				briefPath = filepath.Join(os.Getenv("YANSU_MOBILE_STATE_DIR"), mobileBriefDirName, ref.BriefID+".json")
				if err := os.MkdirAll(filepath.Dir(briefPath), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(briefPath, []byte(`{"briefId":"2026-10-17-evening"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := app.readTodayChatAdmissionFiles(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			if err := app.validateTodayChatAdmissionFiles(t.Context(), ref, before); err != nil {
				t.Fatalf("unchanged service state rejected: %v", err)
			}
			switch source {
			case "brief_item":
				err = os.Remove(briefPath)
			case "find":
				_, err = app.topicsSvc().store.update(func(state *topicsStateFile) {
					state.Finds.Items = []TopicFind{{ID: "selected", Title: "Revised finding"}}
				})
			case "handoff", "deliverable":
				_, err = app.handoffStore.MutateIfStatus("selected", nil, func(entry *handoff.Entry) { entry.Title = "Revised title" })
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := app.validateTodayChatAdmissionFiles(t.Context(), ref, before); !errors.Is(err, errTodayChatItemUnavailable) {
				t.Fatalf("changed source remained admissible: %v", err)
			}
		})
	}
}

func TestTodayChatAdmissionFilesIgnoreMediaAndRejectInvalidReads(t *testing.T) {
	app := newTodayContextTestApp(t, false)
	writeTodayContextMemory(t, "selected", "# Allowed memory")
	ref := TodayChatContextRef{SourceKind: "memory", SourceID: "selected"}
	before, err := app.readTodayChatAdmissionFiles(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := memoryDir()
	if err := os.WriteFile(filepath.Join(dir, "recording.wav"), []byte("unrelated media"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.validateTodayChatAdmissionFiles(t.Context(), ref, before); err != nil {
		t.Fatalf("unrelated media changed source revision: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := app.readTodayChatAdmissionFiles(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled validation continued: %v", err)
	}
	if _, err := app.readTodayChatAdmissionFiles(t.Context(), TodayChatContextRef{SourceKind: "brief_item", BriefID: "../../outside"}); !errors.Is(err, errTodayChatItemUnavailable) {
		t.Fatalf("invalid brief path admitted: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "selected.md"), filepath.Join(dir, "linked.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := app.readTodayChatAdmissionFiles(t.Context(), ref); !errors.Is(err, errTodayChatItemUnavailable) {
		t.Fatalf("source snapshot followed a symlink: %v", err)
	}
}

func TestTodayChatAdmissionFilesRejectRawWithdrawalBeforeCommit(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	writeTodayContextMemory(t, "selected", "# Title withdrawn before publication")
	ref := TodayChatContextRef{SourceKind: "memory", SourceID: "selected"}
	files, err := app.readTodayChatAdmissionFiles(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := app.store.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := memoryDir()
	order, err := app.store.AppendUserStreamItemAtomicIfCurrent(t.Context(), main.LocalID, storage.StreamItemRow{ID: "withdrawn", Kind: "user", Content: "Title withdrawn before publication"}, revision, func() error {
		if err := os.Remove(filepath.Join(dir, "selected.md")); err != nil {
			return err
		}
		return app.validateTodayChatAdmissionFiles(t.Context(), ref, files)
	})
	if order != -1 || !errors.Is(err, errTodayChatItemUnavailable) {
		t.Fatalf("raw source withdrawal admitted: order=%d err=%v", order, err)
	}
	rows, err := app.store.LoadStreamItems(t.Context(), main.LocalID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("withdrawn title reached durable history: rows=%+v err=%v", rows, err)
	}
}
