// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.22.0)
// impl/envoy (cancellation testbed): tests of the peak_ewma_deadline load balancer (deadline_lb.h): the deadline
// filter over every host, the lowest-cost choice, and the per-worker FIFO wait queue.

#include <chrono>
#include <cmath>
#include <memory>
#include <string>

#include "source/common/network/address_impl.h"

#include "test/common/stats/stat_test_utility.h"
#include "test/mocks/common.h"
#include "test/mocks/event/mocks.h"
#include "test/mocks/network/connection.h"
#include "test/mocks/runtime/mocks.h"
#include "test/mocks/stream_info/mocks.h"
#include "test/mocks/upstream/cluster_info.h"
#include "test/mocks/upstream/host.h"
#include "test/mocks/upstream/load_balancer_context.h"
#include "test/mocks/upstream/priority_set.h"
#include "test/test_common/utility.h"

#include "contrib/peak_ewma/load_balancing_policies/source/deadline_lb.h"
#include "contrib/peak_ewma/load_balancing_policies/source/host_data.h"
#include "gmock/gmock.h"
#include "gtest/gtest.h"

using testing::_;
using testing::NiceMock;
using testing::Return;
using testing::ReturnRef;

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {
namespace {

constexpr int64_t kNowNs = 1'700'000'000'000'000'000;
constexpr int64_t kMs = 1'000'000;

// One request: its router context, headers and (shared) downstream connection.
struct Request {
  NiceMock<Upstream::MockLoadBalancerContext> context;
  Http::TestRequestHeaderMapImpl headers;
  NiceMock<StreamInfo::MockStreamInfo> stream_info; // attempt_count_: 1 first attempt, > 1 an LB retry
};

class PeakEwmaDeadlineTest : public ::testing::Test {
protected:
  void SetUp() override {
    stat_names_ = std::make_unique<Upstream::ClusterLbStatNames>(store_.symbolTable());
    stats_ = std::make_unique<Upstream::ClusterLbStats>(*stat_names_, *store_.rootScope());
    for (int i = 0; i < 2; ++i) {
      auto host = std::make_shared<NiceMock<Upstream::MockHost>>();
      ON_CALL(*host, address())
          .WillByDefault(Return(std::make_shared<Network::Address::Ipv4Instance>(
              "10.0.0." + std::to_string(i + 1), 8080)));
      hosts_.push_back(host);
    }
    host_set_ = priority_set_.getMockHostSet(0);
    host_set_->hosts_ = hosts_;
    host_set_->healthy_hosts_ = hosts_;
    ON_CALL(priority_set_, hostSetsPerPriority()).WillByDefault(ReturnRef(priority_set_.host_sets_));
    ON_CALL(*cluster_info_, statsScope()).WillByDefault(ReturnRef(*store_.rootScope()));
    ON_CALL(time_source_, monotonicTime()).WillByDefault([this]() {
      return MonotonicTime(std::chrono::duration_cast<MonotonicTime::duration>(std::chrono::nanoseconds(mono_ns_)));
    });
    ON_CALL(time_source_, systemTime()).WillByDefault([this]() {
      return SystemTime(std::chrono::duration_cast<SystemTime::duration>(std::chrono::nanoseconds(now_ns_)));
    });
    lb_ = std::make_unique<PeakEwmaDeadlineLoadBalancer>(priority_set_, nullptr, *stats_, runtime_, random_,
                                                         50, *cluster_info_, time_source_, config_);
  }

  // Host i's EWMA RTT (0: no sample), last sampled now, and active requests: cost = ewma_ms * (active + 1).
  void setHost(size_t i, double ewma_ms, uint64_t active) {
    auto data = hosts_[i]->typedLbPolicyData<PeakEwmaHostLbPolicyData>();
    ASSERT_TRUE(data.has_value());
    data->updateEwma(ewma_ms, static_cast<uint64_t>(mono_ns_));
    hosts_[i]->stats().rq_active_.set(active);
  }

  // A request with deadline now + remaining_ns (no header if remaining_ns is nullopt).
  std::unique_ptr<Request> request(absl::optional<int64_t> remaining_ns, uint32_t attempt = 1) {
    auto r = std::make_unique<Request>();
    r->stream_info.attempt_count_ = attempt;
    ON_CALL(r->context, requestStreamInfo()).WillByDefault(Return(&r->stream_info));
    if (remaining_ns.has_value()) {
      r->headers.addCopy(Http::LowerCaseString("x-request-deadline"), std::to_string(now_ns_ + *remaining_ns));
    }
    ON_CALL(r->context, downstreamHeaders()).WillByDefault(Return(&r->headers));
    ON_CALL(r->context, downstreamConnection()).WillByDefault(Return(&connection_));
    return r;
  }

  // The worker's queue timer, created on the first enqueue.
  Event::MockTimer* expectTimer() { return new NiceMock<Event::MockTimer>(&connection_.dispatcher_); }

  Stats::TestUtil::TestStore store_;
  std::unique_ptr<Upstream::ClusterLbStatNames> stat_names_;
  std::unique_ptr<Upstream::ClusterLbStats> stats_;
  std::shared_ptr<NiceMock<Upstream::MockClusterInfo>> cluster_info_{
      std::make_shared<NiceMock<Upstream::MockClusterInfo>>()};
  NiceMock<Upstream::MockPrioritySet> priority_set_;
  Upstream::MockHostSet* host_set_;
  NiceMock<Runtime::MockLoader> runtime_;
  NiceMock<Random::MockRandomGenerator> random_;
  NiceMock<MockTimeSystem> time_source_;
  NiceMock<Network::MockConnection> connection_;
  int64_t now_ns_{kNowNs};
  int64_t mono_ns_{1234567890LL * kMs}; // monotonic clock (RTT sample and pending start times)
  void advance(int64_t ms) {          // both clocks
    now_ns_ += ms * kMs;
    mono_ns_ += ms * kMs;
  }
  Upstream::HostVector hosts_;
  DeadlineConfig config_;
  std::unique_ptr<PeakEwmaDeadlineLoadBalancer> lb_;
};

// FilterMargin gamma = 1.5: the same boundary as RttEqualToTimeLeftPassesOneNanosecondMoreWaits, at gamma x RTT.
class PeakEwmaDeadlineMarginTest : public PeakEwmaDeadlineTest {
protected:
  void SetUp() override {
    config_.set_filter_margin(1.5);
    PeakEwmaDeadlineTest::SetUp();
  }
};

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineMarginTest, MarginScalesTheProvisionalRtt) {
  setHost(0, 1000, 0); // RTT 1000 ms: 1.5 x 1000 ms = 1500 ms
  setHost(1, 8000, 0);
  auto equal = request(1500 * kMs);
  EXPECT_EQ(lb_->chooseHost(&equal->context).host, hosts_[0]); // 1.5e9 is not > 1.5e9: kept
  expectTimer();
  auto tight = request(1500 * kMs - 1);
  auto response = lb_->chooseHost(&tight->context);
  EXPECT_EQ(response.host, nullptr); // 1.5e9 > 1.5e9 - 1: refused everywhere, waits
  EXPECT_EQ(lb_->queueLength(), 1u);
}

// FilterProbe with tau = 4 s.
class PeakEwmaDeadlineProbeTest : public PeakEwmaDeadlineTest {
protected:
  void SetUp() override {
    config_.mutable_probe_tau()->set_seconds(4);
    PeakEwmaDeadlineTest::SetUp();
  }
};

// Design: abstraction/ingress/load_balancer/design.md#filterprobe
TEST_F(PeakEwmaDeadlineTest, ProbeOffRefusedStaysRefused) {
  setHost(0, 8000, 0);
  setHost(1, 8000, 0);
  expectTimer();
  auto r = request(4000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&r->context).host, nullptr); // both refused, no probe: waits
}

