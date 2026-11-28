#pragma once

// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.25.0)
// impl/envoy (cancellation testbed): the BbrFilter's per-host BbrState, Linux BBR v1 (tcp_bbr.c + tcp_rate.c) with
// one upstream attempt per packet (abstraction/ingress/load_balancer/design.md#bbrfilter). Pure logic: times and
// the random source are passed in. One state per host, kept in the host's PendingRequests (deadline_lb.h) and so
// shared by every worker's balancer; the testbed_bbr upstream filter reports each attempt's send, answer or loss.

#include <cstdint>
#include <functional>
#include <string>

#include "absl/synchronization/mutex.h"
#include "absl/types/optional.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {

// Constants copied from Linux (abstraction/ingress/load_balancer/design.md#bbrfilter).
constexpr double kBbrHighGain = 2.885;                 // bbr_high_gain
constexpr double kBbrDrainGain = 1.0 / 2.885;          // bbr_drain_gain
constexpr double kBbrCwndGain = 2.0;                   // bbr_cwnd_gain
constexpr uint32_t kBbrCycleLen = 8;                   // CYCLE_LEN
constexpr uint64_t kBbrCycleRand = 7;                  // bbr_cycle_rand
constexpr uint64_t kBbrBwWindowRounds = 10;            // bbr_bw_rtts
constexpr int64_t kBbrMinRttWindowNs = 10000000000;    // bbr_min_rtt_win_sec
constexpr int64_t kBbrProbeRttNs = 200000000;          // bbr_probe_rtt_mode_ms
constexpr uint32_t kBbrMinCwnd = 4;                    // bbr_cwnd_min_target
constexpr double kBbrFullBwThresh = 1.25;              // bbr_full_bw_thresh
constexpr uint32_t kBbrFullBwCount = 3;                // bbr_full_bw_cnt
constexpr double kBbrPacingMargin = 0.01;              // bbr_pacing_margin_percent
constexpr int64_t kBbrInitRttNs = 1000000;             // USEC_PER_MSEC
constexpr uint64_t kBbrUnpacedSends = 10;              // data_segs_out >= 10
constexpr int64_t kBbrRateMinRttWindowNs = 300000000000; // tcp_min_rtt_wlen
constexpr uint32_t kBbrLtIntvlMinRounds = 4;           // bbr_lt_intvl_min_rtts
constexpr double kBbrLtBwRatio = 1.0 / 8.0;            // bbr_lt_bw_ratio
constexpr double kBbrLtBwDiff = 1.0 / 3.0;             // bbr_lt_bw_diff, req/s
constexpr uint32_t kBbrLtBwMaxRounds = 48;             // bbr_lt_bw_max_rtts

enum class BbrMode { Startup, Drain, ProbeBw, ProbeRtt };
const char* bbrModeName(BbrMode mode);

// What an attempt carries from its send to its answer or loss.
struct BbrSendTag {
  uint64_t seq{0};
  uint64_t delivered{0};
  int64_t delivered_ns{0};
  int64_t first_send_ns{0};
  int64_t send_ns{0};
  bool app_limited{false};
};

// A view of the state for the trace and tests.
struct BbrSnapshot {
  BbrMode mode;
  double btl_bw;        // req/s
  double min_rtt_ms;    // 0: none yet
  double pacing_rate;   // req/s
  uint32_t cwnd;
  uint32_t inflight;
  bool in_recovery;
  bool lt_use_bw;
  uint32_t cycle_idx;
  uint64_t round_count;
  bool full_bw_reached;
};

// Kathleen Nichols' windowed min/max filter (Linux lib/minmax.c).
struct BbrMinMax {
  struct Sample {
    uint64_t t;
    double v;
  };
  Sample s[3]{{0, 0.0}, {0, 0.0}, {0, 0.0}};
  void reset(uint64_t t, double v);
  double runningMax(uint64_t win, uint64_t t, double v);
  double runningMin(uint64_t win, uint64_t t, double v);
  double value() const { return s[0].v; }

private:
  double subwinUpdate(uint64_t win, Sample val);
};

using BbrRandom = std::function<uint64_t()>;

class BbrState {
public:
  // Creates the state (STARTUP, cwnd init_cwnd) at now_ns; only the first call counts.
  void init(uint32_t init_cwnd, int64_t now_ns);
  bool initialized() const;

  // BbrAdmit: room in the cwnd and the pacing time has come; false while not initialized.
  bool allows(int64_t now_ns) const;
  // BbrAdmit on the chosen host: re-checks allows (unless force: no RequestDeadline), marks app-limited if
  // idle_queue and there is room, and takes the pacing slot. False: another worker took the room first.
  bool admit(int64_t now_ns, bool idle_queue, bool force);
  // DeliveryRateSample hooks; answer and loss are no-ops while not initialized.
  BbrSendTag onSend(int64_t now_ns, const BbrRandom& rand);
  void onAnswer(const BbrSendTag& tag, int64_t now_ns, const BbrRandom& rand);
  void onLoss(const BbrSendTag& tag, int64_t now_ns);
  BbrSnapshot snapshot() const;

private:
  struct RateSample {
    int64_t delivered{0};
    absl::optional<int64_t> interval_ns; // none: not a valid sample
    int64_t rtt_ns{0};
    bool is_app_limited{false};
    uint64_t prior_delivered{0};
    uint32_t prior_inflight{0};
    uint32_t acked{0};
    uint32_t losses{0};
  };

