#!/usr/bin/env python3
"""Check that staged Go files are already gofmt-formatted."""

from __future__ import annotations

import subprocess
import sys


def main_for_files(files: list[str]) -> int:
    go_files = [name for name in files if name.endswith(".go")]
    if not go_files:
        return 0

    try:
        result = subprocess.run(
            ["gofmt", "-l", *go_files],
            check=False,
            capture_output=True,
            text=True,
        )
    except OSError as exc:
        print(f"cannot run gofmt: {exc}", file=sys.stderr)
        return 2

    if result.returncode != 0:
        if result.stderr:
            print(result.stderr, file=sys.stderr, end="")
        return result.returncode
    unformatted = [line for line in result.stdout.splitlines() if line]
    if unformatted:
        print("Go files need gofmt:", file=sys.stderr)
        for name in unformatted:
            print(f"  {name}", file=sys.stderr)
        return 1
    return 0


def main() -> int:
    return main_for_files(sys.argv[1:])


if __name__ == "__main__":
    raise SystemExit(main())