// Design: abstraction/ingress/load_balancer/design.md#filterprobe
TEST_F(PeakEwmaDeadlineProbeTest, NeverSentHostIsProbedAndTheProbeResetsItsClock) {
  setHost(0, 8000, 0); // cost 8000
  setHost(1, 9000, 0); // cost 9000
  ON_CALL(random_, random()).WillByDefault(Return(0)); // u = 0: heads whenever p > 0
  auto a = request(4000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&a->context).host, hosts_[0]); // both refused, both never sent (p ~ 1): cheaper probed
  auto b = request(4000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&b->context).host, hosts_[1]); // host 0 just sent (p = 0); host 1 still probed
}

// Design: abstraction/ingress/load_balancer/design.md#filterprobe
TEST_F(PeakEwmaDeadlineProbeTest, CoinFollowsTimeSinceLastSend) {
  setHost(0, 100, 0);
  setHost(1, 200, 0);
  auto warm0 = request(absl::nullopt); // no deadline: never filtered; host 0 is cheaper
  EXPECT_EQ(lb_->chooseHost(&warm0->context).host, hosts_[0]);
  setHost(0, 300, 0);
  auto warm1 = request(absl::nullopt); // now host 1 is cheaper
  EXPECT_EQ(lb_->chooseHost(&warm1->context).host, hosts_[1]);
  warm0.reset(); // both answered: nothing pending, so both may be probed
  warm1.reset();
  ON_CALL(random_, random()).WillByDefault(Return(1ULL << 63)); // u = 0.5
  advance(400);
  setHost(0, 8000, 0);
  setHost(1, 8000, 0);
  expectTimer();
  auto early = request(4000 * kMs, 2); // an LB retry: refused retries wait apart, so the FIFO stays empty
  EXPECT_EQ(lb_->chooseHost(&early->context).host, nullptr); // dt 0.4 s: p = 0.095 < 0.5: tails, waits
  advance(3600);
  setHost(0, 8000, 0);
  setHost(1, 9000, 0);
  auto later = request(4000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&later->context).host, hosts_[0]); // dt 4 s: p = 0.632 > 0.5: heads, cheaper
}

