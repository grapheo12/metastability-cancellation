#pragma once

// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.25.0)
// impl/envoy (cancellation testbed): the testbed_token_bucket upstream filter. One instance per upstream attempt:
// decodeHeaders notes the attempt's host and send time, encodeHeaders (the attempt answered, whatever the status)
// reports the latency to the host's TokenBucket (abstraction/ingress/load_balancer/design.md#tokenbucketfilter).
// An attempt destroyed without response headers (cancelled, reset, connection error) reports nothing.

#include <cstdint>

#include "envoy/common/time.h"
#include "envoy/http/filter.h"
#include "envoy/upstream/upstream.h"

#include "source/extensions/filters/http/common/pass_through_filter.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedTokenBucket {

class TokenRateFilter : public Http::PassThroughFilter {
public:
  explicit TokenRateFilter(TimeSource& time_source) : time_source_(time_source) {}

  Http::FilterHeadersStatus decodeHeaders(Http::RequestHeaderMap& headers, bool end_stream) override;
  Http::FilterHeadersStatus encodeHeaders(Http::ResponseHeaderMap& headers, bool end_stream) override;

private:
  int64_t monoNowNs() const;

  TimeSource& time_source_;
  Upstream::HostDescriptionConstSharedPtr host_;
  int64_t sent_mono_ns_{0};
  bool reported_{false};
};

} // namespace TestbedTokenBucket
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
