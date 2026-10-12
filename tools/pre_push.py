#!/usr/bin/env python3
"""Validate every ref in Git's pre-push input before running repository checks."""

from __future__ import annotations

import os
import subprocess
import sys

from check_push_state import parse_push_updates, state_errors, validate_push_updates


def git_text(*args: str) -> str:
    result = subprocess.run(
        ["git", *args],
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or f"git {' '.join(args)} failed")
    return result.stdout.strip()


def resolve_commit(oid: str) -> str | None:
    result = subprocess.run(
        ["git", "rev-parse", "--verify", f"{oid}^{{commit}}"],
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode:
        return None
    commit = result.stdout.strip()
    if not commit or any(character not in "0123456789abcdefABCDEF" for character in commit):
        return None
    return commit.casefold()


def reject(errors: list[str]) -> int:
    for error in errors:
        print(error, file=sys.stderr)
    return 1


def main() -> int:
    if len(sys.argv) != 3:
        print("pre-push hook requires Git's remote name and URL arguments", file=sys.stderr)
        return 2
    try:
        push_input = sys.stdin.read()
        object_format = git_text("rev-parse", "--show-object-format")
        oid_length = {"sha1": 40, "sha256": 64}.get(object_format)
        if oid_length is None:
            raise RuntimeError(f"unsupported Git object format: {object_format}")
        updates, errors = parse_push_updates(push_input, oid_length)
        if errors:
            return reject(errors)
        if not any(not update.is_deletion for update in updates):
            return 0
        head = git_text("rev-parse", "HEAD")
        errors, has_updates = validate_push_updates(updates, head, resolve_commit)
        if errors:
            return reject(errors)
        if not has_updates:
            return 0

        status = git_text("status", "--porcelain", "--untracked-files=all")
    except (OSError, RuntimeError) as exc:
        print(f"cannot verify push state: {exc}", file=sys.stderr)
        return 2

    errors = state_errors(head, head, status)
    if errors:
        return reject(errors)

    environment = os.environ.copy()
    environment["PRE_COMMIT_TO_REF"] = head
    try:
        result = subprocess.run(
            [sys.executable, "-m", "pre_commit", "run", "--all-files", "--hook-stage", "pre-push"],
            check=False,
            env=environment,
        )
    except OSError as exc:
        print(f"cannot run pre-push validation: {exc}", file=sys.stderr)
        return 2
    if result.returncode:
        return result.returncode

    try:
        checked_head = git_text("rev-parse", "HEAD")
        checked_status = git_text("status", "--porcelain", "--untracked-files=all")
    except (OSError, RuntimeError) as exc:
        print(f"cannot recheck push state after validation: {exc}", file=sys.stderr)
        return 2
    errors = state_errors(head, checked_head, checked_status)
    if errors:
        return reject(errors)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
