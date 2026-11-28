// Source: abstraction/load_generator/client/design.md (client v0.11.0)
// Source: abstraction/protocol/design.md (protocol v0.8.0)
// impl/envoy: tests for the ping RequestSource's body format (abstraction/protocol/design.md#pingrequestbody)
// and the per-request log's field parsing (source/client/request_log.h).

#include <string>

#include "source/client/request_log.h"
#include "source/common/http/header_map_impl.h"
#include "source/request_source/ping_request_source_plugin_impl.h"

#include "gtest/gtest.h"

namespace Nighthawk {
namespace {

// Design: abstraction/protocol/design.md#pingrequestbody
TEST(PingRequestBody, MatchesGoBuildRequestBody) {
  // Go: protocol.BuildRequestBody(MakeRequestID(1, 1), 5, 76): 36 fixed + 10 id digits + 1 deadline digit
  // + 29 payload bytes.
  const std::string body = BuildPingRequestBody((1ULL << 32) | 1, 5, 76);
  EXPECT_EQ(body, R"({"id":"4294967297","deadline":"5","payload":")" + std::string(29, 'x') + R"("})");
  EXPECT_EQ(body.size(), 76u);
}

// Design: abstraction/protocol/design.md#minimum-body-size
TEST(PingRequestBody, LongestIdAndDeadlineNeed76Bytes) {
  EXPECT_EQ(BuildPingRequestBody(~0ULL, ~0ULL, 76).size(), 76u); // 36 + 20 + 20, empty payload
  EXPECT_EQ(BuildPingRequestBody(~0ULL, ~0ULL, 75), "");
}

// Design: abstraction/protocol/design.md#requestdeadlineheader
// Design: abstraction/load_generator/client/design.md#requestdeadline
TEST(PingRequestSource, DeadlineHeaderMatchesBody) {
  PingRequestSourcePlugin source(3, 128, 17000, "/ping", Envoy::Http::RequestHeaderMapImpl::create());
  const auto generate = source.get();
  for (uint32_t seq = 0; seq < 2; ++seq) {
    const auto request = generate();
    const auto values =
        request->header()->get(Envoy::Http::LowerCaseString(std::string(kPingDeadlineHeader)));
    ASSERT_EQ(values.size(), 1u);
    const std::string header_deadline(values[0]->value().getStringView());
    EXPECT_EQ(Client::RequestLog::stringField(request->body(), "deadline"), header_deadline);
    EXPECT_EQ(Client::RequestLog::requestId(request->body()), std::to_string((3ULL << 32) | seq));
    EXPECT_EQ(request->body().size(), 128u);
  }
}

// Design: abstraction/protocol/design.md#parsing
// Design: abstraction/protocol/design.md#execreport
TEST(RequestLog, ParsesIdAndExecReport) {
  EXPECT_EQ(Client::RequestLog::requestId(R"({"id":"42","deadline":"7","payload":"xx"})"), "42");
  const std::string reply = R"({"id":"42","exec_ns":"400000000","start_slack_ns":"-12","payload":"x"})";
  EXPECT_EQ(Client::RequestLog::stringField(reply, "exec_ns"), "400000000");
  EXPECT_EQ(Client::RequestLog::stringField(reply, "start_slack_ns"), "-12");
  EXPECT_EQ(Client::RequestLog::stringField(R"({"id":"1","start_slack_ns":"","payload":"x"})", "start_slack_ns"),
            "");
  EXPECT_EQ(Client::RequestLog::stringField("not json", "exec_ns"), "");
  EXPECT_EQ(Client::RequestLog::stringField(R"({"exec_ns":"12)", "exec_ns"), ""); // truncated prefix
}

} // namespace
} // namespace Nighthawk
