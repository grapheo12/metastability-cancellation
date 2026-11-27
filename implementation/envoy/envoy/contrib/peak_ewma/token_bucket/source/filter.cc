// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.24.0)
#include "contrib/peak_ewma/token_bucket/source/filter.h"

#include <chrono>

#include "contrib/peak_ewma/load_balancing_policies/source/deadline_lb.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedTokenBucket {

using LoadBalancingPolicies::PeakEwma::PendingRequests;

int64_t TokenRateFilter::monoNowNs() const {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(time_source_.monotonicTime().time_since_epoch())
      .count();
}

// Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
Http::FilterHeadersStatus TokenRateFilter::decodeHeaders(Http::RequestHeaderMap&, bool) {
  const auto upstream = decoder_callbacks_->streamInfo().upstreamInfo();
  if (upstream != nullptr && upstream->upstreamHost() != nullptr) {
    host_ = upstream->upstreamHost(); // this attempt's host
  }
  sent_mono_ns_ = monoNowNs(); // the attempt's request is being sent
  return Http::FilterHeadersStatus::Continue;
}

// Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
Http::FilterHeadersStatus TokenRateFilter::encodeHeaders(Http::ResponseHeaderMap&, bool) {
  if (reported_ || host_ == nullptr) {
    return Http::FilterHeadersStatus::Continue;
  }
  reported_ = true; // one sample per attempt (response headers, whatever the status)
  auto pending = host_->typedLbPolicyData<PendingRequests>();
  if (!pending.has_value()) {
    return Http::FilterHeadersStatus::Continue; // not balanced by peak_ewma_deadline
  }
  const int64_t now = monoNowNs();
  const int64_t latency_ns = now - sent_mono_ns_;
  const auto change = pending->tokenBucket().rateSample(latency_ns, now); // TokenRate AIMD
  if (change.has_value()) {
    const int64_t unix_ns =
        std::chrono::duration_cast<std::chrono::nanoseconds>(time_source_.systemTime().time_since_epoch()).count();
    LoadBalancingPolicies::PeakEwma::tokenTrace(unix_ns, change->event, host_->address()->asString(),
                                                static_cast<double>(latency_ns) / 1e6, change->rate_before,
                                                change->rate_after, change->tokens);
  }
  return Http::FilterHeadersStatus::Continue;
}

} // namespace TestbedTokenBucket
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