// Design: abstraction/ingress/load_balancer/design.md#filterprobe
TEST_F(PeakEwmaDeadlineProbeTest, HostWithSomethingPendingIsNeverProbed) {
  setHost(0, 100, 0);
  setHost(1, 200, 0);
  auto stuck = request(absl::nullopt); // no deadline: sent to host 0, and stays pending there (never answered)
  EXPECT_EQ(lb_->chooseHost(&stuck->context).host, hosts_[0]);
  ON_CALL(random_, random()).WillByDefault(Return(0)); // u = 0: heads whenever a coin is flipped
  advance(8000);
  setHost(0, 8000, 0);
  setHost(1, 9000, 0);
  auto r = request(4000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&r->context).host, hosts_[1]); // host 0 is cheaper but pending: only host 1 probed
}

// Design: abstraction/ingress/load_balancer/design.md#filterprobe
TEST_F(PeakEwmaDeadlineProbeTest, ProbeAnswerReplacesTheAverageLaterSamplesAreTimeWeighted) {
  setHost(0, 2604, 0); // the worked example's average; 2604 ms > 2000 ms left: refused
  setHost(1, 9000, 0);
  ON_CALL(random_, random()).WillByDefault(Return(0)); // heads
  auto probe = request(2000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&probe->context).host, hosts_[0]); // both refused, never sent: the cheaper is probed
  probe.reset();
  auto data = hosts_[0]->typedLbPolicyData<PeakEwmaHostLbPolicyData>();
  ASSERT_TRUE(data.has_value());
  advance(400);
  data->recordRttSample(401, static_cast<uint64_t>(mono_ns_)); // the probe's answer
  advance(200);                                                // past the aggregation interval
  auto fold1 = request(absl::nullopt);
  lb_->chooseHost(&fold1->context);
  EXPECT_DOUBLE_EQ(data->getEwmaRtt(), 401.0); // ProbeSample: weight 1 (time-based would give 2517.6)
  fold1.reset();
  advance(400);
  data->recordRttSample(2000, static_cast<uint64_t>(mono_ns_)); // an ordinary sample: the mark was cleared
  advance(200);
  auto fold2 = request(absl::nullopt);
  lb_->chooseHost(&fold2->context);
  const double a = 1.0 - std::exp(-0.6 / 10.0); // dt 0.6 s since the probe's answer, tau 10 s
  EXPECT_NEAR(data->getEwmaRtt(), a * 2000 + (1 - a) * 401, 1e-6);
}

