// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.27.0)
// impl/envoy (cancellation testbed): tests of the BbrFilter's BbrState (bbr.h): the spec's worked examples, and
// small simulations of one backend for the mode machine, loss recovery, PROBE_RTT and LongTermBw.

#include <cmath>
#include <cstdint>
#include <map>
#include <utility>

#include "contrib/peak_ewma/load_balancing_policies/source/bbr.h"
#include "gtest/gtest.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {
namespace {

constexpr int64_t kMs = 1'000'000;

uint64_t zero() { return 0; }

// One backend: attempts sent every send_every_ms, each answered (or lost) rtt_ms later.
class BbrSim {
public:
  explicit BbrSim(uint32_t init_cwnd = 10) { b.init(init_cwnd, 0); }

  // Runs until until_ms. lost(k): attempt k is lost instead of answered. respect: send only when admitted.
  template <class Lost> void run(int64_t until_ms, int64_t send_every_ms, int64_t rtt_ms, Lost lost, bool respect) {
    for (; t_ms < until_ms; ++t_ms) {
      const int64_t now = t_ms * kMs;
      for (auto it = events.begin(); it != events.end() && it->first <= now;) {
        if (it->second.second) {
          b.onLoss(it->second.first, now);
        } else {
          b.onAnswer(it->second.first, now, rand);
        }
        it = events.erase(it);
        after_event();
      }
      if (t_ms % send_every_ms == 0 && (!respect || b.admit(now, false, false))) {
        const BbrSendTag tag = b.onSend(now, rand);
        events.emplace(now + rtt_ms * kMs, std::make_pair(tag, lost(sent)));
        sent += 1;
      }
    }
  }
  void run(int64_t until_ms, int64_t send_every_ms, int64_t rtt_ms) {
    run(until_ms, send_every_ms, rtt_ms, [](uint64_t) { return false; }, true);
  }

