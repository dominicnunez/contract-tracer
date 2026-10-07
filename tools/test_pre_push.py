import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import venv

from install_hooks import (
    PRE_COMMIT_MARKER,
    PRE_PUSH_MARKER,
    hooks_directory,
    install_hooks,
    install_pre_push_hook,
    render_pre_push_hook,
)


def git(cwd, *args, input_text=None, check=True, env=None):
    result = subprocess.run(
        ["git", *args],
        cwd=cwd,
        input=input_text,
        check=False,
        capture_output=True,
        text=True,
        env=env,
    )
    if check and result.returncode:
        raise AssertionError(f"git {' '.join(args)} failed: {result.stderr}\n{result.stdout}")
    return result


class PrePushHookTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="contract-tracer-push-test-")
        self.addCleanup(self.temporary.cleanup)
        root = Path(self.temporary.name)
        self.work = root / "working copy"
        self.remote = root / "remote.git"
        self.stub = root / "stub"
        self.log = root / "pre-commit.log"
        git(root, "init", "--bare", "--initial-branch=main", str(self.remote))
        git(root, "--git-dir", str(self.remote), "config", "receive.denyDeleteCurrent", "ignore")
        git(root, "init", "--initial-branch=main", str(self.work))
        git(self.work, "config", "user.name", "Hook Test")
        git(self.work, "config", "user.email", "hook-test@example.invalid")
        (self.work / "source.txt").write_text("first\n", encoding="utf-8")
        git(self.work, "add", "source.txt")
        git(self.work, "commit", "-m", "first")
        self.first_commit = git(self.work, "rev-parse", "HEAD").stdout.strip()
        git(self.work, "remote", "add", "origin", str(self.remote))
        git(self.work, "push", "origin", "main")
        (self.work / "source.txt").write_text("second\n", encoding="utf-8")
        git(self.work, "commit", "-am", "second")
        self.head = git(self.work, "rev-parse", "HEAD").stdout.strip()
        git(self.work, "branch", "older", self.first_commit)
        git(self.work, "tag", "current-expression", "HEAD~0")

        pre_commit_stub = self.stub / "pre_commit"
        pre_commit_stub.mkdir(parents=True)
        (pre_commit_stub / "__init__.py").write_text("", encoding="utf-8")
        (pre_commit_stub / "__main__.py").write_text(
            "import json, os, pathlib, sys\n"
            "record = {'args': sys.argv[1:], 'to_ref': os.environ.get('PRE_COMMIT_TO_REF'), 'cwd': os.getcwd()}\n"
            "with pathlib.Path(os.environ['PRE_COMMIT_STUB_LOG']).open('a', encoding='utf-8') as stream:\n"
            "    stream.write(json.dumps(record) + '\\n')\n"
            "target = os.environ.get('PRE_COMMIT_STUB_CHECKOUT')\n"
            "if target:\n"
            "    import subprocess\n"
            "    subprocess.run(['git', 'checkout', '--detach', target], check=True)\n"
            "late_file = os.environ.get('PRE_COMMIT_STUB_WRITE_FILE')\n"
            "if late_file:\n"
            "    pathlib.Path(late_file).write_text('late change\\n', encoding='utf-8')\n",
            encoding="utf-8",
        )
        self.environment = os.environ.copy()
        self.environment["PYTHONPATH"] = str(self.stub) + os.pathsep + self.environment.get("PYTHONPATH", "")
        self.environment["PRE_COMMIT_STUB_LOG"] = str(self.log)
        self.hook = install_pre_push_hook(
            self.work,
            sys.executable,
            Path(__file__).with_name("pre_push.py"),
        )

    def push(self, *refspecs):
        return git(self.work, "push", "origin", *refspecs, check=False, env=self.environment)

    def log_records(self):
        if not self.log.exists():
            return []
        return [json.loads(line) for line in self.log.read_text(encoding="utf-8").splitlines()]

    def assert_remote_missing(self, ref):
        result = git(self.remote.parent, "--git-dir", str(self.remote), "show-ref", "--verify", "--quiet", ref, check=False)
        self.assertNotEqual(result.returncode, 0, f"unexpected remote ref {ref}")

    def test_installed_hook_validates_every_ref_before_running_precommit(self):
        orders = (
            ("main:refs/heads/head-first", "older:refs/heads/old-second"),
            ("older:refs/heads/old-first", "main:refs/heads/head-second"),
        )
        for refspecs in orders:
            result = self.push(*refspecs)
            output = result.stdout + result.stderr
            self.assertNotEqual(result.returncode, 0, output)
            self.assertIn("not checked-out HEAD", output)
            self.assertEqual(self.log_records(), [])
        for ref in (
            "refs/heads/head-first",
            "refs/heads/old-second",
            "refs/heads/old-first",
            "refs/heads/head-second",
        ):
            self.assert_remote_missing(ref)

        result = self.push("main:refs/heads/single-head")
        self.assertEqual(result.returncode, 0, result.stderr)
        records = self.log_records()
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["args"], ["run", "--all-files", "--hook-stage", "pre-push"])
        self.assertEqual(records[0]["to_ref"], self.head)

        result = self.push("main:refs/heads/multiple-a", "main:refs/heads/multiple-b")
        self.assertEqual(result.returncode, 0, result.stderr)
        records = self.log_records()
        self.assertEqual(len(records), 2, "multiple valid refs should run validation exactly once")
        self.assertEqual(records[-1]["to_ref"], self.head)

        result = self.push(
            f"{self.head}:refs/heads/by-sha",
            "HEAD~0:refs/heads/by-expression",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.log_records()), 3)

    def test_hook_resolves_lightweight_and_annotated_tags_to_commits(self):
        git(self.work, "tag", "lightweight-head", self.head)
        git(self.work, "tag", "--annotate", "annotated-head", self.head, "--message", "release")
        result = self.push("refs/tags/lightweight-head", "refs/tags/annotated-head")
        self.assertEqual(result.returncode, 0, result.stderr)
        records = self.log_records()
        self.assertEqual(len(records), 1)

        git(self.work, "tag", "old-commit", self.first_commit)
        result = self.push("refs/tags/old-commit")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not checked-out HEAD", result.stdout + result.stderr)
        self.assertEqual(len(self.log_records()), 1)
        self.assert_remote_missing("refs/tags/old-commit")

        blob = git(self.work, "hash-object", "-w", "--stdin", input_text="not a commit\n").stdout.strip()
        git(self.work, "tag", "--annotate", "blob-object", blob, "--message", "invalid target")
        result = self.push("refs/tags/blob-object")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("does not resolve to a commit", result.stdout + result.stderr)
        self.assertEqual(len(self.log_records()), 1)

    def test_dirty_tree_blocks_updates_but_deletions_skip_checks(self):
        result = self.push("main:refs/heads/dirty-control")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.log_records()), 1)

        (self.work / "untracked.txt").write_text("dirty\n", encoding="utf-8")
        result = self.push("main:refs/heads/dirty-rejected")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("untracked files", result.stdout + result.stderr)
        self.assertEqual(len(self.log_records()), 1)

        result = self.push(":refs/heads/dirty-control")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.log_records()), 1, "deletion-only push must skip validation")
        self.assert_remote_missing("refs/heads/dirty-control")
        self.assert_remote_missing("refs/heads/dirty-rejected")

    def test_deletion_from_unborn_worktree_skips_head_lookup_and_checks(self):
        unborn = self.work.parent / "unborn"
        git(unborn.parent, "init", "--initial-branch=main", str(unborn))
        git(unborn, "remote", "add", "origin", str(self.remote))
        install_pre_push_hook(unborn, sys.executable, Path(__file__).with_name("pre_push.py"))

        result = git(unborn, "push", "origin", ":refs/heads/main", check=False, env=self.environment)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.log_records(), [])
        self.assert_remote_missing("refs/heads/main")

    def test_malformed_wrapper_input_fails_before_validation(self):
        result = subprocess.run(
            [sys.executable, str(Path(__file__).with_name("pre_push.py")), "origin", str(self.remote)],
            cwd=self.work,
            input="not four fields\n",
            check=False,
            capture_output=True,
            text=True,
            env=self.environment,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("malformed pre-push input", result.stdout + result.stderr)
        self.assertEqual(self.log_records(), [])

    def test_runner_cannot_change_checked_out_head_before_push_continues(self):
        self.environment["PRE_COMMIT_STUB_CHECKOUT"] = self.first_commit

        result = self.push("main:refs/heads/runner-changed-head")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("does not match checked-out HEAD", result.stdout + result.stderr)
        self.assertEqual(len(self.log_records()), 1)
        self.assert_remote_missing("refs/heads/runner-changed-head")

    def test_runner_cannot_leave_worktree_dirty_before_push_continues(self):
        late_file = self.work / "late-untracked.txt"
        self.environment["PRE_COMMIT_STUB_WRITE_FILE"] = str(late_file)

        result = self.push("main:refs/heads/runner-dirtied-tree")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("worktree has staged, unstaged, or untracked files", result.stdout + result.stderr)
        self.assertTrue(late_file.exists())
        self.assertEqual(len(self.log_records()), 1)
        self.assert_remote_missing("refs/heads/runner-dirtied-tree")

    def test_installed_hook_runs_with_selected_pip_free_venv(self):
        venv_root = self.work.parent / "isolated venv"
        venv.EnvBuilder(with_pip=False).create(venv_root)
        python = venv_root / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
        purelib = subprocess.run(
            [str(python), "-c", "import sysconfig; print(sysconfig.get_paths()['purelib'])"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()
        log = self.work.parent / "selected-venv.json"
        Path(purelib, "pre_commit.py").write_text(
            "import json, os, pathlib, sys\n"
            "pathlib.Path(os.environ['SELECTED_VENV_LOG']).write_text(\n"
            "    json.dumps({'prefix': sys.prefix, 'args': sys.argv[1:]}), encoding='utf-8')\n",
            encoding="utf-8",
        )

        work = self.work.parent / "venv work"
        remote = self.work.parent / "venv remote.git"
        git(self.work.parent, "init", "--bare", "--initial-branch=main", str(remote))
        git(work.parent, "init", "--initial-branch=main", str(work))
        git(work, "config", "user.name", "Hook Test")
        git(work, "config", "user.email", "hook-test@example.invalid")
        (work / "tracked.txt").write_text("tracked\n", encoding="utf-8")
        git(work, "add", "tracked.txt")
        git(work, "commit", "-m", "fixture")
        git(work, "remote", "add", "origin", str(remote))
        git(work, "push", "origin", "main")
        install_pre_push_hook(work, str(python), Path(__file__).with_name("pre_push.py"))
        environment = os.environ.copy()
        environment.pop("PYTHONPATH", None)
        environment["SELECTED_VENV_LOG"] = str(log)

        result = git(work, "push", "origin", "main:refs/heads/venv-checked", check=False, env=environment)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        record = json.loads(log.read_text(encoding="utf-8"))
        self.assertEqual(Path(record["prefix"]).resolve(), venv_root.resolve())
        self.assertEqual(record["args"], ["run", "--all-files", "--hook-stage", "pre-push"])


class HookInstallerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="contract-tracer-hook-install-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        git(self.root, "init")
        self.hooks = hooks_directory(self.root)

    def test_installer_preserves_unknown_hook(self):
        hook = self.hooks / "pre-push"
        hook.parent.mkdir(parents=True, exist_ok=True)
        custom = b"#!/bin/sh\n# user custom hook\nexit 42\n"
        hook.write_bytes(custom)
        with self.assertRaisesRegex(RuntimeError, "refusing to replace unrecognized hook"):
            install_pre_push_hook(self.root, sys.executable, Path(__file__).with_name("pre_push.py"))
        self.assertEqual(hook.read_bytes(), custom)

    def test_installer_replaces_a_precommit_managed_push_hook(self):
        hook = self.hooks / "pre-push"
        hook.parent.mkdir(parents=True, exist_ok=True)
        hook.write_bytes(b"#!/bin/sh\n# File generated by pre-commit: https://pre-commit.com\nexit 0\n")
        install_pre_push_hook(self.root, sys.executable, Path(__file__).with_name("pre_push.py"))
        contents = hook.read_bytes()
        self.assertIn(PRE_PUSH_MARKER, contents)
        self.assertNotIn(PRE_COMMIT_MARKER, contents)

    def test_installer_refuses_legacy_hooks_before_mutating_either_hook(self):
        self.hooks.mkdir(parents=True, exist_ok=True)
        pre_commit_hook = self.hooks / "pre-commit"
        pre_push_hook = self.hooks / "pre-push"
        pre_commit_hook.write_bytes(b"#!/bin/sh\n# File generated by pre-commit\nexit 0\n")
        pre_push_hook.write_bytes(b"#!/bin/sh\n# user hook\nexit 42\n")
        legacy = self.hooks / "pre-commit.legacy"
        legacy_contents = b"#!/bin/sh\n# preserved legacy hook\nexit 9\n"
        legacy.write_bytes(legacy_contents)
        before = (pre_commit_hook.read_bytes(), pre_push_hook.read_bytes(), legacy.read_bytes())

        with self.assertRaisesRegex(RuntimeError, "legacy hook"):
            install_hooks(self.root, sys.executable)

        self.assertEqual(
            (pre_commit_hook.read_bytes(), pre_push_hook.read_bytes(), legacy.read_bytes()),
            before,
        )

    def test_installer_preserves_unrecognized_commit_hook_and_existing_push_hook(self):
        self.hooks.mkdir(parents=True, exist_ok=True)
        pre_commit_hook = self.hooks / "pre-commit"
        pre_push_hook = self.hooks / "pre-push"
        custom_commit = b"#!/bin/sh\n# custom commit hook\nexit 31\n"
        managed_push = b"#!/bin/sh\n# Managed by Contract Tracer: multi-ref pre-push validation\nexit 0\n"
        pre_commit_hook.write_bytes(custom_commit)
        pre_push_hook.write_bytes(managed_push)

        with self.assertRaisesRegex(RuntimeError, "unrecognized hook"):
            install_hooks(self.root, sys.executable)

        self.assertEqual(pre_commit_hook.read_bytes(), custom_commit)
        self.assertEqual(pre_push_hook.read_bytes(), managed_push)

    def test_push_legacy_hook_is_not_discarded(self):
        hook = self.hooks / "pre-push"
        hook.parent.mkdir(parents=True, exist_ok=True)
        hook.write_bytes(b"#!/bin/sh\n# File generated by pre-commit\nexit 0\n")
        legacy = self.hooks / "pre-push.legacy"
        legacy_contents = b"#!/bin/sh\n# user legacy hook\nexit 17\n"
        legacy.write_bytes(legacy_contents)
        before = (hook.read_bytes(), legacy.read_bytes())

        with self.assertRaisesRegex(RuntimeError, "legacy hook"):
            install_pre_push_hook(self.root, sys.executable, Path(__file__).with_name("pre_push.py"))

        self.assertEqual((hook.read_bytes(), legacy.read_bytes()), before)

    @unittest.skipIf(os.name == "nt", "symlink executable paths are covered on POSIX")
    def test_generated_hook_preserves_selected_python_symlink(self):
        executable_link = self.root / "venv" / "bin" / "python"
        executable_link.parent.mkdir(parents=True)
        executable_link.symlink_to(sys.executable)

        hook = render_pre_push_hook(str(executable_link), Path(__file__).with_name("pre_push.py"))

        self.assertIn(str(executable_link).encode(), hook)
        self.assertNotIn(str(Path(sys.executable).resolve()).encode(), hook)


if __name__ == "__main__":
    unittest.main()
