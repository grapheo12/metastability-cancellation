#pragma once

// impl/envoy (cancellation testbed): the AnxiousTracker of the Go Ingress's `anxious` RetryPolicy
// (abstraction/ingress/retry_policy/design.md#anxious), as plain logic. It follows every attempt in flight
// (AttemptSent / AttemptFinished, with its RequestDeadline) and every backend's EwmaExecTime and
// EwmaAntiServiceTime (abstraction/ingress/load_balancer/design.md#ewmaexectime, #ewmaantiservicetime), and, at
// most every kAnxiousTickNs and only after a change, publishes the anxious attempts with their CurrentExpFinish:
//
//   for an attempt with deadline d on backend b, L = the live backends other than b (all are live here):
//     mine    = EdfAhead(b, d) - 1                      (attempts on b with deadline <= d, itself excluded)
//     current = (mine + 1) * exec[b]                    (CurrentExpFinish, ns)
//     others  = sum over c in L of EdfAhead(c, d)
//     retry   = (others / |L| + 1) * anti[b]            (RetryExpFinish)
//     anxious iff L non-empty, exec[b] and anti[b] known, exec[b] != 0, (current - retry) / current >= alpha
//
// decide(seq, remaining) is the policy's decision for a request's newest attempt: retry (RetryElsewhere) iff the
// attempt is published and ExpectedLate, current > beta * remaining (strict; no deadline: never). binary64
// throughout, each step rounded, no fused operations.

#include <cstdint>
#include <map>
#include <string>
#include <vector>

#include "absl/strings/string_view.h"
#include "absl/synchronization/mutex.h"
#include "absl/types/optional.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedAnxious {

constexpr uint64_t kNoDeadline = ~0ULL;            // NO_DEADLINE = 2^64 - 1
constexpr int64_t kAnxiousTickNs = 10'000'000;     // ANXIOUS_TICK_MS = 10

class AnxiousTracker {
public:
  AnxiousTracker(std::vector<std::string> backends, double alpha, double beta, double ewma_beta);

  // The backend index of "host:port", or none if it is not a configured backend.
  absl::optional<size_t> backendIndex(absl::string_view address) const;
  size_t backendCount() const { return backends_.size(); }
  const std::string& backendName(size_t b) const { return backends_[b]; }

  // Observations (any thread).
  void attemptSent(uint64_t seq, size_t b, uint64_t deadline);
  void attemptFinished(uint64_t seq, size_t b);
  // One ExecutionTime sample served by b: EwmaExecTime[b] first, then EwmaAntiServiceTime[c] for every c != b in
  // increasing c (the first sample of each average is the average).
  void execSample(size_t b, uint64_t exec_ns);

  // AnxiousEvaluation: recomputes the published set if something changed since the last evaluation and at least
  // kAnxiousTickNs passed since it (the first evaluation runs at once). Returns whether it evaluated.
  bool maybeEvaluate(int64_t now_mono_ns);

  // The policy's decision for a request's newest attempt (see the top of this file).
  bool decide(uint64_t seq, absl::optional<int64_t> remaining_ns) const;
  // The published CurrentExpFinish of seq, or none if it is not anxious.
  absl::optional<double> currentExpFinish(uint64_t seq) const;
  size_t inFlight() const;
  size_t anxiousCount() const;

  // Accessors (tests, trace).
  absl::optional<double> exec(size_t b) const;
  absl::optional<double> anti(size_t b) const;
  uint64_t edfAhead(size_t b, uint64_t deadline) const;

  // The pure parts, as in the spec (tests).
  using Entries = std::vector<std::map<uint64_t, uint64_t>>; // per backend: TicketSeq -> deadline
  static std::map<uint64_t, double> anxiousSet(const Entries& entries,
                                               const std::vector<absl::optional<double>>& exec,
                                               const std::vector<absl::optional<double>>& anti,
                                               const std::vector<bool>& live, double alpha);
  static bool expectedLate(double current, absl::optional<int64_t> remaining_ns, double beta);

private:
  const std::vector<std::string> backends_;
  const double alpha_;
  const double beta_;
  const double ewma_beta_;

  mutable absl::Mutex mu_;
  Entries entries_ ABSL_GUARDED_BY(mu_);
  std::vector<absl::optional<double>> exec_ ABSL_GUARDED_BY(mu_);
  std::vector<absl::optional<double>> anti_ ABSL_GUARDED_BY(mu_);
  bool dirty_ ABSL_GUARDED_BY(mu_) = false;
  absl::optional<int64_t> last_eval_ns_ ABSL_GUARDED_BY(mu_);
  std::map<uint64_t, double> published_ ABSL_GUARDED_BY(mu_);
};

} // namespace TestbedAnxious
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
