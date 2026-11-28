// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.27.0)
// impl/envoy (cancellation testbed): tests of the TokenBucketFilter's TokenBucket and TokenRate (token_bucket.h),
// the spec's worked examples.

#include <cstdint>

#include "contrib/peak_ewma/load_balancing_policies/source/token_bucket.h"
#include "gtest/gtest.h"

namespace Envoy {
namespace Extensions {
namespace LoadBalancingPolicies {
namespace PeakEwma {
namespace {

constexpr int64_t kMs = 1'000'000;
constexpr int64_t kS = 1'000'000'000;

TokenParams params() {
  TokenParams p;
  p.slo_ns = 4000 * kMs;
  p.init_rate = 50;
  p.max_tokens = 10;
  p.mult_dec = 2;
  p.add_inc_limit = 2;
  p.add_inc = 0.1 * 50;
  p.r_min = 0.1 * 50;
  return p;
}

// Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
TEST(TokenBucketTest, RefillWorkedExample) {
  TokenBucket b;
  EXPECT_FALSE(b.hasToken(0)); // not initialized: no token
  b.init(params(), 0);
  EXPECT_FALSE(b.hasToken(0));             // starts empty
  EXPECT_DOUBLE_EQ(b.tokens(30 * kMs), 1.5); // 0 + 50 x 0.030
  EXPECT_TRUE(b.take(30 * kMs));           // leaves 0.5
  EXPECT_FALSE(b.take(30 * kMs));          // 0.5 < 1
  EXPECT_TRUE(b.hasToken(40 * kMs));       // 0.5 + 50 x 0.010 = 1.0
  EXPECT_TRUE(b.take(40 * kMs));
  EXPECT_DOUBLE_EQ(b.tokens(340 * kMs), 10.0); // min(10, 0 + 15): the cap
  EXPECT_DOUBLE_EQ(b.tokens(300 * kMs), 10.0); // an earlier clock reading: no time passes
}

// Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
TEST(TokenBucketTest, InitOnlyOnce) {
  TokenBucket b;
  b.init(params(), 0);
  TokenParams other = params();
  other.init_rate = 999;
  b.init(other, 5 * kS); // a later balancer: ignored
  EXPECT_DOUBLE_EQ(b.rate(), 50.0);
  EXPECT_DOUBLE_EQ(b.tokens(100 * kMs), 5.0); // refilled from 0 s, not from 5 s
}

// Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
TEST(TokenBucketTest, RateWorkedExample) {
  TokenBucket b;
  EXPECT_FALSE(b.rateSample(4500 * kMs, 0).has_value()); // not initialized
  b.init(params(), 0);
  struct Step {
    int64_t at_ns;
    int64_t latency_ms;
    double rate_after;
    const char* event;
  };
  const Step steps[] = {
      {0, 4500, 25, "slow"},         // no earlier halving
      {1 * kS, 4500, 25, "slow_held"}, // 1 s < 4 s since the last halving
      {4 * kS, 4500, 12.5, "slow"},  // 4 s >= 4 s
      {8 * kS, 4500, 6.25, "slow"},
      {12 * kS, 4500, 5, "slow"},    // max(5, 3.125): the floor
      {13 * kS, 1500, 10, "fast"},      // 1500 < 2000, no earlier increase
      {14 * kS, 1500, 10, "fast_held"}, // 1 s < 4 s since the last increase
      {15 * kS, 2000, 10, "mid"},       // not < 2000
      {16 * kS, 4000, 10, "mid"},       // not > 4000
      {17 * kS, 1500, 15, "fast"},      // 17 - 13 = 4 s >= 4 s
  };
  for (const Step& s : steps) {
    const auto change = b.rateSample(s.latency_ms * kMs, s.at_ns);
    ASSERT_TRUE(change.has_value());
    EXPECT_DOUBLE_EQ(change->rate_after, s.rate_after) << s.at_ns;
    EXPECT_STREQ(change->event, s.event) << s.at_ns;
    EXPECT_DOUBLE_EQ(b.rate(), s.rate_after);
  }
}

// Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
TEST(TokenBucketTest, StuckAttemptHalvesOncePerWindow) {
  TokenBucket b;
  EXPECT_FALSE(b.stuck(0).has_value()); // not initialized
  b.init(params(), 0);
  b.rateSample(4500 * kMs, 12 * kS); // last halving at 12 s: 50 -> 25
  const auto at18 = b.stuck(18 * kS); // 18 - 12 = 6 s >= 4 s
  ASSERT_TRUE(at18.has_value());
  EXPECT_STREQ(at18->event, "stuck");
  EXPECT_DOUBLE_EQ(at18->rate_before, 25.0);
  EXPECT_DOUBLE_EQ(at18->rate_after, 12.5);
  EXPECT_FALSE(b.stuck(19 * kS).has_value()); // 1 s < 4 s: held
  EXPECT_DOUBLE_EQ(b.rate(), 12.5);
  for (int64_t t = 22; t <= 34; t += 4) {
    b.stuck(t * kS); // 6.25, then the floor 5
  }
  EXPECT_DOUBLE_EQ(b.rate(), 5.0);
}

// Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
TEST(TokenBucketTest, RateChangeAppliesFromNowOn) {
  TokenBucket b;
  b.init(params(), 0);
  b.rateSample(4500 * kMs, 100 * kMs); // refills 5 tokens at 50/s first, then the rate becomes 25
  EXPECT_DOUBLE_EQ(b.tokens(100 * kMs), 5.0);
  EXPECT_DOUBLE_EQ(b.tokens(200 * kMs), 7.5); // 5 + 25 x 0.1
}

} // namespace
} // namespace PeakEwma
} // namespace LoadBalancingPolicies
} // namespace Extensions
} // namespace Envoy
