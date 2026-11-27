#include "contrib/peak_ewma/load_balancing_policies/source/peak_ewma_lb.h"

#include <chrono>
#include <cstdlib>
#include <memory>

#include "envoy/common/optref.h"
#include "envoy/upstream/upstream.h"

#include "source/common/common/assert.h"
#include "source/common/common/utility.h"
#include "source/common/protobuf/utility.h"

#include "absl/base/attributes.h"
#include "absl/status/status.h"
#include "contrib/peak_ewma/load_balancing_policies/source/cost.h"
#include "contrib/peak_ewma/load_balancing_policies/source/host_data.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

// GlobalHostStats implementation.

GlobalHostStats::GlobalHostStats(Upstream::HostConstSharedPtr host, Stats::Scope& scope)
    : cost_stat_(scope.gaugeFromString("peak_ewma." + host->address()->asString() + ".cost",
                                       Stats::Gauge::ImportMode::NeverImport)),
      ewma_rtt_stat_(
          scope.gaugeFromString("peak_ewma." + host->address()->asString() + ".ewma_rtt_ms",
                                Stats::Gauge::ImportMode::NeverImport)),
      active_requests_stat_(
          scope.gaugeFromString("peak_ewma." + host->address()->asString() + ".active_requests",
                                Stats::Gauge::ImportMode::NeverImport)),
      host_(host) {}

void GlobalHostStats::setComputedCostStat(double cost) {
  cost_stat_.set(static_cast<uint64_t>(cost));
}

void GlobalHostStats::setEwmaRttStat(double ewma_rtt_ms) {
  ewma_rtt_stat_.set(static_cast<uint64_t>(ewma_rtt_ms));
}

void GlobalHostStats::setActiveRequestsStat(double active_requests) {
  active_requests_stat_.set(static_cast<uint64_t>(active_requests));
}

// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.21.0)
// impl/envoy: PeakEwmaTrace (see peak_ewma_lb.h).

namespace {
const char* tracePrefix() {
  static const char* p = std::getenv("PEAK_EWMA_TRACE_PREFIX");
  return p;
}
} // namespace

PeakEwmaTrace::PeakEwmaTrace(TimeSource& time_source)
    : time_source_(time_source), enabled_(tracePrefix() != nullptr && tracePrefix()[0] != '\0') {
  if (enabled_) {
    const int64_t unix_ns = std::chrono::duration_cast<std::chrono::nanoseconds>(
                                time_source_.systemTime().time_since_epoch())
                                .count();
    unix_minus_mono_ns_ = unix_ns - monoNowNs();
  }
}

PeakEwmaTrace::~PeakEwmaTrace() {
  for (std::FILE* f : {updates_, picks_, evals_, queue_}) {
    if (f != nullptr) {
      std::fclose(f);
    }
  }
}

int64_t PeakEwmaTrace::monoNowNs() const {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(
             time_source_.monotonicTime().time_since_epoch())
      .count();
}

std::FILE* PeakEwmaTrace::file(std::FILE*& f, const char* kind, const char* header) {
  if (f == nullptr) {
    if (index_ < 0) {
      static std::atomic<int> next_index{0};
      index_ = next_index++;
    }
    f = std::fopen((std::string(tracePrefix()) + "." + kind + "." + std::to_string(index_) + ".csv").c_str(),
                   "w");
    if (f == nullptr) {
      return nullptr;
    }
    std::setvbuf(f, nullptr, _IOLBF, 1 << 16);
    std::fputs(header, f);
  }
  return f;
}

void PeakEwmaTrace::update(uint64_t sample_mono_ns, const std::string& host, double rtt_ms, int64_t dt_ns,
                           double alpha, double ewma_before_ms, double ewma_after_ms) {
  std::FILE* f = enabled_ ? file(updates_, "ewma",
                                 "t_unix_ns,host,rtt_ms,dt_ns,alpha,ewma_before_ms,ewma_after_ms\n")
                          : nullptr;
  if (f == nullptr) {
    return;
  }
  std::fprintf(f, "%lld,%s,%.9g,%lld,%.9g,%.9g,%.9g\n",
               static_cast<long long>(static_cast<int64_t>(sample_mono_ns) + unix_minus_mono_ns_),
               host.c_str(), rtt_ms, static_cast<long long>(dt_ns), alpha, ewma_before_ms, ewma_after_ms);
}

