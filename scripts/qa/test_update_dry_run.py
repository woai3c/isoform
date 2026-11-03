import json
import os
import shutil
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from update_dry_run import (
    CheckFailed,
    create_workspace,
    overlaps,
    qa_environment,
    render_feed,
    main,
)


class IsolationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.base = Path(self.directory.name).resolve()
        self.home = self.base / "user"
        self.home.mkdir()
        self.apps = (self.base / "RC1.app", self.base / "RC2.app")
        for app in self.apps:
            app.mkdir()
            (app / "keep").write_text("input sentinel")

    def allocate(self):
        root, app_tmp = create_workspace(self.apps)
        self.addCleanup(shutil.rmtree, app_tmp)
        self.assertEqual(root.parent, app_tmp)
        self.assertEqual(app_tmp.parent, Path("/tmp").resolve(strict=True))
        for directory in (root, app_tmp):
            self.assertEqual(directory, directory.resolve(strict=True))
            self.assertFalse(directory.is_symlink())
            self.assertEqual(directory.stat().st_mode & 0o777, 0o700)
            for app in self.apps:
                self.assertFalse(overlaps(directory, app))
        return root, app_tmp

    def snapshot(self):
        return sorted(
            (str(p.relative_to(self.base)), p.read_bytes() if p.is_file() else None)
            for p in self.base.rglob("*")
        )

    def exercise_inherited_destination(self, destination):
        before = self.snapshot()
        inherited = {
            key: str(destination)
            for key in (
                "TMPDIR",
                "TMP",
                "TEMP",
                "BUN_INSTALL_CACHE_DIR",
                "YANSU_AGENT_HOME",
            )
        }
        inherited["HOME"] = str(self.home)
        # tempfile caches defaults separately from environment variables. Neither
        # may choose the allocation parent, even for a nonexistent live home.
        with patch.dict(os.environ, inherited), patch.object(
            tempfile, "tempdir", str(destination)
        ):
            root, app_tmp = self.allocate()
            home = root / "data"
            home.mkdir()
            (root / "result.json").write_text("owned evidence")
            env = qa_environment(home, root / "hook", "http://127.0.0.1/feed", app_tmp)
        for key in ("TMPDIR", "TMP", "TEMP", "BUN_INSTALL_CACHE_DIR"):
            self.assertEqual(env[key], str(app_tmp))
        self.assertEqual(env["YANSU_AGENT_HOME"], str(home))
        # Real child/grandchild processes inherit exactly the same workspace as
        # the detached updater and relaunched app, and actually write there.
        child = (
            "import json,os,tempfile; from pathlib import Path; "
            "cache=Path(tempfile.mkdtemp()); (cache/'keep').write_text('temp'); "
            "data=Path(os.environ['YANSU_AGENT_HOME']); (data/'keep').write_text('data'); "
            "bun=Path(os.environ['BUN_INSTALL_CACHE_DIR']); (bun/'bun-cache').write_text('cache'); "
            "print(json.dumps([tempfile.gettempdir(),str(data),str(bun)]))"
        )
        result = subprocess.run(
            [
                sys.executable,
                "-c",
                "import subprocess,sys; subprocess.run([sys.executable, '-c', sys.argv[1]], check=True)",
                child,
            ],
            env=env,
            check=True,
            capture_output=True,
            text=True,
        )
        self.assertEqual(
            json.loads(result.stdout), [str(app_tmp), str(home), str(app_tmp)]
        )
        self.assertEqual(before, self.snapshot())

    def test_no_writes_to_existing_or_absent_live_homes_and_case_aliases(self):
        for name in (".yansu-agent", ".isoform-agent"):
            for spelling in (name, name.upper(), name.title()):
                for exists in (False, True):
                    with self.subTest(name=name, spelling=spelling, exists=exists):
                        live = self.home / name
                        alias = self.home / spelling
                        if exists:
                            live.mkdir()
                            (live / "keep").write_text("live sentinel")
                            # Also cover distinct case-sensitive directories;
                            # allocation must ignore these spellings everywhere.
                            if not alias.exists():
                                alias.mkdir()
                                (alias / "keep").write_text("alias sentinel")
                        self.exercise_inherited_destination(alias)
                        self.exercise_inherited_destination(
                            alias / "new-parent" / "evidence"
                        )
                        distinct = alias.exists() and not alias.samefile(live)
                        if live.exists():
                            shutil.rmtree(live)
                        if distinct:
                            shutil.rmtree(alias)

    def test_symlinked_live_homes_and_aliases_receive_no_writes(self):
        for name in (".yansu-agent", ".isoform-agent"):
            with self.subTest(name=name):
                target = self.base / (name + "-external")
                target.mkdir()
                (target / "keep").write_text("live sentinel")
                (self.home / name).symlink_to(target, target_is_directory=True)
                alias = self.base / (name + "-alias")
                alias.symlink_to(target, target_is_directory=True)
                self.exercise_inherited_destination(alias / "new-parent" / "evidence")

    def test_applications_symlink_into_either_live_home_is_ignored(self):
        applications = self.home / "Applications"
        for name in (".yansu-agent", ".isoform-agent"):
            for exists in (False, True):
                with self.subTest(name=name, exists=exists):
                    live = self.home / name
                    if exists:
                        live.mkdir()
                    applications.symlink_to(live, target_is_directory=True)
                    self.exercise_inherited_destination(
                        applications / "new-parent" / "evidence"
                    )
                    applications.unlink()

    def test_caller_cannot_choose_evidence_or_working_directory(self):
        script = Path(__file__).with_name("update_dry_run.py")
        for option in ("--evidence-dir", "--work-dir", "--working-dir", "--tmpdir"):
            for name in (".yansu-agent", ".isoform-agent"):
                for spelling in (name, name.upper()):
                    for exists in (False, True):
                        with self.subTest(
                            option=option, spelling=spelling, exists=exists
                        ):
                            live = self.home / name
                            if exists:
                                live.mkdir()
                                (live / "keep").write_text("live sentinel")
                            before = self.snapshot()
                            result = subprocess.run(
                                [
                                    sys.executable,
                                    str(script),
                                    str(self.apps[0]),
                                    str(self.apps[1]),
                                    "feed.json",
                                    option,
                                    str(
                                        self.home / spelling / "new-parent" / "evidence"
                                    ),
                                ],
                                capture_output=True,
                                text=True,
                            )
                            self.assertEqual(result.returncode, 2)
                            self.assertIn(
                                "unrecognized arguments: " + option, result.stderr
                            )
                            self.assertEqual(before, self.snapshot())
                            if exists:
                                shutil.rmtree(live)

    def test_working_directory_does_not_choose_workspace(self):
        previous = Path.cwd()
        try:
            for name in (".yansu-agent", ".isoform-agent"):
                live = self.home / name
                live.mkdir()
                os.chdir(live)
                before = self.snapshot()
                self.allocate()
                self.assertEqual(before, self.snapshot())
        finally:
            os.chdir(previous)

    def test_fresh_workspace_for_each_run(self):
        first, first_tmp = self.allocate()
        second, second_tmp = self.allocate()
        self.assertNotEqual(first_tmp, second_tmp)
        self.assertFalse(overlaps(first, second))

    def test_input_bundle_overlap_and_symlink_alias_rejected(self):
        for app in self.apps:
            alias = self.base / (app.name + "-alias")
            alias.symlink_to(app, target_is_directory=True)
            for input_app in (app, alias):
                with self.subTest(input_app=input_app):
                    # Supply a real existing allocation beneath an input bundle
                    # to exercise the guard before any workspace/copy write.
                    allocated = Path(tempfile.mkdtemp(dir=app))
                    with patch(
                        "update_dry_run.tempfile.mkdtemp", return_value=str(allocated)
                    ) as allocate:
                        with self.assertRaisesRegex(
                            CheckFailed, "separate from input apps"
                        ):
                            create_workspace((input_app,))
                        allocate.assert_called_once()
                    self.assertFalse(allocated.exists())
                    self.assertEqual((app / "keep").read_text(), "input sentinel")
        for parent in (Path("/tmp"), Path("/tmp").resolve(), Path("/")):
            with patch("update_dry_run.tempfile.mkdtemp") as allocate:
                with self.assertRaises(CheckFailed):
                    create_workspace((parent,))
                allocate.assert_not_called()

    def test_symlink_allocation_rejected_without_touching_target(self):
        alias = self.base / "allocated-alias"
        alias.symlink_to(self.apps[0], target_is_directory=True)
        before = self.snapshot()
        with patch("update_dry_run.tempfile.mkdtemp", return_value=str(alias)):
            with self.assertRaisesRegex(CheckFailed, "symlink"):
                create_workspace(self.apps)
        self.assertEqual(before, self.snapshot())

    def test_overlap_uses_existing_ancestor_identity_in_both_directions(self):
        app = self.apps[0]
        alias = self.base / "app-alias"
        alias.symlink_to(app, target_is_directory=True)
        self.assertTrue(alias.samefile(app))
        missing = alias / "new-parent" / "evidence"
        self.assertTrue(overlaps(missing, app))
        self.assertTrue(overlaps(app, missing))
        self.assertFalse(overlaps(self.base / "safe-evidence", app))
        self.assertFalse(missing.exists())

    def test_case_aliased_input_bundles_and_ancestors_overlap(self):
        probe = self.base / "case-probe"
        probe.mkdir()
        alias = self.base / "CASE-PROBE"
        if not alias.exists() or not alias.samefile(probe):
            self.skipTest("requires a case-insensitive filesystem")
        for app in self.apps:
            alias = app.with_name(app.name.upper())
            self.assertTrue(alias.samefile(app))
            for missing in (alias, alias / "new-parent" / "evidence"):
                self.assertTrue(overlaps(app, missing))
                self.assertTrue(overlaps(missing, app))
        self.assertTrue(
            overlaps(self.base.with_name(self.base.name.upper()), self.apps[0])
        )
        self.assertFalse(overlaps(self.base / "safe-sibling", self.apps[0]))

    def test_distinct_case_sensitive_input_directories_do_not_overlap(self):
        distinct = self.apps[0].with_name(self.apps[0].name.upper())
        if distinct.exists():
            self.skipTest("requires a case-sensitive filesystem")
        distinct.mkdir()
        self.assertFalse(distinct.samefile(self.apps[0]))
        self.assertFalse(overlaps(distinct, self.apps[0]))

    def test_evidence_path_printed_even_when_verification_fails(self):
        root, app_tmp = self.allocate()
        with patch("update_dry_run.sys.platform", "darwin"), patch(
            "update_dry_run.sys.argv",
            ["update_dry_run.py", *(str(app) for app in self.apps), "feed.json"],
        ), patch(
            "update_dry_run.metadata",
            side_effect=[("0.1.0", "Yansu"), ("0.2.0", "Yansu")],
        ), patch(
            "update_dry_run.create_workspace", return_value=(root, app_tmp)
        ), patch(
            "update_dry_run.run_update", side_effect=CheckFailed("verification failed")
        ), patch(
            "builtins.print"
        ) as output:
            with self.assertRaisesRegex(CheckFailed, "verification failed"):
                main()
            output.assert_called_once_with(f"Evidence: {root}", flush=True)


