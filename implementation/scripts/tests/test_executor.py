# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/experiment_executor/design.md (experiment_executor v0.2.0)
import json
import os
import tempfile
import unittest
from types import SimpleNamespace

from benchrunner import executor
from benchrunner.config import SweepPoint
from benchrunner.remote_setup import MachineReady
from benchrunner.transport import CommandResult


def ready(pinning, thread_env="GOMAXPROCS"):
    return MachineReady(None, "client", "/w", "/w/repo", {}, None, thread_env, pinning)


def point(server=(None, None), loadgen=(None, None), canceller=(None, None), canceller_enabled=True):
    placement = {name: {"threads": t, "cpu_set": c}
                 for name, (t, c) in (("server", server), ("loadgen", loadgen), ("canceller", canceller))}
    return SweepPoint(0, {}, [], [], [] if canceller_enabled else None, placement)


class PlacementPrefixTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/experiment_executor/design.md#processplacement
    def test_prefix_table(self):
        cases = [((None, None), True, []),
                 ((4, None), True, ["env", "GOMAXPROCS=4"]),
                 ((None, "4-7"), True, ["taskset", "-c", "4-7"]),
                 ((4, "0-3"), True, ["env", "GOMAXPROCS=4", "taskset", "-c", "0-3"]),
                 ((1, "8"), False, ["env", "GOMAXPROCS=1"])]
        for placement, pinning, want in cases:
            self.assertEqual(executor.placement_prefix("loadgen", point(loadgen=placement), ready(pinning)), want)

    # Design: abstraction/benchmark_runner/experiment_executor/design.md#processplacement
    def test_placement_json(self):
        with tempfile.TemporaryDirectory() as d:
            executor.write_placement(point(server=(4, "0-3"), canceller=(1, "8")), ready(True), ready(False), d)
            with open(os.path.join(d, "placement.json")) as f:
                doc = json.load(f)
            self.assertEqual(doc, {"server": ["env", "GOMAXPROCS=4", "taskset", "-c", "0-3"], "loadgen": [],
                                   "canceller": ["env", "GOMAXPROCS=1"]})
            executor.write_placement(point(canceller_enabled=False), ready(True), ready(True), d)
            with open(os.path.join(d, "placement.json")) as f:
                self.assertEqual(json.load(f), {"server": [], "loadgen": []})


class Recorder:
    def __init__(self):
        self.commands = []

    def run(self, command, timeout_s=120.0):
        self.commands.append(command)
        return CommandResult(0, "", "")


class ClientRunTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/experiment_executor/design.md#clientrun
    def test_prefix_before_each_binary(self):
        t = Recorder()
        client = MachineReady(t, "client", "/w", "/w/repo", {"loadgen": "/b/loadgen", "canceller": "/b/canceller"},
                              None, "GOMAXPROCS", True)
        server = SimpleNamespace(data_addr="127.0.0.1")
        config = SimpleNamespace(listen_port=8080, run=SimpleNamespace(client_timeout_margin_s=1.0))
        p = point(loadgen=(4, "4-7"), canceller=(1, "8"))
        p.loadgen_args, p.canceller_args = [("duration_s", 1), ("slo_ms", 5)], [("duration_s", 1)]
        self.assertTrue(executor.client_run(p, config, server, client, "/rd"))
        script = t.commands[0]
        self.assertIn("env GOMAXPROCS=4 taskset -c 4-7 /b/loadgen --target 127.0.0.1:8080", script)
        self.assertIn("env GOMAXPROCS=1 taskset -c 8 /b/canceller --target 127.0.0.1:8080", script)


if __name__ == "__main__":
    unittest.main()