// Design: abstraction/ingress/load_balancer/design.md#filterprobe
TEST_F(PeakEwmaDeadlineProbeTest, LateRequestIsNeverProbed) {
  setHost(0, 8000, 0);
  setHost(1, 8000, 0);
  ON_CALL(random_, random()).WillByDefault(Return(0));
  expectTimer();
  auto late = request(0);
  EXPECT_EQ(lb_->chooseHost(&late->context).host, nullptr); // remaining <= 0: no backend, no coin
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, NoDeadlineHeaderTakesLowestCostOverAllHosts) {
  setHost(0, 100, 5); // cost 600
  setHost(1, 100, 1); // cost 200
  auto r = request(absl::nullopt);
  auto response = lb_->chooseHost(&r->context);
  EXPECT_EQ(response.host, hosts_[1]);
  EXPECT_EQ(response.cancelable, nullptr);
  EXPECT_EQ(lb_->queueLength(), 0u);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, RttEqualToTimeLeftPassesOneNanosecondMoreWaits) {
  setHost(0, 4000, 9); // RTT 4000 ms (cost 40 s: the test does not use the active requests)
  setHost(1, 8000, 0); // RTT 8000 ms
  auto equal = request(4000 * kMs);
  auto response = lb_->chooseHost(&equal->context);
  EXPECT_EQ(response.host, hosts_[0]); // 4000 ms is not > 4000 ms: kept

  expectTimer();
  auto tight = request(4000 * kMs - 1);
  response = lb_->chooseHost(&tight->context);
  EXPECT_EQ(response.host, nullptr); // 4000 ms > 4e9 - 1 ns: rejected everywhere, waits
  EXPECT_NE(response.cancelable, nullptr);
  EXPECT_EQ(lb_->queueLength(), 1u);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, ChoiceAmongKeptIsLowestCost) {
  setHost(0, 100, 50); // RTT 100 ms, cost 5100
  setHost(1, 400, 1);  // RTT 400 ms, cost 800: both kept with 1 s left, host 1 is cheaper
  auto r = request(1000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&r->context).host, hosts_[1]);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, HostWithoutSampleIsEligible) {
  setHost(0, 0, 3);   // no RTT sample: eligible whatever the deadline
  setHost(1, 400, 0); // RTT 400 ms > 100 ms left
  auto r = request(100 * kMs);
  EXPECT_EQ(lb_->chooseHost(&r->context).host, hosts_[0]);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, TiesBrokenAtRandom) {
  setHost(0, 100, 1);
  setHost(1, 100, 1);
  auto r = request(1000 * kMs);
  EXPECT_CALL(random_, random()).WillOnce(Return(1)).WillOnce(Return(2));
  EXPECT_EQ(lb_->chooseHost(&r->context).host, hosts_[1]);
  EXPECT_EQ(lb_->chooseHost(&r->context).host, hosts_[0]);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, FifoAdmissionScanSendsOldestFirstAndDropsPastDeadline) {
  setHost(0, 10000, 9); // RTT 10 s
  setHost(1, 10000, 9); // RTT 10 s
  Event::MockTimer* timer = expectTimer();
  auto a = request(5000 * kMs);
  auto ra = lb_->chooseHost(&a->context);
  EXPECT_EQ(ra.host, nullptr);
  EXPECT_TRUE(timer->enabled_);

  setHost(1, 2000, 1); // host 1 now has RTT 2 s
  auto b = request(1000 * kMs); // 1 s left: host 1 would not fit anyway, but it must not overtake A
  auto c = request(9000 * kMs); // would fit host 1 now, but the queue is not empty: waits (FIFO)
  EXPECT_EQ(lb_->chooseHost(&b->context).host, nullptr);
  EXPECT_EQ(lb_->chooseHost(&c->context).host, nullptr);
  EXPECT_EQ(lb_->queueLength(), 3u);

  // Scan: A fits host 1 (2 s <= 5 s), B does not (2 s > 1 s) and stays, C fits.
  EXPECT_CALL(a->context, onAsyncHostSelection(_, _))
      .WillOnce([this](Upstream::HostConstSharedPtr&& host, std::string&& details) {
        EXPECT_EQ(host, hosts_[1]);
        EXPECT_EQ(details, "");
      });
  EXPECT_CALL(c->context, onAsyncHostSelection(_, _))
      .WillOnce([this](Upstream::HostConstSharedPtr&& host, std::string&&) { EXPECT_EQ(host, hosts_[1]); });
  EXPECT_CALL(b->context, onAsyncHostSelection(_, _)).Times(0);
  timer->invokeCallback();
  EXPECT_EQ(lb_->queueLength(), 1u);
  EXPECT_TRUE(timer->enabled_); // re-armed: B still waits
  testing::Mock::VerifyAndClearExpectations(&b->context);

  // B's deadline passes: dropped with no host.
  now_ns_ += 1000 * kMs;
  EXPECT_CALL(b->context, onAsyncHostSelection(_, _))
      .WillOnce([](Upstream::HostConstSharedPtr&& host, std::string&& details) {
        EXPECT_EQ(host, nullptr);
        EXPECT_EQ(details, std::string(kDroppedDetails));
      });
  timer->invokeCallback();
  EXPECT_EQ(lb_->queueLength(), 0u);
  EXPECT_FALSE(timer->enabled_);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, AlreadyLateWaitsThenIsDropped) {
  setHost(0, 100, 0);
  setHost(1, 100, 0);
  Event::MockTimer* timer = expectTimer();
  auto r = request(0); // deadline == now: passes nowhere
  EXPECT_EQ(lb_->chooseHost(&r->context).host, nullptr);
  EXPECT_CALL(r->context, onAsyncHostSelection(_, _))
      .WillOnce([](Upstream::HostConstSharedPtr&& host, std::string&&) { EXPECT_EQ(host, nullptr); });
  timer->invokeCallback();
  EXPECT_EQ(lb_->queueLength(), 0u);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, CancelledRequestIsRemovedWithoutCallback) {
  setHost(0, 10000, 9);
  setHost(1, 10000, 9);
  Event::MockTimer* timer = expectTimer();
  auto r = request(5000 * kMs);
  auto response = lb_->chooseHost(&r->context);
  ASSERT_NE(response.cancelable, nullptr);
  response.cancelable->cancel();
  response.cancelable.reset();
  setHost(1, 1000, 0); // would fit now
  EXPECT_CALL(r->context, onAsyncHostSelection(_, _)).Times(0);
  timer->invokeCallback();
  EXPECT_EQ(lb_->queueLength(), 0u);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, RetryIsPickedAtOnceEvenWhileFirstAttemptsWait) {
  setHost(0, 10000, 9); // RTT 10 s
  setHost(1, 10000, 9); // RTT 10 s
  expectTimer();
  auto first = request(5000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&first->context).host, nullptr); // waits in the FIFO
  setHost(1, 2000, 1);                                        // host 1 now has RTT 2 s
  auto retry = request(9000 * kMs, 2);
  auto response = lb_->chooseHost(&retry->context);
  EXPECT_EQ(response.host, hosts_[1]); // not behind the FIFO
  EXPECT_EQ(lb_->queueLength(), 1u);
  EXPECT_EQ(lb_->retryWaiters(), 0u);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, RefusedRetryIsRecheckedAndSentLater) {
  setHost(0, 10000, 9);
  setHost(1, 10000, 9);
  Event::MockTimer* timer = expectTimer();
  auto retry = request(5000 * kMs, 2);
  auto response = lb_->chooseHost(&retry->context);
  EXPECT_EQ(response.host, nullptr);
  EXPECT_NE(response.cancelable, nullptr); // refused: the router waits on it
  EXPECT_EQ(lb_->retryWaiters(), 1u);
  EXPECT_EQ(lb_->queueLength(), 0u); // not a FIFO entry

  EXPECT_CALL(retry->context, onAsyncHostSelection(_, _)).Times(0);
  timer->invokeCallback(); // still nothing fits
  EXPECT_EQ(lb_->retryWaiters(), 1u);
  EXPECT_TRUE(timer->enabled_);
  testing::Mock::VerifyAndClearExpectations(&retry->context);

  setHost(0, 2000, 1); // host 0 now has RTT 2 s
  EXPECT_CALL(retry->context, onAsyncHostSelection(_, _))
      .WillOnce([this](Upstream::HostConstSharedPtr&& host, std::string&&) { EXPECT_EQ(host, hosts_[0]); });
  timer->invokeCallback();
  EXPECT_EQ(lb_->retryWaiters(), 0u);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, RefusedRetryIsNeverDroppedPastItsDeadline) {
  setHost(0, 10000, 9);
  setHost(1, 10000, 9);
  Event::MockTimer* timer = expectTimer();
  auto retry = request(5000 * kMs, 2);
  EXPECT_EQ(lb_->chooseHost(&retry->context).host, nullptr);
  now_ns_ += 6000 * kMs; // past its deadline
  EXPECT_CALL(retry->context, onAsyncHostSelection(_, _)).Times(0); // no 503 for the whole request
  timer->invokeCallback();
  timer->invokeCallback();
  EXPECT_EQ(lb_->retryWaiters(), 1u);
  EXPECT_TRUE(timer->enabled_);
}

// Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
TEST_F(PeakEwmaDeadlineTest, CancelledRetryIsRemovedWithoutCallback) {
  setHost(0, 10000, 9);
  setHost(1, 10000, 9);
  Event::MockTimer* timer = expectTimer();
  auto retry = request(5000 * kMs, 2);
  auto response = lb_->chooseHost(&retry->context);
  ASSERT_NE(response.cancelable, nullptr);
  response.cancelable->cancel(); // the earlier attempt answered: the router ends the request
  response.cancelable.reset();
  setHost(1, 1000, 0);
  EXPECT_CALL(retry->context, onAsyncHostSelection(_, _)).Times(0);
  timer->invokeCallback();
  EXPECT_EQ(lb_->retryWaiters(), 0u);
  EXPECT_FALSE(timer->enabled_);
}

// ProvisionalCurrentRTT: a host's oldest pending request counts as answered now (deadline_lb.h).
// Design: abstraction/ingress/load_balancer/design.md#provisionalcurrentrtt
// Design: abstraction/ingress/load_balancer/design.md#worked-examples
TEST_F(PeakEwmaDeadlineTest, StalledHostIsRejectedByProvisionalRtt) {
  setHost(0, 400, 0);
  setHost(1, 400, 0);
  auto a = request(17000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&a->context).host, hosts_[0]); // tie, random 0: host 0; A stays pending there
  advance(10000);                                           // 10 s: host 0 never answers
  setHost(1, 400, 0);                                       // host 1 keeps answering (sampled now)
  // Host 0: L = 10000 ms, alpha = 1 - e^-1 = 0.63212: 0.36788 * 400 + 0.63212 * 10000 = 6468 ms.
  auto b = request(5000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&b->context).host, hosts_[1]); // 6468 ms > 5 s left: host 0 rejected
  auto c = request(7000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&c->context).host, hosts_[0]); // 6468 ms <= 7 s: kept (tie, random 0: host 0)
}

