#pragma once

// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.25.0)
// impl/envoy (cancellation testbed): the testbed_bbr upstream filter. One instance per upstream attempt: decodeHeaders
// (the attempt's request is sent) calls the host's BbrState onSend, encodeHeaders (response headers, whatever the
// status) onAnswer, and onDestroy without response headers (cancelled, reset, connection error) onLoss
// (abstraction/ingress/load_balancer/design.md#deliveryratesample, #attemptloss).

#include <cstdint>

#include "envoy/common/random_generator.h"
#include "envoy/common/time.h"
#include "envoy/http/filter.h"
#include "envoy/upstream/upstream.h"

#include "source/extensions/filters/http/common/pass_through_filter.h"

#include "contrib/peak_ewma/load_balancing_policies/source/bbr.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedBbr {

class BbrAttemptFilter : public Http::PassThroughFilter {
public:
  BbrAttemptFilter(TimeSource& time_source, Random::RandomGenerator& random)
      : time_source_(time_source), random_(random) {}

  Http::FilterHeadersStatus decodeHeaders(Http::RequestHeaderMap& headers, bool end_stream) override;
  Http::FilterHeadersStatus encodeHeaders(Http::ResponseHeaderMap& headers, bool end_stream) override;
  void onDestroy() override;

private:
  int64_t monoNowNs() const;
  int64_t unixNowNs() const;

  TimeSource& time_source_;
  Random::RandomGenerator& random_;
  Upstream::HostDescriptionConstSharedPtr host_;
  LoadBalancingPolicies::PeakEwma::BbrSendTag tag_;
  bool sent_{false};     // onSend was called
  bool resolved_{false}; // onAnswer or onLoss was called
};

} // namespace TestbedBbr
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
