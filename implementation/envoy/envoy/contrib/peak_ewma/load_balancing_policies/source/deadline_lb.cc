// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.21.0)
// Source: abstraction/protocol/design.md (protocol v0.8.0)
#include "contrib/peak_ewma/load_balancing_policies/source/deadline_lb.h"

#include <algorithm>
#include <cmath>
#include <chrono>
#include <limits>
#include <vector>

#include "envoy/common/optref.h"
#include "envoy/network/connection.h"
#include "envoy/stream_info/stream_info.h"

#include "source/common/protobuf/utility.h"

#include "absl/strings/numbers.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

// Design: abstraction/ingress/load_balancer/design.md#provisionalcurrentrtt
void PendingRequests::add(int64_t start_ns) {
  absl::MutexLock lock(&mu_);
  starts_.insert(start_ns);
}

void PendingRequests::remove(int64_t start_ns) {
  absl::MutexLock lock(&mu_);
  auto it = starts_.find(start_ns);
  if (it != starts_.end()) {
    starts_.erase(it);
  }
}

absl::optional<int64_t> PendingRequests::oldest() const {
  absl::MutexLock lock(&mu_);
  if (starts_.empty()) {
    return absl::nullopt;
  }
  return *starts_.begin();
}

// Design: abstraction/ingress/load_balancer/design.md#provisionalcurrentrtt
PendingRegistration::~PendingRegistration() {
  for (const auto& [host, start_ns] : entries_) {
    auto pending = host->typedLbPolicyData<PendingRequests>();
    if (pending.has_value()) {
      pending->remove(start_ns);
    }
  }
}

void PendingRegistration::add(Upstream::HostConstSharedPtr host, int64_t start_ns) {
  entries_.emplace_back(std::move(host), start_ns);
}

void PeakEwmaDeadlineLoadBalancer::addPendingDataToHosts(const Upstream::HostVector& hosts) {
  static absl::Mutex attach_mu; // worker balancers are created concurrently; attach once per host
  absl::MutexLock lock(&attach_mu);
  for (const auto& host : hosts) {
    if (!host->typedLbPolicyData<PendingRequests>().has_value()) {
      host->addLbPolicyData(std::make_unique<PendingRequests>());
    }
  }
}

PeakEwmaDeadlineLoadBalancer::PeakEwmaDeadlineLoadBalancer(
    const Upstream::PrioritySet& priority_set, const Upstream::PrioritySet* local_priority_set,
    Upstream::ClusterLbStats& stats, Runtime::Loader& runtime, Random::RandomGenerator& random,
    uint32_t healthy_panic_threshold, const Upstream::ClusterInfo& cluster_info, TimeSource& time_source,
    const DeadlineConfig& config)
    : PeakEwmaLoadBalancer(priority_set, local_priority_set, stats, runtime, random,
                           healthy_panic_threshold, cluster_info, time_source, config.peak_ewma()),
      deadline_header_(config.deadline_header().empty() ? std::string(kDefaultDeadlineHeader)
                                                        : config.deadline_header()),
      queue_tick_(std::max<int64_t>(
          1, config.has_queue_tick() ? DurationUtil::durationToMilliseconds(config.queue_tick())
                                     : kDefaultQueueTickMilliseconds)),
      filter_margin_(config.filter_margin() >= 1.0 ? config.filter_margin() : 1.0),
      probe_tau_ms_(config.has_probe_tau()
                        ? static_cast<double>(DurationUtil::durationToMilliseconds(config.probe_tau()))
                        : 0.0) {
  for (const auto& host_set : priority_set_.hostSetsPerPriority()) {
    addPendingDataToHosts(host_set->hosts());
  }
  pending_update_cb_ = priority_set_.addPriorityUpdateCb(
      [](uint32_t, const Upstream::HostVector& hosts_added, const Upstream::HostVector&) -> absl::Status {
        addPendingDataToHosts(hosts_added);
        return absl::OkStatus();
      });
}

int64_t PeakEwmaDeadlineLoadBalancer::monoNowNs() const {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(
             time_source_.monotonicTime().time_since_epoch())
      .count();
}

// Design: abstraction/ingress/load_balancer/design.md#provisionalcurrentrtt
void PeakEwmaDeadlineLoadBalancer::registerPending(Upstream::LoadBalancerContext* context,
                                                   const Upstream::HostConstSharedPtr& host) {
  StreamInfo::StreamInfo* info = context != nullptr ? context->requestStreamInfo() : nullptr;
  OptRef<PendingRequests> pending;
  if (host != nullptr) {
    pending = host->typedLbPolicyData<PendingRequests>();
  }
  // Design: abstraction/ingress/load_balancer/design.md#filterprobe
  if (pending.has_value()) {
    pending->markSent(monoNowNs()); // LastSentTime: every request handed to the host, probes included
  }
  if (info == nullptr || !pending.has_value()) {
    return; // not tracked
  }
  const StreamInfo::FilterStateSharedPtr& filter_state = info->filterState();
  auto* registration = filter_state->getDataMutable<PendingRegistration>(kPendingFilterStateKey);
  if (registration == nullptr) {
    auto created = std::make_shared<PendingRegistration>();
    registration = created.get();
    filter_state->setData(kPendingFilterStateKey, std::move(created),
                          StreamInfo::FilterState::LifeSpan::FilterChain);
  }
  const int64_t now = monoNowNs();
  pending->add(now);
  registration->add(host, now);
}

