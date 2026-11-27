#include "contrib/peak_ewma/token_bucket/source/config.h"

#include <memory>

#include "envoy/registry/registry.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedTokenBucket {

absl::StatusOr<Http::FilterFactoryCb> TestbedTokenBucketFilterFactory::createHttpFilterFactoryFromProtoTyped(
    const ProtoConfig&, Server::Configuration::ServerFactoryContext& context,
    Server::Configuration::ExtraFactoryContext&) {
  TimeSource& time_source = context.timeSource();
  return [&time_source](Http::FilterChainFactoryCallbacks& callbacks) -> void {
    callbacks.addStreamFilter(std::make_shared<TokenRateFilter>(time_source));
  };
}

using UpstreamTestbedTokenBucketFilterFactory = TestbedTokenBucketFilterFactory;
REGISTER_FACTORY(TestbedTokenBucketFilterFactory, Server::Configuration::NamedHttpFilterConfigFactory);
REGISTER_FACTORY(UpstreamTestbedTokenBucketFilterFactory, Server::Configuration::UpstreamHttpFilterConfigFactory);

} // namespace TestbedTokenBucket
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
