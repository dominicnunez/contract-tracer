#!/usr/bin/env python3
"""Require pre-push checks to run on the exact, clean revision being pushed."""

from __future__ import annotations

import os
import re
import subprocess
import sys
from dataclasses import dataclass
from typing import Callable


def state_errors(to_ref: str, head: str, porcelain_status: str) -> list[str]:
    errors: list[str] = []
    if to_ref and to_ref != head:
        errors.append(
            f"push source {to_ref} does not match checked-out HEAD {head}; "
            "check out the revision being pushed and rerun the checks"
        )
    if porcelain_status.strip():
        errors.append(
            "the worktree has staged, unstaged, or untracked files; "
            "commit or remove them before pushing"
        )
    return errors


@dataclass(frozen=True)
class PushUpdate:
    local_ref: str
    local_oid: str
    remote_ref: str
    remote_oid: str

    @property
    def is_deletion(self) -> bool:
        return self.local_oid == "0" * len(self.local_oid)


def parse_push_updates(push_input: str, oid_length: int) -> tuple[list[PushUpdate], list[str]]:
    if oid_length <= 0:
        return [], ["cannot validate push updates without a valid object ID length"]
    if push_input == "":
        return [], []

    zeros = "0" * oid_length
    oid_pattern = re.compile(rf"[0-9a-fA-F]{{{oid_length}}}")
    updates: list[PushUpdate] = []
    errors: list[str] = []
    for line_number, line in enumerate(push_input.splitlines(), 1):
        fields = line.split()
        if len(fields) != 4:
            errors.append(f"malformed pre-push input on line {line_number}; expected four fields")
            continue
        local_ref, local_oid, remote_ref, remote_oid = fields
        if not remote_ref.startswith("refs/"):
            errors.append(f"malformed remote ref on pre-push input line {line_number}: {remote_ref!r}")
            continue
        if oid_pattern.fullmatch(local_oid) is None or oid_pattern.fullmatch(remote_oid) is None:
            errors.append(f"malformed object ID on pre-push input line {line_number}")
            continue
        is_deletion = local_oid.casefold() == zeros
        if (is_deletion and local_ref != "(delete)") or (not is_deletion and local_ref == "(delete)"):
            errors.append(f"inconsistent deletion marker on pre-push input line {line_number}")
            continue
        updates.append(PushUpdate(local_ref, local_oid.casefold(), remote_ref, remote_oid.casefold()))
    return updates, errors


def validate_push_updates(
    updates: list[PushUpdate],
    head: str,
    resolve_commit: Callable[[str], str | None],
) -> tuple[list[str], bool]:
    errors: list[str] = []
    has_updates = False
    for update in updates:
        if update.is_deletion:
            continue
        has_updates = True
        commit = resolve_commit(update.local_oid)
        if commit is None:
            errors.append(
                f"pushed object for {update.remote_ref} does not resolve to a commit; "
                "push only commit refs or tags that point to a commit"
            )
        elif commit.casefold() != head.casefold():
            errors.append(
                f"pushed ref {update.local_ref} ({update.remote_ref}) resolves to {commit}, "
                f"not checked-out HEAD {head}; check out the revision being pushed and rerun the checks"
            )
    return errors, has_updates


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


def main() -> int:
    try:
        head = git_text("rev-parse", "HEAD")
        status = git_text("status", "--porcelain", "--untracked-files=all")
    except (OSError, RuntimeError) as exc:
        print(f"cannot verify push state: {exc}", file=sys.stderr)
        return 2

    errors = state_errors(os.environ.get("PRE_COMMIT_TO_REF", ""), head, status)
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
