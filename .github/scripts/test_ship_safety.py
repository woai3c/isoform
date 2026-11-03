"""Publication guards must fail before invoking GitHub."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class ShipSafetyTests(unittest.TestCase):
    def test_notary_credential_order_and_failure(self):
        helper = ROOT / "scripts/release/notary-credentials.sh"
        for profile_ok, key_exists, key_ok, apple_id, expected in (
            (True, True, True, True, "profile"),
            (False, True, True, True, "key"),
            (False, True, False, True, "failure"),
            (False, False, True, True, "apple-id"),
            (False, False, True, False, "failure"),
        ):
            with self.subTest(
                expected=expected, profile_ok=profile_ok, key_exists=key_exists
            ), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                xcrun = root / "xcrun"
                xcrun.write_text(
                    "#!/bin/bash\n"
                    'printf "%s\\n" "$*" >> "$CALLS"\n'
                    'case "$*" in\n'
                    '  *--keychain-profile*) exit "$PROFILE_RESULT" ;;\n'
                    '  *--key-id*) exit "$KEY_RESULT" ;;\n'
                    "  *) exit 0 ;;\n"
                    "esac\n"
                )
                xcrun.chmod(0o755)
                key = root / "test-key.p8"
                if key_exists:
                    key.touch()
                env = {
                    "PATH": f"{root}{os.pathsep}{os.environ['PATH']}",
                    "HOME": str(root),
                    "CALLS": str(root / "calls"),
                    "PROFILE_RESULT": str(int(not profile_ok)),
                    "KEY_RESULT": str(int(not key_ok)),
                    "APPLE_NOTARY_KEY_PATH": str(key),
                    "APPLE_NOTARY_KEY_ID": "test-id",
                    "APPLE_NOTARY_ISSUER": "test-issuer",
                }
                if apple_id:
                    env.update(
                        APPLE_ID="test",
                        APPLE_APP_SPECIFIC_PASSWORD="test",
                        APPLE_TEAM_ID="test",
                    )
                result = subprocess.run(
                    [
                        "bash",
                        "-c",
                        '. "$1"; notary_credentials_available && notarytool_with_credentials log example',
                        "test",
                        str(helper),
                    ],
                    env=env,
                    capture_output=True,
                    text=True,
                    check=False,
                )
                self.assertEqual(result.returncode == 0, expected != "failure")
                calls = (root / "calls").read_text().splitlines()
                if expected != "failure":
                    flag = {
                        "profile": "--keychain-profile",
                        "key": "--key-id",
                        "apple-id": "--apple-id",
                    }[expected]
                    self.assertIn(flag, calls[-1])
                    self.assertIn("notarytool log example", calls[-1])
                elif key_exists:
                    self.assertIn("notary API key", result.stderr)
                    self.assertFalse(any("--apple-id" in call for call in calls))

    def test_notary_key_has_no_implicit_account_identifiers(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            xcrun = root / "xcrun"
            xcrun.write_text("#!/bin/bash\nexit 1\n")
            xcrun.chmod(0o755)
            env = {
                "PATH": f"{root}{os.pathsep}{os.environ['PATH']}",
                "HOME": str(root),
            }
            result = subprocess.run(
                [
                    "bash",
                    "-c",
                    '. "$1"; notary_credentials_available >/dev/null 2>&1; test -z "$APPLE_NOTARY_KEY_ID" && test -z "$APPLE_NOTARY_ISSUER"',
                    "test",
                    str(ROOT / "scripts/release/notary-credentials.sh"),
                ],
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_dry_run_and_publish_are_mutually_exclusive(self):
        result = subprocess.run(
            [
                str(ROOT / "scripts/release/ship.sh"),
                "1.0.0-beta.1",
                "--channel",
                "beta",
                "--dry-run",
                "--publish",
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("cannot be combined", result.stderr)

    def test_incomplete_rehearsal_cannot_be_published(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            marker = root / "gh-invoked"
            for name, body in {
                "uname": "#!/bin/sh\necho Darwin\n",
                "xcrun": "#!/bin/sh\nexit 0\n",
                "gh": f"#!/bin/sh\ntouch '{marker}'\nexit 0\n",
            }.items():
                executable = root / name
                executable.write_text(body)
                executable.chmod(0o755)
            (root / "1.0.0-beta.2").mkdir()
            env = {
                "PATH": f"{root}{os.pathsep}{os.environ['PATH']}",
                "HOME": str(root),
                "LANG": "C.UTF-8",
                "ISOFORM_RELEASE_OUTPUT_ROOT": str(root),
            }
            result = subprocess.run(
                [
                    str(ROOT / "scripts/release/ship.sh"),
                    "1.0.0-beta.2",
                    "--channel",
                    "beta",
                    "--publish",
                ],
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(marker.exists(), "incomplete rehearsal reached GitHub")

    def test_invalid_beta_feed_is_rejected_before_gh(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            marker = root / "gh-invoked"
            gh = root / "gh"
            gh.write_text(f"#!/bin/sh\ntouch '{marker}'\nexit 1\n")
            gh.chmod(0o755)
            feed = {
                "version": "1.0.0-beta.2",
                "updater_assets": {"darwin-arm64": "https://example.invalid/asset"},
                "updater_checksums": {"darwin-arm64": "a" * 64},
            }
            (root / "version.json").write_text(json.dumps(feed))
            (root / "checksums.txt").write_text("")
            env = {
                "PATH": f"{root}{os.pathsep}{os.environ['PATH']}",
                "HOME": str(root),
                "LANG": "C.UTF-8",
                "PYTHONOPTIMIZE": "1",
            }
            result = subprocess.run(
                [
                    str(ROOT / "scripts/release/publish-artifacts.sh"),
                    "1.0.0-beta.2",
                    "beta",
                    str(root),
                ],
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(marker.exists(), "invalid feed reached GitHub")

    def test_existing_rehearsal_is_reverified_before_publishing(self):
        for verify_result in (0, 1):
            with self.subTest(
                verify_result=verify_result
            ), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                repo = root / "repo"
                scripts = repo / "scripts" / "release"
                scripts.mkdir(parents=True)
                shutil.copy(ROOT / "scripts/release/ship.sh", scripts / "ship.sh")
                shutil.copy(
                    ROOT / "scripts/release/notary-credentials.sh",
                    scripts / "notary-credentials.sh",
                )
                shutil.copy(
                    ROOT / "scripts/release/posthog-env.sh", scripts / "posthog-env.sh"
                )
                verified = root / "verified"
                published = root / "published"
                for name, body in {
                    "verify-artifact.sh": f"#!/bin/sh\necho \"$5\" >> '{verified}'\nexit {verify_result}\n",
                    "publish-artifacts.sh": f"#!/bin/sh\ntouch '{published}'\n",
                }.items():
                    path = scripts / name
                    path.write_text(body)
                    path.chmod(0o755)
                for name, body in {
                    "uname": "#!/bin/sh\necho Darwin\n",
                    "xcrun": "#!/bin/sh\nexit 0\n",
                    "shasum": "#!/bin/sh\nexit 0\n",
                }.items():
                    path = root / name
                    path.write_text(body)
                    path.chmod(0o755)
                out = root / "output" / "1.0.0-beta.2"
                out.mkdir(parents=True)
                for arch in ("arm64", "x64"):
                    (out / f"build-{arch}.log").write_text(
                        "Isoform.app: submitted, submission id 1111-2222\nDMG: submitted, submission id 3333-4444\n"
                    )
                env = {
                    "PATH": f"{root}{os.pathsep}{os.environ['PATH']}",
                    "HOME": str(root),
                    "LANG": "C.UTF-8",
                    "ISOFORM_RELEASE_OUTPUT_ROOT": str(root / "output"),
                    "APPLE_SIGNING_IDENTITY": "Developer ID Application: Example",
                    "APPLE_TEAM_ID": "EXAMPLE123",
                }
                result = subprocess.run(
                    [
                        str(scripts / "ship.sh"),
                        "1.0.0-beta.2",
                        "--channel",
                        "beta",
                        "--publish",
                    ],
                    env=env,
                    capture_output=True,
                    text=True,
                    check=False,
                )
                if verify_result == 0:
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(
                        verified.read_text().splitlines(), ["arm64", "x64"]
                    )
                    self.assertTrue(published.exists())
                else:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertFalse(
                        published.exists(), "failed verification reached publisher"
                    )


if __name__ == "__main__":
    unittest.main()
