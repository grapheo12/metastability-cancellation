# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/experiment_executor/design.md (experiment_executor v0.4.0)
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


def point(server=(None, None), loadgen=(None, None), ingress=(None, None), instances=1):
    placement = {name: {"threads": t, "cpu_set": c}
                 for name, (t, c) in (("server", server), ("loadgen", loadgen), ("ingress", ingress))}
    return SweepPoint(0, {}, [], [], [], server_instances=instances, placement=placement)


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
            executor.write_placement(point(server=(4, "0-3"), ingress=(1, "8")), ready(True), ready(False), ready(False), d)
            with open(os.path.join(d, "placement.json")) as f:
                doc = json.load(f)
            self.assertEqual(doc, {"server": ["env", "GOMAXPROCS=4", "taskset", "-c", "0-3"], "loadgen": [],
                                   "ingress": ["env", "GOMAXPROCS=1"]})


class Recorder:
    def __init__(self):
        self.commands = []

    def run(self, command, timeout_s=120.0):
        self.commands.append(command)
        return CommandResult(0, "", "")


class ClientRunTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/experiment_executor/design.md#clientrun
    def test_loadgen_targets_local_ingress(self):
        t = Recorder()
        client = MachineReady(t, "client", "/w", "/w/repo", {"loadgen": "/b/loadgen", "ingress": "/b/ingress"},
                              None, "GOMAXPROCS", True)
        config = SimpleNamespace(ingress_port=9090, run=SimpleNamespace(client_timeout_margin_s=1.0))
        p = point(loadgen=(4, "4-7"))
        p.loadgen_args = [("duration_s", 1), ("slo_ms", 5)]
        self.assertTrue(executor.client_run(p, config, client, "/rd"))
        script = t.commands[0]
        self.assertIn("env GOMAXPROCS=4 taskset -c 4-7 /b/loadgen --target 127.0.0.1:9090", script)
        self.assertNotIn("canceller", script)
        p.loadgen_target = "172.31.23.63:9090"  # Ingress on the server machine
        executor.client_run(p, config, client, "/rd")
        self.assertIn("/b/loadgen --target 172.31.23.63:9090", t.commands[-1])


class LocalMachine:
    """A MachineReady-like object on a LocalTransport, with a temporary workdir."""

    def __init__(self, workdir):
        from benchrunner.transport import LocalTransport
        self.transport, self.workdir, self.data_addr = LocalTransport(), workdir, "127.0.0.1"


class PointOutputsTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/experiment_executor/design.md#point-outputs
    def test_ingress_outputs_from_ingress_host(self):
        with tempfile.TemporaryDirectory() as rc, tempfile.TemporaryDirectory() as rs, \
                tempfile.TemporaryDirectory() as pdir:
            client, server = LocalMachine(rc), LocalMachine(rs)
            for d, name in ((rc, "loadgen.summary.json"), (rs, "ingress.summary.json")):
                with open(os.path.join(d, name), "w") as f:
                    f.write("{}")
            executor.collect_outputs(point(instances=0), server, client, rs, rc, pdir, server, rs)
            self.assertTrue(os.path.exists(os.path.join(pdir, "loadgen.summary.json")))
            self.assertTrue(os.path.exists(os.path.join(pdir, "ingress.summary.json")))

    # Design: abstraction/benchmark_runner/experiment_executor/design.md#point-outputs
    def test_counters_summed_over_instances(self):
        with tempfile.TemporaryDirectory() as remote, tempfile.TemporaryDirectory() as pdir:
            m = LocalMachine(remote)
            for k, c in enumerate([(10, 1, 5), (7, 0, 4)]):
                with open(os.path.join(remote, f"server_{k}.out"), "w") as f:
                    f.write("listening on 0.0.0.0:808%d\n" % k)
                    f.write(json.dumps(dict(zip(executor.COUNTER_KEYS, c)), separators=(",", ":")) + "\n")
            p = point(instances=2)
            executor.collect_outputs(p, m, m, remote, remote, pdir)
            with open(os.path.join(pdir, "server_counters.json")) as f:
                self.assertEqual(json.load(f), dict(zip(executor.COUNTER_KEYS, (17, 1, 9))))
            self.assertTrue(os.path.exists(os.path.join(pdir, "server_counters_1.json")))
            self.assertEqual(executor.validate_outputs(p, pdir), "loadgen summary missing")
            for name in ("loadgen.summary.json", "ingress.summary.json"):
                open(os.path.join(pdir, name), "w").close()
            self.assertIsNone(executor.validate_outputs(p, pdir))
            for path in (os.path.join(remote, "server_1.out"), os.path.join(pdir, "server_1.out")):
                os.remove(path)  # a fresh point directory would not have the old copy
            os.remove(os.path.join(pdir, "server_counters.json"))
            os.remove(os.path.join(pdir, "server_counters_1.json"))
            executor.collect_outputs(p, m, m, remote, remote, pdir)
            self.assertFalse(os.path.exists(os.path.join(pdir, "server_counters.json")))
            self.assertIn("instance 1", executor.validate_outputs(p, pdir))


if __name__ == "__main__":
    unittest.main()
