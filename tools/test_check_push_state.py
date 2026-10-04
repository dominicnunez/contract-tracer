import unittest

from check_push_state import state_errors


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


if __name__ == "__main__":
    unittest.main()
