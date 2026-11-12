# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/config/design.md (config v0.11.0)
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
[ingress]
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
        self.assertEqual(dict(p.ingress_args), {"cancel_rate": 100, "backends": "127.0.0.1:8080", "num_clients": 4,
                                                "duration_s": 2})
        self.assertEqual(p.server_instances, 1)

    # Design: abstraction/benchmark_runner/config/design.md#sweepexpansion
    def test_sweep_order(self):
        c = self.load(BASE + '[run]\nsweep_order = ["load_gen.global_rate", "server.cancel_mode"]\n')
        points = cfg.expand_sweep(c)
        got = [(p.swept["load_gen.global_rate"], p.swept["server.cancel_mode"]) for p in points]
        self.assertEqual(got, [(1000, "ignore"), (1000, "cooperative"), (2000, "ignore"), (2000, "cooperative")])

    # Design: abstraction/benchmark_runner/config/design.md#backends
    def test_instances_and_backends(self):
        text = BASE.replace("response_rate = 2000", "response_rate = 2000\ninstances = 2\nlisten_port = 8080") \
            .replace('[machines.server]\nhost = "local"', '[machines.server]\nhost = "local"\ndata_addr = "172.31.23.63"')
        p = cfg.expand_sweep(self.load(text))[0]
        self.assertEqual(dict(p.ingress_args)["backends"], "172.31.23.63:8080,172.31.23.63:8081")
        self.assertEqual(p.server_instances, 2)
        self.assertEqual(p.to_json()["server_instances"], 2)
        self.assertNotIn("instances", dict(p.server_args))
        self.assertConfigError(BASE.replace("response_rate = 2000", "instances = 0"), "server.instances must be")
        self.assertConfigError(BASE.replace("response_rate = 2000", "instances = [1, 2]"), "cannot be swept")
        self.assertConfigError(BASE.replace("response_rate = 2000", "listen_port = 65535\ninstances = 2"), "<= 65535")
        self.assertConfigError(BASE.replace("[ingress]", "[canceller]"), "unknown table [canceller]")
        self.assertConfigError(BASE.replace("cancel_rate = 100", "backends = \"x:1\""), "set by the runner")

    # Design: abstraction/benchmark_runner/config/design.md#ingressplacement
    def test_ingress_placement(self):
        srv = BASE.replace("response_rate = 2000", "response_rate = 2000\ninstances = 2") \
            .replace('[machines.server]\nhost = "local"', '[machines.server]\nhost = "local"\ndata_addr = "172.31.23.63"')
        p = cfg.expand_sweep(self.load(srv))[0]
        self.assertEqual((p.ingress_machine, p.loadgen_target), ("client", "127.0.0.1:9090"))
        p = cfg.expand_sweep(self.load(srv.replace("[ingress]", '[ingress]\nmachine = "server"')))[0]
        self.assertEqual((p.ingress_machine, p.loadgen_target), ("server", "172.31.23.63:9090"))
        self.assertEqual(dict(p.ingress_args)["backends"], "127.0.0.1:8080,127.0.0.1:8081")
        self.assertEqual(p.to_json()["ingress_machine"], "server")
        self.assertConfigError(srv.replace("[ingress]", '[ingress]\nmachine = "both"'), 'must be "client" or "server"')
        self.assertConfigError(srv.replace("[ingress]", '[ingress]\nmachine = "server"\nlisten_port = 8081'),
                               "collides with a server instance port")
        self.load(srv.replace("[ingress]", '[ingress]\nlisten_port = 8081'))  # no collision across machines

    def test_no_sweep_is_one_point(self):
        text = BASE.replace('["ignore", "cooperative"]', '"ignore"').replace("[1000, 2000]", "1000")
        points = cfg.expand_sweep(self.load(text))
        self.assertEqual(len(points), 1)
        self.assertEqual(points[0].swept, {})

    # Design: abstraction/benchmark_runner/config/design.md#benchconfig
    def test_validation_errors(self):
        self.assertConfigError(BASE + "[server2]\n", "unknown table")
        self.assertConfigError(BASE.replace("response_rate", "listen_addr"), "set by the runner")
        self.assertConfigError(BASE.replace("num_clients = 4", 'num_clients = 4').replace(
            "response_rate = 2000", 'response_rate = 2000\nqueue_log_path = "x.csv"'), "server.queue_log_path is set by the runner")
        swept = cfg.expand_sweep(self.load(BASE.replace("response_rate = 2000",
                                                        "response_rate = 2000\nqueue_sample_interval_ms = [5, 50]")))
        self.assertEqual(sorted({dict(p.server_args)["queue_sample_interval_ms"] for p in swept}), [5, 50])
        self.assertConfigError(BASE.replace("global_rate", "global_load"), "unknown key load_gen.global_load")
        self.assertConfigError(BASE.replace("response_rate = 2000", "listen_port = [1, 2]"), "cannot be swept")
        self.assertConfigError(BASE.replace("slo_ms = 5", ""), "load_gen.slo_ms is required")
        self.assertConfigError(BASE.replace("global_rate = [1000, 2000]", "global_rate = []"), "non-empty array")
        self.assertConfigError(BASE + '[run]\nsweep_order = ["server.cancel_mode"]\n', "exactly once")
        self.assertConfigError(BASE.replace('host = "local"\n[machines.client]', 'host = "h"\nuser = ""\n[machines.client]', 1),
                               "machines.server.user must be a non-empty string")
        self.assertConfigError(BASE.replace('host = "local"\n[machines.client]', 'host = "h"\nssh_port = 0\n[machines.client]', 1),
                               "ssh_port must be an integer")

    # Design: abstraction/benchmark_runner/config/design.md#benchconfig
    def test_ssh_config_alias_machines(self):
        from unittest import mock
        text = BASE.replace('[machines.server]\nhost = "local"', '[machines.server]\nhost = "metafail-exp-1"') \
            .replace('[machines.client]\nhost = "local"', '[machines.client]\nhost = "metafail-exp-2"')
        with mock.patch.object(cfg.transport, "ssh_hostname", return_value="18.144.18.190") as resolve:
            c = self.load(text)
        resolve.assert_called_once_with("metafail-exp-1")
        s = c.machines["server"]
        self.assertEqual((s.user, s.ssh_key, s.ssh_port, s.data_addr), (None, None, None, "18.144.18.190"))
        with mock.patch.object(cfg.transport, "ssh_hostname") as resolve:
            c = self.load(text.replace('host = "metafail-exp-1"', 'host = "metafail-exp-1"\ndata_addr = "172.31.23.63"'))
        resolve.assert_not_called()
        self.assertEqual(c.machines["server"].data_addr, "172.31.23.63")

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

    # Design: abstraction/benchmark_runner/config/design.md#cancelratefraction
    def test_cancel_rate_fraction_worked_example(self):
        text = BASE.replace("[1000, 2000]", "[10, 700]").replace('["ignore", "cooperative"]', '"ignore"') \
            .replace("cancel_rate = 100", "cancel_rate_fraction = [0.01, 0.75]")
        points = cfg.expand_sweep(self.load(text))
        got = [(p.swept["load_gen.global_rate"], p.swept["ingress.cancel_rate_fraction"],
                cfg.format_arg(dict(p.ingress_args)["cancel_rate"])) for p in points]
        self.assertEqual(got, [(10, 0.01, "0.1"), (10, 0.75, "7.5"), (700, 0.01, "7.0"), (700, 0.75, "525.0")])
        self.assertEqual([k for k, _ in points[0].ingress_args], ["backends", "num_clients", "duration_s", "cancel_rate"])
        self.assertNotIn("cancel_rate_fraction", dict(points[0].ingress_args))
        for rate, frac, want in [(3, 0.1, "0.30000000000000004"), (1000, 1.0, "1000.0"), (10, 2.5, "25.0")]:
            self.assertEqual(cfg.format_arg(cfg.derived_cancel_rate(rate, frac)), want)

    # Design: abstraction/benchmark_runner/config/design.md#cancelratefraction
    def test_cancel_rate_fraction_sweep_order_and_errors(self):
        text = BASE.replace("cancel_rate = 100", "cancel_rate_fraction = [0.1, 0.2]")
        c = self.load(text + '[run]\nsweep_order = ["load_gen.global_rate", "ingress.cancel_rate_fraction", '
                             '"server.cancel_mode"]\n')
        self.assertEqual(len(cfg.expand_sweep(c)), 8)
        self.assertEqual(self.load(text).swept_keys(),
                         ["server.cancel_mode", "load_gen.global_rate", "ingress.cancel_rate_fraction"])
        self.assertConfigError(BASE.replace("cancel_rate = 100", "cancel_rate = 1\ncancel_rate_fraction = 0.1"),
                               "not both")
        self.assertConfigError(BASE.replace("cancel_rate = 100", "cancel_rate_fraction = 0"), "number > 0")
        self.assertConfigError(BASE.replace("cancel_rate = 100", "cancel_rate_fraction = []"), "non-empty array")
        self.assertConfigError(BASE.replace("global_rate = [1000, 2000]", "")
                               .replace("cancel_rate = 100", "cancel_rate_fraction = 0.1"), "global_rate is required")

    # Design: abstraction/benchmark_runner/config/design.md#benchconfig
    def test_lb_policy_is_an_ingress_param(self):
        points = cfg.expand_sweep(self.load(BASE.replace("cancel_rate = 100",
                                                         'cancel_rate = 100\nlb_policy = ["round_robin", "power_of_two"]')))
        self.assertEqual(len(points), 8)
        self.assertEqual([dict(p.ingress_args)["lb_policy"] for p in points[:2]], ["round_robin", "power_of_two"])
        self.assertConfigError(BASE.replace("cancel_rate = 100", "incr_rand_max = 4"), "unknown key ingress.incr_rand_max")

    # Design: abstraction/benchmark_runner/config/design.md#benchconfig
    def test_gc_trace(self):
        self.assertFalse(self.load(BASE).gc_trace)
        c = self.load(BASE.replace("[server]", "[server]\ngc_trace = true", 1))
        self.assertTrue(c.gc_trace)
        self.assertNotIn("gc_trace", dict(cfg.expand_sweep(c)[0].server_args))  # runner-owned, never passed on
        for bad in ('"yes"', "1", "[true, false]"):
            self.assertConfigError(BASE.replace("[server]", f"[server]\ngc_trace = {bad}", 1),
                                   "server.gc_trace must be true or false")

    # Design: abstraction/benchmark_runner/config/design.md#benchconfig
    def test_server_workload_params(self):
        points = cfg.expand_sweep(self.load(BASE.replace("[server]",
            '[server]\nworkload = "message_buffer"\nwindow_size = 1000\nmessage_bytes = 64\n'
            'n_messages_per_request = [10, 50]', 1)))
        self.assertEqual(len(points), 8)
        args = [dict(p.server_args) for p in points]
        self.assertEqual(sorted({a["n_messages_per_request"] for a in args}), [10, 50])
        self.assertEqual((args[0]["workload"], args[0]["window_size"], args[0]["message_bytes"]),
                         ("message_buffer", 1000, 64))

    # Design: abstraction/benchmark_runner/config/design.md#benchconfig
    def test_ewma_beta_is_an_ingress_param(self):
        points = cfg.expand_sweep(self.load(BASE.replace("cancel_rate = 100",
                                                         'cancel_rate = 100\nlb_policy = "ewma"\newma_beta = [0.5, 0.9]')))
        self.assertEqual(len(points), 8)
        self.assertEqual([dict(p.ingress_args)["ewma_beta"] for p in points[:2]], [0.5, 0.9])
        self.assertEqual(dict(points[0].ingress_args)["lb_policy"], "ewma")

    # Design: abstraction/benchmark_runner/config/design.md#processplacement
    def test_process_placement(self):
        text = BASE.replace("response_rate = 2000", 'response_rate = 2000\nthreads = 4\ncpu_set = "0-3"')
        p = cfg.expand_sweep(self.load(text))[0]
        self.assertEqual(p.placement, {"server": {"threads": 4, "cpu_set": "0-3"},
                                       "loadgen": {"threads": None, "cpu_set": None},
                                       "ingress": {"threads": None, "cpu_set": None}})
        self.assertNotIn("threads", dict(p.server_args))
        self.assertEqual(p.to_json()["placement"]["server"]["cpu_set"], "0-3")
        none = cfg.expand_sweep(self.load(BASE))[0].placement
        self.assertTrue(all(v is None for prog in none.values() for v in prog.values()))
        self.assertConfigError(BASE.replace("num_clients = 4", "num_clients = 4\nthreads = 0"), "integer >= 1")
        self.assertConfigError(BASE.replace("num_clients = 4", "num_clients = 4\nthreads = [1, 2]"), "cannot be swept")
        self.assertConfigError(BASE.replace("num_clients = 4", 'num_clients = 4\ncpu_set = "0-"'), "CPU list")
        self.assertEqual(self.load(BASE.replace("num_clients = 4", 'num_clients = 4\ncpu_set = "0,2,4-5"'))
                         .placement["load_gen"]["cpu_set"], "0,2,4-5")

    # Design: abstraction/benchmark_runner/config/design.md#processplacement
    def test_n_worker_threads_is_a_program_param(self):
        text = BASE.replace("response_rate = 2000", "response_rate = 2000\nn_worker_threads = [1, 4]\nthreads = 8")
        points = cfg.expand_sweep(self.load(text))
        self.assertEqual(len(points), 8)  # 2 cancel_mode x 2 n_worker_threads x 2 global_rate
        self.assertEqual(sorted({dict(p.server_args)["n_worker_threads"] for p in points}), [1, 4])
        self.assertTrue(all(p.placement["server"] == {"threads": 8, "cpu_set": None} for p in points))
        only_threads = cfg.expand_sweep(self.load(BASE.replace("response_rate = 2000", "response_rate = 2000\nthreads = 2")))[0]
        self.assertNotIn("n_worker_threads", dict(only_threads.server_args))

    # Design: abstraction/benchmark_runner/config/design.md#benchconfig
    def test_initial_stall_ms_per_program(self):
        text = BASE.replace("response_rate = 2000", "response_rate = 2000\ninitial_stall_ms = [0, 2000]").replace(
            "cancel_rate = 100", "cancel_rate = 100\ninitial_stall_ms = 500")
        points = cfg.expand_sweep(self.load(text))
        self.assertEqual(sorted({dict(p.server_args)["initial_stall_ms"] for p in points}), [0, 2000])
        self.assertTrue(all(dict(p.ingress_args)["initial_stall_ms"] == 500 for p in points))
        unset = cfg.expand_sweep(self.load(BASE))[0]
        self.assertNotIn("initial_stall_ms", dict(unset.server_args))  # program default (1000) applies

    # Design: abstraction/benchmark_runner/config/design.md#benchconfig
    def test_n_waiting_requests_is_a_load_gen_param(self):
        text = BASE.replace("num_clients = 4", "num_clients = 4\nn_waiting_requests = [1, 100]")
        points = cfg.expand_sweep(self.load(text))
        self.assertEqual(sorted({dict(p.loadgen_args)["n_waiting_requests"] for p in points}), [1, 100])
        self.assertNotIn("n_waiting_requests", dict(cfg.expand_sweep(self.load(BASE))[0].loadgen_args))


if __name__ == "__main__":
    unittest.main()
