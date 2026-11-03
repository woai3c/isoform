#!/usr/bin/env python3
"""Run the full Go suite, retrying only the exact ledger flakes once in isolation."""

import json
import re
import subprocess
import sys

# docs/hub-reviews/desk-ledger.md sections 4 and the PR #29 trial.
# Package-qualified names prevent an unrelated same-name test being retried.
KNOWN_FLAKES = {
    ("wails-gui", "TestCleanupPlanChangesNothingAndRunMatchesIt"),
    ("wails-gui", "TestActivityTriagerDispatchesShadowBeforeLegacyTriageFailure"),
    ("yansu-agent/agent", "TestControlledClaudeIntegration"),
}


def completed_execution(events, expected_tests=None):
    terminal = {"pass", "fail", "skip"}
    started = {
        (e["Package"], e["Test"])
        for e in events
        if e.get("Action") == "run" and e.get("Test")
    }
    ended = {
        (e["Package"], e["Test"])
        for e in events
        if e.get("Action") in terminal and e.get("Test")
    }
    packages = {e["Package"] for e in events if e.get("Action") == "start"}
    package_results = {
        e["Package"]: e["Action"]
        for e in events
        if e.get("Action") in terminal and not e.get("Test")
    }
    failed_test_packages = {
        e["Package"] for e in events if e.get("Action") == "fail" and e.get("Test")
    }
    failed_packages = {p for p, action in package_results.items() if action == "fail"}
    if (
        not packages
        or started != ended
        or packages != package_results.keys()
        or failed_test_packages != failed_packages
        or any(
            e.get("Action") in {"build-fail", "invalid"} or e.get("FailedBuild")
            for e in events
        )
    ):
        return False
    if expected_tests is not None:
        expected = {(p, name) for p, names in expected_tests.items() for name in names}
        if packages != expected_tests.keys() or expected != {
            (p, name) for p, name in ended if "/" not in name
        }:
            return False
    return True


def retryable_failures(events, expected_tests=None):
    failed_tests = {
        (event["Package"], event["Test"].split("/")[0])
        for event in events
        if event.get("Action") == "fail" and event.get("Test")
    }
    failed_packages = {
        event["Package"]
        for event in events
        if event.get("Action") == "fail" and not event.get("Test")
    }
    # A panic, timeout, build failure or unknown failing test must remain red.
    abnormal = any(
        re.search(r"(?m)^(panic:|fatal error:)|\[build failed\]", e.get("Output", ""))
        for e in events
    )
    if (
        not completed_execution(events, expected_tests)
        or abnormal
        or not failed_tests
        or not failed_tests <= KNOWN_FLAKES
        or failed_packages != {package for package, _ in failed_tests}
    ):
        return []
    return sorted(failed_tests)


def run_go(arguments, show_output=True):
    events = []
    command = ["go", "test", "-json", "-count=1", *arguments]
    with subprocess.Popen(command, stdout=subprocess.PIPE, text=True) as process:
        for line in process.stdout:
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                # Malformed/missing harness output cannot establish completion.
                event = {"Action": "invalid", "Output": line}
            events.append(event)
            if show_output and "Output" in event:
                print(event["Output"], end="", flush=True)
        result = process.wait()
    return result, events


def main():
    result, events = run_go(["-timeout=45m", "./..."])
    if not completed_execution(events):
        print(
            "::error::Go test execution was incomplete or abnormal; refusing retries."
        )
        return result or 1
    if result == 0:
        return 0
    retries = retryable_failures(events)
    if not retries:
        return result

    # A terminal event for each started test does not prove that later tests
    # ever started. Enumerate the same packages before accepting flake recovery.
    listed_result, listed = run_go(
        ["-timeout=5m", "-list", "^(Test|Example|Fuzz)", "./..."], show_output=False
    )
    if listed_result or not completed_execution(listed):
        print("::error::Cannot enumerate the full Go suite; refusing retries.")
        for event in listed:
            if "Output" in event:
                print(event["Output"], end="")
        return result
    expected_tests = {e["Package"]: set() for e in listed if e.get("Action") == "start"}
    for event in listed:
        for name in event.get("Output", "").splitlines():
            if event.get("Package") in expected_tests and re.fullmatch(
                r"(?:Test|Example|Fuzz)\w*", name
            ):
                expected_tests[event["Package"]].add(name)
    if not retryable_failures(events, expected_tests):
        print("::error::Go suite did not complete all listed tests; refusing retries.")
        return result
    for package, test in retries:
        print(f"::warning::Retrying ledger flake once: {package}.{test}", flush=True)
        retry_result, retry_events = run_go(
            ["-timeout=5m", "-run", f"^{test}$", package]
        )
        if retry_result:
            return retry_result
        if not completed_execution(retry_events, {package: {test}}):
            print("::error::Go retry did not finish the requested test normally.")
            return result
    return 0


if __name__ == "__main__":
    sys.exit(main())