// Design: abstraction/ingress/load_balancer/design.md#provisionalcurrentrtt
TEST_F(PeakEwmaDeadlineTest, FinishedRequestIsNoLongerPending) {
  setHost(0, 400, 0);
  setHost(1, 400, 0);
  auto a = request(17000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&a->context).host, hosts_[0]);
  advance(10000);
  setHost(1, 400, 0);
  a.reset(); // A's stream ended: its pending entry is removed with its filter state
  auto b = request(5000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&b->context).host, hosts_[0]); // nothing pending: provisional = 400 ms, kept
}

// Design: abstraction/ingress/load_balancer/design.md#provisionalcurrentrtt
TEST_F(PeakEwmaDeadlineTest, HostWithoutSampleButPendingUsesThePendingLatency) {
  setHost(0, 0, 0);   // no RTT sample yet: cost 10 (default RTT), the cheaper host
  setHost(1, 400, 0); // cost 400
  auto a = request(17000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&a->context).host, hosts_[0]);
  advance(10000);
  setHost(1, 400, 0);
  auto b = request(5000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&b->context).host, hosts_[1]); // host 0: provisional = L = 10 s > 5 s
  auto c = request(11000 * kMs);
  EXPECT_EQ(lb_->chooseHost(&c->context).host, hosts_[0]); // 10 s <= 11 s: kept, and still the cheaper host
}

// RetryElsewhere: a retry never goes back to a backend the router's previous_hosts predicate excludes.
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
TEST_F(PeakEwmaDeadlineTest, RetryAvoidsExcludedBackend) {
  setHost(0, 400, 0);
  setHost(1, 400, 0); // tie: random 0 would pick host 0
  auto retry = request(17000 * kMs, 2);
  ON_CALL(retry->context, shouldSelectAnotherHost(_))
      .WillByDefault([this](const Upstream::Host& host) { return &host == hosts_[0].get(); });
  EXPECT_EQ(lb_->chooseHost(&retry->context).host, hosts_[1]);
}

// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
TEST_F(PeakEwmaDeadlineTest, AllBackendsExcludedFallsBackToAll) {
  setHost(0, 400, 0);
  setHost(1, 400, 0);
  auto retry = request(17000 * kMs, 2);
  ON_CALL(retry->context, shouldSelectAnotherHost(_)).WillByDefault(Return(true));
  EXPECT_EQ(lb_->chooseHost(&retry->context).host, hosts_[0]); // nothing left: exclusion ignored
}

} // namespace
} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
