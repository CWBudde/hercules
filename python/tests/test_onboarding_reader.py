import io
import unittest

from labours.pb_pb2 import (
    AnalysisResults,
    AuthorOnboardingData,
    CohortStats,
    OnboardingAverageSnapshot,
    OnboardingResults,
    OnboardingSnapshot,
)
from labours.readers import ProtobufReader, YamlReader


class OnboardingReaderTest(unittest.TestCase):
    def test_yaml_reader_normalizes_onboarding_payload(self):
        reader = YamlReader()
        reader.read(
            io.BytesIO(
                b"""
hercules:
  repository: example
  begin_unix_time: 1700000000
  end_unix_time: 1700100000
Onboarding:
  onboarding:
    window_days: [7, 30]
    meaningful_threshold: 10
    authors:
      0:
        first_commit_tick: 2
        join_cohort: "2025-01"
        snapshots:
          7: {days: 7, commits: 3, files: 4, lines: 50, meaningful_commits: 2, meaningful_files: 3, meaningful_lines: 40}
    cohorts:
      "2025-01":
        author_count: 1
        average_snapshots:
          7: {days: 7, commits: 3, files: 4, lines: 50, meaningful_commits: 2, meaningful_files: 3, meaningful_lines: 40}
    people:
      - Alice
    tick_size: 86400
"""
            )
        )

        authors, cohorts, people, window_days, threshold, tick_size = reader.get_onboarding()

        self.assertEqual(window_days, [7, 30])
        self.assertEqual(threshold, 10)
        self.assertEqual(tick_size, 86400)
        self.assertEqual(people, ["Alice"])
        self.assertEqual(authors[0]["join_cohort"], "2025-01")
        self.assertEqual(authors[0]["snapshots"][7]["meaningful_lines"], 40)
        self.assertEqual(cohorts["2025-01"]["author_count"], 1)
        self.assertEqual(cohorts["2025-01"]["average_snapshots"][7]["meaningful_commits"], 2.0)

    def test_protobuf_reader_normalizes_onboarding_payload(self):
        onboarding = OnboardingResults(
            window_days=[7, 30],
            meaningful_threshold=10,
            dev_index=["Alice"],
            tick_size=86400 * 1_000_000_000,
        )
        onboarding.authors[0].CopyFrom(
            AuthorOnboardingData(
                first_commit_tick=2,
                join_cohort="2025-01",
                snapshots={
                    7: OnboardingSnapshot(
                        days_since_join=7,
                        total_commits=3,
                        total_files=4,
                        total_lines=50,
                        meaningful_commits=2,
                        meaningful_files=3,
                        meaningful_lines=40,
                    )
                },
            )
        )
        onboarding.cohorts["2025-01"].CopyFrom(
            CohortStats(
                cohort="2025-01",
                author_count=1,
                average_snapshots={
                    7: OnboardingAverageSnapshot(
                        days_since_join=7,
                        avg_total_commits=3.0,
                        avg_total_files=4.0,
                        avg_total_lines=50.0,
                        avg_meaningful_commits=2.0,
                        avg_meaningful_files=3.0,
                        avg_meaningful_lines=40.0,
                    )
                },
            )
        )
        envelope = AnalysisResults()
        envelope.header.repository = "example"
        envelope.contents["Onboarding"] = onboarding.SerializeToString()

        reader = ProtobufReader()
        reader.read(io.BytesIO(envelope.SerializeToString()))

        authors, cohorts, people, window_days, threshold, tick_size = reader.get_onboarding()

        self.assertEqual(window_days, [7, 30])
        self.assertEqual(threshold, 10)
        self.assertEqual(tick_size, 86400 * 1_000_000_000)
        self.assertEqual(people, ["Alice"])
        self.assertEqual(authors[0]["snapshots"][7]["meaningful_lines"], 40)
        self.assertEqual(cohorts["2025-01"]["average_snapshots"][7]["meaningful_lines"], 40.0)


if __name__ == "__main__":
    unittest.main()