void PeakEwmaTrace::pick(const std::string& host_a, double active_a, double ewma_a_ms, double cost_a,
                         const std::string& host_b, double active_b, double ewma_b_ms, double cost_b,
                         bool chose_a) {
  std::FILE* f =
      enabled_ ? file(picks_, "picks",
                      "t_unix_ns,host_a,active_a,ewma_a_ms,cost_a,host_b,active_b,ewma_b_ms,cost_b,chosen\n")
               : nullptr;
  if (f == nullptr) {
    return;
  }
  std::fprintf(f, "%lld,%s,%.9g,%.9g,%.9g,%s,%.9g,%.9g,%.9g,%s\n", static_cast<long long>(unixNowNs()),
               host_a.c_str(), active_a, ewma_a_ms, cost_a, host_b.c_str(), active_b, ewma_b_ms, cost_b,
               chose_a ? "a" : "b");
}

void PeakEwmaTrace::eval(uint64_t decision, const char* kind, const std::string& host, double active,
                         double ewma_ms, double cost, bool has_remaining, int64_t remaining_ns, bool eligible,
                         bool chosen, double provisional_ms) {
  std::FILE* f = enabled_ ? file(evals_, "evals",
                                 "t_unix_ns,decision,kind,host,active,ewma_ms,cost,remaining_ns,eligible,chosen,"
                                 "provisional_ms\n")
                          : nullptr;
  if (f == nullptr) {
    return;
  }
  const std::string remaining = has_remaining ? std::to_string(remaining_ns) : std::string();
  std::fprintf(f, "%lld,%llu,%s,%s,%.9g,%.9g,%.9g,%s,%d,%d,%.9g\n", static_cast<long long>(unixNowNs()),
               static_cast<unsigned long long>(decision), kind, host.c_str(), active, ewma_ms, cost,
               remaining.c_str(), eligible ? 1 : 0, chosen ? 1 : 0, provisional_ms);
}

void PeakEwmaTrace::queueEvent(const char* event, int64_t wait_ns, int64_t remaining_ns,
                               const std::string& host, int64_t refused) {
  std::FILE* f =
      enabled_ ? file(queue_, "queue", "t_unix_ns,event,wait_ns,remaining_ns,host,refused\n") : nullptr;
  if (f == nullptr) {
    return;
  }
  const std::string refused_text = refused >= 0 ? std::to_string(refused) : std::string();
  std::fprintf(f, "%lld,%s,%lld,%lld,%s,%s\n", static_cast<long long>(unixNowNs()), event,
               static_cast<long long>(wait_ns), static_cast<long long>(remaining_ns), host.c_str(),
               refused_text.c_str());
}

// Peak EWMA Load Balancer Implementation.

