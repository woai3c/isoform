import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

from desktop_release import area_for, generate_manifest, render_notes
from go_test import completed_execution, retryable_failures


@unittest.skipUnless(shutil.which("go"), "Go is required for real-process regressions")
class GoProcessTests(unittest.TestCase):
    def fixture(self, directory, crash):
        root = Path(directory)
        (root / "go.mod").write_text("module wails-gui\n\ngo 1.23.0\n")
        (root / "suite_test.go").write_text(
            r"""package fixture

import (
    "flag"
    "fmt"
    "os"
    "testing"
)

func TestCleanupPlanChangesNothingAndRunMatchesIt(t *testing.T) {
    f, err := os.OpenFile("cleanup-runs", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
    if err != nil { t.Fatal(err) }
    if _, err := f.WriteString("run\n"); err != nil { t.Fatal(err) }
    if err := f.Close(); err != nil { t.Fatal(err) }
    if os.Getenv("GO_TEST_FIXTURE_FLAKE") != "0" && flag.Lookup("test.run").Value.String() == "" {
        t.Fatal("allowlisted flake only fails in the full suite")
    }
}

func TestProcessCrash(t *testing.T) {
    if os.Getenv("GO_TEST_FIXTURE_CRASH") == "1" { os.Exit(2) }
}

func TestMustAlsoRun(t *testing.T) {
    if err := os.WriteFile("must-also-run", []byte("ran"), 0600); err != nil {
        t.Fatal(err)
    }
}

func TestParallelAndSkipped(t *testing.T) {
    t.Parallel()
    t.Run("skipped", func(t *testing.T) { t.Skip("control") })
}

func FuzzSeed(f *testing.F) {
    f.Add("seed")
    f.Fuzz(func(t *testing.T, value string) {})
}

func Example() {
    fmt.Println("example")
    // Output: example
}

func Example_withoutOutput() {}
"""
        )
        (root / "support").mkdir()
        (root / "support" / "empty.go").write_text("package support\n")
        env = dict(os.environ, GOWORK="off", GOTOOLCHAIN="local")
        env["GO_TEST_FIXTURE_CRASH"] = "1" if crash else "0"
        return root, env

    def wrapper(self, root, env):
        return subprocess.run(
            [sys.executable, str(Path(__file__).with_name("go_test.py").resolve())],
            cwd=root,
            env=env,
            capture_output=True,
            text=True,
            timeout=180,
        )

    def test_crashed_suite_never_recovers_by_retrying_only_the_flake(self):
        with tempfile.TemporaryDirectory(prefix="go-wrapper-") as directory:
            root, env = self.fixture(directory, crash=True)
            direct = subprocess.run(
                ["go", "test", "-json", "-count=1", "./..."],
                cwd=root,
                env=env,
                capture_output=True,
                text=True,
                timeout=180,
            )
            self.assertNotEqual(direct.returncode, 0, direct.stdout + direct.stderr)
            events = [json.loads(line) for line in direct.stdout.splitlines()]
            crash_actions = {
                event["Action"]
                for event in events
                if event.get("Test") == "TestProcessCrash"
            }
            self.assertIn("run", crash_actions)
            self.assertFalse(crash_actions & {"pass", "fail", "skip"})
            self.assertFalse((root / "must-also-run").exists())
            (root / "cleanup-runs").unlink()

            result = self.wrapper(root, env)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertNotIn("Retrying ledger flake", result.stdout)
            self.assertEqual((root / "cleanup-runs").read_text().splitlines(), ["run"])
            self.assertFalse((root / "must-also-run").exists())

    def test_complete_suite_can_recover_from_one_isolated_flake_retry(self):
        with tempfile.TemporaryDirectory(prefix="go-wrapper-") as directory:
            root, env = self.fixture(directory, crash=False)
            result = self.wrapper(root, env)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout.count("Retrying ledger flake once"), 1)
            self.assertEqual(
                (root / "cleanup-runs").read_text().splitlines(), ["run", "run"]
            )
            self.assertEqual((root / "must-also-run").read_text(), "ran")
            self.assertIn("PASS: FuzzSeed/seed#0", result.stdout)
            self.assertIn("PASS: Example", result.stdout)
            self.assertIn("SKIP: TestParallelAndSkipped/skipped", result.stdout)

    def test_clean_suite_passes_without_retry(self):
        with tempfile.TemporaryDirectory(prefix="go-wrapper-") as directory:
            root, env = self.fixture(directory, crash=False)
            env["GO_TEST_FIXTURE_FLAKE"] = "0"
            result = self.wrapper(root, env)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertNotIn("Retrying ledger flake", result.stdout)
            self.assertEqual((root / "cleanup-runs").read_text().splitlines(), ["run"])

    def test_retry_that_exits_without_running_the_requested_test_stays_red(self):
        with tempfile.TemporaryDirectory(prefix="go-wrapper-") as directory:
            root, env = self.fixture(directory, crash=False)
            with (root / "suite_test.go").open("a") as source:
                source.write(
                    """
func TestMain(m *testing.M) {
    flag.Parse()
    if flag.Lookup("test.run").Value.String() != "" { os.Exit(0) }
    os.Exit(m.Run())
}
"""
                )
            result = self.wrapper(root, env)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout.count("Retrying ledger flake once"), 1)
            self.assertEqual((root / "cleanup-runs").read_text().splitlines(), ["run"])
            self.assertTrue((root / "must-also-run").exists())

    def test_compile_failure_alongside_a_known_flake_stays_red(self):
        with tempfile.TemporaryDirectory(prefix="go-wrapper-") as directory:
            root, env = self.fixture(directory, crash=False)
            (root / "support" / "empty.go").write_text(
                "package support\nvar _ = missing\n"
            )
            result = self.wrapper(root, env)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertNotIn("Retrying ledger flake", result.stdout)


