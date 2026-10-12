import unittest
from unittest.mock import patch

import check_gofmt


class GoFormatTests(unittest.TestCase):
    @patch("check_gofmt.subprocess.run")
    def test_formatted_files_pass(self, run):
        run.return_value.returncode = 0
        run.return_value.stdout = ""
        run.return_value.stderr = ""

        self.assertEqual(check_gofmt.main_for_files(["main.go"]), 0)
        run.assert_called_once_with(
            ["gofmt", "-l", "main.go"],
            check=False,
            capture_output=True,
            text=True,
        )

    @patch("check_gofmt.subprocess.run")
    def test_unformatted_files_are_rejected_without_rewriting(self, run):
        run.return_value.returncode = 0
        run.return_value.stdout = "main.go\n"
        run.return_value.stderr = ""

        self.assertEqual(check_gofmt.main_for_files(["main.go"]), 1)
        self.assertEqual(run.call_args.args[0], ["gofmt", "-l", "main.go"])

    @patch("check_gofmt.subprocess.run")
    def test_gofmt_failure_is_returned(self, run):
        run.return_value.returncode = 2
        run.return_value.stdout = ""
        run.return_value.stderr = "gofmt failed\n"

        self.assertEqual(check_gofmt.main_for_files(["main.go"]), 2)

    @patch("check_gofmt.subprocess.run", side_effect=FileNotFoundError("missing"))
    def test_missing_gofmt_is_an_error(self, run):
        self.assertEqual(check_gofmt.main_for_files(["main.go"]), 2)

    @patch("check_gofmt.subprocess.run")
    def test_no_go_files_skips_tool(self, run):
        self.assertEqual(check_gofmt.main_for_files(["fixture.txt"]), 0)
        run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
