#include "contrib/peak_ewma/load_balancing_policies/source/deadline_config.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

Upstream::LoadBalancerPtr PeakEwmaDeadlineCreator::operator()(
    Upstream::LoadBalancerParams /* params */, OptRef<const Upstream::LoadBalancerConfig> lb_config,
    const Upstream::ClusterInfo& cluster_info, const Upstream::PrioritySet& priority_set,
    Runtime::Loader& runtime, Envoy::Random::RandomGenerator& random, TimeSource& time_source) {
  const auto* config = dynamic_cast<const TypedPeakEwmaDeadlineLbConfig*>(lb_config.ptr());
  if (config == nullptr) {
    ENVOY_LOG(error, "Peak EWMA deadline load balancer config is required");
    return nullptr;
  }
  // As the Peak EWMA creator (config.cc): one balancer per worker over the cluster's priority set.
  return std::make_unique<PeakEwmaDeadlineLoadBalancer>(
      priority_set, nullptr, cluster_info.lbStats(), runtime, random,
      PROTOBUF_PERCENT_TO_ROUNDED_INTEGER_OR_DEFAULT(cluster_info.lbConfig(),
                                                     healthy_panic_threshold, 100, 50),
      cluster_info, time_source, config->lb_config_);
}

/**
 * Static registration for the Factory. @see RegisterFactory.
 */
REGISTER_FACTORY(DeadlineFactory, Upstream::TypedLoadBalancerFactory);

} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