// Design: abstraction/ingress/load_balancer/design.md#provisionalcurrentrtt
double PeakEwmaDeadlineLoadBalancer::provisionalRtt(const Upstream::HostConstSharedPtr& host,
                                                    int64_t now_mono_ns, bool& has_pending) {
  auto* data = getPeakEwmaData(host);
  const double ewma_ms = data != nullptr ? data->getEwmaRtt() : 0.0;
  auto pending = host->typedLbPolicyData<PendingRequests>();
  const absl::optional<int64_t> oldest = pending.has_value() ? pending->oldest() : absl::nullopt;
  has_pending = oldest.has_value();
  if (!has_pending) {
    return ewma_ms; // nothing pending: ProvisionalCurrentRTT = CurrentRTT
  }
  // The oldest pending request answered now: one provisional sample, time-based alpha since the last sample.
  const double latency_ms = static_cast<double>(now_mono_ns - *oldest) / 1e6;
  const uint64_t last = data != nullptr ? data->last_update_timestamp_.load() : 0;
  const double alpha = calculateTimeBasedAlpha(static_cast<uint64_t>(now_mono_ns), last);
  return updateEwmaWithSample(ewma_ms, latency_ms, alpha); // ewma 0 (no sample yet): the latency itself
}

absl::optional<int64_t>
PeakEwmaDeadlineLoadBalancer::deadlineOf(Upstream::LoadBalancerContext* context) const {
  if (context == nullptr) {
    return absl::nullopt;
  }
  const Http::RequestHeaderMap* headers = context->downstreamHeaders();
  if (headers == nullptr) {
    return absl::nullopt;
  }
  const auto values = headers->get(deadline_header_);
  int64_t deadline_unix_ns;
  // Design: abstraction/protocol/design.md#requestdeadlineheader
  if (values.empty() || !absl::SimpleAtoi(values[0]->value().getStringView(), &deadline_unix_ns)) {
    return absl::nullopt;
  }
  return deadline_unix_ns;
}

bool PeakEwmaDeadlineLoadBalancer::isRetry(Upstream::LoadBalancerContext* context) {
  const StreamInfo::StreamInfo* info = context != nullptr ? context->requestStreamInfo() : nullptr;
  return info != nullptr && info->attemptCount().value_or(1) > 1;
}

int64_t PeakEwmaDeadlineLoadBalancer::nowUnixNs() const {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(
             time_source_.systemTime().time_since_epoch())
      .count();
}

