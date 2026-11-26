#include "contrib/peak_ewma/anxious/source/filter.h"

#include <chrono>
#include <cstdlib>

#include "source/common/http/utility.h"

#include "absl/strings/numbers.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/str_format.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedAnxious {

absl::optional<uint64_t> parseExecNs(absl::string_view body) {
  constexpr absl::string_view needle = "\"exec_ns\":\"";
  const size_t at = body.find(needle);
  if (at == absl::string_view::npos) {
    return absl::nullopt;
  }
  const size_t start = at + needle.size();
  const size_t end = body.find('"', start);
  uint64_t value;
  if (end == absl::string_view::npos || !absl::SimpleAtoi(body.substr(start, end - start), &value)) {
    return absl::nullopt;
  }
  return value;
}

namespace {
std::vector<std::string> backendsOf(const ProtoConfig& config) {
  return {config.backends().begin(), config.backends().end()};
}
const char* tracePrefixEnv() {
  const char* p = std::getenv("PEAK_EWMA_TRACE_PREFIX");
  return p != nullptr ? p : "";
}
} // namespace

AnxiousState::AnxiousState(const ProtoConfig& config, TimeSource& time_source)
    : time_source_(time_source),
      deadline_header_(config.deadline_header().empty() ? std::string("x-request-deadline")
                                                        : config.deadline_header()),
      tracker_(backendsOf(config), config.alpha(), config.beta(), config.ewma_beta()),
      trace_prefix_(tracePrefixEnv()) {}

AnxiousState::~AnxiousState() {
  if (Router::TestbedRetryHook::get() == this) {
    Router::TestbedRetryHook::set(nullptr);
  }
  absl::MutexLock lock(&trace_mu_);
  for (std::FILE* f : {anxious_trace_, exec_trace_}) {
    if (f != nullptr) {
      std::fclose(f);
    }
  }
}

int64_t AnxiousState::unixNowNs() const {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(time_source_.systemTime().time_since_epoch())
      .count();
}

int64_t AnxiousState::monoNowNs() const {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(time_source_.monotonicTime().time_since_epoch())
      .count();
}

absl::optional<uint64_t> AnxiousState::deadlineOf(const Http::RequestHeaderMap& headers) const {
  const auto values = headers.get(deadline_header_);
  uint64_t deadline;
  if (values.empty() || !absl::SimpleAtoi(values[0]->value().getStringView(), &deadline)) {
    return absl::nullopt;
  }
  return deadline;
}

std::FILE* AnxiousState::traceFile(std::FILE*& f, const char* kind, const char* header) {
  if (trace_prefix_.empty()) {
    return nullptr;
  }
  if (f == nullptr) {
    f = std::fopen(absl::StrCat(trace_prefix_, ".", kind, ".0.csv").c_str(), "w");
    if (f != nullptr) {
      std::setvbuf(f, nullptr, _IOLBF, 1 << 16);
      std::fputs(header, f);
    }
  }
  return f;
}

void AnxiousState::onExecSample(size_t backend, uint64_t exec_ns) {
  tracker_.execSample(backend, exec_ns);
  absl::MutexLock lock(&trace_mu_);
  std::FILE* f = traceFile(exec_trace_, "exec", "t_unix_ns,host,exec_ns,backend,ewma_exec_ns,ewma_anti_ns\n");
  if (f == nullptr) {
    return;
  }
  const long long now = static_cast<long long>(unixNowNs());
  for (size_t c = 0; c < tracker_.backendCount(); ++c) { // one row per backend after the sample
    const absl::optional<double> e = tracker_.exec(c);
    const absl::optional<double> a = tracker_.anti(c);
    std::fprintf(f, "%lld,%s,%llu,%s,%s,%s\n", now, tracker_.backendName(backend).c_str(),
                 static_cast<unsigned long long>(exec_ns), tracker_.backendName(c).c_str(),
                 e.has_value() ? absl::StrFormat("%.17g", *e).c_str() : "",
                 a.has_value() ? absl::StrFormat("%.17g", *a).c_str() : "");
  }
}

