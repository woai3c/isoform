package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/isoformskill"
)

func isoformSkillTestHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "isk-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	return home
}

func writeTestIsoformSkill(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// plantInstalledIsoformSkill writes the exact copy older versions installed.
func plantInstalledIsoformSkill(t *testing.T, home, assistant string) {
	t.Helper()
	writeTestIsoformSkill(t, filepath.Join(home, assistant, "skills", "isoform", isoformSkillFile),
		stampedIsoformSkill(isoformskill.Content, isoformskill.Version))
}

func TestRemoveInstalledIsoformSkillsRemovesStampedCopiesOnce(t *testing.T) {
	home := isoformSkillTestHome(t)
	for _, assistant := range []string{".claude", ".codex"} {
		plantInstalledIsoformSkill(t, home, assistant)
	}
	writeTestIsoformSkill(t, filepath.Join(home, isoformSkillConsentDir, isoformSkillConsentFile), []byte(`{"format":1}`))
	writeTestIsoformSkill(t, filepath.Join(home, isoformSkillConsentDir, isoformSkillConsentLock), []byte{})

	copies, store, err := removeInstalledIsoformSkills(home)
	if err != nil || copies != 2 || !store {
		t.Fatalf("cleanup = %d copies, store %v, %v", copies, store, err)
	}
	for _, assistant := range []string{".claude", ".codex"} {
		if _, err := os.Lstat(filepath.Join(home, assistant, "skills", "isoform")); !os.IsNotExist(err) {
			t.Fatalf("%s: the installed skill directory survived: %v", assistant, err)
		}
		if _, err := os.Stat(filepath.Join(home, assistant, "skills")); err != nil {
			t.Fatalf("%s: cleanup removed an assistant parent: %v", assistant, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, isoformSkillConsentDir)); !os.IsNotExist(err) {
		t.Fatalf("the consent store survived: %v", err)
	}

	// A later start finds nothing and removes nothing.
	copies, store, err = removeInstalledIsoformSkills(home)
	if err != nil || copies != 0 || store {
		t.Fatalf("second cleanup = %d copies, store %v, %v", copies, store, err)
	}
}

func TestRemoveInstalledIsoformSkillsWithoutCopiesCreatesNothing(t *testing.T) {
	home := isoformSkillTestHome(t)
	copies, store, err := removeInstalledIsoformSkills(home)
	if err != nil || copies != 0 || store {
		t.Fatalf("empty home: %d copies, store %v, %v", copies, store, err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cleanup created files: %v, %v", entries, err)
	}
}

func TestRemoveInstalledIsoformSkillsPreservesExtraFiles(t *testing.T) {
	home := isoformSkillTestHome(t)
	for _, assistant := range []string{".claude", ".codex"} {
		plantInstalledIsoformSkill(t, home, assistant)
		writeTestIsoformSkill(t, filepath.Join(home, assistant, "skills", "isoform", "notes.txt"), []byte("user notes"))
	}
	copies, _, err := removeInstalledIsoformSkills(home)
	if err != nil || copies != 2 {
		t.Fatalf("cleanup = %d copies, %v", copies, err)
	}
	for _, assistant := range []string{".claude", ".codex"} {
		dir := filepath.Join(home, assistant, "skills", "isoform")
		if _, err := os.Lstat(filepath.Join(dir, isoformSkillFile)); !os.IsNotExist(err) {
			t.Fatalf("%s: managed file survived cleanup: %v", assistant, err)
		}
		content, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
		if err != nil || string(content) != "user notes" {
			t.Fatalf("%s: cleanup changed an extra file: %v", assistant, err)
		}
	}
}

// An unstamped or locally edited SKILL.md is the user's own: cleanup never
// touches it, and it is not an error.
func TestRemoveInstalledIsoformSkillsPreservesUnmanagedAndEdited(t *testing.T) {
	for _, assistant := range []string{".claude", ".codex"} {
		for _, kind := range []string{"unmanaged", "modified", "modified-version", "invalid-stamp"} {
			t.Run(assistant+"/"+kind, func(t *testing.T) {
				home := isoformSkillTestHome(t)
				content := []byte("user-owned skill\n")
				switch kind {
				case "modified":
					content = append([]byte("local edit\n"), stampedIsoformSkill("older body\n", "0.0.1")...)
				case "modified-version":
					content = bytes.Replace(stampedIsoformSkill("older body\n", "0.0.1"), []byte(`"version":"0.0.1"`), []byte(`"version":"local"`), 1)
				case "invalid-stamp":
					content = []byte("body" + isoformSkillStampStart + "invalid" + isoformSkillStampEnd)
				}
				path := filepath.Join(home, assistant, "skills", "isoform", isoformSkillFile)
				writeTestIsoformSkill(t, path, content)
				copies, _, err := removeInstalledIsoformSkills(home)
				if err != nil || copies != 0 {
					t.Fatalf("user's own skill touched or reported: %d, %v", copies, err)
				}
				got, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(got, content) {
					t.Fatalf("user's own skill changed: %v", readErr)
				}
			})
		}
	}
}

// A stamped copy behind a symlink is reported and left alone, and the other
// assistant's copy is still removed. Nothing is ever removed through a link.
func TestRemoveInstalledIsoformSkillsRejectsSymlinks(t *testing.T) {
	for _, assistant := range []string{".claude", ".codex"} {
		for _, component := range []string{"", "skills", "skills/isoform", "skills/isoform/SKILL.md"} {
			t.Run(assistant+"/"+component, func(t *testing.T) {
				home := isoformSkillTestHome(t)
				outside, err := os.MkdirTemp("/tmp", "isk-out-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(outside) })
				target := outside
				if strings.HasSuffix(component, isoformSkillFile) {
					target = filepath.Join(outside, isoformSkillFile)
					writeTestIsoformSkill(t, target, stampedIsoformSkill("external body\n", "0.0.1"))
				}
				link := filepath.Join(home, assistant, component)
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				// The other assistant's genuine copy is still removed.
				other := ".claude"
				if assistant == other {
					other = ".codex"
				}
				plantInstalledIsoformSkill(t, home, other)

				copies, _, err := removeInstalledIsoformSkills(home)
				if err == nil || !strings.Contains(err.Error(), assistant) {
					t.Fatalf("symlink not reported for %s: %d, %v", assistant, copies, err)
				}
				if copies != 1 {
					t.Fatalf("the other assistant's copy was not removed: %d", copies)
				}
				info, err := os.Lstat(link)
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("symlink changed: %v", err)
				}
				entries, err := os.ReadDir(outside)
				if err != nil {
					t.Fatal(err)
				}
				if target == outside && len(entries) != 0 {
					t.Fatal("cleanup wrote through a directory symlink")
				}
				if target != outside {
					content, err := os.ReadFile(target)
					if err != nil || !bytes.Equal(content, stampedIsoformSkill("external body\n", "0.0.1")) {
						t.Fatalf("cleanup changed a symlink target: %v", err)
					}
				}
			})
		}
	}
}

func TestRemoveInstalledIsoformSkillsRejectsHardlinksAndSpecialFiles(t *testing.T) {
	for _, kind := range []string{"hardlink", "directory", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			home := isoformSkillTestHome(t)
			path := filepath.Join(home, ".claude", "skills", "isoform", isoformSkillFile)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "hardlink":
				source := filepath.Join(home, "original.md")
				writeTestIsoformSkill(t, source, stampedIsoformSkill("body\n", "0.0.1"))
				err = os.Link(source, path)
			case "directory":
				err = os.Mkdir(path, 0o755)
			case "dangling-symlink":
				err = os.Symlink(filepath.Join(home, "absent"), path)
			}
			if err != nil {
				t.Fatal(err)
			}
			copies, _, cleanErr := removeInstalledIsoformSkills(home)
			if cleanErr == nil || copies != 0 {
				t.Fatalf("unsafe entry accepted: %d, %v", copies, cleanErr)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("unsafe entry removed: %v", err)
			}
		})
	}
}