// The DeadlineFilter over EVERY healthy host (not two random ones): reject a host whose ProvisionalCurrentRTT >
// time left; then the lowest Peak EWMA cost among the kept hosts.
Upstream::HostConstSharedPtr
// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
PeakEwmaDeadlineLoadBalancer::decide(absl::optional<int64_t> remaining_ns, const char* kind,
                                     Upstream::LoadBalancerContext* context) {
  const auto& host_sets = priority_set_.hostSetsPerPriority();
  if (host_sets.empty() || host_sets[0]->healthyHosts().empty()) {
    return nullptr;
  }
  const auto& hosts = host_sets[0]->healthyHosts();
  const uint64_t decision = next_decision_++;
  const bool late = remaining_ns.has_value() && *remaining_ns <= 0; // already late: passes nowhere

  std::vector<double> costs(hosts.size());
  std::vector<double> provisional(hosts.size());
  std::vector<bool> keep(hosts.size());
  std::vector<bool> probe(hosts.size(), false); // FilterProbe: refused hosts whose coin landed heads
  // RetryElsewhere: hosts the context asks to avoid (retry host predicates) are not kept, unless that is all.
  std::vector<bool> excluded(hosts.size(), false);
  size_t n_excluded = 0;
  if (context != nullptr) {
    for (size_t i = 0; i < hosts.size(); ++i) {
      // Design: abstraction/ingress/load_balancer/design.md#lbpolicy
      excluded[i] = context->shouldSelectAnotherHost(*hosts[i]);
      n_excluded += excluded[i] ? 1 : 0;
    }
    if (n_excluded == hosts.size()) {
      std::fill(excluded.begin(), excluded.end(), false);
    }
  }
  double best = std::numeric_limits<double>::infinity();
  const int64_t now_mono = monoNowNs();
  for (size_t i = 0; i < hosts.size(); ++i) {
    auto* data = getPeakEwmaData(hosts[i]);
    const double ewma_ms = data != nullptr ? data->getEwmaRtt() : 0.0;
    costs[i] = calculateHostCost(hosts[i]); // ewma_ms * (rq_active + 1) once a sample exists: the choice
    bool has_pending = false;
    provisional[i] = provisionalRtt(hosts[i], now_mono, has_pending);
    if (excluded[i]) {
      keep[i] = false; // RetryElsewhere: not the backend(s) this request already tried
    } else if (!remaining_ns.has_value()) {
      keep[i] = true; // no deadline: not filtered
    } else if (late) {
      keep[i] = false;
    } else if (ewma_ms <= 0.0 && !has_pending) {
      keep[i] = true; // no RTT sample and nothing pending: eligible (as the Go DeadlineFilter with no sample)
    } else {
      // The filter: reject the host if its ProvisionalCurrentRTT exceeds the request's time left.
      keep[i] = !(filter_margin_ * provisional[i] * 1e6 > static_cast<double>(*remaining_ns)); // FilterMargin
      // Design: abstraction/ingress/load_balancer/design.md#filterprobe
      if (!keep[i] && probe_tau_ms_ > 0.0 && !has_pending) { // refused, nothing pending: P(heads) = 1 - e^(-dt/tau)
        auto pending = hosts[i]->typedLbPolicyData<PendingRequests>();
        const int64_t last = pending.has_value() ? pending->lastSent() : 0;
        const double dt_ms = static_cast<double>(static_cast<uint64_t>(now_mono - last)) / 1e6;
        const double p = 1.0 - std::exp(-dt_ms / probe_tau_ms_);
        const double u = static_cast<double>(random_.random() >> 11) * (1.0 / 9007199254740992.0); // [0, 1)
        probe[i] = u < p;
      }
    }
    if (keep[i] && costs[i] < best) {
      best = costs[i];
    }
  }
  // FilterProbe: a probed host wins over the kept ones; the lowest cost among the probed.
  const bool probing = std::find(probe.begin(), probe.end(), true) != probe.end();
  if (probing) {
    keep = probe;
    best = std::numeric_limits<double>::infinity();
    for (size_t i = 0; i < hosts.size(); ++i) {
      if (keep[i] && costs[i] < best) {
        best = costs[i];
      }
    }
  }
  std::vector<size_t> ties;
  for (size_t i = 0; i < hosts.size(); ++i) {
    if (keep[i] && costs[i] == best) {
      ties.push_back(i);
    }
  }
  size_t chosen = hosts.size(); // none
  if (ties.size() == 1) {
    chosen = ties[0];
  } else if (ties.size() > 1) {
    chosen = ties[random_.random() % ties.size()];
  }

  // A refused retry is re-checked every scan until its request ends; its refused re-checks are counted on its
  // retry_sent / retry_gone queue event instead of traced one by one (they would dominate the trace).
  const bool traced = !(absl::string_view(kind) == "retry_rescan" && chosen == hosts.size());
  if (trace_.enabled() && traced) {
    for (size_t i = 0; i < hosts.size(); ++i) {
      auto* data = getPeakEwmaData(hosts[i]);
      trace_.eval(decision, probing ? "probe" : kind, hosts[i]->address()->asString(),
                  static_cast<double>(hosts[i]->stats().rq_active_.value()),
                  data != nullptr ? data->getEwmaRtt() : 0.0, costs[i], remaining_ns.has_value(),
                  remaining_ns.value_or(0), keep[i], i == chosen, provisional[i]);
    }
  }
  return chosen < hosts.size() ? hosts[chosen] : nullptr;
}

Upstream::HostSelectionResponse
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
PeakEwmaDeadlineLoadBalancer::chooseHost(Upstream::LoadBalancerContext* context) {
  maybeAggregate();
  const absl::optional<int64_t> deadline = deadlineOf(context);
  if (!deadline.has_value()) {
    // No RequestDeadline: never filtered, never waits.
    Upstream::HostConstSharedPtr host = decide(absl::nullopt, "nofilter", context);
    registerPending(context, host);
    return {host};
  }
  if (isRetry(context)) { // an LB retry: picked at once, refused (re-checked each scan) if nothing is kept
    Upstream::HostConstSharedPtr host = decide(*deadline - nowUnixNs(), "retry", context);
    if (host != nullptr) {
      registerPending(context, host);
      return {host};
    }
    return waitAsRetry(context, *deadline);
  }
  if (!queue_.empty()) {
    return enqueue(context, *deadline); // FIFO: never overtake a waiting request at admission
  }
  Upstream::HostConstSharedPtr host = decide(*deadline - nowUnixNs(), "admit", context);
  if (host != nullptr) {
    registerPending(context, host);
    return {host};
  }
  return enqueue(context, *deadline);
}