class RetryTests(unittest.TestCase):
    def failures(self, *names):
        return (
            [{"Action": "start", "Package": "wails-gui"}]
            + [
                {"Action": "run", "Package": "wails-gui", "Test": name}
                for name in names
            ]
            + [
                {"Action": "fail", "Package": "wails-gui", "Test": name}
                for name in names
            ]
            + [{"Action": "fail", "Package": "wails-gui"}]
        )

    def test_known_flake_subtest_retries_the_parent_once(self):
        name = "TestCleanupPlanChangesNothingAndRunMatchesIt"
        self.assertEqual(
            retryable_failures(self.failures(name, name + "/child")),
            [("wails-gui", name)],
        )

    def test_unknown_failure_is_not_hidden_by_a_passing_retry(self):
        self.assertEqual(
            retryable_failures(
                self.failures(
                    "TestCleanupPlanChangesNothingAndRunMatchesIt", "TestNewRegression"
                )
            ),
            [],
        )

    def test_started_test_without_terminal_event_is_not_retryable(self):
        events = self.failures("TestCleanupPlanChangesNothingAndRunMatchesIt")
        events.insert(
            -1, {"Action": "run", "Package": "wails-gui", "Test": "TestCrash"}
        )
        self.assertEqual(retryable_failures(events), [])

    def test_listed_test_that_never_started_is_not_retryable(self):
        name = "TestCleanupPlanChangesNothingAndRunMatchesIt"
        self.assertEqual(
            retryable_failures(
                self.failures(name), {"wails-gui": {name, "TestMustAlsoRun"}}
            ),
            [],
        )

    def test_incomplete_or_unexplained_package_result_is_not_retryable(self):
        events = self.failures("TestCleanupPlanChangesNothingAndRunMatchesIt")
        self.assertEqual(retryable_failures(events[:-1]), [])
        self.assertEqual(
            retryable_failures(events + [{"Action": "start", "Package": "other"}]), []
        )
        events[-1]["Action"] = "pass"
        self.assertFalse(completed_execution(events))

    def test_build_failure_and_panic_are_never_retryable(self):
        name = "TestCleanupPlanChangesNothingAndRunMatchesIt"
        self.assertEqual(retryable_failures(self.failures()), [])
        self.assertEqual(
            retryable_failures(
                self.failures(name)
                + [{"Action": "fail", "Package": "wails-gui/storage"}]
            ),
            [],
        )
        for output in ("panic: test timed out", "fatal error: concurrent map writes"):
            self.assertEqual(
                retryable_failures(self.failures(name) + [{"Output": output}]), []
            )