PeakEwmaLoadBalancer::PeakEwmaLoadBalancer(
    const Upstream::PrioritySet& priority_set, const Upstream::PrioritySet* /*local_priority_set*/,
    Upstream::ClusterLbStats& /*stats*/, Runtime::Loader& runtime, Random::RandomGenerator& random,
    uint32_t /* healthy_panic_threshold */, const Upstream::ClusterInfo& cluster_info,
    TimeSource& time_source,
    const envoy::extensions::load_balancing_policies::peak_ewma::v3alpha::PeakEwma& config)
    : LoadBalancerBase(priority_set, cluster_info.lbStats(), runtime, random,
                       PROTOBUF_PERCENT_TO_ROUNDED_INTEGER_OR_DEFAULT(
                           cluster_info.lbConfig(), healthy_panic_threshold, 100, 50)),
      priority_set_(priority_set), config_proto_(config), random_(random),
      time_source_(time_source), stats_scope_(cluster_info.statsScope()),
      cost_(config.has_penalty_value() ? config.penalty_value().value() : 1000000.0),
      aggregation_interval_(config_proto_.has_aggregation_interval()
                                ? std::chrono::milliseconds(DurationUtil::durationToMilliseconds(
                                      config_proto_.aggregation_interval()))
                                : std::chrono::milliseconds(100)),
      last_aggregation_time_(time_source_.monotonicTime()),
      tau_nanos_(config_proto_.has_decay_time()
                     ? DurationUtil::durationToMilliseconds(config_proto_.decay_time()) * 1000000LL
                     : kDefaultDecayTimeSeconds * 1000000000LL),
      max_samples_(config_proto_.has_max_samples_per_host()
                       ? config_proto_.max_samples_per_host().value()
                       : 1000),
      trace_(time_source) {

  // Add PeakEwmaHostLbPolicyData to all existing hosts.
  for (const auto& host_set : priority_set_.hostSetsPerPriority()) {
    addPeakEwmaLbPolicyDataToHosts(host_set->hosts());
  }

  // Setup callback to add data to new hosts and clean up removed hosts.
  priority_update_cb_ = priority_set_.addPriorityUpdateCb(
      [this](uint32_t, const Upstream::HostVector& hosts_added,
             const Upstream::HostVector& hosts_removed) -> absl::Status {
        addPeakEwmaLbPolicyDataToHosts(hosts_added);
        for (const auto& host : hosts_removed) {
          all_host_stats_.erase(host);
        }
        return absl::OkStatus();
      });
}

// Host management.
void PeakEwmaLoadBalancer::addPeakEwmaLbPolicyDataToHosts(const Upstream::HostVector& hosts) {
  for (const auto& host_ptr : hosts) {
    if (!host_ptr->typedLbPolicyData<PeakEwmaHostLbPolicyData>().has_value()) {
      host_ptr->addLbPolicyData(std::make_unique<PeakEwmaHostLbPolicyData>(max_samples_));
    }
  }
}

PeakEwmaHostLbPolicyData* PeakEwmaLoadBalancer::getPeakEwmaData(Upstream::HostConstSharedPtr host) {
  return host->typedLbPolicyData<PeakEwmaHostLbPolicyData>().ptr();
}

void PeakEwmaLoadBalancer::maybeAggregate() {
  const auto now = time_source_.monotonicTime();
  if (now - last_aggregation_time_ >= aggregation_interval_) {
    aggregateWorkerData();
    last_aggregation_time_ = now;
  }
}

double PeakEwmaLoadBalancer::calculateHostCost(Upstream::HostConstSharedPtr host) {
  // Get EWMA RTT from host-attached atomic data.
  auto* peak_data = getPeakEwmaData(host);
  double ewma_rtt = peak_data ? peak_data->getEwmaRtt() : 0.0;

  // Get active requests from host stats.
  double active_requests = host->stats().rq_active_.value();

  // Calculate cost using business logic.
  double default_rtt_ms = config_proto_.has_default_rtt()
                              ? DurationUtil::durationToMilliseconds(config_proto_.default_rtt())
                              : kDefaultRttMilliseconds;

  return cost_.compute(ewma_rtt, active_requests, default_rtt_ms);
}

