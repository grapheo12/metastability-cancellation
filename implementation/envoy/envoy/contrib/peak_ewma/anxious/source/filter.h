#pragma once

// impl/envoy (cancellation testbed): the testbed_anxious upstream filter and the AnxiousRetry router hook. One
// filter instance runs per upstream attempt (it is configured in the cluster's upstream HTTP filters):
//   decodeHeaders  AttemptSent: a new TicketSeq, the attempt's backend and the request's deadline
//                  (x-request-deadline, decimal Unix ns; NO_DEADLINE without it), recorded in the AnxiousTracker
//                  and, as an AnxiousAttempt, in the request's stream-info filter state under
//                  kAttemptFilterStateKey + "." + the attempt's number (upstream filters see the request's stream
//                  info, and its attemptCount() is the number of the attempt being started);
//   encodeData     the ExecReport's exec_ns of a 202 or 503 answer (first 512 bytes): EwmaExecTime /
//                  EwmaAntiServiceTime;
//   onDestroy      AttemptFinished (answered or reset).
// AnxiousState is the router's TestbedRetryHook: every 10 ms the router asks it about a request's newest attempt;
// it runs a pending AnxiousEvaluation and answers AnxiousTracker::decide (anxious and ExpectedLate).

#include <atomic>
#include <cstdio>
#include <memory>
#include <string>

#include "envoy/common/time.h"
#include "envoy/http/filter.h"
#include "envoy/stream_info/filter_state.h"

#include "source/common/router/testbed_retry_hook.h"
#include "source/extensions/filters/http/common/pass_through_filter.h"

#include "absl/synchronization/mutex.h"
#include "contrib/envoy/extensions/filters/http/testbed_anxious/v3alpha/testbed_anxious.pb.h"
#include "contrib/peak_ewma/anxious/source/anxious_tracker.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedAnxious {

using ProtoConfig = envoy::extensions::filters::http::testbed_anxious::v3alpha::TestbedAnxious;

constexpr absl::string_view kAttemptFilterStateKey = "envoy.testbed.anxious.attempt"; // + "." + attempt number
constexpr size_t kExecReportPrefixBytes = 512;

// An attempt's TicketSeq and backend, on its stream info (read by the router hook).
class AnxiousAttempt : public StreamInfo::FilterState::Object {
public:
  AnxiousAttempt(uint64_t seq, size_t backend) : seq_(seq), backend_(backend) {}
  uint64_t seq() const { return seq_; }
  size_t backend() const { return backend_; }

private:
  const uint64_t seq_;
  const size_t backend_;
};

// The exec_ns string value of an ExecReport body prefix ({"id":"..","exec_ns":"<dec>",...}), or none.
absl::optional<uint64_t> parseExecNs(absl::string_view body);

class AnxiousState : public Router::TestbedRetryHook {
public:
  AnxiousState(const ProtoConfig& config, TimeSource& time_source);
  ~AnxiousState() override;

  AnxiousTracker& tracker() { return tracker_; }
  uint64_t nextSeq() { return next_seq_.fetch_add(1); }
  absl::optional<uint64_t> deadlineOf(const Http::RequestHeaderMap& headers) const;
  void onExecSample(size_t backend, uint64_t exec_ns);

  // Router::TestbedRetryHook
  bool shouldRetryNow(const StreamInfo::StreamInfo& request_info,
                      const Http::RequestHeaderMap& request) override;

private:
  int64_t unixNowNs() const;
  int64_t monoNowNs() const;
  std::FILE* traceFile(std::FILE*& f, const char* kind, const char* header) ABSL_EXCLUSIVE_LOCKS_REQUIRED(trace_mu_);

  TimeSource& time_source_;
  const Http::LowerCaseString deadline_header_;
  AnxiousTracker tracker_;
  std::atomic<uint64_t> next_seq_{1};

  // Trace (PEAK_EWMA_TRACE_PREFIX): <prefix>.anxious.0.csv (evaluations, retries decided) and <prefix>.exec.0.csv.
  const std::string trace_prefix_;
  absl::Mutex trace_mu_;
  std::FILE* anxious_trace_ ABSL_GUARDED_BY(trace_mu_) = nullptr;
  std::FILE* exec_trace_ ABSL_GUARDED_BY(trace_mu_) = nullptr;
};

class AnxiousUpstreamFilter : public Http::PassThroughFilter {
public:
  explicit AnxiousUpstreamFilter(std::shared_ptr<AnxiousState> state) : state_(std::move(state)) {}

  Http::FilterHeadersStatus decodeHeaders(Http::RequestHeaderMap& headers, bool end_stream) override;
  Http::FilterHeadersStatus encodeHeaders(Http::ResponseHeaderMap& headers, bool end_stream) override;
  Http::FilterDataStatus encodeData(Buffer::Instance& data, bool end_stream) override;
  void onDestroy() override;

private:
  void maybeParseExec(bool end);

  const std::shared_ptr<AnxiousState> state_;
  absl::optional<size_t> backend_;
  uint64_t seq_{0};
  bool sent_{false};
  uint64_t status_{0};
  std::string body_prefix_;
  bool parsed_{false};
};

} // namespace TestbedAnxious
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
