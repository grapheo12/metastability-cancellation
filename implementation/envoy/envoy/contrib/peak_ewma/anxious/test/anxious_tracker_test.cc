// Source: abstraction/ingress/retry_policy/design.md (retry_policy v0.8.0)
// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.25.0)
// Source: abstraction/protocol/design.md (protocol v0.8.0)
// impl/envoy (cancellation testbed): the AnxiousTracker against every worked example of
// abstraction/ingress/retry_policy/design.md#anxious and load_balancer/design.md#ewmaantiservicetime.

#include <map>
#include <string>
#include <vector>

#include "contrib/peak_ewma/anxious/source/anxious_tracker.h"
#include "contrib/peak_ewma/anxious/source/filter.h"
#include "gtest/gtest.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedAnxious {
namespace {

constexpr uint64_t MAX = kNoDeadline;

// Entries from per-backend deadline lists; seqs are assigned in order. star = (backend, index) of the evaluated
// entry; returns its seq.
uint64_t build(const std::vector<std::vector<uint64_t>>& deadlines, std::pair<size_t, size_t> star,
               AnxiousTracker::Entries& entries) {
  entries.assign(deadlines.size(), {});
  uint64_t seq = 1, star_seq = 0;
  for (size_t b = 0; b < deadlines.size(); ++b) {
    for (size_t i = 0; i < deadlines[b].size(); ++i, ++seq) {
      entries[b][seq] = deadlines[b][i];
      if (b == star.first && i == star.second) {
        star_seq = seq;
      }
    }
  }
  return star_seq;
}

struct Row {
  const char* name;
  std::vector<std::vector<uint64_t>> deadlines;
  std::pair<size_t, size_t> star;
  std::vector<absl::optional<double>> exec, anti;
  std::vector<bool> live;
  bool anxious;
  double current; // when anxious
};

// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
TEST(AnxiousSet, WorkedExamples) {
  const absl::optional<double> none;
  const std::vector<Row> rows = {
      {"EDF, not FIFO", {{3, 5, 10, 20, 30, 40}, {4, 12, 15}}, {0, 2}, {80, 80}, {80, 80}, {true, true}, true, 240},
      {"Late deadline", {{3, 5, 20, 30, 40, 50}, {4, 12, 15}}, {0, 5}, {80, 80}, {80, 80}, {true, true}, true, 480},
      {"Earliest deadline", {{2, 3, 5}, {4, 12, 15}}, {0, 0}, {80, 80}, {80, 80}, {true, true}, false, 0},
      {"Exactly alpha", {{1, 2, 3, 4, 10}, {5, 6, 7, 20}}, {0, 4}, {80, 80}, {70, 70}, {true, true}, true, 400},
      {"Tie", {{10, 10}, {10, 10}}, {0, 0}, {80, 80}, {80, 80}, {true, true}, false, 0},
      {"No deadline", {{3, MAX}, {4, MAX}}, {0, 1}, {80, 80}, {80, 80}, {true, true}, false, 0},
      {"Three backends", {{3, 5, 7, 9, 10}, {1, 2, 30}, {50}}, {0, 4}, {100, 100, 100}, {100, 100, 100},
       {true, true, true}, true, 500},
      {"Three, one dead", {{3, 5, 7, 9, 10}, {1, 2, 30}, {50}}, {0, 4}, {100, 100, 100}, {100, 100, 100},
       {true, true, false}, true, 500},
      {"Other backend dead", {{3, 10}, {}}, {0, 1}, {80, 80}, {80, 80}, {true, false}, false, 0},
      {"No sample", {{1}, {10}}, {1, 0}, {80, none}, {80, 80}, {true, true}, false, 0},
      {"One backend", {{9}}, {0, 0}, {80}, {none}, {true}, false, 0},
  };
  for (const Row& row : rows) {
    AnxiousTracker::Entries entries;
    const uint64_t star = build(row.deadlines, row.star, entries);
    const auto set = AnxiousTracker::anxiousSet(entries, row.exec, row.anti, row.live, 0.3);
    auto it = set.find(star);
    EXPECT_EQ(it != set.end(), row.anxious) << row.name;
    if (row.anxious && it != set.end()) {
      EXPECT_EQ(it->second, row.current) << row.name;
    }
  }
}

// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
TEST(AnxiousDecide, ExpectedLateTable) {
  EXPECT_TRUE(AnxiousTracker::expectedLate(400, 30, 10));    // 400 > 300
  EXPECT_FALSE(AnxiousTracker::expectedLate(400, 40, 10));   // tie: strict >
  EXPECT_FALSE(AnxiousTracker::expectedLate(400, 150, 10));  // not late enough
  EXPECT_TRUE(AnxiousTracker::expectedLate(400, -20, 10));   // already past the deadline
  EXPECT_FALSE(AnxiousTracker::expectedLate(400, absl::nullopt, 10)); // no RequestDeadline
  EXPECT_TRUE(AnxiousTracker::expectedLate(400, 150, 1));    // beta = 1
  EXPECT_TRUE(AnxiousTracker::expectedLate(400, 4000, 0));   // beta = 0: any anxious attempt with a deadline
  AnxiousTracker tracker({"a", "b"}, 0.3, 10, 0.7);
  EXPECT_FALSE(tracker.decide(1, 10)); // not in the published map
}

// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
TEST(AnxiousTracker, EndToEndEdfRow) {
  AnxiousTracker tracker({"a", "b"}, 0.3, 10, 0.7);
  tracker.execSample(0, 80); // exec[a] = 80, anti[b] = 80
  tracker.execSample(1, 80); // exec[b] = 80, anti[a] = 80
  const std::vector<uint64_t> on_a = {3, 5, 10, 20, 30, 40}, on_b = {4, 12, 15};
  uint64_t seq = 1, star = 0;
  for (uint64_t d : on_a) {
    if (d == 10) {
      star = seq;
    }
    tracker.attemptSent(seq++, 0, d);
  }
  for (uint64_t d : on_b) {
    tracker.attemptSent(seq++, 1, d);
  }
  EXPECT_TRUE(tracker.maybeEvaluate(0));
  ASSERT_TRUE(tracker.currentExpFinish(star).has_value());
  EXPECT_EQ(*tracker.currentExpFinish(star), 240);
  EXPECT_TRUE(tracker.decide(star, 20));  // 240 > 10 * 20
  EXPECT_FALSE(tracker.decide(star, 24)); // 240 = 10 * 24: tie waits
  EXPECT_EQ(tracker.inFlight(), 9u);
}

// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
TEST(AnxiousTracker, EvaluatesOnlyAfterAChangeAtMostEveryTick) {
  AnxiousTracker tracker({"a", "b"}, 0.3, 10, 0.7);
  constexpr int64_t ms = 1'000'000;
  EXPECT_FALSE(tracker.maybeEvaluate(0)); // nothing changed yet
  tracker.attemptSent(7, 0, 100);
  EXPECT_TRUE(tracker.maybeEvaluate(0));       // first evaluation at once
  EXPECT_FALSE(tracker.maybeEvaluate(5 * ms)); // nothing changed
  tracker.attemptFinished(7, 0);
  EXPECT_FALSE(tracker.maybeEvaluate(5 * ms)); // changed, but less than a tick since the last evaluation
  EXPECT_TRUE(tracker.maybeEvaluate(10 * ms)); // a tick later
  EXPECT_FALSE(tracker.maybeEvaluate(30 * ms));
  tracker.execSample(1, 500); // EwmaChanged
  EXPECT_TRUE(tracker.maybeEvaluate(30 * ms));
}

// Design: abstraction/ingress/retry_policy/design.md#retrystate
TEST(AnxiousTracker, EdfAheadBookkeeping) {
  AnxiousTracker tracker({"a", "b"}, 0.3, 10, 0.7);
  tracker.attemptSent(9, 0, 5);
  tracker.attemptSent(10, 0, 8);
  tracker.attemptSent(11, 0, 12);
  EXPECT_EQ(tracker.edfAhead(0, 12), 3u);
  tracker.attemptSent(12, 0, 20);
  EXPECT_EQ(tracker.edfAhead(0, 12), 3u); // deadline 20 is after 12
  tracker.attemptSent(13, 0, 1);
  EXPECT_EQ(tracker.edfAhead(0, 12), 4u); // deadline 1 is before 12, though sent later
  tracker.attemptFinished(9, 0);
  EXPECT_EQ(tracker.edfAhead(0, 12), 3u);
  tracker.attemptFinished(42, 0); // not tracked
  EXPECT_EQ(tracker.edfAhead(0, 12), 3u);
}

// Design: abstraction/ingress/load_balancer/design.md#ewmaantiservicetime
TEST(AnxiousTracker, EwmaAntiServiceTimeExample) {
  AnxiousTracker tracker({"a", "b", "c"}, 0.3, 10, 0.5);
  tracker.execSample(0, 100);
  EXPECT_FALSE(tracker.anti(0).has_value());
  EXPECT_EQ(*tracker.anti(1), 100);
  EXPECT_EQ(*tracker.anti(2), 100);
  tracker.execSample(1, 300);
  EXPECT_EQ(*tracker.anti(0), 300);
  EXPECT_EQ(*tracker.anti(1), 100);
  EXPECT_EQ(*tracker.anti(2), 200);
  tracker.execSample(2, 500);
  EXPECT_EQ(*tracker.anti(0), 400);
  EXPECT_EQ(*tracker.anti(1), 300);
  EXPECT_EQ(*tracker.anti(2), 200);
  EXPECT_EQ(*tracker.exec(0), 100);
  EXPECT_EQ(*tracker.exec(1), 300);
  EXPECT_EQ(*tracker.exec(2), 500);
}

// Design: abstraction/protocol/design.md#parsing
TEST(ExecReport, ParsesExecNs) {
  EXPECT_EQ(parseExecNs(R"({"id":"42","exec_ns":"400000000","start_slack_ns":"-12","payload":"x"})"),
            absl::optional<uint64_t>(400000000));
  EXPECT_EQ(parseExecNs(R"({"id":"42","payload":"x"})"), absl::nullopt);
  EXPECT_EQ(parseExecNs(R"({"id":"42","exec_ns":"4000)"), absl::nullopt); // truncated
  EXPECT_EQ(parseExecNs(R"({"exec_ns":"x1"})"), absl::nullopt);
}

TEST(AnxiousTracker, BackendIndex) {
  AnxiousTracker tracker({"10.0.0.1:8080", "10.0.0.2:8080"}, 0.3, 10, 0.7);
  EXPECT_EQ(tracker.backendIndex("10.0.0.2:8080"), absl::optional<size_t>(1));
  EXPECT_EQ(tracker.backendIndex("10.0.0.3:8080"), absl::nullopt);
}

} // namespace
} // namespace TestbedAnxious
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
