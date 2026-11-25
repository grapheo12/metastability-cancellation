// impl/envoy (cancellation testbed): tests of the peak_ewma_deadline load balancer (deadline_lb.h): the deadline
// filter over every host, the lowest-cost choice, and the per-worker FIFO wait queue.

#include <chrono>
#include <memory>
#include <string>

#include "source/common/network/address_impl.h"

#include "test/common/stats/stat_test_utility.h"
#include "test/mocks/common.h"
#include "test/mocks/event/mocks.h"
#include "test/mocks/network/connection.h"
#include "test/mocks/runtime/mocks.h"
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
    ON_CALL(time_source_, monotonicTime())
        .WillByDefault(Return(MonotonicTime(std::chrono::milliseconds(1234567890))));
    ON_CALL(time_source_, systemTime()).WillByDefault([this]() {
      return SystemTime(std::chrono::duration_cast<SystemTime::duration>(std::chrono::nanoseconds(now_ns_)));
    });
    lb_ = std::make_unique<PeakEwmaDeadlineLoadBalancer>(priority_set_, nullptr, *stats_, runtime_, random_,
                                                         50, *cluster_info_, time_source_, config_);
  }

  // Host i's EWMA RTT (0: no sample) and active requests: cost = ewma_ms * (active + 1).
  void setHost(size_t i, double ewma_ms, uint64_t active) {
    auto data = hosts_[i]->typedLbPolicyData<PeakEwmaHostLbPolicyData>();
    ASSERT_TRUE(data.has_value());
    data->updateEwma(ewma_ms, 1);
    hosts_[i]->stats().rq_active_.set(active);
  }

  // A request with deadline now + remaining_ns (no header if remaining_ns is nullopt).
  std::unique_ptr<Request> request(absl::optional<int64_t> remaining_ns) {
    auto r = std::make_unique<Request>();
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
  Upstream::HostVector hosts_;
  DeadlineConfig config_;
  std::unique_ptr<PeakEwmaDeadlineLoadBalancer> lb_;
};

TEST_F(PeakEwmaDeadlineTest, NoDeadlineHeaderTakesLowestCostOverAllHosts) {
  setHost(0, 100, 5); // cost 600
  setHost(1, 100, 1); // cost 200
  auto r = request(absl::nullopt);
  auto response = lb_->chooseHost(&r->context);
  EXPECT_EQ(response.host, hosts_[1]);
  EXPECT_EQ(response.cancelable, nullptr);
  EXPECT_EQ(lb_->queueLength(), 0u);
}

TEST_F(PeakEwmaDeadlineTest, ExactFitPassesAndOneNanosecondLessWaits) {
  setHost(0, 400, 9);  // cost 4000 ms
  setHost(1, 400, 19); // cost 8000 ms
  auto fits = request(4000 * kMs);
  auto response = lb_->chooseHost(&fits->context);
  EXPECT_EQ(response.host, hosts_[0]); // 4000 ms * 1e6 <= 4e9 ns: exactly fits

  expectTimer();
  auto tight = request(4000 * kMs - 1);
  response = lb_->chooseHost(&tight->context);
  EXPECT_EQ(response.host, nullptr);
  EXPECT_NE(response.cancelable, nullptr); // waits
  EXPECT_EQ(lb_->queueLength(), 1u);
}

TEST_F(PeakEwmaDeadlineTest, HostWithoutSampleIsEligible) {
  setHost(0, 0, 3);   // no RTT sample: eligible whatever the deadline
  setHost(1, 400, 0); // cost 400 ms > 100 ms left
  auto r = request(100 * kMs);
  EXPECT_EQ(lb_->chooseHost(&r->context).host, hosts_[0]);
}

TEST_F(PeakEwmaDeadlineTest, TiesBrokenAtRandom) {
  setHost(0, 100, 1);
  setHost(1, 100, 1);
  auto r = request(1000 * kMs);
  EXPECT_CALL(random_, random()).WillOnce(Return(1)).WillOnce(Return(2));
  EXPECT_EQ(lb_->chooseHost(&r->context).host, hosts_[1]);
  EXPECT_EQ(lb_->chooseHost(&r->context).host, hosts_[0]);
}

TEST_F(PeakEwmaDeadlineTest, FifoAdmissionScanSendsOldestFirstAndDropsPastDeadline) {
  setHost(0, 1000, 9); // cost 10 s
  setHost(1, 1000, 9); // cost 10 s
  Event::MockTimer* timer = expectTimer();
  auto a = request(5000 * kMs);
  auto ra = lb_->chooseHost(&a->context);
  EXPECT_EQ(ra.host, nullptr);
  EXPECT_TRUE(timer->enabled_);

  setHost(1, 1000, 1); // host 1 now costs 2 s
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

TEST_F(PeakEwmaDeadlineTest, CancelledRequestIsRemovedWithoutCallback) {
  setHost(0, 1000, 9);
  setHost(1, 1000, 9);
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

} // namespace
} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
