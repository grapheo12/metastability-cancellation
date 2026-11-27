#pragma once

// impl/envoy (cancellation testbed): factory of the testbed_token_bucket HTTP filter (filter.h), registered for
// upstream (cluster) filter chains.

#include "source/extensions/filters/http/common/factory_base.h"

#include "contrib/envoy/extensions/filters/http/testbed_token_bucket/v3alpha/testbed_token_bucket.pb.h"
#include "contrib/envoy/extensions/filters/http/testbed_token_bucket/v3alpha/testbed_token_bucket.pb.validate.h"
#include "contrib/peak_ewma/token_bucket/source/filter.h"

namespace Envoy {
namespace Extensions {
namespace HttpFilters {
namespace TestbedTokenBucket {

using ProtoConfig = envoy::extensions::filters::http::testbed_token_bucket::v3alpha::TestbedTokenBucket;

class TestbedTokenBucketFilterFactory : public Common::UnifiedFactoryBase<ProtoConfig> {
public:
  TestbedTokenBucketFilterFactory() : UnifiedFactoryBase("envoy.filters.http.testbed_token_bucket") {}

private:
  absl::StatusOr<Http::FilterFactoryCb>
  createHttpFilterFactoryFromProtoTyped(const ProtoConfig& proto_config,
                                        Server::Configuration::ServerFactoryContext& context,
                                        Server::Configuration::ExtraFactoryContext& extra_context) override;
};

} // namespace TestbedTokenBucket
} // namespace HttpFilters
} // namespace Extensions
} // namespace Envoy
