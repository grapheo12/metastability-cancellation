# Source: abstraction/ingress/design.md (ingress v0.19.1)
# Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.24.0)
"""Tests of the impl/envoy Ingress wrapper (bin/ingress.py): validation and the generated Envoy config."""
import io
import os
import sys
import unittest
from contextlib import redirect_stderr

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "bin"))
import ingress  # noqa: E402

BASE = ["--backends", "10.0.0.1:8080,10.0.0.2:8080", "--num_clients", "4", "--lb_policy", "peak_ewma",
        "--forward_mode", "hedge"]


def error_of(argv):
    """The validation error printed for argv (exit status 2), or None if it validates."""
    err = io.StringIO()
    try:
        with redirect_stderr(err):
            ingress.parse(argv)
    except SystemExit as e:
        assert e.code == 2, e.code
        return err.getvalue().strip()
    return None


class TokenBucketFlags(unittest.TestCase):
    # Design: abstraction/ingress/design.md#configuration-validation
    def test_validation(self):
        self.assertIsNone(error_of(BASE + ["--lb_filter", "token_bucket", "--lb_tb_slo_ms", "4000"]))
        self.assertEqual(error_of(BASE + ["--lb_filter", "token_bucket"]),
                         "lb_tb_slo_ms is required with lb_filter token_bucket")
        self.assertEqual(error_of(BASE[:4] + ["--lb_policy", "ewma", "--lb_filter", "token_bucket"]),
                         "lb_filter token_bucket needs lb_policy peak_ewma")
        for flag, bad, msg in [("--lb_tb_init_rate", "0", "lb_tb_init_rate must be a finite number > 0"),
                               ("--lb_tb_max_tokens", "0.5", "lb_tb_max_tokens must be a finite number >= 1"),
                               ("--lb_tb_mult_dec", "1", "lb_tb_mult_dec must be a finite number > 1"),
                               ("--lb_tb_add_inc_limit", "nan", "lb_tb_add_inc_limit must be a finite number >= 1"),
                               ("--lb_tb_add_inc_frac", "-1", "lb_tb_add_inc_frac must be a finite number >= 0"),
                               ("--lb_tb_min_rate_frac", "1.5", "lb_tb_min_rate_frac must be a number in (0, 1]"),
                               ("--lb_tb_slo_ms", "inf", "lb_tb_slo_ms must be a finite number > 0")]:
            self.assertEqual(error_of(BASE + [flag, bad]), msg, flag)  # validated even without token_bucket

    # Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
    def test_envoy_config(self):
        args = ingress.parse(BASE + ["--lb_filter", "token_bucket", "--lb_tb_slo_ms", "4000", "--lb_tb_init_rate",
                                     "100", "--lb_retry_policy", "anxious", "--lb_max_retries", "1"])
        cluster = ingress.envoy_config(args, args.backends.split(","), 9901, "/dev/null")["static_resources"][
            "clusters"][0]
        tc = cluster["load_balancing_policy"]["policies"][0]["typed_extension_config"]["typed_config"]
        self.assertEqual(tc["token_bucket"], {"slo": "4.000000s", "init_rate": 100.0, "max_tokens": 10.0,
                                              "mult_dec": 2.0, "add_inc_limit": 2.0, "add_inc_frac": 0.1,
                                              "min_rate_frac": 0.1})
        names = [f["name"] for f in cluster["typed_extension_protocol_options"][
            "envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]["http_filters"]]
        self.assertEqual(names, ["envoy.filters.http.testbed_token_bucket", "envoy.filters.http.testbed_anxious",
                                 "envoy.filters.http.upstream_codec"])
        plain = ingress.parse(BASE + ["--lb_filter", "deadline_provisional_rtt"])
        c2 = ingress.envoy_config(plain, plain.backends.split(","), 9901, "/dev/null")["static_resources"]["clusters"][0]
        self.assertNotIn("token_bucket", c2["load_balancing_policy"]["policies"][0]["typed_extension_config"][
            "typed_config"])
        self.assertNotIn("http_filters", c2["typed_extension_protocol_options"][
            "envoy.extensions.upstreams.http.v3.HttpProtocolOptions"])


if __name__ == "__main__":
    unittest.main()