Upstream::HostConstSharedPtr
PeakEwmaLoadBalancer::selectFromTwoCandidates(const Upstream::HostVector& hosts,
                                              uint64_t random_value) {

  if (hosts.size() < 2) {
    return hosts.empty() ? nullptr : hosts[0];
  }

  // Generate two distinct host indices using random value.
  const size_t host_count = hosts.size();
  const size_t first_index = random_value % host_count;
  size_t second_index = (random_value >> 16) % host_count;

  // Ensure distinct indices.
  if (second_index == first_index) {
    second_index = (second_index + 1) % host_count;
  }

  auto first_host = hosts[first_index];
  auto second_host = hosts[second_index];

  // Calculate costs using host-attached EWMA data.
  double first_cost = calculateHostCost(first_host);
  double second_cost = calculateHostCost(second_host);

  // Select host with lower cost (tie-breaking with random).
  bool costs_equal = (first_cost == second_cost);
  bool prefer_first = costs_equal ? (random_value & 0x1) != 0 : first_cost < second_cost;

  auto selected_host = prefer_first ? first_host : second_host;

  // impl/envoy: trace this decision (both candidates' state and the choice).
  if (trace_.enabled()) {
    auto* data_a = getPeakEwmaData(first_host);
    auto* data_b = getPeakEwmaData(second_host);
    trace_.pick(first_host->address()->asString(),
                static_cast<double>(first_host->stats().rq_active_.value()),
                data_a ? data_a->getEwmaRtt() : 0.0, first_cost, second_host->address()->asString(),
                static_cast<double>(second_host->stats().rq_active_.value()),
                data_b ? data_b->getEwmaRtt() : 0.0, second_cost, prefer_first);
  }

  // Host selection complete.

  return selected_host;
}

Upstream::HostSelectionResponse
PeakEwmaLoadBalancer::chooseHost(Upstream::LoadBalancerContext* context) {
  // Lazily aggregate EWMA data if the interval has elapsed.
  maybeAggregate();

  // Power of Two Choices selection using host-attached EWMA data.
  const auto& host_sets = priority_set_.hostSetsPerPriority();

  if (host_sets.empty()) {
    return {nullptr, ""};
  }

  // Use first priority for now (can be extended for multi-priority).
  const auto& healthy = host_sets[0]->healthyHosts();

  if (healthy.empty()) {
    return {nullptr, ""};
  }

  // Design: abstraction/ingress/load_balancer/design.md#lbpolicy
  // impl/envoy (cancellation testbed): honor the retry host predicates (e.g. previous_hosts for the testbed's
  // RetryElsewhere): hosts the context asks to avoid are left out, unless that would leave none.
  Upstream::HostVector allowed;
  if (context != nullptr) {
    for (const auto& host : healthy) {
      if (!context->shouldSelectAnotherHost(*host)) {
        allowed.push_back(host);
      }
    }
  }
  const auto& hosts = (context != nullptr && !allowed.empty()) ? allowed : healthy;

  if (hosts.size() == 1) {
    return {hosts[0], ""};
  }

  // Power of Two Choices selection using host-attached EWMA data.
  uint64_t random_value = random_.random();
  return {selectFromTwoCandidates(hosts, random_value), ""};
}

Upstream::HostConstSharedPtr PeakEwmaLoadBalancer::peekAnotherHost(
    ABSL_ATTRIBUTE_UNUSED Upstream::LoadBalancerContext* context) {
  return nullptr;
}

void PeakEwmaLoadBalancer::aggregateWorkerData() {
  // Process atomic ring buffers attached to each host.

  // Process each host's atomic ring buffer directly (no cross-thread complexity).
  for (const auto& host_set : priority_set_.hostSetsPerPriority()) {
    for (const auto& host : host_set->hosts()) {
      auto* peak_data = getPeakEwmaData(host);
      if (peak_data) {
        processHostSamples(host, peak_data);
      }
    }
  }

  // Publish stats for admin interface visibility.
  for (const auto& host_set : priority_set_.hostSetsPerPriority()) {
    for (const auto& host : host_set->hosts()) {
      auto* peak_data = getPeakEwmaData(host);
      if (peak_data) {
        double ewma_rtt = peak_data->getEwmaRtt();
        double active_requests = host->stats().rq_active_.value();
        double cost =
            cost_.compute(ewma_rtt, active_requests,
                          config_proto_.has_default_rtt()
                              ? DurationUtil::durationToMilliseconds(config_proto_.default_rtt())
                              : kDefaultRttMilliseconds);

        // Create stats object if it doesn't exist.
        auto it = all_host_stats_.find(host);
        if (it == all_host_stats_.end()) {
          all_host_stats_[host] = std::make_unique<GlobalHostStats>(host, stats_scope_);
          it = all_host_stats_.find(host);
        }

        // Update stats for observability.
        if (it != all_host_stats_.end()) {
          it->second->setEwmaRttStat(ewma_rtt);
          it->second->setActiveRequestsStat(active_requests);
          it->second->setComputedCostStat(cost);
        }

        // Host processing complete.
      }
    }
  }

  // Aggregation cycle complete.
}

