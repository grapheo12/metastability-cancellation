// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.27.0)
#include "contrib/peak_ewma/bbr/source/filter.h"

#include <chrono>

#include "contrib/peak_ewma/load_balancing_policies/source/deadline_lb.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedBbr {

using LoadBalancingPolicies::PeakEwma::bbrTrace;
using LoadBalancingPolicies::PeakEwma::PendingRequests;

int64_t BbrAttemptFilter::monoNowNs() const {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(time_source_.monotonicTime().time_since_epoch())
      .count();
}

int64_t BbrAttemptFilter::unixNowNs() const {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(time_source_.systemTime().time_since_epoch()).count();
}

// Design: abstraction/ingress/load_balancer/design.md#deliveryratesample
Http::FilterHeadersStatus BbrAttemptFilter::decodeHeaders(Http::RequestHeaderMap&, bool) {
  const auto upstream = decoder_callbacks_->streamInfo().upstreamInfo();
  if (sent_ || upstream == nullptr || upstream->upstreamHost() == nullptr) {
    return Http::FilterHeadersStatus::Continue;
  }
  auto pending = upstream->upstreamHost()->typedLbPolicyData<PendingRequests>();
  if (!pending.has_value() || !pending->bbr().initialized()) {
    return Http::FilterHeadersStatus::Continue; // not balanced by peak_ewma_deadline with bbr
  }
  host_ = upstream->upstreamHost(); // this attempt's host
  tag_ = pending->bbr().onSend(monoNowNs(), [this]() { return random_.random(); });
  sent_ = true;
  bbrTrace(unixNowNs(), "send", host_->address()->asString(), pending->bbr().snapshot(), 0.0);
  return Http::FilterHeadersStatus::Continue;
}

// Design: abstraction/ingress/load_balancer/design.md#deliveryratesample
Http::FilterHeadersStatus BbrAttemptFilter::encodeHeaders(Http::ResponseHeaderMap&, bool) {
  if (!sent_ || resolved_) {
    return Http::FilterHeadersStatus::Continue;
  }
  resolved_ = true; // the attempt answered (response headers, whatever the status)
  StreamInfo::FilterState& state = *decoder_callbacks_->streamInfo().filterState();
  if (!state.hasDataWithName(kBbrAnsweredKey)) { // HedgeCancel for the request's other attempts
    state.setData(kBbrAnsweredKey, std::make_shared<BbrAnswered>(), StreamInfo::FilterState::LifeSpan::FilterChain);
  }
  auto pending = host_->typedLbPolicyData<PendingRequests>();
  if (!pending.has_value()) {
    return Http::FilterHeadersStatus::Continue;
  }
  const int64_t now = monoNowNs();
  pending->bbr().onAnswer(tag_, now, [this]() { return random_.random(); });
  bbrTrace(unixNowNs(), "answer", host_->address()->asString(), pending->bbr().snapshot(),
           static_cast<double>(now - tag_.send_ns) / 1e6);
  return Http::FilterHeadersStatus::Continue;
}

// Design: abstraction/ingress/load_balancer/design.md#attemptloss
void BbrAttemptFilter::onDestroy() {
  if (!sent_ || resolved_) {
    return;
  }
  resolved_ = true; // ended without response headers
  auto pending = host_->typedLbPolicyData<PendingRequests>();
  if (!pending.has_value()) {
    return;
  }
  const int64_t now = monoNowNs();
  // HedgeCancel if another attempt of this request already answered, else an AttemptLoss.
  const bool cancel = decoder_callbacks_->streamInfo().filterState()->hasDataWithName(kBbrAnsweredKey);
  if (cancel) {
    pending->bbr().onCancel(tag_, now);
  } else {
    pending->bbr().onLoss(tag_, now);
  }
  bbrTrace(unixNowNs(), cancel ? "cancel" : "loss", host_->address()->asString(), pending->bbr().snapshot(),
           static_cast<double>(now - tag_.send_ns) / 1e6);
}

} // namespace TestbedBbr
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
