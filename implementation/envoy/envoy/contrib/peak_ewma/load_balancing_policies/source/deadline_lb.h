#pragma once

// impl/envoy (cancellation testbed): Peak EWMA with a deadline filter over every host and a FIFO wait queue,
// the testbed's DeadlineFilter and FilterWaitQueue (abstraction/ingress/load_balancer/design.md#deadlinefilter,
// #filterwaitqueue) with Peak EWMA's cost as the per-host test:
//
//   reject host h  iff  provisional_rtt_ms(h) * 1e6 > remaining_ns                         (binary64)
//   choose the kept host with the lowest Peak EWMA cost, ewma_rtt_ms * (rq_active + 1); ties at random
//
// ProvisionalCurrentRTT treats h's pending requests as if they were all answered now. With the time-based alpha
// only the first of several simultaneous samples gets weight, so it is the oldest pending request's latency:
//   provisional_rtt_ms(h) = ewma_rtt_ms(h)                              if nothing is pending at h
//                         = (1 - a) * ewma_rtt_ms(h) + a * L            otherwise (= L if h has no RTT sample yet)
//   L = (now - start of h's oldest pending request) / 1e6 ms,  a = 1 - e^(-max(now - last sample, 0) / tau)
// A host with no RTT sample and nothing pending is kept. The real EWMA is never changed by provisional values.
// Worked example (tau = 10 s, EWMA 400 ms, last sample and oldest pending both at the stall, t s ago):
//   t =  5 s: a = 0.3935, L =  5000 ms -> 0.6065 * 400 + 0.3935 *  5000 =  2210 ms
//   t = 10 s: a = 0.6321, L = 10000 ms -> 0.3679 * 400 + 0.6321 * 10000 =  6468 ms
//   t = 15 s: a = 0.7769, L = 15000 ms -> 0.2231 * 400 + 0.7769 * 15000 = 11743 ms
//   t = 20 s: a = 0.8647, L = 20000 ms -> 0.1353 * 400 + 0.8647 * 20000 = 17348 ms
// (Earlier versions: reject h iff ewma_rtt_ms(h) * 1e6 > remaining_ns; before that, keep h iff
// ewma_rtt_ms(h) * (rq_active(h) + 1) * 1e6 <= remaining_ns.)
//
// Pending requests: when this balancer hands a host to a request it records (host, now) in the host's
// PendingRequests (shared by all workers) and in the request's stream-info filter state (PendingRegistration); the
// registration removes its entries when the downstream stream ends (answered, cancelled or reset).
//
// A request without the deadline header is not filtered (lowest cost over all healthy hosts) and never waits.
// A request with it is sent at once if this worker's queue is empty and a host is kept; otherwise it joins the
// queue (FIFO) and the router waits (asynchronous host selection). Every queue_tick the worker re-checks its
// queue oldest first: a request whose deadline has passed fails (no host: 503 "no healthy upstream"), one that
// now fits is sent, the others stay. One balancer, hence one queue, per worker thread.
//
// LB retries (the router's attempts after the first, e.g. a hedge on per-try timeout) are picked like the Go
// Ingress's LbRetry picks (abstraction/ingress/frontend/design.md#lbretryloop): at once, even if first attempts are
// waiting (they never join the FIFO); if no host is kept the retry is refused and simply re-checked at every scan,
// while the request's earlier attempt keeps running. A refused retry is never dropped (that would fail the whole
// request); it ends when it is sent or when the request ends (the router cancels its handle).

#include <cstdint>
#include <list>
#include <memory>
#include <set>
#include <string>
#include <utility>
#include <vector>

#include "envoy/event/dispatcher.h"
#include "envoy/event/timer.h"
#include "envoy/http/header_map.h"
#include "envoy/stream_info/filter_state.h"
#include "envoy/upstream/load_balancer.h"
#include "envoy/upstream/upstream.h"

#include "absl/synchronization/mutex.h"
#include "absl/types/optional.h"
#include "contrib/envoy/extensions/load_balancing_policies/peak_ewma_deadline/v3alpha/peak_ewma_deadline.pb.h"
#include "contrib/peak_ewma/load_balancing_policies/source/peak_ewma_lb.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

using DeadlineConfig =
    envoy::extensions::load_balancing_policies::peak_ewma_deadline::v3alpha::PeakEwmaDeadline;

constexpr int64_t kDefaultQueueTickMilliseconds = 10;             // FILTER_QUEUE_TICK_MS
constexpr absl::string_view kDefaultDeadlineHeader = "x-request-deadline"; // decimal Unix ns
constexpr absl::string_view kDroppedDetails = "deadline_filter_dropped";
constexpr absl::string_view kPendingFilterStateKey = "envoy.testbed.peak_ewma_deadline.pending";

