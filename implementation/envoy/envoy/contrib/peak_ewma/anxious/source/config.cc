#include "contrib/peak_ewma/anxious/source/config.h"

#include <memory>

#include "envoy/registry/registry.h"

#include "absl/synchronization/mutex.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedAnxious {

absl::StatusOr<Http::FilterFactoryCb> TestbedAnxiousFilterFactory::createHttpFilterFactoryFromProtoTyped(
    const ProtoConfig& proto_config, Server::Configuration::ServerFactoryContext& context,
    Server::Configuration::ExtraFactoryContext&) {
  auto state = std::make_shared<AnxiousState>(proto_config, context.timeSource());
  // The router reads the hook from every worker. The newest state is kept alive for the rest of the process, so
  // a worker never sees a destroyed hook (configs are loaded once in the testbed).
  static absl::Mutex holder_mu;
  static std::shared_ptr<AnxiousState>* holder = new std::shared_ptr<AnxiousState>();
  {
    absl::MutexLock lock(&holder_mu);
    Router::TestbedRetryHook::set(state.get());
    *holder = state;
  }
  return [state](Http::FilterChainFactoryCallbacks& callbacks) -> void {
    callbacks.addStreamFilter(std::make_shared<AnxiousUpstreamFilter>(state));
  };
}

using UpstreamTestbedAnxiousFilterFactory = TestbedAnxiousFilterFactory;

REGISTER_FACTORY(TestbedAnxiousFilterFactory, Server::Configuration::NamedHttpFilterConfigFactory);
REGISTER_FACTORY(UpstreamTestbedAnxiousFilterFactory, Server::Configuration::UpstreamHttpFilterConfigFactory);

} // namespace TestbedAnxious
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