  double btlBw() const ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  uint32_t bdp(double bw, double gain) const ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void markAppLimited() ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void idleRestart(int64_t now_ns, const BbrRandom& rand) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void pace(int64_t now_ns) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void bbrMain(const RateSample& rs, int64_t now_ns, const BbrRandom& rand) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void updateBw(const RateSample& rs, const BbrRandom& rand) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  bool isNextCyclePhase(const RateSample& rs) const ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void advanceCyclePhase() ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void resetProbeBwMode(const BbrRandom& rand) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void resetMode(const BbrRandom& rand) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void checkFullBwReached(const RateSample& rs) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void checkDrain(const RateSample& rs, const BbrRandom& rand) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void updateMinRtt(const RateSample& rs, int64_t now_ns, const BbrRandom& rand)
      ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void checkProbeRttDone(int64_t now_ns, const BbrRandom& rand) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void updateGains() ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void setPacingRate(double bw, double gain) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void saveCwnd() ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void setCwnd(const RateSample& rs, double bw, double gain) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  bool recoveryCwnd(uint32_t acked) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void endRecovery() ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void resetLtInterval() ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void resetLt() ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void ltBwSampling(const RateSample& rs, const BbrRandom& rand) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);
  void ltIntervalDone(double bw) ABSL_EXCLUSIVE_LOCKS_REQUIRED(mu_);

  mutable absl::Mutex mu_;
  bool init_ ABSL_GUARDED_BY(mu_){false};
  uint32_t init_cwnd_ ABSL_GUARDED_BY(mu_){10};
  // delivery accounting
  uint64_t sent_ ABSL_GUARDED_BY(mu_){0};
  uint32_t inflight_ ABSL_GUARDED_BY(mu_){0};
  uint64_t delivered_ ABSL_GUARDED_BY(mu_){0};
  uint64_t lost_ ABSL_GUARDED_BY(mu_){0};
  int64_t delivered_ns_ ABSL_GUARDED_BY(mu_){0};
  int64_t first_send_ns_ ABSL_GUARDED_BY(mu_){0};
  uint64_t app_limited_ ABSL_GUARDED_BY(mu_){0};
  BbrMinMax rate_min_rtt_ ABSL_GUARDED_BY(mu_);
  bool rate_min_rtt_set_ ABSL_GUARDED_BY(mu_){false};
  // model
  BbrMode mode_ ABSL_GUARDED_BY(mu_){BbrMode::Startup};
  BbrMinMax bw_ ABSL_GUARDED_BY(mu_);
  absl::optional<int64_t> min_rtt_ns_ ABSL_GUARDED_BY(mu_);
  int64_t min_rtt_stamp_ns_ ABSL_GUARDED_BY(mu_){0};
  uint64_t round_count_ ABSL_GUARDED_BY(mu_){0};
  uint64_t next_round_delivered_ ABSL_GUARDED_BY(mu_){0};
  bool round_start_ ABSL_GUARDED_BY(mu_){false};
  double full_bw_ ABSL_GUARDED_BY(mu_){0.0};
  uint32_t full_bw_count_ ABSL_GUARDED_BY(mu_){0};
  bool full_bw_reached_ ABSL_GUARDED_BY(mu_){false};
  uint32_t cycle_idx_ ABSL_GUARDED_BY(mu_){0};
  int64_t cycle_stamp_ns_ ABSL_GUARDED_BY(mu_){0};
  double pacing_gain_ ABSL_GUARDED_BY(mu_){kBbrHighGain};
  double cwnd_gain_ ABSL_GUARDED_BY(mu_){kBbrHighGain};
  bool has_seen_rtt_ ABSL_GUARDED_BY(mu_){false};
  double pacing_rate_ ABSL_GUARDED_BY(mu_){0.0};
  int64_t next_send_ns_ ABSL_GUARDED_BY(mu_){0};
  uint32_t cwnd_ ABSL_GUARDED_BY(mu_){10};
  uint32_t prior_cwnd_ ABSL_GUARDED_BY(mu_){0};
  bool idle_restart_ ABSL_GUARDED_BY(mu_){false};
  absl::optional<int64_t> probe_rtt_done_ns_ ABSL_GUARDED_BY(mu_);
  bool probe_rtt_round_done_ ABSL_GUARDED_BY(mu_){false};
  // loss
  bool in_recovery_ ABSL_GUARDED_BY(mu_){false};
  uint64_t recovery_seq_ ABSL_GUARDED_BY(mu_){0};
  bool packet_conservation_ ABSL_GUARDED_BY(mu_){false};
  uint32_t unreported_losses_ ABSL_GUARDED_BY(mu_){0};
  // LongTermBw
  bool lt_is_sampling_ ABSL_GUARDED_BY(mu_){false};
  bool lt_use_bw_ ABSL_GUARDED_BY(mu_){false};
  double lt_bw_ ABSL_GUARDED_BY(mu_){0.0};
  uint32_t lt_rtt_count_ ABSL_GUARDED_BY(mu_){0};
  uint64_t lt_last_delivered_ ABSL_GUARDED_BY(mu_){0};
  uint64_t lt_last_lost_ ABSL_GUARDED_BY(mu_){0};
  int64_t lt_last_stamp_ns_ ABSL_GUARDED_BY(mu_){0};
};

// Diagnostic trace <PEAK_EWMA_TRACE_PREFIX>.bbr.0.csv (one file per process, under a lock):
// t_unix_ns,event,host,mode,btl_bw,min_rtt_ms,pacing_rate,cwnd,inflight,in_recovery,lt_use_bw,cycle_idx,rtt_ms.
// Events: admit, race, send, answer, loss. No-op without the environment variable.
void bbrTrace(int64_t unix_ns, const char* event, const std::string& host, const BbrSnapshot& s, double rtt_ms);

} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
