#!/usr/bin/env python3
"""Require pre-push checks to run on the exact, clean revision being pushed."""

from __future__ import annotations

import os
import subprocess
import sys


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
