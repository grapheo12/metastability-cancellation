#pragma once

// impl/envoy: a RequestSource that produces the testbed's PingRequestBody
// ({"id":"<dec>","deadline":"<unix ns>","payload":"xxx..."}, abstraction/protocol/design.md#pingrequestbody),
// so Nighthawk can drive the Go /ping server. One instance per worker; each instance is one client:
// RequestId = client_id << 32 | seq (abstraction/protocol/design.md#requestid).
// Each request also carries its RequestDeadline as the header x-request-deadline (decimal Unix ns, the body's
// "deadline" value), so an Envoy load balancer, which sees only headers, can filter by deadline.

#include <atomic>
#include <cstdint>
#include <memory>
#include <string>

#include "source/request_source/ping_request_source_plugin.pb.h"

#include "absl/strings/string_view.h"

#include "envoy/api/api.h"
#include "envoy/http/header_map.h"
#include "source/common/protobuf/protobuf.h"

#include "nighthawk/common/request_source.h"
#include "nighthawk/request_source/request_source_plugin_config_factory.h"

namespace Nighthawk {

constexpr inline absl::string_view kPingRequestSourcePluginName = "nighthawk.request_source.ping";
// The RequestDeadline header (decimal Unix ns), read by Envoy's peak_ewma_deadline load balancer.
constexpr inline absl::string_view kPingDeadlineHeader = "x-request-deadline";

// BuildPingRequestBody returns {"id":"<id>","deadline":"<deadline>","payload":"xxx..."} of exactly
// total_bytes bytes (the Go implementation's protocol.BuildRequestBody), or "" if total_bytes is too small.
std::string BuildPingRequestBody(uint64_t id, uint64_t deadline_unix_ns, uint32_t total_bytes);

class PingRequestSourcePlugin : public Nighthawk::RequestSource {
public:
  PingRequestSourcePlugin(uint32_t client_id, uint32_t request_bytes, uint64_t slo_ms, std::string path,
                          Envoy::Http::RequestHeaderMapPtr header)
      : client_id_(client_id), request_bytes_(request_bytes), slo_ns_(slo_ms * 1000000ULL),
        path_(std::move(path)), header_(std::move(header)) {}

  Nighthawk::RequestGenerator get() override;
  void initOnThread() override {}
  void destroyOnThread() override {}

private:
  const uint32_t client_id_;
  const uint32_t request_bytes_;
  const uint64_t slo_ns_;
  const std::string path_;
  Envoy::Http::RequestHeaderMapPtr header_;
  uint32_t next_seq_{0};
};

class PingRequestSourcePluginFactory : public virtual Nighthawk::RequestSourcePluginConfigFactory {
public:
  std::string name() const override { return std::string(kPingRequestSourcePluginName); }
  Envoy::ProtobufTypes::MessagePtr createEmptyConfigProto() override {
    return std::make_unique<nighthawk::PingRequestSourcePluginConfig>();
  }
  Nighthawk::RequestSourcePtr createRequestSourcePlugin(const Envoy::Protobuf::Message& message,
                                                        Envoy::Api::Api& api,
                                                        Envoy::Http::RequestHeaderMapPtr header) override;

private:
  std::atomic<uint32_t> instances_{0}; // client id offset of the next instance
};

} // namespace Nighthawk