// Start times (monotonic ns) of the requests pending at one host, shared by all workers' balancers.
struct PendingRequests : public Upstream::HostLbPolicyData {
  bool receivesOrcaLoadReport() const override { return false; }
  void add(int64_t start_ns);
  void remove(int64_t start_ns);
  absl::optional<int64_t> oldest() const;

private:
  mutable absl::Mutex mu_;
  std::multiset<int64_t> starts_ ABSL_GUARDED_BY(mu_);
};

// A request's pending entries (one per host handed to it, retries included); removed from the hosts'
// PendingRequests when the request's stream info (and so its filter state) is destroyed.
class PendingRegistration : public StreamInfo::FilterState::Object {
public:
  ~PendingRegistration() override;
  void add(Upstream::HostConstSharedPtr host, int64_t start_ns);

private:
  std::vector<std::pair<Upstream::HostConstSharedPtr, int64_t>> entries_;
};

class PeakEwmaDeadlineLoadBalancer : public PeakEwmaLoadBalancer {
public:
  PeakEwmaDeadlineLoadBalancer(const Upstream::PrioritySet& priority_set,
                               const Upstream::PrioritySet* local_priority_set,
                               Upstream::ClusterLbStats& stats, Runtime::Loader& runtime,
                               Random::RandomGenerator& random, uint32_t healthy_panic_threshold,
                               const Upstream::ClusterInfo& cluster_info, TimeSource& time_source,
                               const DeadlineConfig& config);

  Upstream::HostSelectionResponse chooseHost(Upstream::LoadBalancerContext* context) override;

  // One FilterWaitQueue re-check; run by this worker's timer every queue_tick (public for tests).
  void scan();
  size_t queueLength() const { return queue_.size(); }
  size_t retryWaiters() const { return retry_waiters_.size(); }

private:
  struct Entry {
    Upstream::LoadBalancerContext* context; // the router; never touched once *gone is true
    int64_t deadline_unix_ns;
    int64_t queued_unix_ns;
    std::shared_ptr<bool> gone; // set by the handle's cancel(): the stream ended while waiting
  };

  // The router's handle on a waiting request. cancel() only marks the entry; the next scan removes it.
  class Handle : public Upstream::AsyncHostSelectionHandle {
  public:
    explicit Handle(std::shared_ptr<bool> gone) : gone_(std::move(gone)) {}
    void cancel() override { *gone_ = true; }

  private:
    std::shared_ptr<bool> gone_;
  };

  // Attaches PendingRequests to hosts that do not have it yet.
  static void addPendingDataToHosts(const Upstream::HostVector& hosts);
  // Records that host was handed to the request (its pending entry, until the request's stream ends).
  void registerPending(Upstream::LoadBalancerContext* context, const Upstream::HostConstSharedPtr& host);
  int64_t monoNowNs() const;
  // ProvisionalCurrentRTT of a host (see the top of this file); has_pending tells whether anything is pending.
  double provisionalRtt(const Upstream::HostConstSharedPtr& host, int64_t now_mono_ns, bool& has_pending);
  // True for the router's attempts after the first (stream info attempt count > 1).
  static bool isRetry(Upstream::LoadBalancerContext* context);
  // Creates this worker's scan timer on first use; false if the context has no downstream connection.
  bool ensureTimer(Upstream::LoadBalancerContext* context);
  // The request's RequestDeadline from the deadline header, or none (absent or not a decimal integer).
  absl::optional<int64_t> deadlineOf(Upstream::LoadBalancerContext* context) const;
  int64_t nowUnixNs() const;
  // One decision over every healthy host: with remaining_ns, the filtered lowest-cost host or nullptr if none is
  // kept; without, the lowest-cost host. kind names the decision in the trace (admit, rescan, nofilter).
  // context: hosts it asks to avoid (retry host predicates, e.g. previous_hosts) are not kept, unless all are.
  Upstream::HostConstSharedPtr decide(absl::optional<int64_t> remaining_ns, const char* kind,
                                      Upstream::LoadBalancerContext* context);
  Upstream::HostSelectionResponse enqueue(Upstream::LoadBalancerContext* context, int64_t deadline_unix_ns);
  Upstream::HostSelectionResponse waitAsRetry(Upstream::LoadBalancerContext* context, int64_t deadline_unix_ns);

  const Http::LowerCaseString deadline_header_;
  const std::chrono::milliseconds queue_tick_;
  std::list<Entry> queue_;         // first attempts waiting (FilterWaitQueue, FIFO)
  std::list<Entry> retry_waiters_; // refused LB retries, re-checked every scan, never dropped
  Event::TimerPtr timer_; // created on the first enqueue, on this worker's dispatcher
  uint64_t next_decision_{0};
  ::Envoy::Common::CallbackHandlePtr pending_update_cb_; // attaches PendingRequests to hosts added later
};

} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