bool AnxiousState::shouldRetryNow(const StreamInfo::StreamInfo& request_info,
                                  const Http::RequestHeaderMap& request) {
  // The newest attempt's record (the router only asks about the newest attempt; its number is attemptCount()).
  const auto* attempt = request_info.filterState().getDataReadOnly<AnxiousAttempt>(
      absl::StrCat(kAttemptFilterStateKey, ".", request_info.attemptCount().value_or(1)));
  if (attempt == nullptr) {
    return false; // not a tracked attempt
  }
  if (tracker_.maybeEvaluate(monoNowNs())) { // AnxiousEvaluation (only after a change, at most every 10 ms)
    absl::MutexLock lock(&trace_mu_);
    std::FILE* f = traceFile(anxious_trace_, "anxious",
                             "t_unix_ns,event,seq,host,current_ns,remaining_ns,in_flight,anxious\n");
    if (f != nullptr) {
      std::fprintf(f, "%lld,eval,,,,,%zu,%zu\n", static_cast<long long>(unixNowNs()), tracker_.inFlight(),
                   tracker_.anxiousCount());
    }
  }
  const absl::optional<uint64_t> deadline = deadlineOf(request);
  absl::optional<int64_t> remaining;
  if (deadline.has_value()) {
    remaining = static_cast<int64_t>(*deadline) - unixNowNs();
  }
  const bool retry = tracker_.decide(attempt->seq(), remaining);
  if (retry) {
    const absl::optional<double> current = tracker_.currentExpFinish(attempt->seq());
    absl::MutexLock lock(&trace_mu_);
    std::FILE* f = traceFile(anxious_trace_, "anxious",
                             "t_unix_ns,event,seq,host,current_ns,remaining_ns,in_flight,anxious\n");
    if (f != nullptr) {
      std::fprintf(f, "%lld,retry,%llu,%s,%s,%lld,,\n", static_cast<long long>(unixNowNs()),
                   static_cast<unsigned long long>(attempt->seq()),
                   tracker_.backendName(attempt->backend()).c_str(),
                   current.has_value() ? absl::StrFormat("%.17g", *current).c_str() : "",
                   static_cast<long long>(remaining.value_or(0)));
    }
  }
  return retry;
}

Http::FilterHeadersStatus AnxiousUpstreamFilter::decodeHeaders(Http::RequestHeaderMap& headers, bool) {
  StreamInfo::StreamInfo& info = decoder_callbacks_->streamInfo();
  const auto upstream = info.upstreamInfo();
  if (upstream != nullptr && upstream->upstreamHost() != nullptr) {
    backend_ = state_->tracker().backendIndex(upstream->upstreamHost()->address()->asString());
  }
  if (backend_.has_value()) { // AttemptSent
    seq_ = state_->nextSeq();
    state_->tracker().attemptSent(seq_, *backend_, state_->deadlineOf(headers).value_or(kNoDeadline));
    sent_ = true;
    // Upstream filters see the request's stream info: one record per attempt, keyed by the attempt's number.
    info.filterState()->setData(absl::StrCat(kAttemptFilterStateKey, ".", info.attemptCount().value_or(1)),
                                std::make_shared<AnxiousAttempt>(seq_, *backend_),
                                StreamInfo::FilterState::LifeSpan::FilterChain);
  }
  return Http::FilterHeadersStatus::Continue;
}

Http::FilterHeadersStatus AnxiousUpstreamFilter::encodeHeaders(Http::ResponseHeaderMap& headers,
                                                               bool end_stream) {
  status_ = Http::Utility::getResponseStatus(headers);
  if (end_stream) {
    maybeParseExec(true);
  }
  return Http::FilterHeadersStatus::Continue;
}

Http::FilterDataStatus AnxiousUpstreamFilter::encodeData(Buffer::Instance& data, bool end_stream) {
  if (!parsed_ && body_prefix_.size() < kExecReportPrefixBytes) {
    const size_t take = std::min<size_t>(kExecReportPrefixBytes - body_prefix_.size(), data.length());
    const size_t old = body_prefix_.size();
    body_prefix_.resize(old + take);
    data.copyOut(0, take, body_prefix_.data() + old);
  }
  maybeParseExec(end_stream || body_prefix_.size() >= kExecReportPrefixBytes);
  return Http::FilterDataStatus::Continue;
}

void AnxiousUpstreamFilter::maybeParseExec(bool end) {
  if (parsed_ || !backend_.has_value() || (status_ != 202 && status_ != 503)) {
    return; // only ok and rejected answers carry a sample
  }
  const absl::optional<uint64_t> exec_ns = parseExecNs(body_prefix_);
  if (exec_ns.has_value()) {
    parsed_ = true;
    state_->onExecSample(*backend_, *exec_ns);
  } else if (end) {
    parsed_ = true; // no ExecReport in this answer
  }
}

void AnxiousUpstreamFilter::onDestroy() {
  if (sent_) { // AttemptFinished
    state_->tracker().attemptFinished(seq_, *backend_);
    sent_ = false;
  }
}

} // namespace TestbedAnxious
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
