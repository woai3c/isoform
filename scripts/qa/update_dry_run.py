#!/usr/bin/env python3
"""Exercise the actual running Wails App bindings through the updateqa hook."""

import argparse
import functools
import hashlib
import http.server
import json
import os
from pathlib import Path
import plistlib
import secrets
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request


class CheckFailed(RuntimeError):
    pass


def metadata(app):
    with (app / "Contents/Info.plist").open("rb") as source:
        info = plistlib.load(source)
    return info["CFBundleShortVersionString"], info["CFBundleExecutable"]


def request(endpoint, path, method="GET"):
    req = urllib.request.Request(
        endpoint["url"] + path,
        method=method,
        headers={"Authorization": "Bearer " + endpoint["token"]},
    )
    # Always talk directly to the loopback hook; no external proxy is involved.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(req, timeout=60) as response:
        return json.load(response)


def wait_status(endpoint_file, predicate, timeout):
    deadline = time.monotonic() + timeout
    last = "no endpoint"
    while time.monotonic() < deadline:
        try:
            endpoint = json.loads(endpoint_file.read_text())
            status = request(endpoint, "/status")
            last = status
            if predicate(status):
                return endpoint, status
        except (OSError, ValueError, urllib.error.URLError) as error:
            last = str(error)
        time.sleep(0.25)
    raise CheckFailed(f"Timed out waiting for app status: {last}")


def render_feed(template, version, archive_url, checksum, platform):
    """Keep the supplied feed's version and platform; replace explicit tokens."""
    source = template.read_text()
    source = source.replace("@ARCHIVE_URL@", archive_url).replace("@SHA256@", checksum)
    manifest = json.loads(source)
    if manifest.get("version", "").removeprefix("v") != version:
        raise CheckFailed("Feed version must match the second built app")
    if manifest.get("updater_assets", {}).get(platform) != archive_url:
        raise CheckFailed(f"Feed updater_assets.{platform} must be @ARCHIVE_URL@")
    if manifest.get("updater_checksums", {}).get(platform, "").lower() != checksum:
        raise CheckFailed(f"Feed updater_checksums.{platform} must be @SHA256@")
    return manifest


def overlaps(first, second):
    if first == second or first in second.parents or second in first.parents:
        return True
    # resolve() can preserve case aliases on macOS. Compare existing ancestor
    # identities in both directions, walking past missing descendant components.
    # Leave permission and other inspection errors to fail closed before mkdir.
    for parent, child in ((first, second), (second, first)):
        try:
            identity = parent.stat()
        except FileNotFoundError:
            continue
        for ancestor in (child, *child.parents):
            try:
                if os.path.samestat(identity, ancestor.stat()):
                    return True
            except FileNotFoundError:
                continue
    return False


def create_workspace(apps):
    # No caller-selected destination, HOME, Applications, inherited TMPDIR or
    # cached tempfile default participates in allocation. /tmp is canonicalized
    # for macOS's /tmp -> /private/tmp alias before checking input-app overlap.
    parent = Path("/tmp").resolve(strict=True)
    apps = tuple(app.resolve(strict=True) for app in apps)
    if any(app == parent or app in parent.parents for app in apps):
        raise CheckFailed("QA temporary directory must be separate from input apps")
    app_tmp = Path(tempfile.mkdtemp(prefix="yansu-update-qa-", dir=parent))
    try:
        if app_tmp.is_symlink():
            raise CheckFailed("QA temporary directory must not be a symlink")
        app_tmp = app_tmp.resolve(strict=True)
        if any(overlaps(app_tmp, app) for app in apps):
            raise CheckFailed("QA temporary directory must be separate from input apps")
        root = Path(tempfile.mkdtemp(prefix="workspace-", dir=app_tmp))
        if root.is_symlink():
            raise CheckFailed("QA workspace must not be a symlink")
        return root.resolve(strict=True), app_tmp
    except BaseException:
        # Remove only an empty directory allocated by this run, never inputs.
        if not app_tmp.is_symlink():
            try:
                app_tmp.rmdir()
            except OSError:
                pass
        raise


def qa_environment(home, hook, feed, app_tmp):
    # The updater helper inherits this environment when reopening either app.
    # OpenCLI sets its Bun cache to os.TempDir(); also override the tool-level
    # variables so all temporary/cache writes stay in our owned directory.
    return dict(
        os.environ,
        YANSU_APP_FLAVOR="isoform",
        YANSU_AGENT_HOME=str(home),
        YANSU_UPDATE_QA_DIR=str(hook),
        YANSU_UPDATE_QA_FEED=feed,
        TMPDIR=str(app_tmp),
        TMP=str(app_tmp),
        TEMP=str(app_tmp),
        BUN_INSTALL_CACHE_DIR=str(app_tmp),
    )


def main():
    parser = argparse.ArgumentParser(
        description="Runner-only macOS update/relaunch QA; launches a disposable copy, never the supplied apps.",
        epilog="Both apps need the updateqa build tag, Developer ID signatures and stapled notarization. Evidence is retained in an owned temporary workspace; its path is printed at the end.",
    )
    parser.add_argument("old_app", type=Path)
    parser.add_argument("new_app", type=Path)
    parser.add_argument(
        "feed", type=Path, help="local version.json using @ARCHIVE_URL@ and @SHA256@"
    )
    parser.add_argument("--timeout", type=int, default=150)
    args = parser.parse_args()
    if sys.platform != "darwin":
        parser.error("run this script through the user's macOS app-runner loop")
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    old_app, new_app = args.old_app.resolve(strict=True), args.new_app.resolve(
        strict=True
    )
    old_version, old_executable = metadata(old_app)
    new_version, new_executable = metadata(new_app)
    old_parts, new_parts = tuple(map(int, old_version.split("."))), tuple(
        map(int, new_version.split("."))
    )
    if len(old_parts) != 3 or len(new_parts) != 3 or new_parts <= old_parts:
        parser.error("second app must have a newer stable X.Y.Z version")
    if old_executable != new_executable:
        parser.error("both versions must have the same bundle executable")
    try:
        root, app_tmp = create_workspace((old_app, new_app))
    except (CheckFailed, OSError, RuntimeError) as error:
        parser.error(str(error))
    try:
        run_update(
            args,
            old_app,
            new_app,
            old_version,
            new_version,
            old_executable,
            root,
            app_tmp,
        )
    finally:
        print(f"Evidence: {root}", flush=True)


