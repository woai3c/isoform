#!/usr/bin/env python3
"""Select installed App Store profiles and add manual export options."""
import datetime
import glob
import hashlib
import os
import plistlib
import re
import subprocess
import sys
from pathlib import Path


def fail(message):
    sys.exit(f"Manual signing: {message}")


options_path, team = sys.argv[1:]
try:
    identities = subprocess.check_output(
        ["security", "find-identity", "-v", "-p", "codesigning"], text=True
    )
except subprocess.CalledProcessError:
    fail("cannot list local signing identities")
installed = set(re.findall(r'\b([A-F0-9]{40}) "Apple Distribution:', identities))
requested_cert = os.environ.get("ISOFORM_IOS_CERTIFICATE", "").upper()
if requested_cert and not re.fullmatch(r"[A-F0-9]{40}", requested_cert):
    fail("ISOFORM_IOS_CERTIFICATE must be a distribution certificate SHA-1 fingerprint")
if requested_cert:
    installed &= {requested_cert}
if not installed:
    fail("no matching local Apple Distribution signing identity")

bundles = {
    "com.isoform.companion": "ISOFORM_IOS_APP_PROFILE",
    "com.isoform.companion.widget": "ISOFORM_IOS_WIDGET_PROFILE",
    "com.isoform.companion.notificationservice": "ISOFORM_IOS_NOTIFICATION_PROFILE",
}
profiles = {bundle: [] for bundle in bundles}
paths = set()
for directory in (
    "Library/MobileDevice/Provisioning Profiles",
    "Library/Developer/Xcode/UserData/Provisioning Profiles",
):
    paths.update(glob.glob(str(Path.home() / directory / "*.mobileprovision")))
now = datetime.datetime.now(datetime.timezone.utc).replace(tzinfo=None)
for path in sorted(paths):
    try:
        raw = subprocess.check_output(
            ["security", "cms", "-D", "-i", path], stderr=subprocess.DEVNULL
        )
        profile = plistlib.loads(raw)
        entitlements = profile["Entitlements"]
        app_id = entitlements.get("application-identifier", "")
        bundle = next((b for b in bundles if app_id == team + "." + b), None)
        if (
            not bundle
            or team not in profile.get("TeamIdentifier", [])
            or profile["ExpirationDate"] <= now
            or profile.get("ProvisionedDevices")
            or profile.get("ProvisionsAllDevices")
            or entitlements.get("get-task-allow") is not False
        ):
            continue
        selector = os.environ.get(bundles[bundle], "")
        if selector and selector not in (profile.get("Name"), profile.get("UUID")):
            continue
        certs = {
            hashlib.sha1(cert).hexdigest().upper()
            for cert in profile.get("DeveloperCertificates", [])
        } & installed
        if certs:
            profiles[bundle].append(
                (profile["ExpirationDate"], profile["UUID"], profile["Name"], certs)
            )
    except (OSError, subprocess.CalledProcessError, ValueError, KeyError, TypeError):
        continue

for bundle, candidates in profiles.items():
    if not candidates:
        fail(
            f"no valid installed App Store profile for {bundle} matching {bundles[bundle]} and a local distribution identity"
        )
common = set.intersection(
    *(set.union(*(p[3] for p in candidates)) for candidates in profiles.values())
)
if len(common) != 1:
    fail(
        "profiles must share one local distribution identity; set ISOFORM_IOS_CERTIFICATE to its SHA-1 fingerprint"
    )
certificate = common.pop()
selected = {}
for bundle, candidates in profiles.items():
    candidates = [p for p in candidates if certificate in p[3]]
    _, uuid, name, _ = max(candidates, key=lambda p: (p[0], p[1]))
    selected[bundle] = uuid
    print(f"Manual profile {bundle}: {name} ({uuid})")
with open(options_path, "rb") as f:
    options = plistlib.load(f)
options.update(
    signingStyle="manual", signingCertificate=certificate, provisioningProfiles=selected
)
with open(options_path, "wb") as f:
    plistlib.dump(options, f)