double PeakEwmaLoadBalancer::calculateTimeBasedAlpha(uint64_t later_time_ns,
                                                     uint64_t earlier_time_ns) {
  int64_t time_delta_ns = static_cast<int64_t>(later_time_ns - earlier_time_ns);
  // Design: abstraction/ingress/load_balancer/design.md#peakewmartt
  // impl/envoy: alpha = 1 - e^(-max(dt, 0)/tau). A concurrent or out-of-order sample (dt <= 0, e.g. samples from
  // two workers reaching the shared ring buffer a few ns out of order) gets no weight; upstream gave it full weight
  // (alpha = 1), which replaced the whole EWMA by that one sample.
  if (time_delta_ns <= 0) {
    return 0.0;
  }

  // Time-based exponential decay: α = 1 - e^(-Δt/τ).
  double time_delta_s = time_delta_ns / 1000000000.0;
  double tau_s = tau_nanos_ / 1000000000.0;
  double alpha = 1.0 - std::exp(-time_delta_s / tau_s);

  // Clamp alpha to reasonable bounds.
  return std::min(1.0, std::max(0.0, alpha));
}

double PeakEwmaLoadBalancer::updateEwmaWithSample(double current_ewma, double new_rtt_ms,
                                                  double alpha) {
  if (current_ewma == 0.0) {
    // First sample - initialize EWMA.
    return new_rtt_ms;
  }

  // EWMA update: new_ewma = α × new_rtt + (1-α) × old_ewma.
  return alpha * new_rtt_ms + (1.0 - alpha) * current_ewma;
}

void PeakEwmaLoadBalancer::processHostSamples(Upstream::HostConstSharedPtr host,
                                              PeakEwmaHostLbPolicyData* data) {
  if (!data) {
    return;
  }

  // Get the range of new samples to process (atomic ring buffer).
  auto [last_processed, current_write] = data->getNewSampleRange();
  if (last_processed == current_write) {
    return;
  }

  // If ring buffer was fully overwritten, skip to oldest valid slot.
  // Uses unsigned arithmetic (always correct since write_index_ only increments).
  if (current_write - last_processed > data->max_samples_) {
    last_processed = current_write - data->max_samples_;
  }

  size_t num_new_samples = current_write - last_processed;

  // Get current EWMA state.
  double current_ewma = data->getEwmaRtt();
  uint64_t reference_time = data->last_update_timestamp_.load();

  // Process all new samples in chronological order.
  size_t processed_index = last_processed;
  for (size_t i = 0; i < num_new_samples; ++i) {
    size_t ring_index = processed_index % data->max_samples_;

    double rtt_ms = data->rtt_samples_[ring_index].load();
    uint64_t timestamp_ns = data->timestamps_[ring_index].load();

    // Skip invalid samples (should be rare).
    if (rtt_ms <= 0.0 || timestamp_ns == 0) {
      processed_index++;
      continue;
    }

    double alpha = calculateTimeBasedAlpha(timestamp_ns, reference_time);
    const double ewma_before = current_ewma;
    current_ewma = updateEwmaWithSample(current_ewma, rtt_ms, alpha);
    // impl/envoy: trace this EWMA update.
    if (trace_.enabled()) {
      trace_.update(timestamp_ns, host->address()->asString(), rtt_ms,
                    static_cast<int64_t>(timestamp_ns - reference_time), alpha, ewma_before, current_ewma);
    }
    reference_time = timestamp_ns;
    processed_index++;
  }

  // Update atomic EWMA in host data.
  data->updateEwma(current_ewma, reference_time);
  data->markSamplesProcessed(current_write);
}

} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
