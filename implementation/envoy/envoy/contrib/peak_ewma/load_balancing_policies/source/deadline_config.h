#pragma once

// impl/envoy (cancellation testbed): factory of the peak_ewma_deadline load balancing policy (deadline_lb.h).

#include "envoy/upstream/load_balancer.h"

#include "source/common/common/logger.h"
#include "source/extensions/load_balancing_policies/common/factory_base.h"

#include "contrib/envoy/extensions/load_balancing_policies/peak_ewma_deadline/v3alpha/peak_ewma_deadline.pb.h"
#include "contrib/envoy/extensions/load_balancing_policies/peak_ewma_deadline/v3alpha/peak_ewma_deadline.pb.validate.h"
#include "contrib/peak_ewma/load_balancing_policies/source/deadline_lb.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

using PeakEwmaDeadlineLbProto = DeadlineConfig;

class TypedPeakEwmaDeadlineLbConfig : public Upstream::LoadBalancerConfig {
public:
  explicit TypedPeakEwmaDeadlineLbConfig(const PeakEwmaDeadlineLbProto& lb_config) : lb_config_(lb_config) {}

  PeakEwmaDeadlineLbProto lb_config_;
};

struct PeakEwmaDeadlineCreator : public Logger::Loggable<Logger::Id::upstream> {
  Upstream::LoadBalancerPtr operator()(
      Upstream::LoadBalancerParams params, OptRef<const Upstream::LoadBalancerConfig> lb_config,
      const Upstream::ClusterInfo& cluster_info, const Upstream::PrioritySet& priority_set,
      Runtime::Loader& runtime, Envoy::Random::RandomGenerator& random, TimeSource& time_source);
};

class DeadlineFactory
    : public ::Envoy::Extensions::LoadBalancingPolicies::Common::FactoryBase<PeakEwmaDeadlineLbProto,
                                                                             PeakEwmaDeadlineCreator> {
public:
  DeadlineFactory() : FactoryBase("envoy.load_balancing_policies.peak_ewma_deadline") {}

  absl::StatusOr<Upstream::LoadBalancerConfigPtr>
  loadConfig(Server::Configuration::ServerFactoryContext&,
             const Protobuf::Message& config) override {
    const auto& typed_config = dynamic_cast<const PeakEwmaDeadlineLbProto&>(config);
    return Upstream::LoadBalancerConfigPtr{new TypedPeakEwmaDeadlineLbConfig(typed_config)};
  }
};

DECLARE_FACTORY(DeadlineFactory);

} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