class FeedTests(unittest.TestCase):
    def test_renders_supplied_local_feed(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "version.json"
            manifest = {
                "version": "0.2.0",
                "release_notes": "runner evidence",
                "updater_assets": {"darwin-arm64": "@ARCHIVE_URL@"},
                "updater_checksums": {"darwin-arm64": "@SHA256@"},
            }
            path.write_text(json.dumps(manifest))
            got = render_feed(
                path, "0.2.0", "http://127.0.0.1:1234/archive", "a" * 64, "darwin-arm64"
            )
            self.assertEqual(got["release_notes"], "runner evidence")
            self.assertEqual(got["updater_checksums"]["darwin-arm64"], "a" * 64)
            with self.assertRaises(CheckFailed):
                render_feed(
                    path,
                    "0.3.0",
                    "http://127.0.0.1:1234/archive",
                    "a" * 64,
                    "darwin-arm64",
                )
            with self.assertRaises(CheckFailed):
                render_feed(
                    path,
                    "0.2.0",
                    "http://127.0.0.1:1234/archive",
                    "a" * 64,
                    "darwin-x64",
                )
            manifest["updater_assets"][
                "darwin-arm64"
            ] = "https://external.example/archive"
            path.write_text(json.dumps(manifest))
            with self.assertRaises(CheckFailed):
                render_feed(
                    path,
                    "0.2.0",
                    "http://127.0.0.1:1234/archive",
                    "a" * 64,
                    "darwin-arm64",
                )