def run_update(
    args, old_app, new_app, old_version, new_version, old_executable, root, app_tmp
):
    home, hook, served = root / "data", root / "hook", root / "feed"
    for folder in (home, hook, served):
        folder.mkdir(mode=0o700)
    env = qa_environment(home, hook, "", app_tmp)
    # Verify both input bundles before launching or copying either one.
    for app in (old_app, new_app):
        subprocess.run(
            ["/usr/bin/codesign", "--verify", "--deep", "--strict", str(app)],
            check=True,
            env=env,
        )
        subprocess.run(
            ["/usr/bin/xcrun", "stapler", "validate", str(app)], check=True, env=env
        )
        subprocess.run(
            ["/usr/sbin/spctl", "--assess", "--type", "execute", str(app)],
            check=True,
            env=env,
        )
    installed = root / "Isoform.app"
    subprocess.run(
        ["/usr/bin/ditto", str(old_app), str(installed)], check=True, env=env
    )
    archive = served / "update.app.tar.gz"
    subprocess.run(
        ["/usr/bin/tar", "-czf", str(archive), "-C", str(new_app.parent), new_app.name],
        check=True,
        env=env,
    )
    digest = hashlib.sha256()
    with archive.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(block)
    checksum = digest.hexdigest()
    handler = functools.partial(
        http.server.SimpleHTTPRequestHandler, directory=str(served)
    )
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    base_url = f"http://127.0.0.1:{server.server_port}"
    arch = "arm64" if os.uname().machine == "arm64" else "x64"
    feed = render_feed(
        args.feed,
        new_version,
        base_url + "/" + archive.name,
        checksum,
        "darwin-" + arch,
    )
    (served / "version.json").write_text(json.dumps(feed, indent=2) + "\n")
    marker = secrets.token_hex(32)
    (home / "update-qa-keep.txt").write_text(marker)
    env["YANSU_UPDATE_QA_FEED"] = base_url + "/version.json"
    endpoint_file = hook / "endpoint.json"
    log = (root / "runner.log").open("wb")
    process = None
    latest_endpoint = None
    evidence = {
        "old_version": old_version,
        "new_version": new_version,
        "data_home": str(home),
        "temp_dir": str(app_tmp),
        "result": "FAIL",
    }
    try:
        process = subprocess.Popen(
            [str(installed / "Contents/MacOS" / old_executable)],
            env=env,
            stdout=log,
            stderr=subprocess.STDOUT,
        )
        latest_endpoint, before = wait_status(
            endpoint_file,
            lambda s: s["version"] == old_version and s["pid"] == process.pid,
            args.timeout,
        )
        if before["temp_dir"] != str(app_tmp):
            raise CheckFailed(f"Initial app changed QA temporary directory: {before}")
        evidence["before"] = before
        info = request(latest_endpoint, "/check", "POST")
        if (
            not info.get("available")
            or info.get("error")
            or info["latest"] != new_version
            or info.get("download_url") != base_url + "/" + archive.name
        ):
            raise CheckFailed(f"CheckForUpdates rejected the local feed: {info}")
        evidence["check"] = info
        downloaded = request(latest_endpoint, "/download", "POST")
        if downloaded["status"] != "update-downloaded":
            raise CheckFailed(f"DownloadUpdate failed: {downloaded}")
        evidence["download"] = downloaded
        request(latest_endpoint, "/install", "POST")
        process.wait(timeout=args.timeout)
        latest_endpoint, after = wait_status(
            endpoint_file,
            lambda s: s["version"] == new_version and s["pid"] != before["pid"],
            args.timeout,
        )
        if after["bundle"] != str(installed) or after["data_home"] != str(home):
            raise CheckFailed(f"Relaunch changed bundle or data home: {after}")
        if after["temp_dir"] != str(app_tmp):
            raise CheckFailed(f"Relaunch changed QA temporary directory: {after}")
        if (home / "update-qa-keep.txt").read_text() != marker:
            raise CheckFailed("Data-home sentinel changed")
        deadline = time.monotonic() + 10
        while list(root.glob(".yansu-transaction-*")) and time.monotonic() < deadline:
            time.sleep(0.1)
        if list(root.glob(".yansu-transaction-*")):
            raise CheckFailed(
                "Startup was not acknowledged; previous bundle is still retained"
            )
        evidence.update(after=after, result="PASS")
        print(
            f"PASS: {old_version} -> {new_version}; new PID; same bundle and data home",
            flush=True,
        )
    finally:
        # Only the PID authenticated by this run's endpoint may be quit. Never
        # kill by process name or touch any existing installation/data home.
        try:
            if endpoint_file.exists():
                endpoint = json.loads(endpoint_file.read_text())
                request(endpoint, "/quit", "POST")
        except (OSError, ValueError, urllib.error.URLError):
            if process is not None and process.poll() is None:
                process.terminate()
        server.shutdown()
        server.server_close()
        log.close()
        (root / "result.json").write_text(json.dumps(evidence, indent=2) + "\n")


if __name__ == "__main__":
    try:
        main()
    except (CheckFailed, OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
