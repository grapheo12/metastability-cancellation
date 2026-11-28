# Source: abstraction/load_generator/run_controller/design.md (run_controller v0.9.0)
"""Tests of the impl/envoy LoadGenerator wrapper (bin/loadgen.py): rate_schedule validation."""
import io
import os
import sys
import unittest
from contextlib import redirect_stderr

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "bin"))
import loadgen  # noqa: E402

BASE = ["--num_clients", "4", "--global_rate", "120", "--request_bytes", "128", "--slo_ms", "4000",
        "--duration_s", "120", "--max_retries", "0", "--slo_cancel", "false", "--arrival_rng", "native"]


def error_of(argv):
    """The validation error printed for argv (exit status 2), or None if it validates."""
    err = io.StringIO()
    try:
        with redirect_stderr(err):
            loadgen.parse(argv)
    except SystemExit as e:
        assert e.code == 2, e.code
        return err.getvalue().strip()
    return None


class RateScheduleFlag(unittest.TestCase):
    # Design: abstraction/load_generator/run_controller/design.md#configuration-validation
    def test_parse(self):
        self.assertIsNone(loadgen.parse(BASE).rate_steps)
        args = loadgen.parse(BASE + ["--rate_schedule", "0:200,30:120"])
        self.assertEqual(args.rate_steps, [(0, 50.0), (30_000_000_000, 30.0)])
        for bad in ["5:200", "0:200,30:0", "0:200,30:120,30:100", "0:200;30:120", "0:200,x:1", "0:inf"]:
            self.assertEqual(error_of(BASE + ["--rate_schedule", bad]), loadgen.RATE_SCHEDULE_ERROR, bad)


if __name__ == "__main__":
    unittest.main()
