import sys
import unittest
from unittest.mock import patch

from labours import cli


class CliModesTest(unittest.TestCase):
    def test_parse_args_accepts_onboarding_mode(self):
        with patch.object(cli, "list_matplotlib_styles", return_value=["default", "ggplot"]):
            with patch.object(sys, "argv", ["labours", "-m", "onboarding"]):
                args = cli.parse_args()

        self.assertEqual(args.modes, ["onboarding"])


if __name__ == "__main__":
    unittest.main()
