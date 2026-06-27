import unittest

from labours.modes.onboarding import build_author_ramp_series, build_cohort_heatmap


class OnboardingModeTest(unittest.TestCase):
    def test_build_cohort_heatmap_orders_cohorts_and_windows(self):
        cohorts = {
            "2025-02": {
                "author_count": 2,
                "average_snapshots": {
                    30: {"meaningful_lines": 200.0},
                    7: {"meaningful_lines": 80.0},
                },
            },
            "2025-01": {
                "author_count": 1,
                "average_snapshots": {
                    7: {"meaningful_lines": 40.0},
                },
            },
        }

        cohort_names, day_labels, matrix = build_cohort_heatmap(
            cohorts, [7, 30], "meaningful_lines"
        )

        self.assertEqual(cohort_names, ["2025-01 (n=1)", "2025-02 (n=2)"])
        self.assertEqual(day_labels, ["7d", "30d"])
        self.assertEqual(matrix.tolist(), [[40.0, 0.0], [80.0, 200.0]])

    def test_build_author_ramp_series_returns_named_series_sorted_by_latest_value(self):
        authors = {
            0: {
                "join_cohort": "2025-01",
                "snapshots": {
                    7: {"meaningful_lines": 10},
                    30: {"meaningful_lines": 50},
                },
            },
            1: {
                "join_cohort": "2025-02",
                "snapshots": {
                    7: {"meaningful_lines": 25},
                    30: {"meaningful_lines": 30},
                },
            },
        }

        series = build_author_ramp_series(authors, ["Alice", "Bob"], [7, 30], "meaningful_lines")

        self.assertEqual(
            series,
            [
                {"author": "Alice", "cohort": "2025-01", "values": [10.0, 50.0]},
                {"author": "Bob", "cohort": "2025-02", "values": [25.0, 30.0]},
            ],
        )

    def test_build_author_ramp_series_shortens_pipe_separated_identity(self):
        authors = {
            0: {
                "join_cohort": "2025-01",
                "snapshots": {7: {"meaningful_lines": 10}},
            },
        }

        series = build_author_ramp_series(
            authors, ["Alice|alice@example.com|alice@users.noreply.github.com"], [7], "meaningful_lines"
        )

        self.assertEqual(series[0]["author"], "Alice")


if __name__ == "__main__":
    unittest.main()
