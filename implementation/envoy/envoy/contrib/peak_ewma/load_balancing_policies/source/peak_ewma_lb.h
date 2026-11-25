#pragma once

#include <atomic>
#include <cstdio>
#include <cstring>
#include <string>
#include <deque>
#include <limits>
#include <memory>
#include <new>
#include <vector>

#include "envoy/upstream/load_balancer.h"

#include "source/common/common/callback_impl.h"
#include "source/common/common/thread.h"
#include "source/extensions/load_balancing_policies/common/load_balancer_impl.h"

#include "absl/container/flat_hash_map.h"
#include "contrib/envoy/extensions/load_balancing_policies/peak_ewma/v3alpha/peak_ewma.pb.h"
#include "contrib/peak_ewma/load_balancing_policies/source/cost.h"
#include "contrib/peak_ewma/load_balancing_policies/source/host_data.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

// Forward declarations and type aliases.
using Config = envoy::extensions::load_balancing_policies::peak_ewma::v3alpha::PeakEwma;

constexpr int64_t kDefaultDecayTimeSeconds = 10;
constexpr double kDefaultRttMilliseconds = 10.0; // Default RTT for new hosts (10ms).

namespace {
class PeakEwmaTestPeer;
} // namespace

class PeakEwmaLoadBalancerFactory;
class PeakEwmaLoadBalancer;
class Cost;

/**
 * Manages stats for a single host, publishing EWMA RTT, active requests, and computed cost
 * to the admin interface for observability.
 */
struct GlobalHostStats {
  GlobalHostStats(Upstream::HostConstSharedPtr host, Stats::Scope& scope);

  void setComputedCostStat(double cost);
  void setEwmaRttStat(double ewma_rtt_ms);
  void setActiveRequestsStat(double active_requests);

private:
  Stats::Gauge& cost_stat_;
  Stats::Gauge& ewma_rtt_stat_;
  Stats::Gauge& active_requests_stat_;
  Upstream::HostConstSharedPtr host_;
};

/**
 * impl/envoy (cancellation testbed): optional trace of the balancer's internals, enabled by the environment
 * variable PEAK_EWMA_TRACE_PREFIX. Each balancer instance (one per worker thread) that records anything opens
 * <prefix>.ewma.<n>.csv and <prefix>.picks.<n>.csv (n: 0, 1, ... in the order instances first record), written
 * only from that instance's thread, line-buffered:
 *   ewma:  t_unix_ns,host,rtt_ms,dt_ns,alpha,ewma_before_ms,ewma_after_ms   (one row per processed RTT sample;
 *          t_unix_ns is the sample's timestamp, dt_ns the time since the previous sample, negative when samples
 *          arrive out of order)
 *   picks: t_unix_ns,host_a,active_a,ewma_a_ms,cost_a,host_b,active_b,ewma_b_ms,cost_b,chosen   (one row per
 *          Power of Two Choices decision; chosen is "a" or "b")
 * Monotonic times are converted to Unix ns with an offset taken once at construction.
 */
class PeakEwmaTrace {
public:
  explicit PeakEwmaTrace(TimeSource& time_source);
  ~PeakEwmaTrace();
  bool enabled() const { return enabled_; }
  int64_t monoNowNs() const;
  void update(uint64_t sample_mono_ns, const std::string& host, double rtt_ms, int64_t dt_ns, double alpha,
              double ewma_before_ms, double ewma_after_ms);
  void pick(const std::string& host_a, double active_a, double ewma_a_ms, double cost_a,
            const std::string& host_b, double active_b, double ewma_b_ms, double cost_b, bool chose_a);

private:
  bool open();

  TimeSource& time_source_;
  const bool enabled_;
  int64_t unix_minus_mono_ns_{0};
  bool opened_{false};
  std::FILE* updates_{nullptr};
  std::FILE* picks_{nullptr};
};

/**
 * Peak EWMA Load Balancer Implementation.
 *
 * Uses host-attached atomic ring buffers for RTT sample storage. Worker threads
 * record RTT samples directly into host objects. Main thread periodically processes
 * these samples to update EWMA values. Load balancing uses Power of Two Choices
 * algorithm with latency-aware cost function.
 *
 * Architecture:
 * - HTTP filter records RTT samples in host-attached ring buffers (lock-free)
 * - Aggregation happens lazily inline in chooseHost() when the interval elapses
 * - P2C selection uses current EWMA + active requests for cost calculation
 */
class PeakEwmaLoadBalancer : public Upstream::LoadBalancerBase {
public:
  PeakEwmaLoadBalancer(
      const Upstream::PrioritySet& priority_set, const Upstream::PrioritySet* local_priority_set,
      Upstream::ClusterLbStats& stats, Runtime::Loader& runtime, Random::RandomGenerator& random,
      uint32_t healthy_panic_threshold, const Upstream::ClusterInfo& cluster_info,
      TimeSource& time_source,
      const envoy::extensions::load_balancing_policies::peak_ewma::v3alpha::PeakEwma& config);

  // LoadBalancer interface
  Upstream::HostSelectionResponse chooseHost(Upstream::LoadBalancerContext* context) override;
  Upstream::HostConstSharedPtr peekAnotherHost(Upstream::LoadBalancerContext* context) override;

private:
  friend struct GlobalHostStats;

  // Host management - attach atomic data structures to each host.
  void addPeakEwmaLbPolicyDataToHosts(const Upstream::HostVector& hosts);
  PeakEwmaHostLbPolicyData* getPeakEwmaData(Upstream::HostConstSharedPtr host);

  // Inline aggregation - processes host-attached sample data.
  void aggregateWorkerData();
  void processHostSamples(Upstream::HostConstSharedPtr host, PeakEwmaHostLbPolicyData* data);
  void maybeAggregate();

  // Power of Two Choices selection.
  Upstream::HostConstSharedPtr selectFromTwoCandidates(const Upstream::HostVector& hosts,
                                                       uint64_t random_value);
  double calculateHostCost(Upstream::HostConstSharedPtr host);

  // EWMA calculation helpers.
  double calculateTimeBasedAlpha(uint64_t later_time_ns, uint64_t earlier_time_ns);
  double updateEwmaWithSample(double current_ewma, double new_rtt_ms, double alpha);

  // Core infrastructure.
  const Upstream::PrioritySet& priority_set_;
  const envoy::extensions::load_balancing_policies::peak_ewma::v3alpha::PeakEwma config_proto_;
  Random::RandomGenerator& random_;
  TimeSource& time_source_;
  Stats::Scope& stats_scope_;

  // Business logic components.
  Cost cost_;

  // Inline aggregation state.
  const std::chrono::milliseconds aggregation_interval_;
  MonotonicTime last_aggregation_time_;

  // Host stats for admin interface visibility.
  absl::flat_hash_map<Upstream::HostConstSharedPtr, std::unique_ptr<GlobalHostStats>>
      all_host_stats_;

  // Priority set callback for adding atomic data to new hosts.
  ::Envoy::Common::CallbackHandlePtr priority_update_cb_;

  // EWMA calculation constants.
  const int64_t tau_nanos_;  // Decay time in nanoseconds.
  const size_t max_samples_; // Configurable ring buffer size per host.

  // impl/envoy: optional trace of EWMA updates and picks (PEAK_EWMA_TRACE_PREFIX).
  PeakEwmaTrace trace_;
};

} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
