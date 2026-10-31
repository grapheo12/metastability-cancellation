# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/config/design.md (config v0.1.0)
"""Run from the repository root:
python3 -B -m unittest discover -s implementation/scripts/tests -t implementation/scripts"""

import os
import tempfile
import unittest

from benchrunner import config as cfg

BASE = """
[repo]
url = "file:///tmp/repo"
[machines.server]
host = "local"
[machines.client]
host = "local"
[server]
response_rate = 2000
cancel_mode = ["ignore", "cooperative"]
[load_gen]
num_clients = 4
global_rate = [1000, 2000]
slo_ms = 5
duration_s = 2
[canceller]
cancel_rate = 100
"""


class ConfigTest(unittest.TestCase):
    def load(self, text):
        with tempfile.NamedTemporaryFile("w", suffix=".toml", delete=False) as f:
            f.write(text)
        try:
            return cfg.load_config(f.name, "/runner")
        finally:
            os.unlink(f.name)

    def assertConfigError(self, text, fragment):
        with self.assertRaises(cfg.ConfigError) as cm:
            self.load(text)
        self.assertIn(fragment, str(cm.exception))

    # Design: abstraction/benchmark_runner/config/design.md#sweepexpansion
    def test_worked_example_order(self):
        points = cfg.expand_sweep(self.load(BASE))
        got = [(p.swept["server.cancel_mode"], p.swept["load_gen.global_rate"]) for p in points]
        self.assertEqual(got, [("ignore", 1000), ("ignore", 2000), ("cooperative", 1000), ("cooperative", 2000)])
        self.assertEqual([p.id for p in points], [0, 1, 2, 3])
        p = points[3]
        self.assertEqual(p.server_args, [("response_rate", 2000), ("cancel_mode", "cooperative")])
        self.assertEqual(dict(p.canceller_args), {"cancel_rate": 100, "num_clients": 4, "duration_s": 2})

    # Design: abstraction/benchmark_runner/config/design.md#sweepexpansion
    def test_sweep_order_and_disabled_canceller(self):
        c = self.load(BASE.replace("cancel_rate = 100", "enabled = false\ncancel_rate = 100") +
                      '[run]\nsweep_order = ["load_gen.global_rate", "server.cancel_mode"]\n')
        points = cfg.expand_sweep(c)
        got = [(p.swept["load_gen.global_rate"], p.swept["server.cancel_mode"]) for p in points]
        self.assertEqual(got, [(1000, "ignore"), (1000, "cooperative"), (2000, "ignore"), (2000, "cooperative")])
        self.assertIsNone(points[0].canceller_args)

    def test_no_sweep_is_one_point(self):
        text = BASE.replace('["ignore", "cooperative"]', '"ignore"').replace("[1000, 2000]", "1000")
        points = cfg.expand_sweep(self.load(text))
        self.assertEqual(len(points), 1)
        self.assertEqual(points[0].swept, {})

    # Design: abstraction/benchmark_runner/config/design.md#benchconfig
    def test_validation_errors(self):
        self.assertConfigError(BASE + "[server2]\n", "unknown table")
        self.assertConfigError(BASE.replace("response_rate", "listen_addr"), "set by the runner")
        self.assertConfigError(BASE.replace("global_rate", "global_load"), "unknown key load_gen.global_load")
        self.assertConfigError(BASE.replace("response_rate = 2000", "listen_port = [1, 2]"), "cannot be swept")
        self.assertConfigError(BASE.replace("slo_ms = 5", ""), "load_gen.slo_ms is required")
        self.assertConfigError(BASE.replace("global_rate = [1000, 2000]", "global_rate = []"), "non-empty array")
        self.assertConfigError(BASE + '[run]\nsweep_order = ["server.cancel_mode"]\n', "exactly once")
        self.assertConfigError(BASE.replace('host = "local"\n[machines.client]', 'host = "h"\n[machines.client]', 1),
                               "machines.server.user is required")

    def test_defaults(self):
        c = self.load(BASE)
        self.assertEqual(c.listen_port, 8080)
        self.assertEqual(c.machines["server"].data_addr, "127.0.0.1")
        self.assertEqual(c.machines["client"].workdir, "~/cancellation-bench")
        self.assertTrue(c.run.output_dir.startswith("/runner/results/"))
        self.assertEqual((c.run.cooldown_s, c.run.on_failure), (2.0, "continue"))

    # Design: abstraction/benchmark_runner/config/design.md#argument-formatting
    def test_format_arg(self):
        self.assertEqual([cfg.format_arg(v) for v in (True, 8, 0.5, 2000.0, "ignore")],
                         ["true", "8", "0.5", "2000.0", "ignore"])


if __name__ == "__main__":
    unittest.main()
