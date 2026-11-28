// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.26.0)
#include "contrib/peak_ewma/load_balancing_policies/source/bbr.h"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstdlib>

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

namespace {
constexpr double kBbrPacingGain[kBbrCycleLen] = {1.25, 0.75, 1, 1, 1, 1, 1, 1}; // bbr_pacing_gain
} // namespace

const char* bbrModeName(BbrMode mode) {
  switch (mode) {
  case BbrMode::Startup:
    return "STARTUP";
  case BbrMode::Drain:
    return "DRAIN";
  case BbrMode::ProbeBw:
    return "PROBE_BW";
  case BbrMode::ProbeRtt:
    return "PROBE_RTT";
  }
  return "?";
}

// Design: abstraction/ingress/load_balancer/design.md#bbrfilter
void BbrMinMax::reset(uint64_t t, double v) { s[0] = s[1] = s[2] = Sample{t, v}; }

double BbrMinMax::subwinUpdate(uint64_t win, Sample val) {
  const uint64_t dt = val.t - s[0].t;
  if (dt > win) {
    s[0] = s[1];
    s[1] = s[2];
    s[2] = val;
    if (val.t - s[0].t > win) {
      s[0] = s[1];
      s[1] = s[2];
      s[2] = val;
    }
  } else if (s[1].t == s[0].t && dt > win / 4) {
    s[2] = s[1] = val;
  } else if (s[2].t == s[1].t && dt > win / 2) {
    s[2] = val;
  }
  return s[0].v;
}

double BbrMinMax::runningMax(uint64_t win, uint64_t t, double v) {
  const Sample val{t, v};
  if (v >= s[0].v || val.t - s[2].t > win) {
    reset(t, v);
    return v;
  }
  if (v >= s[1].v) {
    s[2] = s[1] = val;
  } else if (v >= s[2].v) {
    s[2] = val;
  }
  return subwinUpdate(win, val);
}

double BbrMinMax::runningMin(uint64_t win, uint64_t t, double v) {
  const Sample val{t, v};
  if (v <= s[0].v || val.t - s[2].t > win) {
    reset(t, v);
    return v;
  }
  if (v <= s[1].v) {
    s[2] = s[1] = val;
  } else if (v <= s[2].v) {
    s[2] = val;
  }
  return subwinUpdate(win, val);
}

// Design: abstraction/ingress/load_balancer/design.md#bbrfilter
void BbrState::init(uint32_t init_cwnd, int64_t now_ns) {
  absl::MutexLock lock(&mu_);
  if (init_) {
    return; // the first balancer to start sets it; later workers reuse it
  }
  init_ = true;
  init_cwnd_ = init_cwnd;
  cwnd_ = init_cwnd;
  mode_ = BbrMode::Startup;
  pacing_gain_ = kBbrHighGain;
  cwnd_gain_ = kBbrHighGain;
  min_rtt_stamp_ns_ = now_ns;
  bw_.reset(0, 0.0);
  pacing_rate_ = kBbrHighGain * static_cast<double>(init_cwnd) / (static_cast<double>(kBbrInitRttNs) / 1e9) *
                 (1.0 - kBbrPacingMargin);
  resetLt(); // Linux bbr_init
}

bool BbrState::initialized() const {
  absl::MutexLock lock(&mu_);
  return init_;
}

double BbrState::btlBw() const { return lt_use_bw_ ? lt_bw_ : bw_.value(); }

// Design: abstraction/ingress/load_balancer/design.md#bbrfilter
uint32_t BbrState::bdp(double bw, double gain) const {
  if (!min_rtt_ns_.has_value()) {
    return init_cwnd_;
  }
  const double w = bw * (static_cast<double>(*min_rtt_ns_) / 1e9) * gain;
  return static_cast<uint32_t>(std::ceil(w));
}

// Design: abstraction/ingress/load_balancer/design.md#bbradmit
bool BbrState::allows(int64_t now_ns) const {
  absl::MutexLock lock(&mu_);
  return init_ && inflight_ < cwnd_ && next_send_ns_ <= now_ns;
}

// Design: abstraction/ingress/load_balancer/design.md#bbradmit
bool BbrState::admit(int64_t now_ns, bool idle_queue, bool force) {
  absl::MutexLock lock(&mu_);
  if (!init_) {
    return force;
  }
  if (!force && !(inflight_ < cwnd_ && next_send_ns_ <= now_ns)) {
    return false; // another worker took the room first
  }
  if (idle_queue && inflight_ < cwnd_) {
    markAppLimited(); // the Ingress sends all it has for this host
  }
  pace(now_ns);
  return true;
}

