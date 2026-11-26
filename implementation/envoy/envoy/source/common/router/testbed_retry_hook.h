#pragma once

// impl/envoy (cancellation testbed): a process-wide hook that lets a retry policy start an LB retry (a hedge) of an
// upstream attempt that has not answered, decided anew every kTestbedRetryHookTick, like the Go Ingress's
// LbRetryLoop ticks (abstraction/ingress/frontend/design.md#lbretryloop). When a hook is registered, the router
// arms a repeating timer on each upstream attempt once its request is sent and, on the newest attempt of a request
// that is not answered yet, asks the hook; on yes it starts a retry as for a hedged per-try timeout (the slow
// attempt keeps running). Without a hook the router behaves as upstream Envoy.

#include <atomic>
#include <chrono>

#include "envoy/http/header_map.h"
#include "envoy/stream_info/stream_info.h"

namespace Envoy {
namespace Router {

constexpr std::chrono::milliseconds kTestbedRetryHookTick{10}; // LB_RETRY_TICK_MS

class TestbedRetryHook {
public:
  virtual ~TestbedRetryHook() = default;

  // Whether to retry the request's newest attempt now. request_info: the request's (downstream) stream info; its
  // attemptCount() is the newest attempt's number (upstream HTTP filters see this same stream info, so a policy's
  // upstream filter can record per-attempt state in its filter state keyed by that number); request: the request
  // headers. Called on the worker thread that owns the request.
  virtual bool shouldRetryNow(const StreamInfo::StreamInfo& request_info,
                              const Http::RequestHeaderMap& request) = 0;

  // The registered hook, or nullptr. set() replaces it (nullptr unregisters); the hook must outlive every use.
  static TestbedRetryHook* get() { return hook_.load(std::memory_order_acquire); }
  static void set(TestbedRetryHook* hook) { hook_.store(hook, std::memory_order_release); }

private:
  static std::atomic<TestbedRetryHook*> hook_;
};

} // namespace Router
} // namespace Envoy
