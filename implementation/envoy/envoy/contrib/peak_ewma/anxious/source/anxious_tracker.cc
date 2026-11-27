// Source: abstraction/ingress/retry_policy/design.md (retry_policy v0.8.0)
// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.23.0)
#include "contrib/peak_ewma/anxious/source/anxious_tracker.h"

#include <algorithm>
#include <utility>

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedAnxious {

namespace {

// One EWMA step, as ewma_exec_update / ewma_anti_update: the first sample is the average, then
// old * beta + x * (1 - beta), each product and the sum rounded on its own (separate statements: no contraction).
// Design: abstraction/ingress/load_balancer/design.md#ewmaexectime
double ewmaStep(const absl::optional<double>& old, double x, double beta) {
  if (!old.has_value()) {
    return x;
  }
  const double kept = *old * beta;
  const double weight = 1.0 - beta;
  const double added = x * weight;
  return kept + added;
}

} // namespace

AnxiousTracker::AnxiousTracker(std::vector<std::string> backends, double alpha, double beta, double ewma_beta)
    : backends_(std::move(backends)), alpha_(alpha), beta_(beta), ewma_beta_(ewma_beta),
      entries_(backends_.size()), exec_(backends_.size()), anti_(backends_.size()) {}

absl::optional<size_t> AnxiousTracker::backendIndex(absl::string_view address) const {
  for (size_t b = 0; b < backends_.size(); ++b) {
    if (backends_[b] == address) {
      return b;
    }
  }
  return absl::nullopt;
}

// Design: abstraction/ingress/retry_policy/design.md#retryobservation
void AnxiousTracker::attemptSent(uint64_t seq, size_t b, uint64_t deadline) {
  absl::MutexLock lock(&mu_);
  entries_[b][seq] = deadline;
  dirty_ = true;
}

// Design: abstraction/ingress/retry_policy/design.md#retryobservation
void AnxiousTracker::attemptFinished(uint64_t seq, size_t b) {
  absl::MutexLock lock(&mu_);
  entries_[b].erase(seq);
  dirty_ = true;
}

// Design: abstraction/ingress/load_balancer/design.md#ewmaexectime
// Design: abstraction/ingress/load_balancer/design.md#ewmaantiservicetime
void AnxiousTracker::execSample(size_t b, uint64_t exec_ns) {
  const double x = static_cast<double>(exec_ns); // binary64, round to nearest
  absl::MutexLock lock(&mu_);
  exec_[b] = ewmaStep(exec_[b], x, ewma_beta_);
  for (size_t c = 0; c < backends_.size(); ++c) {
    if (c != b) {
      anti_[c] = ewmaStep(anti_[c], x, ewma_beta_);
    }
  }
  dirty_ = true; // EwmaChanged
}

// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
bool AnxiousTracker::maybeEvaluate(int64_t now_mono_ns) {
  absl::MutexLock lock(&mu_);
  if (!dirty_ || (last_eval_ns_.has_value() && now_mono_ns - *last_eval_ns_ < kAnxiousTickNs)) {
    return false;
  }
  published_ = anxiousSet(entries_, exec_, anti_, std::vector<bool>(backends_.size(), true), alpha_);
  dirty_ = false;
  last_eval_ns_ = now_mono_ns;
  return true;
}

// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
bool AnxiousTracker::expectedLate(double current, absl::optional<int64_t> remaining_ns, double beta) {
  if (!remaining_ns.has_value()) {
    return false; // no RequestDeadline: never late, never retried
  }
  const double limit = beta * static_cast<double>(*remaining_ns); // the product rounded first
  return current > limit;                                          // ExpectedLate: strict >
}

// Design: abstraction/ingress/retry_policy/design.md#retrypolicy
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
bool AnxiousTracker::decide(uint64_t seq, absl::optional<int64_t> remaining_ns) const {
  const absl::optional<double> c = currentExpFinish(seq);
  return c.has_value() && expectedLate(*c, remaining_ns, beta_);
}

absl::optional<double> AnxiousTracker::currentExpFinish(uint64_t seq) const {
  absl::MutexLock lock(&mu_);
  auto it = published_.find(seq);
  if (it == published_.end()) {
    return absl::nullopt;
  }
  return it->second;
}

size_t AnxiousTracker::inFlight() const {
  absl::MutexLock lock(&mu_);
  size_t n = 0;
  for (const auto& e : entries_) {
    n += e.size();
  }
  return n;
}

size_t AnxiousTracker::anxiousCount() const {
  absl::MutexLock lock(&mu_);
  return published_.size();
}

absl::optional<double> AnxiousTracker::exec(size_t b) const {
  absl::MutexLock lock(&mu_);
  return exec_[b];
}

absl::optional<double> AnxiousTracker::anti(size_t b) const {
  absl::MutexLock lock(&mu_);
  return anti_[b];
}

// Design: abstraction/ingress/retry_policy/design.md#retrystate
uint64_t AnxiousTracker::edfAhead(size_t b, uint64_t deadline) const {
  absl::MutexLock lock(&mu_);
  uint64_t n = 0;
  for (const auto& [seq, d] : entries_[b]) {
    n += d <= deadline ? 1 : 0; // ties count as ahead
  }
  return n;
}

// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
std::map<uint64_t, double> AnxiousTracker::anxiousSet(const Entries& entries,
                                                      const std::vector<absl::optional<double>>& exec,
                                                      const std::vector<absl::optional<double>>& anti,
                                                      const std::vector<bool>& live, double alpha) {
  const size_t B = entries.size();
  // EdfAhead(c, d) = the number of deadlines <= d on c: each backend's deadlines sorted once, then upper_bound.
  std::vector<std::vector<uint64_t>> sorted(B);
  for (size_t c = 0; c < B; ++c) {
    for (const auto& [seq, d] : entries[c]) {
      sorted[c].push_back(d);
    }
    std::sort(sorted[c].begin(), sorted[c].end());
  }
  auto edf_ahead = [&sorted](size_t c, uint64_t d) -> uint64_t {
    return static_cast<uint64_t>(std::upper_bound(sorted[c].begin(), sorted[c].end(), d) - sorted[c].begin());
  };

  std::map<uint64_t, double> out; // TicketSeq -> CurrentExpFinish
  for (size_t b = 0; b < B; ++b) {
    std::vector<size_t> others_live; // L: where a RetryElsewhere can go
    for (size_t c = 0; c < B; ++c) {
      if (c != b && live[c]) {
        others_live.push_back(c);
      }
    }
    if (others_live.empty() || !exec[b].has_value() || !anti[b].has_value() || *exec[b] == 0.0) {
      continue;
    }
    for (const auto& [seq, d] : entries[b]) {
      const uint64_t mine = edf_ahead(b, d) - 1; // ahead of it on its own backend, itself excluded
      const double current = static_cast<double>(mine + 1) * *exec[b];
      uint64_t others = 0;
      for (size_t c : others_live) {
        others += edf_ahead(c, d);
      }
      const double average = static_cast<double>(others) / static_cast<double>(others_live.size());
      const double plus_one = average + 1.0;
      const double retry = plus_one * *anti[b];
      const double saved = current - retry;
      const double ratio = saved / current;
      if (ratio >= alpha) {
        out[seq] = current;
      }
    }
  }
  return out;
}

} // namespace TestbedAnxious
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
