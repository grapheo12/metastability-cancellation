#pragma once

// impl/envoy (cancellation testbed): factory of the testbed_anxious HTTP filter (filter.h), registered for upstream
// (cluster) filter chains; creating it registers its AnxiousState as the router's TestbedRetryHook.

#include "source/extensions/filters/http/common/factory_base.h"

#include "contrib/envoy/extensions/filters/http/testbed_anxious/v3alpha/testbed_anxious.pb.h"
#include "contrib/envoy/extensions/filters/http/testbed_anxious/v3alpha/testbed_anxious.pb.validate.h"
#include "contrib/peak_ewma/anxious/source/filter.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedAnxious {

class TestbedAnxiousFilterFactory : public Common::UnifiedFactoryBase<ProtoConfig> {
public:
  TestbedAnxiousFilterFactory() : UnifiedFactoryBase("envoy.filters.http.testbed_anxious") {}

private:
  absl::StatusOr<Http::FilterFactoryCb>
  createHttpFilterFactoryFromProtoTyped(const ProtoConfig& proto_config,
                                        Server::Configuration::ServerFactoryContext& context,
                                        Server::Configuration::ExtraFactoryContext& extra_context) override;
};

} // namespace TestbedAnxious
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
