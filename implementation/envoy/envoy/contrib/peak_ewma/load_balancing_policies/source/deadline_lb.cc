#include "contrib/peak_ewma/load_balancing_policies/source/deadline_lb.h"

#include <algorithm>
#include <chrono>
#include <limits>
#include <vector>

#include "envoy/network/connection.h"
#include "envoy/stream_info/stream_info.h"

#include "source/common/protobuf/utility.h"

#include "absl/strings/numbers.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

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
                                     : kDefaultQueueTickMilliseconds)) {}

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

// The DeadlineFilter over EVERY healthy host (not two random ones), then the lowest Peak EWMA cost.
Upstream::HostConstSharedPtr
PeakEwmaDeadlineLoadBalancer::decide(absl::optional<int64_t> remaining_ns, const char* kind) {
  const auto& host_sets = priority_set_.hostSetsPerPriority();
  if (host_sets.empty() || host_sets[0]->healthyHosts().empty()) {
    return nullptr;
  }
  const auto& hosts = host_sets[0]->healthyHosts();
  const uint64_t decision = next_decision_++;
  const bool late = remaining_ns.has_value() && *remaining_ns <= 0; // already late: passes nowhere

  std::vector<double> costs(hosts.size());
  std::vector<bool> keep(hosts.size());
  double best = std::numeric_limits<double>::infinity();
  for (size_t i = 0; i < hosts.size(); ++i) {
    auto* data = getPeakEwmaData(hosts[i]);
    const double ewma_ms = data != nullptr ? data->getEwmaRtt() : 0.0;
    costs[i] = calculateHostCost(hosts[i]); // ewma_ms * (rq_active + 1) once a sample exists
    if (!remaining_ns.has_value()) {
      keep[i] = true; // no deadline: not filtered
    } else if (late) {
      keep[i] = false;
    } else if (ewma_ms <= 0.0) {
      keep[i] = true; // no RTT sample yet: eligible (as the Go DeadlineFilter without an EwmaExecTime)
    } else {
      keep[i] = costs[i] * 1e6 <= static_cast<double>(*remaining_ns);
    }
    if (keep[i] && costs[i] < best) {
      best = costs[i];
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

  if (trace_.enabled()) {
    for (size_t i = 0; i < hosts.size(); ++i) {
      auto* data = getPeakEwmaData(hosts[i]);
      trace_.eval(decision, kind, hosts[i]->address()->asString(),
                  static_cast<double>(hosts[i]->stats().rq_active_.value()),
                  data != nullptr ? data->getEwmaRtt() : 0.0, costs[i], remaining_ns.has_value(),
                  remaining_ns.value_or(0), keep[i], i == chosen);
    }
  }
  return chosen < hosts.size() ? hosts[chosen] : nullptr;
}

Upstream::HostSelectionResponse
PeakEwmaDeadlineLoadBalancer::chooseHost(Upstream::LoadBalancerContext* context) {
  maybeAggregate();
  const absl::optional<int64_t> deadline = deadlineOf(context);
  if (!deadline.has_value()) {
    return {decide(absl::nullopt, "nofilter")}; // no RequestDeadline: never filtered, never waits
  }
  if (isRetry(context)) { // an LB retry: picked at once, refused (re-checked each scan) if nothing is kept
    Upstream::HostConstSharedPtr host = decide(*deadline - nowUnixNs(), "retry");
    if (host != nullptr) {
      return {host};
    }
    return waitAsRetry(context, *deadline);
  }
  if (!queue_.empty()) {
    return enqueue(context, *deadline); // FIFO: never overtake a waiting request at admission
  }
  Upstream::HostConstSharedPtr host = decide(*deadline - nowUnixNs(), "admit");
  if (host != nullptr) {
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
PeakEwmaDeadlineLoadBalancer::waitAsRetry(Upstream::LoadBalancerContext* context, int64_t deadline_unix_ns) {
  if (!ensureTimer(context)) {
    // No dispatcher to re-check on. A nullptr host would fail the whole request, so the retry waits on a handle
    // that is never completed: the earlier attempt answers, and the router cancels the handle.
    return {nullptr, std::make_unique<Handle>(std::make_shared<bool>(false))};
  }
  auto gone = std::make_shared<bool>(false);
  const int64_t now = nowUnixNs();
  retry_waiters_.push_back(Entry{context, deadline_unix_ns, now, gone});
  trace_.queueEvent("retry_waiting", 0, deadline_unix_ns - now, "");
  return {nullptr, std::make_unique<Handle>(std::move(gone))};
}

// FilterWaitQueue scan: oldest first; drop past the deadline, send what now fits, keep the rest in place.
// Each entry leaves the queue before its router is called back, so a callback that ends other streams
// (cancel: only a flag) or adds requests (push_back) cannot invalidate this iteration.
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
    Upstream::HostConstSharedPtr host = decide(remaining, "rescan");
    if (host != nullptr) {
      Upstream::LoadBalancerContext* context = it->context;
      it = queue_.erase(it);
      trace_.queueEvent("sent", waited, remaining, host->address()->asString());
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
      trace_.queueEvent("retry_gone", waited, remaining, "");
      it = retry_waiters_.erase(it);
      continue;
    }
    Upstream::HostConstSharedPtr host = decide(remaining, "retry_rescan"); // none once late: stays refused
    if (host != nullptr) {
      Upstream::LoadBalancerContext* context = it->context;
      it = retry_waiters_.erase(it);
      trace_.queueEvent("retry_sent", waited, remaining, host->address()->asString());
      context->onAsyncHostSelection(std::move(host), "");
      continue;
    }
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
