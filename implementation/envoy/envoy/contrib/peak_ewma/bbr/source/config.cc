#include "contrib/peak_ewma/bbr/source/config.h"

#include <memory>

#include "envoy/api/api.h"
#include "envoy/registry/registry.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedBbr {

absl::StatusOr<Http::FilterFactoryCb> TestbedBbrFilterFactory::createHttpFilterFactoryFromProtoTyped(
    const ProtoConfig&, Server::Configuration::ServerFactoryContext& context,
    Server::Configuration::ExtraFactoryContext&) {
  TimeSource& time_source = context.timeSource();
  Random::RandomGenerator& random = context.api().randomGenerator();
  return [&time_source, &random](Http::FilterChainFactoryCallbacks& callbacks) -> void {
    callbacks.addStreamFilter(std::make_shared<BbrAttemptFilter>(time_source, random));
  };
}

using UpstreamTestbedBbrFilterFactory = TestbedBbrFilterFactory;
REGISTER_FACTORY(TestbedBbrFilterFactory, Server::Configuration::NamedHttpFilterConfigFactory);
REGISTER_FACTORY(UpstreamTestbedBbrFilterFactory, Server::Configuration::UpstreamHttpFilterConfigFactory);

} // namespace TestbedBbr
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
