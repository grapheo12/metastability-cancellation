// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.26.0)
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

// Design: abstraction/ingress/load_balancer/design.md#filterprobe
bool PendingRequests::takeProbeAnswer(int64_t t_ns) {
  int64_t p = probe_sent_ns_.load(std::memory_order_relaxed);
  if (p == 0 || t_ns < p) {
    return false;
  }
  return probe_sent_ns_.compare_exchange_strong(p, 0, std::memory_order_relaxed); // one of two folders wins
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

void PeakEwmaDeadlineLoadBalancer::addPendingDataToHosts(const Upstream::HostVector& hosts,
                                                         const absl::optional<TokenParams>& token_params,
                                                         absl::optional<uint32_t> bbr_init_cwnd,
                                                         int64_t now_mono_ns) {
  static absl::Mutex attach_mu; // worker balancers are created concurrently; attach once per host
  absl::MutexLock lock(&attach_mu);
  for (const auto& host : hosts) {
    if (!host->typedLbPolicyData<PendingRequests>().has_value()) {
      host->addLbPolicyData(std::make_unique<PendingRequests>());
    }
    // Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
    if (token_params.has_value()) { // TokenBucket: empty, rate init_r, from the first balancer's start
      host->typedLbPolicyData<PendingRequests>()->tokenBucket().init(*token_params, now_mono_ns);
    }
    // Design: abstraction/ingress/load_balancer/design.md#bbrfilter
    if (bbr_init_cwnd.has_value()) { // BbrState: STARTUP, cwnd init_cwnd, from the first balancer's start
      host->typedLbPolicyData<PendingRequests>()->bbr().init(*bbr_init_cwnd, now_mono_ns);
    }
  }
}

namespace {
// TokenBucketFilter parameters from the config (lb_tb_*); unset fields take the spec defaults.
absl::optional<TokenParams> tokenParamsOf(const DeadlineConfig& config) {
  if (!config.has_token_bucket()) {
    return absl::nullopt;
  }
  const auto& tb = config.token_bucket();
  TokenParams p;
  p.slo_ns = DurationUtil::durationToMilliseconds(tb.slo()) * 1000000;
  p.init_rate = tb.has_init_rate() ? tb.init_rate().value() : 50.0;
  p.max_tokens = tb.has_max_tokens() ? tb.max_tokens().value() : 10.0;
  p.mult_dec = tb.has_mult_dec() ? tb.mult_dec().value() : 2.0;
  p.add_inc_limit = tb.has_add_inc_limit() ? tb.add_inc_limit().value() : 2.0;
  p.add_inc = (tb.has_add_inc_frac() ? tb.add_inc_frac().value() : 0.1) * p.init_rate;
  p.r_min = (tb.has_min_rate_frac() ? tb.min_rate_frac().value() : 0.1) * p.init_rate;
  return p;
}

// BbrFilter initial cwnd from the config (lb_bbr_init_cwnd); unset: 10.
absl::optional<uint32_t> bbrInitCwndOf(const DeadlineConfig& config) {
  if (!config.has_bbr()) {
    return absl::nullopt;
  }
  return config.bbr().has_init_cwnd() ? std::max<uint32_t>(4, config.bbr().init_cwnd().value()) : 10;
}
} // namespace

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
                        : 0.0),
      token_params_(tokenParamsOf(config)), bbr_init_cwnd_(bbrInitCwndOf(config)) {
  for (const auto& host_set : priority_set_.hostSetsPerPriority()) {
    addPendingDataToHosts(host_set->hosts(), token_params_, bbr_init_cwnd_, monoNowNs());
  }
  pending_update_cb_ = priority_set_.addPriorityUpdateCb(
      [this](uint32_t, const Upstream::HostVector& hosts_added, const Upstream::HostVector&) -> absl::Status {
        addPendingDataToHosts(hosts_added, token_params_, bbr_init_cwnd_, monoNowNs());
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
  // Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
  if (token_params_.has_value()) { // StuckAttempt, before any token test: oldest pending attempt past the SLO
    for (size_t i = 0; i < hosts.size(); ++i) {
      auto pending = hosts[i]->typedLbPolicyData<PendingRequests>();
      if (excluded[i] || !pending.has_value()) {
        continue;
      }
      const absl::optional<int64_t> oldest = pending->oldest();
      if (oldest.has_value() && now_mono - *oldest > token_params_->slo_ns) {
        const auto change = pending->tokenBucket().stuck(now_mono);
        if (change.has_value()) {
          tokenTrace(nowUnixNs(), change->event, hosts[i]->address()->asString(),
                     static_cast<double>(now_mono - *oldest) / 1e6, change->rate_before, change->rate_after,
                     change->tokens);
        }
      }
    }
  }
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
    } else if (token_params_.has_value()) {
      // Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
      auto pending = hosts[i]->typedLbPolicyData<PendingRequests>(); // TokenBucketFilter: a whole token, no RTT test
      keep[i] = pending.has_value() && pending->tokenBucket().hasToken(now_mono);
    } else if (bbr_init_cwnd_.has_value()) {
      // Design: abstraction/ingress/load_balancer/design.md#bbradmit
      auto pending = hosts[i]->typedLbPolicyData<PendingRequests>(); // BbrFilter: cwnd room and a pacing slot
      keep[i] = pending.has_value() && pending->bbr().allows(now_mono);
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

  // Design: abstraction/ingress/load_balancer/design.md#filterprobe
  if (probing && chosen < hosts.size()) { // ProbeSample: the probed host's next sample is this probe's answer
    auto pending = hosts[chosen]->typedLbPolicyData<PendingRequests>();
    if (pending.has_value()) {
      pending->markProbe(now_mono);
    }
  }

  // Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
  if (token_params_.has_value() && chosen < hosts.size()) { // every attempt takes one token
    auto pending = hosts[chosen]->typedLbPolicyData<PendingRequests>();
    const bool took = pending.has_value() && pending->tokenBucket().take(now_mono);
    if (pending.has_value()) {
      const double rate = pending->tokenBucket().rate();
      tokenTrace(nowUnixNs(), took ? "take" : (remaining_ns.has_value() ? "race" : "untaken"),
                 hosts[chosen]->address()->asString(), 0.0, rate, rate, pending->tokenBucket().tokens(now_mono));
    }
    if (!took && remaining_ns.has_value()) {
      chosen = hosts.size(); // another worker took the token first: Dropped, re-checked at the next scan
    }
  }

  // Design: abstraction/ingress/load_balancer/design.md#bbradmit
  if (bbr_init_cwnd_.has_value() && chosen < hosts.size()) { // re-check under the host's lock, mark, pace
    auto pending = hosts[chosen]->typedLbPolicyData<PendingRequests>();
    const bool idle_queue = queue_.empty() && retry_waiters_.empty(); // app-limited: nothing waits here
    const bool admitted =
        pending.has_value() && pending->bbr().admit(now_mono, idle_queue, !remaining_ns.has_value());
    if (pending.has_value()) {
      bbrTrace(nowUnixNs(), admitted ? "admit" : "race", hosts[chosen]->address()->asString(),
               pending->bbr().snapshot(), 0.0);
    }
    if (!admitted && remaining_ns.has_value()) {
      chosen = hosts.size(); // another worker took the room or the pacing slot first: re-checked at the next scan
    }
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

// Design: abstraction/ingress/load_balancer/design.md#filterprobe
bool PeakEwmaDeadlineLoadBalancer::fullWeightSample(const Upstream::HostConstSharedPtr& host,
                                                    uint64_t timestamp_ns) {
  auto pending = host->typedLbPolicyData<PendingRequests>();
  return pending.has_value() && pending->takeProbeAnswer(static_cast<int64_t>(timestamp_ns)); // ProbeSample
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
