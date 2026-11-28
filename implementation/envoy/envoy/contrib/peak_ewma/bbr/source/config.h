#pragma once

// impl/envoy (cancellation testbed): factory of the testbed_bbr HTTP filter (filter.h), registered for
// upstream (cluster) filter chains.

#include "source/extensions/filters/http/common/factory_base.h"

#include "contrib/envoy/extensions/filters/http/testbed_bbr/v3alpha/testbed_bbr.pb.h"
#include "contrib/envoy/extensions/filters/http/testbed_bbr/v3alpha/testbed_bbr.pb.validate.h"
#include "contrib/peak_ewma/bbr/source/filter.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedBbr {

using ProtoConfig = envoy::extensions::filters::http::testbed_bbr::v3alpha::TestbedBbr;

class TestbedBbrFilterFactory : public Common::UnifiedFactoryBase<ProtoConfig> {
public:
  TestbedBbrFilterFactory() : UnifiedFactoryBase("envoy.filters.http.testbed_bbr") {}

private:
  absl::StatusOr<Http::FilterFactoryCb>
  createHttpFilterFactoryFromProtoTyped(const ProtoConfig& proto_config,
                                        Server::Configuration::ServerFactoryContext& context,
                                        Server::Configuration::ExtraFactoryContext& extra_context) override;
};

} // namespace TestbedBbr
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
