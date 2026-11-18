# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/retry_timeout/design.md (retry_timeout v0.1.0)
"""Run from the repository root:
python3 -B -m unittest discover -s implementation/scripts/tests -t implementation/scripts"""

import contextlib
import io
import math
import unittest

import bench
from benchrunner import retry_timeout as rt


class RetryTimeoutTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/retry_timeout/design.md#worked-examples
    def test_worked_examples_k20(self):
        want = {60: (0.024134, 200.603, 183.945), 70: (0.093561, 203.119, 244.494),
                80: (0.256078, 212.804, 338.171), 90: (0.550769, 255.077, 543.521)}
        for rate, (c, mean, timeout) in want.items():
            r = rt.retry_timeout(20, 200.0, float(rate))
            self.assertAlmostEqual(r.rho, rate / 100)
            self.assertAlmostEqual(r.erlang_c, c, places=6)
            self.assertAlmostEqual(r.mean_sojourn_ms, mean, delta=0.0005)
            self.assertAlmostEqual(r.retry_timeout_ms, timeout, delta=0.0005)
            # the defining equation holds at the result
            self.assertAlmostEqual(rt.sojourn_tail(r.retry_timeout_ms / 1000, 20, 5.0, rate), 1 - rate / 100, places=9)

    # Design: abstraction/benchmark_runner/retry_timeout/design.md#sojourntail
    def test_mm1_closed_form(self):
        r = rt.retry_timeout(1, 200.0, 4.0)
        self.assertAlmostEqual(r.erlang_c, 0.8)
        self.assertAlmostEqual(r.mean_sojourn_ms, 1000.0, places=6)
        self.assertAlmostEqual(r.retry_timeout_ms, -math.log(0.2) * 1000, places=6)  # 1609.438

    # Design: abstraction/benchmark_runner/retry_timeout/design.md#sojourntail
    def test_theta_equals_mu_branch_is_continuous(self):
        at = rt.retry_timeout(10, 200.0, 45.0)  # theta = 50 - 45 = 5 = mu
        self.assertAlmostEqual(at.erlang_c, 0.668732, places=6)
        self.assertAlmostEqual(at.mean_sojourn_ms, 333.746, delta=0.0005)
        self.assertAlmostEqual(at.retry_timeout_ms, 702.181, delta=0.0005)
        for rate in (45 - 1e-7, 45 + 1e-7):
            self.assertAlmostEqual(rt.retry_timeout(10, 200.0, rate).retry_timeout_ms, at.retry_timeout_ms, delta=0.001)

    # Design: abstraction/benchmark_runner/retry_timeout/design.md#timeoutsolve
    def test_low_load_goes_to_zero_and_tail_decreases(self):
        self.assertLess(rt.retry_timeout(1, 1.0, 1e-6).retry_timeout_ms, 1e-3)
        ts = [rt.sojourn_tail(t / 10, 20, 5.0, 90.0) for t in range(0, 50)]
        self.assertAlmostEqual(ts[0], 1.0, places=12)
        self.assertTrue(all(a > b for a, b in zip(ts, ts[1:])))

    # Design: abstraction/benchmark_runner/retry_timeout/design.md#erlangc
    def test_erlang_c_large_k_stays_finite(self):
        c = rt.erlang_c(5000, 4900.0)
        self.assertTrue(0 < c < 1)

    # Design: abstraction/benchmark_runner/retry_timeout/design.md#validation
    def test_validation(self):
        cases = [((0, 200.0, 60.0), "workers must be an integer >= 1"),
                 ((True, 200.0, 60.0), "workers must be an integer >= 1"),
                 ((2.0, 200.0, 60.0), "workers must be an integer >= 1"),
                 ((20, 0.0, 60.0), "mean_service_ms must be a number > 0"),
                 ((20, 200.0, -1.0), "rate must be a number > 0"),
                 ((20, 200.0, 100.0), "rate 100 req/s needs rho < 1: capacity is 100 req/s"),
                 ((20, 200.0, 150.5), "rate 150.5 req/s needs rho < 1: capacity is 100 req/s")]
        for args, msg in cases:
            with self.assertRaises(ValueError) as cm:
                rt.retry_timeout(*args)
            self.assertEqual(str(cm.exception), msg)

    # Design: abstraction/benchmark_runner/retry_timeout/design.md#output-format
    def test_cli_output(self):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            status = bench.main(["retry-timeout", "--workers", "20", "--mean_service_ms", "200", "60", "70", "80", "90"])
        self.assertEqual(status, 0)
        self.assertEqual(out.getvalue(),
                         "rate_per_s\trho\terlang_c\tmean_sojourn_ms\tretry_timeout_ms\n"
                         "60\t0.6000\t0.024134\t200.603\t183.945\n"
                         "70\t0.7000\t0.093561\t203.119\t244.494\n"
                         "80\t0.8000\t0.256078\t212.804\t338.171\n"
                         "90\t0.9000\t0.550769\t255.077\t543.521\n"
                         "retry_timeout_ms = [183.945, 244.494, 338.171, 543.521]\n")
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            status = bench.main(["retry-timeout", "--workers", "20", "--mean_service_ms", "200", "60", "100"])
        self.assertEqual((status, out.getvalue()), (2, ""))
        self.assertIn("error: rate 100 req/s needs rho < 1", err.getvalue())


if __name__ == "__main__":
    unittest.main()
