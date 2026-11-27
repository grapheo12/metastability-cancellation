// Source: abstraction/protocol/design.md (protocol v0.8.0)
#include "source/client/request_log.h"

#include <atomic>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <string>

#include "absl/strings/str_cat.h"

namespace Nighthawk {
namespace Client {
namespace RequestLog {

namespace {

const char* prefix() {
  static const char* p = std::getenv("NH_REQUEST_LOG_PREFIX");
  return p;
}

// One file per thread, closed when the thread exits.
class ThreadFile {
public:
  ~ThreadFile() {
    if (file_ != nullptr) {
      std::fclose(file_);
    }
  }
  std::FILE* get() {
    if (file_ == nullptr) {
      static std::atomic<int> next_index{0};
      const std::string path = absl::StrCat(prefix(), ".", next_index++, ".csv");
      file_ = std::fopen(path.c_str(), "w");
      if (file_ != nullptr) {
        std::setvbuf(file_, nullptr, _IOFBF, 1 << 20);
        std::fputs("id,result,http_status,scheduled_unix_ns,send_unix_ns,end_unix_ns,exec_ns,start_slack_ns\n",
                   file_);
      }
    }
    return file_;
  }

private:
  std::FILE* file_{nullptr};
};

} // namespace

bool enabled() {
  static const bool on = prefix() != nullptr && prefix()[0] != '\0';
  return on;
}

int64_t wallNowNs() {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(
             std::chrono::system_clock::now().time_since_epoch())
      .count();
}

// Design: abstraction/protocol/design.md#parsing
std::string stringField(absl::string_view json, absl::string_view key) {
  const std::string needle = absl::StrCat("\"", key, "\":\"");
  const size_t at = json.find(needle);
  if (at == absl::string_view::npos) {
    return "";
  }
  const size_t start = at + needle.size();
  const size_t end = json.find('"', start);
  if (end == absl::string_view::npos) {
    return "";
  }
  return std::string(json.substr(start, end - start));
}

// Design: abstraction/protocol/design.md#parsing
std::string requestId(absl::string_view request_body) { return stringField(request_body, "id"); }

void record(absl::string_view request_body, absl::string_view result, uint64_t http_status,
            int64_t scheduled_unix_ns, int64_t send_unix_ns, int64_t end_unix_ns,
            absl::string_view response_prefix) {
  thread_local ThreadFile file;
  std::FILE* f = file.get();
  if (f == nullptr) {
    return;
  }
  // Design: abstraction/protocol/design.md#execreport
  const std::string line =
      absl::StrCat(requestId(request_body), ",", result, ",", http_status, ",", scheduled_unix_ns, ",",
                   send_unix_ns, ",", end_unix_ns, ",", stringField(response_prefix, "exec_ns"), ",",
                   stringField(response_prefix, "start_slack_ns"), "\n");
  std::fputs(line.c_str(), f);
}

} // namespace RequestLog
} // namespace Client
} // namespace Nighthawk
