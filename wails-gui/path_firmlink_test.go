package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeHomeWithFirmlink points HOME at a scratch folder and returns it with
// the Data-volume spelling of the same folder. The firmlink cases run only on
// macOS, where /System/Volumes/Data is the volume behind /Users and /private.
func fakeHomeWithFirmlink(t *testing.T) (home, firmHome string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the /System/Volumes/Data firmlink exists only on macOS")
	}
	home = shortGuardTestHome(t)
	firmHome = dataVolumeRoot + home
	if _, err := os.Stat(firmHome); err != nil {
		t.Skipf("Data-volume spelling of the scratch folder is not visible: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YANSU_AGENT_HOME", "")
	return home, firmHome
}

func firmlinkSpellings(t *testing.T, plain string) []string {
	t.Helper()
	var paths []string
	for _, prefix := range []string{dataVolumeRoot, "/system/volumes/data", "/SYSTEM/VOLUMES/DATA", "/sYsTeM/vOlUmEs/dAtA"} {
		path := prefix + plain
		if _, err := os.Stat(path); err == nil {
			paths = append(paths, path)
		}
	}
	return paths
}

func TestStripDataVolumeFirmlinkOnlyWhenItNamesTheSameFolder(t *testing.T) {
	home, _ := fakeHomeWithFirmlink(t)
	for _, spelling := range firmlinkSpellings(t, home) {
		if got := stripDataVolumeFirmlink(spelling); got != home {
			t.Fatalf("stripDataVolumeFirmlink(%q) = %q, want %q", spelling, got, home)
		}
		// A missing folder still maps to its plain spelling, in every case variant.
		if got := stripDataVolumeFirmlink(spelling + "/later"); got != home+"/later" {
			t.Fatalf("missing folder: got %q, want %q", got, home+"/later")
		}
	}
	// A Data-volume path that names a different folder is left as it is.
	other := dataVolumeRoot + "/nonexistent-isoform-firmlink-probe/x"
	if got := stripDataVolumeFirmlink(other); got != other {
		t.Fatalf("unrelated Data-volume path rewritten to %q", got)
	}
	// Plain paths are never touched.
	if got := stripDataVolumeFirmlink(home); got != home {
		t.Fatalf("plain path rewritten to %q", got)
	}
}