  BbrState b;
  BbrRandom rand = zero;
  std::multimap<int64_t, std::pair<BbrSendTag, bool>> events;
  std::function<void()> after_event = [] {};
  int64_t t_ms{0};
  uint64_t sent{0};
};

// Design: abstraction/ingress/load_balancer/design.md#bbrfilter
TEST(BbrMinMaxTest, RunningMaxKeepsTheWindowsBest) {
  BbrMinMax m;
  m.reset(0, 0.0);
  EXPECT_DOUBLE_EQ(m.runningMax(10, 1, 50.0), 50.0);
  EXPECT_DOUBLE_EQ(m.runningMax(10, 2, 30.0), 50.0);  // lower: the max holds (30 is not kept, as in Linux)
  EXPECT_DOUBLE_EQ(m.runningMax(10, 5, 40.0), 50.0);  // dt 4 > 10 / 4: kept as the second-best
  EXPECT_DOUBLE_EQ(m.runningMax(10, 12, 20.0), 40.0); // 50 (round 1) aged out: the second-best takes over
  EXPECT_DOUBLE_EQ(m.runningMax(10, 30, 10.0), 10.0); // everything aged out: reset
  EXPECT_DOUBLE_EQ(m.runningMax(10, 13, 90.0), 90.0); // higher: reset
  BbrMinMax n;
  n.reset(0, 400.0);
  EXPECT_DOUBLE_EQ(n.runningMin(300, 1, 450.0), 400.0);
  EXPECT_DOUBLE_EQ(n.runningMin(300, 2, 370.0), 370.0);
}

// Design: abstraction/ingress/load_balancer/design.md#bbrfilter
TEST(BbrStateTest, InitialState) {
  BbrState b;
  EXPECT_FALSE(b.allows(0)); // not initialized
  b.init(10, 0);
  const BbrSnapshot s = b.snapshot();
  EXPECT_EQ(s.mode, BbrMode::Startup);
  EXPECT_EQ(s.cwnd, 10u);
  EXPECT_NEAR(s.pacing_rate, 28561.5, 1e-6); // 2.885 x 10 / 0.001 x 0.99
  EXPECT_TRUE(b.allows(0));
}

// Design: abstraction/ingress/load_balancer/design.md#deliveryratesample
TEST(BbrStateTest, DeliveryRateWorkedExample) {
  BbrState b;
  b.init(10, 0);
  const BbrSendTag a = b.onSend(0, zero);
  const BbrSendTag bb = b.onSend(10 * kMs, zero);
  EXPECT_EQ(bb.delivered, 0u);
  EXPECT_EQ(bb.first_send_ns, 0);
  b.onAnswer(a, 400 * kMs, zero);
  EXPECT_DOUBLE_EQ(b.snapshot().btl_bw, 2.5);       // 1 over 400 ms
  EXPECT_DOUBLE_EQ(b.snapshot().min_rtt_ms, 400.0); // RTprop
  EXPECT_NEAR(b.snapshot().pacing_rate, 71.40375, 1e-9); // first RTT: 2.885 x 10 / 0.4 x 0.99
  b.onAnswer(bb, 410 * kMs, zero);
  EXPECT_NEAR(b.snapshot().btl_bw, 2.0 / 0.41, 1e-9); // 2 over max(10, 410) ms
  const BbrSendTag c = b.onSend(420 * kMs, zero); // idle: first_send = delivered_ns = 420 ms
  EXPECT_EQ(c.delivered, 2u);
  EXPECT_EQ(c.delivered_ns, 420 * kMs);
  EXPECT_EQ(c.first_send_ns, 420 * kMs);
  b.onAnswer(c, 820 * kMs, zero);
  EXPECT_NEAR(b.snapshot().btl_bw, 2.0 / 0.41, 1e-9); // 2.5 < 4.878: the max holds
  EXPECT_EQ(b.snapshot().round_count, 2u);
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
TEST(BbrStateTest, StartupDrainProbeBwOnASteadyBackend) {
  BbrSim sim;
  BbrMode entered = BbrMode::Startup;
  uint32_t first_cycle = 99;
  sim.after_event = [&] {
    const BbrSnapshot s = sim.b.snapshot();
    if (s.mode == BbrMode::ProbeBw && entered != BbrMode::ProbeBw) {
      first_cycle = s.cycle_idx;
    }
    entered = s.mode;
  };
  sim.run(8000, 10, 400); // 100 req/s offered, 400 ms answers
  const BbrSnapshot s = sim.b.snapshot();
  EXPECT_TRUE(s.full_bw_reached);
  EXPECT_EQ(s.mode, BbrMode::ProbeBw);
  EXPECT_EQ(first_cycle, 0u); // draw 0: 8 - 1 - 0 = 7, advanced to 0 (gain 1.25)
  EXPECT_NEAR(s.btl_bw, 100.0, 5.0);
  EXPECT_DOUBLE_EQ(s.min_rtt_ms, 400.0);
  EXPECT_LE(s.cwnd, static_cast<uint32_t>(std::ceil(s.btl_bw * 0.4 * 2))); // PROBE_BW target 2 x BDP
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
TEST(BbrStateTest, ProbeBwStartDrawOfSix) {
  BbrSim sim;
  sim.rand = [] { return uint64_t{6}; };
  uint32_t first_cycle = 99;
  BbrMode prev = BbrMode::Startup;
  sim.after_event = [&] {
    const BbrSnapshot s = sim.b.snapshot();
    if (s.mode == BbrMode::ProbeBw && prev != BbrMode::ProbeBw) {
      first_cycle = s.cycle_idx;
    }
    prev = s.mode;
  };
  sim.run(8000, 10, 400);
  EXPECT_EQ(first_cycle, 2u); // 8 - 1 - 6 = 1, advanced to 2 (gain 1)
}

// Design: abstraction/ingress/load_balancer/design.md#bbradmit
TEST(BbrStateTest, PacingSlotAndIdleCredit) {
  BbrSim sim;
  sim.run(8000, 10, 400);
  const double rate = sim.b.snapshot().pacing_rate;
  const int64_t len = static_cast<int64_t>(std::floor(1e9 / rate));
  const int64_t t0 = 100000 * kMs; // long idle: next_send_ns is far behind
  ASSERT_TRUE(sim.b.admit(t0, false, false));
  EXPECT_FALSE(sim.b.allows(t0 + len - len / 2 - 1)); // idle credit: at most half the slot
  EXPECT_TRUE(sim.b.allows(t0 + len - len / 2));
  ASSERT_TRUE(sim.b.admit(t0 + len - len / 2, false, false));
  EXPECT_FALSE(sim.b.allows(t0 + 2 * len - len / 2 - 1)); // no gap now: a whole slot
  EXPECT_TRUE(sim.b.allows(t0 + 2 * len - len / 2));
}

// Design: abstraction/ingress/load_balancer/design.md#bbradmit
TEST(BbrStateTest, FullCwndRefusesAndForceIgnoresIt) {
  BbrState b;
  b.init(4, 0);
  for (int i = 0; i < 4; ++i) {
    ASSERT_TRUE(b.admit(0, false, false)); // the first sends are unpaced
    b.onSend(0, zero);
  }
  EXPECT_FALSE(b.allows(0)); // inflight 4 = cwnd 4
  EXPECT_FALSE(b.admit(0, false, false));
  EXPECT_TRUE(b.admit(0, false, true)); // no RequestDeadline: never refused
}

// Design: abstraction/ingress/load_balancer/design.md#attemptloss
TEST(BbrStateTest, LossRecoveryWorkedExample) {
  BbrSim sim;
  sim.run(8000, 10, 400);
  // Hold the in-flight attempts; lose two of the oldest, answer one older and one newer than the first loss.
  ASSERT_GE(sim.events.size(), 4u);
  auto it = sim.events.begin();
  const BbrSendTag l1 = it->second.first;
  const BbrSendTag l2 = std::next(it)->second.first;
  const BbrSendTag old_answer = std::next(it, 2)->second.first;
  sim.events.clear();
  const uint32_t cwnd0 = sim.b.snapshot().cwnd;
  const int64_t now = sim.t_ms * kMs;
  sim.b.onLoss(l1, now);
  BbrSnapshot s = sim.b.snapshot();
  EXPECT_TRUE(s.in_recovery);
  EXPECT_EQ(s.cwnd, s.inflight); // enter: cwnd = in flight, then max(cwnd, in flight)
  sim.b.onLoss(l2, now);
  s = sim.b.snapshot();
  EXPECT_EQ(s.cwnd, s.inflight); // each loss: cwnd - 1, conservation keeps it at in flight
  sim.b.onAnswer(old_answer, now + kMs, zero);
  s = sim.b.snapshot();
  EXPECT_TRUE(s.in_recovery); // sent before the first loss
  EXPECT_EQ(s.cwnd, s.inflight + 1); // conservation: in flight + acked
  const BbrSendTag fresh = sim.b.onSend(now + 2 * kMs, zero);
  sim.b.onAnswer(fresh, now + 402 * kMs, zero);
  s = sim.b.snapshot();
  EXPECT_FALSE(s.in_recovery); // the first answer of an attempt sent after recovery began
  // restored to max(cwnd, prior_cwnd = cwnd0), then PROBE_BW growth: min(cwnd + 1, 2 x BDP)
  EXPECT_EQ(s.cwnd, std::min<uint32_t>(cwnd0 + 1, static_cast<uint32_t>(std::ceil(
                                                      s.btl_bw * (s.min_rtt_ms * kMs / 1e9) * 2))));
}

// Design: abstraction/ingress/load_balancer/design.md#attemptloss
TEST(BbrStateTest, StalledBackendLosesEverythingCwndFallsToOne) {
  BbrSim sim;
  sim.run(8000, 10, 400);
  const int64_t now = sim.t_ms * kMs;
  for (const auto& [t, ev] : sim.events) {
    sim.b.onLoss(ev.first, now); // every attempt in flight is hedged away; none is answered
  }
  sim.events.clear();
  const BbrSnapshot s = sim.b.snapshot();
  EXPECT_EQ(s.inflight, 0u);
  EXPECT_EQ(s.cwnd, 1u);
  EXPECT_TRUE(s.in_recovery); // no answer: recovery never ends
  const BbrSendTag one = sim.b.onSend(now, zero);
  EXPECT_FALSE(sim.b.allows(now + 1000 * kMs)); // cwnd 1, one in flight
  sim.b.onLoss(one, now + kMs);
  EXPECT_EQ(sim.b.snapshot().cwnd, 1u);
}

// Design: abstraction/ingress/load_balancer/design.md#attemptloss
TEST(BbrStateTest, HedgeCancelOnlyLeavesTheInflight) {
  BbrSim sim;
  sim.run(8000, 10, 400);
  const BbrSnapshot before = sim.b.snapshot();
  const BbrSendTag t = sim.events.begin()->second.first;
  sim.events.erase(sim.events.begin());
  sim.b.onCancel(t, sim.t_ms * kMs);
  const BbrSnapshot after = sim.b.snapshot();
  EXPECT_EQ(after.inflight, before.inflight - 1);
  EXPECT_EQ(after.cwnd, before.cwnd); // no cut
  EXPECT_FALSE(after.in_recovery);    // no recovery
  EXPECT_DOUBLE_EQ(after.btl_bw, before.btl_bw);
}

// Design: abstraction/ingress/load_balancer/design.md#bbrmodel
TEST(BbrStateTest, ProbeRttEveryTenSecondsThenBack) {
  BbrSim sim;
  bool saw_probe_rtt = false;
  uint32_t min_cwnd_in_probe_rtt = 1000;
  sim.after_event = [&] {
    const BbrSnapshot s = sim.b.snapshot();
    if (s.mode == BbrMode::ProbeRtt) {
      saw_probe_rtt = true;
      min_cwnd_in_probe_rtt = std::min(min_cwnd_in_probe_rtt, s.cwnd);
    }
  };
  sim.run(13000, 10, 400); // RTprop never falls below 400 ms: it expires at 10 s
  EXPECT_TRUE(saw_probe_rtt);
  EXPECT_EQ(min_cwnd_in_probe_rtt, 4u); // MIN_CWND
  const BbrSnapshot s = sim.b.snapshot();
  EXPECT_EQ(s.mode, BbrMode::ProbeBw); // 200 ms and a round later, back to PROBE_BW
  EXPECT_GT(s.cwnd, 4u);               // the saved cwnd restored
}

// Design: abstraction/ingress/load_balancer/design.md#longtermbw
TEST(BbrStateTest, PolicedBackendUsesTheLongTermRate) {
  BbrSim sim;
  bool used = false;
  double lt = 0;
  sim.after_event = [&] {
    const BbrSnapshot s = sim.b.snapshot();
    if (s.lt_use_bw && !used) {
      used = true;
      lt = s.btl_bw;
    }
  };
  // 100 req/s sent regardless of the cwnd; 3 in 10 lost: about 70 req/s delivered, 30% loss.
  sim.run(30000, 10, 400, [](uint64_t k) { return k % 10 < 3; }, false);
  EXPECT_TRUE(used);
  EXPECT_NEAR(lt, 70.0, 7.0);
}

} // namespace
} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