// Design: abstraction/ingress/load_balancer/design.md#bbradmit
void BbrState::pace(int64_t now_ns) {
  const int64_t prior = next_send_ns_;
  next_send_ns_ = std::max(next_send_ns_, now_ns);
  if (sent_ >= kBbrUnpacedSends && pacing_rate_ > 0.0) {
    int64_t len_ns = static_cast<int64_t>(std::floor(1e9 / pacing_rate_));
    len_ns -= std::min<int64_t>(len_ns / 2, next_send_ns_ - prior); // credit for an idle gap, at most half
    next_send_ns_ += len_ns;
  }
}

// Design: abstraction/ingress/load_balancer/design.md#deliveryratesample
void BbrState::markAppLimited() {
  app_limited_ = delivered_ + inflight_ > 0 ? delivered_ + inflight_ : 1;
}

// Design: abstraction/ingress/load_balancer/design.md#deliveryratesample
BbrSendTag BbrState::onSend(int64_t now_ns, const BbrRandom& rand) {
  absl::MutexLock lock(&mu_);
  if (inflight_ == 0) {
    first_send_ns_ = now_ns;
    delivered_ns_ = now_ns;
    if (init_ && app_limited_ != 0) {
      idleRestart(now_ns, rand); // Linux CA_EVENT_TX_START
    }
  }
  BbrSendTag tag{sent_, delivered_, delivered_ns_, first_send_ns_, now_ns, app_limited_ != 0};
  sent_ += 1;
  inflight_ += 1;
  return tag;
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
void BbrState::idleRestart(int64_t now_ns, const BbrRandom& rand) {
  idle_restart_ = true;
  if (mode_ == BbrMode::ProbeBw) {
    setPacingRate(btlBw(), 1.0);
  } else if (mode_ == BbrMode::ProbeRtt) {
    checkProbeRttDone(now_ns, rand);
  }
}

// Design: abstraction/ingress/load_balancer/design.md#deliveryratesample
void BbrState::onAnswer(const BbrSendTag& tag, int64_t now_ns, const BbrRandom& rand) {
  absl::MutexLock lock(&mu_);
  if (!init_ || inflight_ == 0) {
    return;
  }
  const uint32_t prior_inflight = inflight_;
  inflight_ -= 1;
  delivered_ += 1;
  const int64_t rtt = now_ns - tag.send_ns;
  if (!rate_min_rtt_set_) {
    rate_min_rtt_.reset(static_cast<uint64_t>(now_ns), static_cast<double>(rtt));
    rate_min_rtt_set_ = true;
  } else {
    rate_min_rtt_.runningMin(kBbrRateMinRttWindowNs, static_cast<uint64_t>(now_ns), static_cast<double>(rtt));
  }
  const int64_t send_elapsed = tag.send_ns - tag.first_send_ns;
  first_send_ns_ = tag.send_ns;
  if (app_limited_ != 0 && delivered_ > app_limited_) {
    app_limited_ = 0;
  }
  delivered_ns_ = now_ns;
  RateSample rs;
  rs.delivered = static_cast<int64_t>(delivered_ - tag.delivered);
  rs.interval_ns = std::max(send_elapsed, now_ns - tag.delivered_ns);
  rs.rtt_ns = rtt;
  rs.is_app_limited = tag.app_limited;
  rs.prior_delivered = tag.delivered;
  rs.prior_inflight = prior_inflight;
  rs.acked = 1;
  rs.losses = unreported_losses_;
  unreported_losses_ = 0;
  if (static_cast<double>(*rs.interval_ns) < rate_min_rtt_.value()) {
    rs.interval_ns = absl::nullopt; // not a valid sample
  }
  // Design: abstraction/ingress/load_balancer/design.md#attemptloss
  if (in_recovery_ && tag.seq >= recovery_seq_) {
    endRecovery();
  }
  bbrMain(rs, now_ns, rand);
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
void BbrState::bbrMain(const RateSample& rs, int64_t now_ns, const BbrRandom& rand) {
  updateBw(rs, rand);
  if (mode_ == BbrMode::ProbeBw && isNextCyclePhase(rs)) { // update_cycle_phase
    advanceCyclePhase();
  }
  checkFullBwReached(rs);
  checkDrain(rs, rand);
  updateMinRtt(rs, now_ns, rand);
  updateGains();
  setPacingRate(btlBw(), pacing_gain_);
  setCwnd(rs, btlBw(), cwnd_gain_);
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
void BbrState::updateBw(const RateSample& rs, const BbrRandom& rand) {
  round_start_ = false;
  if (!rs.interval_ns.has_value() || *rs.interval_ns <= 0) {
    return; // not a valid observation
  }
  if (rs.prior_delivered >= next_round_delivered_) { // a round has passed
    next_round_delivered_ = delivered_;
    round_count_ += 1;
    round_start_ = true;
    packet_conservation_ = false;
  }
  ltBwSampling(rs, rand);
  const double bw = static_cast<double>(rs.delivered) * 1e9 / static_cast<double>(*rs.interval_ns);
  if (!rs.is_app_limited || bw >= bw_.value()) {
    bw_.runningMax(kBbrBwWindowRounds, round_count_, bw);
  }
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
bool BbrState::isNextCyclePhase(const RateSample& rs) const {
  const bool full_length = min_rtt_ns_.has_value() && delivered_ns_ - cycle_stamp_ns_ > *min_rtt_ns_;
  if (pacing_gain_ == 1.0) {
    return full_length;
  }
  if (pacing_gain_ > 1.0) { // probing: until a loss or the extra in flight is there
    return full_length && (rs.losses > 0 || rs.prior_inflight >= bdp(btlBw(), pacing_gain_));
  }
  return full_length || rs.prior_inflight <= bdp(btlBw(), 1.0); // draining: until the queue is gone
}

void BbrState::advanceCyclePhase() {
  cycle_idx_ = (cycle_idx_ + 1) % kBbrCycleLen;
  cycle_stamp_ns_ = delivered_ns_;
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
void BbrState::resetProbeBwMode(const BbrRandom& rand) {
  mode_ = BbrMode::ProbeBw;
  cycle_idx_ = static_cast<uint32_t>(kBbrCycleLen - 1 - (rand() % kBbrCycleRand)); // 1 ..= 7
  advanceCyclePhase(); // never starts in the 0.75 phase
}

void BbrState::resetMode(const BbrRandom& rand) {
  if (!full_bw_reached_) {
    mode_ = BbrMode::Startup;
  } else {
    resetProbeBwMode(rand);
  }
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
void BbrState::checkFullBwReached(const RateSample& rs) {
  if (full_bw_reached_ || !round_start_ || rs.is_app_limited) {
    return;
  }
  if (btlBw() >= full_bw_ * kBbrFullBwThresh) {
    full_bw_ = btlBw();
    full_bw_count_ = 0;
    return;
  }
  full_bw_count_ += 1;
  full_bw_reached_ = full_bw_count_ >= kBbrFullBwCount;
}

void BbrState::checkDrain(const RateSample& rs, const BbrRandom& rand) {
  if (mode_ == BbrMode::Startup && full_bw_reached_) {
    mode_ = BbrMode::Drain;
  }
  if (mode_ == BbrMode::Drain && rs.prior_inflight <= bdp(btlBw(), 1.0)) {
    resetProbeBwMode(rand);
  }
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
void BbrState::updateMinRtt(const RateSample& rs, int64_t now_ns, const BbrRandom& rand) {
  const bool expired = now_ns > min_rtt_stamp_ns_ + kBbrMinRttWindowNs;
  if (!min_rtt_ns_.has_value() || rs.rtt_ns < *min_rtt_ns_ || expired) {
    min_rtt_ns_ = rs.rtt_ns;
    min_rtt_stamp_ns_ = now_ns;
  }
  if (expired && !idle_restart_ && mode_ != BbrMode::ProbeRtt) {
    mode_ = BbrMode::ProbeRtt;
    saveCwnd();
    probe_rtt_done_ns_ = absl::nullopt;
  }
  if (mode_ == BbrMode::ProbeRtt) {
    markAppLimited(); // samples taken while holding back are not capacity
    if (!probe_rtt_done_ns_.has_value() && inflight_ <= kBbrMinCwnd) {
      probe_rtt_done_ns_ = now_ns + kBbrProbeRttNs;
      probe_rtt_round_done_ = false;
      next_round_delivered_ = delivered_;
    } else if (probe_rtt_done_ns_.has_value()) {
      if (round_start_) {
        probe_rtt_round_done_ = true;
      }
      if (probe_rtt_round_done_) {
        checkProbeRttDone(now_ns, rand);
      }
    }
  }
  if (rs.delivered > 0) {
    idle_restart_ = false;
  }
}

void BbrState::checkProbeRttDone(int64_t now_ns, const BbrRandom& rand) {
  if (!probe_rtt_done_ns_.has_value() || !(now_ns > *probe_rtt_done_ns_)) {
    return;
  }
  min_rtt_stamp_ns_ = now_ns;
  cwnd_ = std::max(cwnd_, prior_cwnd_);
  resetMode(rand);
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
void BbrState::updateGains() {
  switch (mode_) {
  case BbrMode::Startup:
    pacing_gain_ = kBbrHighGain;
    cwnd_gain_ = kBbrHighGain;
    break;
  case BbrMode::Drain:
    pacing_gain_ = kBbrDrainGain;
    cwnd_gain_ = kBbrHighGain;
    break;
  case BbrMode::ProbeBw:
    pacing_gain_ = lt_use_bw_ ? 1.0 : kBbrPacingGain[cycle_idx_];
    cwnd_gain_ = kBbrCwndGain;
    break;
  case BbrMode::ProbeRtt:
    pacing_gain_ = 1.0;
    cwnd_gain_ = 1.0;
    break;
  }
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
void BbrState::setPacingRate(double bw, double gain) {
  const double r = bw * gain * (1.0 - kBbrPacingMargin);
  if (!has_seen_rtt_ && min_rtt_ns_.has_value()) { // Linux bbr_init_pacing_rate_from_rtt with srtt
    has_seen_rtt_ = true;
    pacing_rate_ = kBbrHighGain * static_cast<double>(cwnd_) / (static_cast<double>(*min_rtt_ns_) / 1e9) *
                   (1.0 - kBbrPacingMargin);
  }
  if (full_bw_reached_ || r > pacing_rate_) {
    pacing_rate_ = r; // STARTUP only raises it
  }
}

void BbrState::saveCwnd() {
  if (!in_recovery_ && mode_ != BbrMode::ProbeRtt) {
    prior_cwnd_ = cwnd_;
  } else {
    prior_cwnd_ = std::max(prior_cwnd_, cwnd_);
  }
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
void BbrState::setCwnd(const RateSample& rs, double bw, double gain) {
  if (rs.acked > 0 && !recoveryCwnd(rs.acked)) {
    const uint32_t target = bdp(bw, gain);
    if (full_bw_reached_) {
      cwnd_ = std::min(cwnd_ + rs.acked, target);
    } else if (cwnd_ < target || delivered_ < init_cwnd_) {
      cwnd_ = cwnd_ + rs.acked;
    }
    cwnd_ = std::max(cwnd_, kBbrMinCwnd);
  }
  if (mode_ == BbrMode::ProbeRtt) {
    cwnd_ = std::min(cwnd_, kBbrMinCwnd);
  }
}

// Design: abstraction/ingress/load_balancer/design.md#attemptloss
void BbrState::onLoss(const BbrSendTag&, int64_t) {
  absl::MutexLock lock(&mu_);
  if (!init_ || inflight_ == 0) {
    return;
  }
  inflight_ -= 1;
  lost_ += 1;
  unreported_losses_ += 1; // reported in the next answer's sample
  cwnd_ = std::max<uint32_t>(cwnd_ > 0 ? cwnd_ - 1 : 0, 1); // rs.losses = 1
  if (!in_recovery_) { // enter recovery (Linux tcp_enter_recovery)
    saveCwnd();        // Linux bbr_ssthresh
    in_recovery_ = true;
    recovery_seq_ = sent_; // Linux high_seq = snd_nxt
    packet_conservation_ = true;
    next_round_delivered_ = delivered_; // a round starts now
    cwnd_ = inflight_;                  // in flight + acked (0)
  }
  if (packet_conservation_) {
    cwnd_ = std::max(cwnd_, inflight_);
  }
  if (mode_ == BbrMode::ProbeRtt) {
    cwnd_ = std::min(cwnd_, kBbrMinCwnd);
  }
}

// Design: abstraction/ingress/load_balancer/design.md#attemptloss
void BbrState::onCancel(const BbrSendTag&, int64_t) {
  absl::MutexLock lock(&mu_);
  if (!init_ || inflight_ == 0) {
    return;
  }
  inflight_ -= 1; // HedgeCancel: neither delivered nor lost
}

// Design: abstraction/ingress/load_balancer/design.md#attemptloss
bool BbrState::recoveryCwnd(uint32_t acked) {
  if (packet_conservation_) {
    cwnd_ = std::max(cwnd_, inflight_ + acked);
    return true;
  }
  return false;
}

void BbrState::endRecovery() {
  in_recovery_ = false;
  packet_conservation_ = false;
  cwnd_ = std::max(cwnd_, prior_cwnd_);
}

// Design: abstraction/ingress/load_balancer/design.md#longtermbw
void BbrState::resetLtInterval() {
  lt_last_stamp_ns_ = delivered_ns_;
  lt_last_delivered_ = delivered_;
  lt_last_lost_ = lost_;
  lt_rtt_count_ = 0;
}

void BbrState::resetLt() {
  lt_bw_ = 0.0;
  lt_use_bw_ = false;
  lt_is_sampling_ = false;
  resetLtInterval();
}

// Design: abstraction/ingress/load_balancer/design.md#longtermbw
void BbrState::ltBwSampling(const RateSample& rs, const BbrRandom& rand) {
  if (lt_use_bw_) {
    if (mode_ == BbrMode::ProbeBw && round_start_) {
      lt_rtt_count_ += 1;
      if (lt_rtt_count_ >= kBbrLtBwMaxRounds) {
        resetLt();
        resetProbeBwMode(rand);
      }
    }
    return;
  }
  if (!lt_is_sampling_) {
    if (rs.losses == 0) {
      return;
    }
    resetLtInterval();
    lt_is_sampling_ = true;
  }
  if (rs.is_app_limited) {
    resetLt();
    return;
  }
  if (round_start_) {
    lt_rtt_count_ += 1;
  }
  if (lt_rtt_count_ < kBbrLtIntvlMinRounds) {
    return;
  }
  if (lt_rtt_count_ > 4 * kBbrLtIntvlMinRounds) {
    resetLt();
    return;
  }
  if (rs.losses == 0) {
    return;
  }
  const uint64_t lost = lost_ - lt_last_lost_;
  const uint64_t delivered = delivered_ - lt_last_delivered_;
  if (delivered == 0 || lost * 256 < 50 * delivered) { // LT_LOSS_THRESH, exact in integers
    return;
  }
  const int64_t t = delivered_ns_ / 1000000 - lt_last_stamp_ns_ / 1000000; // whole ms, as Linux
  if (t < 1) {
    return;
  }
  ltIntervalDone(static_cast<double>(delivered) * 1000.0 / static_cast<double>(t));
}

void BbrState::ltIntervalDone(double bw) {
  if (lt_bw_ > 0.0) {
    const double diff = std::fabs(bw - lt_bw_);
    if (diff <= kBbrLtBwRatio * lt_bw_ || diff <= kBbrLtBwDiff) { // the two intervals agree
      lt_bw_ = (bw + lt_bw_) / 2.0;
      lt_use_bw_ = true;
      pacing_gain_ = 1.0;
      lt_rtt_count_ = 0;
      return;
    }
  }
  lt_bw_ = bw;
  resetLtInterval();
}

BbrSnapshot BbrState::snapshot() const {
  absl::MutexLock lock(&mu_);
  return BbrSnapshot{mode_,
                     btlBw(),
                     min_rtt_ns_.has_value() ? static_cast<double>(*min_rtt_ns_) / 1e6 : 0.0,
                     pacing_rate_,
                     cwnd_,
                     inflight_,
                     in_recovery_,
                     lt_use_bw_,
                     cycle_idx_,
                     round_count_,
                     full_bw_reached_};
}

void bbrTrace(int64_t unix_ns, const char* event, const std::string& host, const BbrSnapshot& s, double rtt_ms) {
  static const char* prefix = std::getenv("PEAK_EWMA_TRACE_PREFIX");
  if (prefix == nullptr || prefix[0] == '\0') {
    return;
  }
  static absl::Mutex mu;
  static std::FILE* f = nullptr;
  absl::MutexLock lock(&mu);
  if (f == nullptr) {
    f = std::fopen((std::string(prefix) + ".bbr.0.csv").c_str(), "w");
    if (f == nullptr) {
      return;
    }
    std::setvbuf(f, nullptr, _IOLBF, 1 << 16);
    std::fputs("t_unix_ns,event,host,mode,btl_bw,min_rtt_ms,pacing_rate,cwnd,inflight,in_recovery,lt_use_bw,"
               "cycle_idx,rtt_ms\n",
               f);
  }
  std::fprintf(f, "%lld,%s,%s,%s,%.9g,%.9g,%.9g,%u,%u,%d,%d,%u,%.9g\n", static_cast<long long>(unix_ns), event,
               host.c_str(), bbrModeName(s.mode), s.btl_bw, s.min_rtt_ms, s.pacing_rate, s.cwnd, s.inflight,
               s.in_recovery ? 1 : 0, s.lt_use_bw ? 1 : 0, s.cycle_idx, rtt_ms);
}

} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