func TestCronProjectDirRefusesFirmlinkedHomeAndPrivateFolders(t *testing.T) {
	home, firmHome := fakeHomeWithFirmlink(t)
	for _, name := range []string{".ssh", ".isoform-agent"} {
		if err := os.MkdirAll(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range append(firmlinkSpellings(t, home), home) {
		for _, suffix := range []string{"", "/", "/.ssh", "/.ssh/missing", "/.isoform-agent", "/.isoform-agent/missing"} {
			dir := root + suffix
			if err := validateCronProjectDir(dir); err == nil {
				t.Errorf("validateCronProjectDir(%q) = nil, want an error", dir)
			}
		}
	}
	// A project folder under the home folder is still accepted in either spelling.
	project := filepath.Join(home, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range append(firmlinkSpellings(t, project), project, firmHome+"/project") {
		if err := validateCronProjectDir(dir); err != nil {
			t.Errorf("validateCronProjectDir(%q) = %v, want nil", dir, err)
		}
	}
}

func TestCronProjectDirFailsClosedWithoutAHomeFolder(t *testing.T) {
	// os.UserHomeDir reads HOME on unix and USERPROFILE on Windows.
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if err := validateCronProjectDir(t.TempDir()); err == nil {
		t.Fatal("validateCronProjectDir accepted a folder while the home folder is unknown")
	}
	if err := validateCronProjectDir(""); err != nil {
		t.Fatalf("empty project folder = %v, want nil", err)
	}
}

func TestCronProjectDirTempFolderStillAllowedAwayFromHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	temp := t.TempDir()
	if err := validateCronProjectDir(temp); err != nil {
		t.Fatalf("validateCronProjectDir(%q) = %v, want nil", temp, err)
	}
}

func TestUploadRefusesFirmlinkedPrivateFiles(t *testing.T) {
	home, firmHome := fakeHomeWithFirmlink(t)
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(ssh, "id_test")
	if err := os.WriteFile(key, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	isoform := filepath.Join(home, ".isoform-agent")
	if err := os.MkdirAll(isoform, 0o700); err != nil {
		t.Fatal(err)
	}
	stateDB := filepath.Join(isoform, "state.db")
	if err := os.WriteFile(stateDB, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()

	for _, file := range []string{key, stateDB} {
		for _, firm := range firmlinkSpellings(t, file) {
			// Even a touched file, named by any Data-volume spelling, stays private.
			written := map[string]struct{}{file: {}}
			if reason := privateUploadReason(firm, workDir, ""); reason == "" {
				t.Errorf("privateUploadReason(%q) accepted private data", firm)
			}
			files, skipped := readDeliverableFiles([]string{firm}, workDir, "", written)
			if len(files) != 0 {
				t.Fatalf("uploaded %s through %s: %+v", file, firm, files)
			}
			if len(skipped) != 1 || !strings.Contains(skipped[0], "private folder") && !strings.Contains(skipped[0], "Isoform's own data folder") {
				t.Fatalf("skipped = %v, want a private-folder reason for %s", skipped, firm)
			}
		}
	}

	// The firmlink spelling of the home folder itself does not turn a private
	// file into a work-folder file.
	if validateUploadDeliverablePath(firmHome+"/.ssh/id_test", nil) {
		t.Fatal("a private key under the firmlink spelling passed the upload containment check")
	}
}

func TestUploadAllowsFirmlinkedProjectFile(t *testing.T) {
	home, _ := fakeHomeWithFirmlink(t)
	report := filepath.Join(home, "report.txt")
	if err := os.WriteFile(report, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := firmlinkSpellings(t, report)
	if upper := strings.ToUpper(report); upper != report {
		if _, err := os.Stat(upper); err == nil {
			paths = append(paths, upper)
		}
	}
	for _, path := range paths {
		files, skipped := readDeliverableFiles([]string{path}, home, "", touchedBy(t, report))
		if len(files) != 1 || len(skipped) != 0 || string(files[0].Data) != "report" {
			t.Errorf("upload(%q): files=%v skipped=%v", path, files, skipped)
		}
	}
}

func TestUploadFailsClosedWithoutAHomeFolder(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("YANSU_AGENT_HOME", "")
	report := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(report, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, skipped := readDeliverableFiles([]string{report}, filepath.Dir(report), "", touchedBy(t, report))
	if len(files) != 0 || len(skipped) != 1 || !strings.Contains(skipped[0], "home folder is unknown") {
		t.Fatalf("unknown HOME upload: files=%v skipped=%v", files, skipped)
	}
}

func TestUploadRefusesDirectorySymlinkSwap(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YANSU_AGENT_HOME", "")
	private := filepath.Join(home, ".ssh")
	work := filepath.Join(home, "work")
	dir := filepath.Join(work, "d")
	for _, path := range []string{private, dir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	report := filepath.Join(dir, "report.txt")
	for path, content := range map[string]string{report: "report", filepath.Join(private, "report.txt"): "PRIVATE-KEY-MARKER"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !validateUploadDeliverablePath(report, touchedBy(t, report)) {
		t.Fatal("legitimate report was not eligible before the directory swap")
	}
	if reason := privateUploadReason(report, work, ""); reason != "" {
		t.Fatalf("legitimate report was private before the directory swap: %s", reason)
	}
	if err := os.Rename(dir, dir+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, dir); err != nil {
		t.Fatal(err)
	}
	f, _, reason := openUploadDeliverable(report)
	if f != nil {
		_ = f.Close()
		t.Fatal("opened a private file through the swapped parent directory")
	}
	if reason == "" {
		t.Fatal("directory swap must have a refusal reason")
	}
}