class ReleaseTests(unittest.TestCase):
    def test_notes_only_include_merged_prs_in_the_source_range(self):
        prs = [
            {
                "number": number,
                "title": title,
                "merged_at": merged,
                "merge_commit_sha": sha,
                "html_url": f"https://github.com/example/isoform/pull/{number}",
            }
            for number, title, merged, sha in (
                (1, "closed but not merged", None, "included"),
                (2, "before previous tag", "date", "old"),
                (3, "fix(privacy): preserve source gates", "date", "included"),
                (4, "after current tag", "date", "future"),
                (5, "feat: quick chat", "date", "also-included"),
            )
        ]
        notes = render_notes(
            prs,
            {"included", "also-included"},
            "example/isoform",
            "current-sha",
            "desktop/v0.1.0",
        )
        self.assertIn("### Privacy", notes)
        self.assertIn("### Chat and voice", notes)
        self.assertIn("[#3]", notes)
        self.assertIn("[#5]", notes)
        for number in (1, 2, 4):
            self.assertNotIn(f"[#{number}]", notes)
        self.assertIn("desktop/v0.1.0...current-sha", notes)

    def test_explicit_area_label_takes_precedence(self):
        self.assertEqual(
            area_for(
                {"title": "fix(chat): title", "labels": [{"name": "area:privacy"}]}
            ),
            "Privacy",
        )

    def test_feed_contains_both_archives_and_their_actual_checksums(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for arch in ("arm64", "x64"):
                for suffix in (".dmg", ".app.tar.gz"):
                    (root / f"Isoform-1.2.3-darwin-{arch}{suffix}").write_bytes(
                        f"{arch}{suffix}".encode()
                    )
            generate_manifest(
                root, "1.2.3", "example/isoform-releases", "Release notes"
            )
            manifest = json.loads((root / "version.json").read_text())
            self.assertEqual(manifest["version"], "1.2.3")
            self.assertEqual(manifest["release_notes"], "Release notes")
            self.assertEqual(
                set(manifest["updater_assets"]), {"darwin-arm64", "darwin-x64"}
            )
            for platform, url in manifest["updater_assets"].items():
                filename = url.rsplit("/", 1)[-1]
                self.assertTrue(filename.endswith(".app.tar.gz"))
                self.assertEqual(filename, f"Isoform-1.2.3-{platform}.app.tar.gz")
                self.assertIn("isoform-releases/releases/download/v1.2.3/", url)
                digest = hashlib.sha256((root / filename).read_bytes()).hexdigest()
                self.assertEqual(manifest["updater_checksums"][platform], digest)
                self.assertIn(
                    f"{digest}  {filename}", (root / "checksums.txt").read_text()
                )
            self.assertEqual(len((root / "checksums.txt").read_text().splitlines()), 4)
            (root / "Isoform-1.2.3-darwin-x64.app.tar.gz").unlink()
            with self.assertRaises(FileNotFoundError):
                generate_manifest(root, "1.2.3", "example/isoform-releases", "notes")

    def test_feed_refuses_artifacts_with_the_retired_yansu_name(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for arch in ("arm64", "x64"):
                for suffix in (".dmg", ".app.tar.gz"):
                    (root / f"Yansu-1.2.3-darwin-{arch}{suffix}").write_bytes(b"old")
            with self.assertRaises(FileNotFoundError):
                generate_manifest(root, "1.2.3", "example/isoform-releases", "notes")
            self.assertFalse((root / "version.json").exists())

    def test_beta_feed_targets_versioned_prerelease_archives(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for arch in ("arm64", "x64"):
                for suffix in (".dmg", ".app.tar.gz"):
                    (root / f"Isoform-1.0.0-beta.2-darwin-{arch}{suffix}").write_bytes(
                        f"{arch}{suffix}".encode()
                    )
            generate_manifest(
                root, "1.0.0-beta.2", "example/isoform-releases", "notes", "beta"
            )
            feed = json.loads((root / "version.json").read_text())
            self.assertIn(
                "/releases/download/v1.0.0-beta.2/",
                feed["updater_assets"]["darwin-x64"],
            )
            self.assertEqual(feed["version"], "1.0.0-beta.2")
            with self.assertRaises(ValueError):
                generate_manifest(
                    root, "1.0.0-beta.2", "example/isoform-releases", "notes", "stable"
                )
            with self.assertRaises(ValueError):
                generate_manifest(
                    root, "1.0.0", "example/isoform-releases", "notes", "beta"
                )


if __name__ == "__main__":
    unittest.main()
