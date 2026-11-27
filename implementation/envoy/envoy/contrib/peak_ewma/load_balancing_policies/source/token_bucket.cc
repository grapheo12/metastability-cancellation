// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.23.0)
#include "contrib/peak_ewma/load_balancing_policies/source/token_bucket.h"

#include <algorithm>
#include <cstdio>
#include <cstdlib>

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

// Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
void TokenBucket::init(const TokenParams& params, int64_t now_ns) {
  absl::MutexLock lock(&mu_);
  if (init_) {
    return; // the first balancer to start sets it; later workers and hosts added later reuse it
  }
  init_ = true;
  params_ = params;
  tokens_ = 0.0;              // starts empty
  rate_ = params.init_rate;   // TokenRate = init_r
  last_refill_ns_ = now_ns;
  last_decrease_ns_ = absl::nullopt;
}

bool TokenBucket::initialized() const {
  absl::MutexLock lock(&mu_);
  return init_;
}

// Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
void TokenBucket::refill(int64_t now_ns) {
  if (now_ns <= last_refill_ns_) {
    return; // another worker refilled with a later clock reading: no time has passed for this caller
  }
  tokens_ = std::min(params_.max_tokens,
                     tokens_ + rate_ * static_cast<double>(now_ns - last_refill_ns_) / 1e9);
  last_refill_ns_ = now_ns;
}

bool TokenBucket::hasToken(int64_t now_ns) {
  absl::MutexLock lock(&mu_);
  if (!init_) {
    return false;
  }
  refill(now_ns);
  return tokens_ >= 1.0;
}

bool TokenBucket::take(int64_t now_ns) {
  absl::MutexLock lock(&mu_);
  if (!init_) {
    return false;
  }
  refill(now_ns);
  if (tokens_ < 1.0) {
    return false;
  }
  tokens_ -= 1.0;
  return true;
}

// Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
absl::optional<TokenRateChange> TokenBucket::rateSample(int64_t latency_ns, int64_t now_ns) {
  absl::MutexLock lock(&mu_);
  if (!init_) {
    return absl::nullopt;
  }
  refill(now_ns); // the old rate holds up to now
  TokenRateChange change{"mid", rate_, rate_, 0.0};
  if (latency_ns > params_.slo_ns) {
    if (!last_decrease_ns_.has_value() || now_ns - *last_decrease_ns_ >= params_.slo_ns) {
      rate_ = std::max(params_.r_min, rate_ / params_.mult_dec); // at most once per SLO, floor r_min
      last_decrease_ns_ = now_ns;
      change.event = "slow";
    } else {
      change.event = "slow_held";
    }
  } else if (static_cast<double>(latency_ns) < static_cast<double>(params_.slo_ns) / params_.add_inc_limit) {
    rate_ = rate_ + params_.add_inc; // no upper bound: max_tokens bounds a burst
    change.event = "fast";
  }
  change.rate_after = rate_;
  change.tokens = tokens_;
  return change;
}

double TokenBucket::rate() const {
  absl::MutexLock lock(&mu_);
  return rate_;
}

double TokenBucket::tokens(int64_t now_ns) {
  absl::MutexLock lock(&mu_);
  if (init_) {
    refill(now_ns);
  }
  return tokens_;
}

void tokenTrace(int64_t unix_ns, const char* event, const std::string& host, double latency_ms, double rate_before,
                double rate_after, double tokens) {
  static const char* prefix = std::getenv("PEAK_EWMA_TRACE_PREFIX");
  if (prefix == nullptr || prefix[0] == '\0') {
    return;
  }
  static absl::Mutex mu;
  static std::FILE* f = nullptr;
  absl::MutexLock lock(&mu);
  if (f == nullptr) {
    f = std::fopen((std::string(prefix) + ".tokens.0.csv").c_str(), "w");
    if (f == nullptr) {
      return;
    }
    std::setvbuf(f, nullptr, _IOLBF, 1 << 16);
    std::fputs("t_unix_ns,event,host,latency_ms,rate_before,rate_after,tokens\n", f);
  }
  std::fprintf(f, "%lld,%s,%s,%.9g,%.9g,%.9g,%.9g\n", static_cast<long long>(unix_ns), event, host.c_str(),
               latency_ms, rate_before, rate_after, tokens);
}

} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
