#pragma once

// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.27.0)
// impl/envoy (cancellation testbed): the TokenBucketFilter's per-host TokenBucket and its TokenRate AIMD
// (abstraction/ingress/load_balancer/design.md#tokenbucketfilter), pure logic (times are passed in). One bucket per
// host, kept in the host's PendingRequests (deadline_lb.h) and so shared by every worker's balancer; the
// testbed_token_bucket upstream filter reports each answered attempt's latency to it.

#include <cstdint>
#include <string>

#include "absl/synchronization/mutex.h"
#include "absl/types/optional.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

// The lb_tb_* parameters, derived values included.
struct TokenParams {
  int64_t slo_ns{0};         // round(lb_tb_slo_ms * 1e6)
  double init_rate{50.0};    // init_r, tokens/s
  double max_tokens{10.0};   // bucket capacity
  double mult_dec{2.0};      // slow answer: rate / mult_dec
  double add_inc_limit{2.0}; // fast answer: latency < slo / add_inc_limit
  double add_inc{5.0};       // lb_tb_add_inc_frac * init_r
  double r_min{5.0};         // lb_tb_min_rate_frac * init_r
};

// What one TokenRate sample did (for the trace).
struct TokenRateChange {
  // "slow" (halved), "slow_held" (slow, within the SLO window), "fast" (raised), "fast_held" (fast, within the
  // increase window), "mid", "stuck" (a StuckAttempt halved the rate)
  const char* event;
  double rate_before;
  double rate_after;
  double tokens; // after the refill
};

class TokenBucket {
public:
  // Sets the parameters, rate = init_r, tokens = 0 and the refill clock to now_ns; only the first call counts.
  void init(const TokenParams& params, int64_t now_ns);
  bool initialized() const;
  // has_token / take_token of the design; false while not initialized.
  bool hasToken(int64_t now_ns);
  bool take(int64_t now_ns);
  // token_rate_sample: one answered attempt; none while not initialized.
  absl::optional<TokenRateChange> rateSample(int64_t latency_ns, int64_t now_ns);
  // stuck_attempt: the host's oldest pending attempt is past the SLO (the caller checks); a change only if the rate
  // was halved (none while not initialized or within the decrease window).
  absl::optional<TokenRateChange> stuck(int64_t now_ns);
  double rate() const;
  double tokens(int64_t now_ns); // after a refill

private:
  void refill(int64_t now_ns) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  // rate_decrease: halves (mult_dec) at most once per SLO window; true if it did.
  bool decrease(int64_t now_ns) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);

  mutable absl::Mutex mu_;
  bool init_ ABSL_GUARDED_BY(mu_){false};
  TokenParams params_ ABSL_GUARDED_BY(mu_);
  double tokens_ ABSL_GUARDED_BY(mu_){0.0};
  double rate_ ABSL_GUARDED_BY(mu_){0.0};
  int64_t last_refill_ns_ ABSL_GUARDED_BY(mu_){0};
  absl::optional<int64_t> last_decrease_ns_ ABSL_GUARDED_BY(mu_);
  absl::optional<int64_t> last_increase_ns_ ABSL_GUARDED_BY(mu_);
};

// Diagnostic trace <PEAK_EWMA_TRACE_PREFIX>.tokens.0.csv (one file per process, under a lock):
// t_unix_ns,event,host,latency_ms,rate_before,rate_after,tokens. Events: take, race (a take lost to another worker),
// slow, slow_held, fast, fast_held, mid, stuck. No-op without the environment variable.
void tokenTrace(int64_t unix_ns, const char* event, const std::string& host, double latency_ms, double rate_before,
                double rate_after, double tokens);

} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