func TestRemoveInstalledIsoformSkillsKeepsUnknownConsentFiles(t *testing.T) {
	home := isoformSkillTestHome(t)
	writeTestIsoformSkill(t, filepath.Join(home, isoformSkillConsentDir, isoformSkillConsentFile), []byte(`{"format":1}`))
	writeTestIsoformSkill(t, filepath.Join(home, isoformSkillConsentDir, "mine.txt"), []byte("not isoform's"))

	copies, store, err := removeInstalledIsoformSkills(home)
	if err != nil || copies != 0 || !store {
		t.Fatalf("cleanup = %d copies, store %v, %v", copies, store, err)
	}
	if _, err := os.Lstat(filepath.Join(home, isoformSkillConsentDir, isoformSkillConsentFile)); !os.IsNotExist(err) {
		t.Fatalf("Isoform's consent file survived: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(home, isoformSkillConsentDir, "mine.txt"))
	if err != nil || string(content) != "not isoform's" {
		t.Fatalf("an unknown file in the consent directory changed: %v", err)
	}
}

func TestRemoveInstalledIsoformSkillsRejectsLinkedConsentDir(t *testing.T) {
	home := isoformSkillTestHome(t)
	outside := filepath.Join(home, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, isoformSkillConsentDir)); err != nil {
		t.Fatal(err)
	}
	_, _, err := removeInstalledIsoformSkills(home)
	if err == nil || !strings.Contains(err.Error(), isoformSkillConsentDir) {
		t.Fatalf("linked consent directory not reported: %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cleanup wrote through a linked directory: %v, %v", entries, err)
	}
}

// The startup cleanup runs once: it logs one line when it removed Isoform's
// copies, and a marked start neither walks the assistant folders nor logs.
func TestRemoveInstalledIsoformSkillsLoggedOneLine(t *testing.T) {
	home := isoformSkillTestHome(t)
	t.Setenv(agentHomeEnv, filepath.Join(home, "data"))
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(previous)

	removeInstalledIsoformSkillsLogged()
	if buf.Len() != 0 {
		t.Fatalf("nothing to remove, but logged %q", buf.String())
	}
	if _, err := os.Stat(filepath.Join(os.Getenv(agentHomeEnv), isoformSkillCleanupMarkerName)); err != nil {
		t.Fatalf("a clean run was not marked: %v", err)
	}

	// A fresh data root (no marker) with a planted copy removes it and logs
	// one line.
	home = isoformSkillTestHome(t)
	t.Setenv(agentHomeEnv, filepath.Join(home, "data"))
	plantInstalledIsoformSkill(t, home, ".claude")
	removeInstalledIsoformSkillsLogged()
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "[isoform-skill]") {
		t.Fatalf("removal logged %q, want one [isoform-skill] line", out)
	}

	// Marked: a later copy (an older build reinstalling) is left alone and
	// nothing is walked or logged.
	plantInstalledIsoformSkill(t, home, ".codex")
	removeInstalledIsoformSkillsLogged()
	if buf.String() != out {
		t.Fatalf("a marked start ran again: %q", buf.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "isoform", isoformSkillFile)); err != nil {
		t.Fatalf("a marked start walked the assistant folders: %v", err)
	}
}