bool PeakEwmaDeadlineLoadBalancer::ensureTimer(Upstream::LoadBalancerContext* context) {
  if (timer_ == nullptr) {
    const Network::Connection* connection =
        context != nullptr ? context->downstreamConnection() : nullptr;
    if (connection == nullptr) {
      return false;
    }
    timer_ = connection->dispatcher().createTimer([this]() { scan(); });
  }
  if (!timer_->enabled()) {
    timer_->enableTimer(queue_tick_);
  }
  return true;
}

Upstream::HostSelectionResponse
// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
PeakEwmaDeadlineLoadBalancer::enqueue(Upstream::LoadBalancerContext* context, int64_t deadline_unix_ns) {
  if (!ensureTimer(context)) {
    return {nullptr, std::string(kDroppedDetails)}; // no worker dispatcher to wait on: fail now
  }
  auto gone = std::make_shared<bool>(false);
  const int64_t now = nowUnixNs();
  queue_.push_back(Entry{context, deadline_unix_ns, now, gone});
  trace_.queueEvent("queued", 0, deadline_unix_ns - now, "");
  return {nullptr, std::make_unique<Handle>(std::move(gone))};
}

Upstream::HostSelectionResponse
// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
PeakEwmaDeadlineLoadBalancer::waitAsRetry(Upstream::LoadBalancerContext* context, int64_t deadline_unix_ns) {
  if (!ensureTimer(context)) {
    // No dispatcher to re-check on. A nullptr host would fail the whole request, so the retry waits on a handle
    // that is never completed: the earlier attempt answers, and the router cancels the handle.
    return {nullptr, std::make_unique<Handle>(std::make_shared<bool>(false))};
  }
  auto gone = std::make_shared<bool>(false);
  const int64_t now = nowUnixNs();
  retry_waiters_.push_back(Entry{context, deadline_unix_ns, now, gone, 1}); // its first pick was refused
  trace_.queueEvent("retry_waiting", 0, deadline_unix_ns - now, "");
  return {nullptr, std::make_unique<Handle>(std::move(gone))};
}

// FilterWaitQueue scan: oldest first; drop past the deadline, send what now fits, keep the rest in place.
// Each entry leaves the queue before its router is called back, so a callback that ends other streams
// (cancel: only a flag) or adds requests (push_back) cannot invalidate this iteration.
// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
void PeakEwmaDeadlineLoadBalancer::scan() {
  maybeAggregate();
  for (auto it = queue_.begin(); it != queue_.end();) {
    const int64_t now = nowUnixNs();
    const int64_t remaining = it->deadline_unix_ns - now;
    const int64_t waited = now - it->queued_unix_ns;
    if (*it->gone) {
      trace_.queueEvent("gone", waited, remaining, "");
      it = queue_.erase(it);
      continue;
    }
    if (remaining <= 0) {
      Upstream::LoadBalancerContext* context = it->context;
      it = queue_.erase(it);
      trace_.queueEvent("dropped", waited, remaining, "");
      context->onAsyncHostSelection(nullptr, std::string(kDroppedDetails));
      continue;
    }
    Upstream::HostConstSharedPtr host = decide(remaining, "rescan", it->context);
    if (host != nullptr) {
      Upstream::LoadBalancerContext* context = it->context;
      it = queue_.erase(it);
      trace_.queueEvent("sent", waited, remaining, host->address()->asString());
      registerPending(context, host);
      context->onAsyncHostSelection(std::move(host), "");
      continue;
    }
    ++it;
  }
  // Refused LB retries: re-picked every scan (as Go re-ticks a refused LbRetry); never dropped.
  for (auto it = retry_waiters_.begin(); it != retry_waiters_.end();) {
    const int64_t now = nowUnixNs();
    const int64_t remaining = it->deadline_unix_ns - now;
    const int64_t waited = now - it->queued_unix_ns;
    if (*it->gone) {
      trace_.queueEvent("retry_gone", waited, remaining, "", static_cast<int64_t>(it->refused));
      it = retry_waiters_.erase(it);
      continue;
    }
    Upstream::HostConstSharedPtr host = decide(remaining, "retry_rescan", it->context); // none once late: stays refused
    if (host != nullptr) {
      Upstream::LoadBalancerContext* context = it->context;
      it = retry_waiters_.erase(it);
      trace_.queueEvent("retry_sent", waited, remaining, host->address()->asString(),
                        static_cast<int64_t>(it->refused));
      registerPending(context, host);
      context->onAsyncHostSelection(std::move(host), "");
      continue;
    }
    ++it->refused;
    ++it;
  }
  if ((!queue_.empty() || !retry_waiters_.empty()) && timer_ != nullptr) {
    timer_->enableTimer(queue_tick_);
  }
}

} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
