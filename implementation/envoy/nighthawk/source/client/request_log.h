#pragma once

// impl/envoy: per-request log for the testbed's load-generator metrics (the Requests CSV,
// abstraction/load_generator/metrics/api.md). Enabled by the environment variable NH_REQUEST_LOG_PREFIX:
// each worker thread appends one CSV line per finished request to <prefix>.<n>.csv (n: 0, 1, ... in the order
// threads first record), with the header
//   id,result,http_status,scheduled_unix_ns,send_unix_ns,end_unix_ns,exec_ns,start_slack_ns
// result is "response" (headers received, http_status set), "reset" (stream reset, e.g. at shutdown) or
// "pool_failure" (no connection). exec_ns and start_slack_ns are copied verbatim from the response body's
// ExecReport (abstraction/protocol/design.md#execreport), empty when absent. Outcome classification is left to
// the wrapper that turns these files into the Requests CSV.

#include <cstdint>
#include <string>

#include "absl/strings/string_view.h"

namespace Nighthawk {
namespace Client {
namespace RequestLog {

// True when NH_REQUEST_LOG_PREFIX is set (read once).
bool enabled();

// Wall-clock time in Unix ns.
int64_t wallNowNs();

// The decimal "id" of a PingRequestBody ({"id":"<dec>",...}), or "" if absent.
std::string requestId(absl::string_view request_body);

// The string value of "key":"<value>" in a JSON object prefix, or "" if absent.
std::string stringField(absl::string_view json, absl::string_view key);

// Appends one line to this thread's file. response_prefix: the first bytes of the response body.
void record(absl::string_view request_body, absl::string_view result, uint64_t http_status,
            int64_t scheduled_unix_ns, int64_t send_unix_ns, int64_t end_unix_ns,
            absl::string_view response_prefix);

} // namespace RequestLog
} // namespace Client
} // namespace Nighthawk
