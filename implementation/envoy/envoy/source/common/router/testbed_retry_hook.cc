#include "source/common/router/testbed_retry_hook.h"

namespace Envoy {
namespace Router {

std::atomic<TestbedRetryHook*> TestbedRetryHook::hook_{nullptr};

} // namespace Router
} // namespace Envoy
