import unittest

from check_push_state import parse_push_updates, state_errors, validate_push_updates


class PushStateTests(unittest.TestCase):
    def test_clean_matching_push_revision_passes(self):
        self.assertEqual(state_errors("abc123", "abc123", ""), [])

    def test_wrong_push_revision_is_rejected(self):
        errors = state_errors("abc123", "def456", "")
        self.assertEqual(len(errors), 1)
        self.assertIn("does not match checked-out HEAD", errors[0])
        self.assertIn("check out the revision being pushed", errors[0])

    def test_dirty_tracked_or_untracked_worktree_is_rejected(self):
        errors = state_errors("abc123", "abc123", " M source.go\n?? new.go\n")
        self.assertEqual(len(errors), 1)
        self.assertIn("staged, unstaged, or untracked files", errors[0])

    def test_manual_pre_push_run_without_revision_still_checks_cleanliness(self):
        self.assertEqual(state_errors("", "abc123", ""), [])
        errors = state_errors("", "abc123", " M source.go")
        self.assertEqual(len(errors), 1)
        self.assertIn("worktree has", errors[0])

    def test_wrong_revision_and_dirty_tree_both_reported(self):
        errors = state_errors("abc123", "def456", "?? new.go")
        self.assertEqual(len(errors), 2)


class PushInputTests(unittest.TestCase):
    def setUp(self):
        self.head = "a" * 40

    def test_empty_and_deletion_only_inputs_skip_validation(self):
        updates, errors = parse_push_updates("", len(self.head))
        self.assertEqual((updates, errors), ([], []))
        updates, errors = parse_push_updates(
            f"(delete) {'0' * 40} refs/heads/remove {'b' * 40}\n",
            len(self.head),
        )
        self.assertEqual(errors, [])
        self.assertEqual(len(updates), 1)
        self.assertTrue(updates[0].is_deletion)
        validation_errors, has_updates = validate_push_updates(updates, self.head, lambda _: None)
        self.assertEqual(validation_errors, [])
        self.assertFalse(has_updates)

    def test_malformed_and_wrong_width_input_fails_closed(self):
        for text, oid_length in (("malformed input\n", 40), (f"refs/heads/main {self.head} refs/heads/main {'b' * 64}\n", 40)):
            with self.subTest(text=text):
                updates, errors = parse_push_updates(text, oid_length)
                self.assertEqual(updates, [])
                self.assertTrue(errors)

    def test_sha256_object_ids_use_their_native_width(self):
        head = "a" * 64
        zeros = "0" * 64
        updates, errors = parse_push_updates(
            f"HEAD~0 {head} refs/heads/sha256 {zeros}\n",
            len(head),
        )
        self.assertEqual(errors, [])
        self.assertEqual(len(updates), 1)
        validation_errors, has_updates = validate_push_updates(updates, head, lambda _: head)
        self.assertEqual(validation_errors, [])
        self.assertTrue(has_updates)

    def test_every_ref_must_resolve_to_head_independent_of_order(self):
        old = "b" * 40
        updates, errors = parse_push_updates(
            f"refs/heads/current {self.head} refs/heads/current {'0' * 40}\n"
            f"refs/heads/older {old} refs/heads/older {'0' * 40}\n",
            len(self.head),
        )
        self.assertEqual(errors, [])
        resolve = {self.head: self.head, old: old}.get
        forward_errors, has_updates = validate_push_updates(updates, self.head, resolve)
        reverse_errors, reverse_has_updates = validate_push_updates(list(reversed(updates)), self.head, resolve)
        self.assertTrue(has_updates)
        self.assertTrue(reverse_has_updates)
        self.assertEqual(len(forward_errors), 1)
        self.assertEqual(len(reverse_errors), 1)
        self.assertIn("not checked-out HEAD", forward_errors[0])
        self.assertIn("not checked-out HEAD", reverse_errors[0])

    def test_multiple_refs_to_head_and_tag_aliases_are_valid(self):
        tag_object = "c" * 40
        updates, errors = parse_push_updates(
            f"refs/heads/main {self.head} refs/heads/main {'0' * 40}\n"
            f"refs/tags/release {tag_object} refs/tags/release {'0' * 40}\n",
            len(self.head),
        )
        self.assertEqual(errors, [])
        resolve = {self.head: self.head, tag_object: self.head}.get
        validation_errors, has_updates = validate_push_updates(updates, self.head, resolve)
        self.assertEqual(validation_errors, [])
        self.assertTrue(has_updates)

    def test_noncommit_and_old_commit_refs_are_rejected(self):
        old = "b" * 40
        blob = "c" * 40
        for oid, resolved in ((old, old), (blob, None)):
            updates, errors = parse_push_updates(
                f"refs/heads/topic {oid} refs/heads/topic {'0' * 40}\n",
                len(self.head),
            )
            self.assertEqual(errors, [])
            validation_errors, has_updates = validate_push_updates(updates, self.head, lambda _: resolved)
            self.assertTrue(has_updates)
            self.assertEqual(len(validation_errors), 1)


if __name__ == "__main__":
    unittest.main()
