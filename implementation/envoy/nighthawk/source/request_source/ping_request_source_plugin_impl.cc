// Source: abstraction/protocol/design.md (protocol v0.8.0)
#include "source/request_source/ping_request_source_plugin_impl.h"

#include <chrono>
#include <memory>
#include <string>

#include "absl/strings/str_cat.h"

#include "envoy/config/core/v3/base.pb.h"
#include "envoy/registry/registry.h"
#include "source/common/http/header_map_impl.h"
#include "source/common/protobuf/utility.h"

#include "source/common/request_impl.h"

namespace Nighthawk {

namespace {
// {"id":"  ","deadline":"  ","payload":"  "}: 7 + 14 + 13 + 2 (protocol.requestBodyFixedOverhead).
// Design: abstraction/protocol/design.md#pingrequestbody
constexpr uint32_t kRequestBodyFixedOverhead = 36;
} // namespace

// Design: abstraction/protocol/design.md#pingrequestbody
std::string BuildPingRequestBody(uint64_t id, uint64_t deadline_unix_ns, uint32_t total_bytes) {
  const std::string dec = absl::StrCat(id);
  const std::string dl = absl::StrCat(deadline_unix_ns);
  const uint64_t fixed = kRequestBodyFixedOverhead + dec.size() + dl.size();
  if (total_bytes < fixed) {
    return "";
  }
  std::string body;
  body.reserve(total_bytes);
  absl::StrAppend(&body, R"({"id":")", dec, R"(","deadline":")", dl, R"(","payload":")");
  body.append(total_bytes - fixed, 'x');
  body.append(R"("})");
  return body;
}

Nighthawk::RequestGenerator PingRequestSourcePlugin::get() {
  return [this]() -> std::unique_ptr<Nighthawk::Request> {
    // Design: abstraction/protocol/design.md#requestid
    const uint64_t id = (static_cast<uint64_t>(client_id_) << 32) | next_seq_++;
    const uint64_t now_ns = static_cast<uint64_t>(
        std::chrono::duration_cast<std::chrono::nanoseconds>(
            std::chrono::system_clock::now().time_since_epoch())
            .count());
    const uint64_t deadline_ns = now_ns + slo_ns_;
    std::string body = BuildPingRequestBody(id, deadline_ns, request_bytes_);
    Envoy::Http::RequestHeaderMapPtr headers = Envoy::Http::RequestHeaderMapImpl::create();
    Envoy::Http::HeaderMapImpl::copyFrom(*headers, *header_);
    headers->setMethod(
        envoy::config::core::v3::RequestMethod_Name(envoy::config::core::v3::RequestMethod::POST));
    headers->setContentType("application/json");
    headers->setContentLength(body.size());
    headers->setPath(path_);
    // Design: abstraction/protocol/design.md#requestdeadlineheader
    headers->addCopy(Envoy::Http::LowerCaseString(std::string(kPingDeadlineHeader)), absl::StrCat(deadline_ns));
    return std::make_unique<Nighthawk::RequestImpl>(std::move(headers), std::move(body));
  };
}

Nighthawk::RequestSourcePtr
PingRequestSourcePluginFactory::createRequestSourcePlugin(const Envoy::Protobuf::Message& message,
                                                          Envoy::Api::Api&,
                                                          Envoy::Http::RequestHeaderMapPtr header) {
  const auto* any = Envoy::Protobuf::DynamicCastToGenerated<const Envoy::Protobuf::Any>(&message);
  nighthawk::PingRequestSourcePluginConfig config;
  THROW_IF_NOT_OK(Envoy::MessageUtil::unpackTo(*any, config));
  // Design: abstraction/protocol/design.md#minimum-body-size
  if (BuildPingRequestBody(~0ULL, ~0ULL, config.request_bytes()).empty()) {
    throw Envoy::EnvoyException(absl::StrCat("ping request source: request_bytes ", config.request_bytes(),
                                             " cannot hold the longest id and deadline (min 76)"));
  }
  const uint32_t client_id = config.client_id_base() + instances_++;
  return std::make_unique<PingRequestSourcePlugin>(
      client_id, config.request_bytes(), config.slo_ms(),
      config.path().empty() ? std::string("/ping") : config.path(), std::move(header));
}

REGISTER_FACTORY(PingRequestSourcePluginFactory, Nighthawk::RequestSourcePluginConfigFactory);

} // namespace Nighthawk
