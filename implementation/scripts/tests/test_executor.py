# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/experiment_executor/design.md (experiment_executor v0.12.0)
import contextlib
import io
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


class TimeoutRecorder(Recorder):
    def __init__(self):
        super().__init__()
        self.timeouts = []

    def run(self, command, timeout_s=120.0):
        self.timeouts.append(timeout_s)
        return super().run(command, timeout_s)


class ClientRunTimeoutTest(unittest.TestCase):
    # The timeout adds drain_timeout_ms only with slo_cancel = false (default 10000).
    # Design: abstraction/benchmark_runner/experiment_executor/design.md#clientrun
    def test_timeout_covers_the_drain(self):
        t = TimeoutRecorder()
        client = MachineReady(t, "client", "/w", "/w/repo", {"loadgen": "/b/loadgen"}, None, "GOMAXPROCS", True)
        config = SimpleNamespace(ingress_port=9090, run=SimpleNamespace(client_timeout_margin_s=60.0))
        cases = [([("duration_s", 120), ("slo_ms", 5000)], 185.0),
                 ([("duration_s", 120), ("slo_ms", 5000), ("slo_cancel", True), ("drain_timeout_ms", 60000)], 185.0),
                 ([("duration_s", 120), ("slo_ms", 5000), ("slo_cancel", False), ("drain_timeout_ms", 60000)], 245.0),
                 ([("duration_s", 120), ("slo_ms", 5000), ("slo_cancel", False)], 195.0)]
        for args, want in cases:
            p = point()
            p.loadgen_args = args
            executor.client_run(p, config, client, "/rd")
            self.assertEqual(t.timeouts[-1], want, args)


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
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                executor.collect_outputs(point(instances=0), server, client, rs, rc, pdir, server, rs)
            self.assertTrue(os.path.exists(os.path.join(pdir, "loadgen.summary.json")))
            self.assertTrue(os.path.exists(os.path.join(pdir, "ingress.summary.json")))
            self.assertEqual(out.getvalue().strip(),
                             "point 0: ingress.forwards.csv missing (left out of the forward timeline plots)")
            with open(os.path.join(rs, "ingress.forwards.csv"), "w") as f:
                f.write("t_ns,server_id,outcome,latency_ns,ewma_latency_ns\n")
            with open(os.path.join(rs, "ingress.liveness.csv"), "w") as f:
                f.write("t_ns,server_id,live\n")
            with open(os.path.join(rs, "ingress.picks.csv"), "w") as f:  # PickLog
                f.write("t_ns,server_id,outstanding,ewma_exec_ns,worker_count\n")
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                executor.collect_outputs(point(instances=0), server, client, rs, rc, pdir, server, rs)
            self.assertTrue(os.path.exists(os.path.join(pdir, "ingress.forwards.csv")))
            self.assertTrue(os.path.exists(os.path.join(pdir, "ingress.liveness.csv")))
            self.assertTrue(os.path.exists(os.path.join(pdir, "ingress.picks.csv")))
            self.assertEqual(out.getvalue(), "")

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

    # Design: abstraction/benchmark_runner/experiment_executor/design.md#point-outputs
    def test_queue_logs_fetched_missing_only_warns(self):
        with tempfile.TemporaryDirectory() as remote, tempfile.TemporaryDirectory() as pdir:
            m = LocalMachine(remote)
            with open(os.path.join(remote, "server_0.queue.csv"), "w") as f:
                f.write("t_ms,queue_len\n0,0\n")
            with open(os.path.join(remote, "ingress.forwards.csv"), "w") as f:
                f.write("t_ns,server_id,outcome,latency_ns,ewma_latency_ns\n")
            for k in range(2):
                with open(os.path.join(remote, f"server_{k}.out"), "w") as f:
                    f.write(json.dumps(dict(zip(executor.COUNTER_KEYS, (0, 0, 0)))) + "\n")
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                executor.collect_outputs(point(instances=2), m, m, remote, remote, pdir)
            self.assertTrue(os.path.exists(os.path.join(pdir, "server_0.queue.csv")))
            self.assertEqual(out.getvalue().strip(),
                             "point 0: server_1.queue.csv missing (left out of queue_length)")
            for name in ("loadgen.summary.json", "ingress.summary.json"):
                open(os.path.join(pdir, name), "w").close()
            self.assertIsNone(executor.validate_outputs(point(instances=2), pdir))


GO_REGEX = (r"^gc (?P<cycle>\d+) @(?P<start_s>[0-9.]+)s \d+%: (?P<stw_start_ms>[0-9.]+)"
            r"\+(?P<mark_ms>[0-9.]+)\+(?P<stw_end_ms>[0-9.]+) ms clock")


class GcEventsTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/experiment_executor/design.md#gcevents
    def test_seconds_to_ns(self):
        self.assertEqual(executor.seconds_to_ns("4.507"), 4_507_000_000)
        self.assertEqual(executor.seconds_to_ns("10"), 10_000_000_000)
        self.assertEqual(executor.seconds_to_ns("0.0000000019"), 1)  # beyond 9 digits: truncated

    # Worked example of experiment_executor/design.md#gcevents.
    #
    # Design: abstraction/benchmark_runner/experiment_executor/design.md#gcevents
    def test_worked_example(self):
        with tempfile.TemporaryDirectory() as pdir:
            with open(os.path.join(pdir, "server_0.out"), "w") as f:
                f.write("process_start_unix_ns 1700000000000000000\n"
                        "gc 1 @0.012s 1%: 0.018+5.1+0.042 ms clock, 0.07+1.2/9.8/20+0.17 ms cpu, 4->4->2 MB\n"
                        "listening on 0.0.0.0:8080\n"
                        "gc 2 @4.507s 3%: 0.020+12+0.050 ms clock, x\n"
                        "gc 3 @10s 2%: 0.01+1+0.02 ms clock, x\n")
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                executor.write_gc_events(point(instances=1), 0, pdir, GO_REGEX)
            self.assertEqual(out.getvalue(), "")
            with open(os.path.join(pdir, "server_0.gc.csv")) as f:
                self.assertEqual(f.read(), executor.GC_HEADER + "\n"
                                 "1700000000012000000,1,0.018,5.1,0.042\n"
                                 "1700000004507000000,2,0.020,12,0.050\n"
                                 "1700000010000000000,3,0.01,1,0.02\n")

    # Design: abstraction/benchmark_runner/experiment_executor/design.md#gcevents
    def test_problems_only_warn(self):
        with tempfile.TemporaryDirectory() as pdir:
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                executor.write_gc_events(point(instances=1), 0, pdir, GO_REGEX)              # no .out
                with open(os.path.join(pdir, "server_0.out"), "w") as f:
                    f.write("gc 1 @0.012s 1%: 0.018+5.1+0.042 ms clock\n")                # no start line
                executor.write_gc_events(point(instances=1), 0, pdir, GO_REGEX)
                self.assertFalse(os.path.exists(os.path.join(pdir, "server_0.gc.csv")))
                with open(os.path.join(pdir, "server_0.out"), "w") as f:
                    f.write("process_start_unix_ns 5\nlistening on x\n")                   # no GcTrace
                executor.write_gc_events(point(instances=1), 0, pdir, GO_REGEX)
            self.assertEqual(out.getvalue().splitlines(), [
                "point 0: server_0.out missing (no GcEvents)",
                "point 0: server_0.out has no process_start_unix_ns line (no GcEvents)",
                "point 0: server_0.out has no GcTrace lines"])
            with open(os.path.join(pdir, "server_0.gc.csv")) as f:
                self.assertEqual(f.read(), executor.GC_HEADER + "\n")                      # header only

    # Design: abstraction/benchmark_runner/experiment_executor/design.md#gcevents
    def test_server_prefix(self):
        server = MachineReady(None, "server", "/w", "/w/repo", {"server": "/b/server"}, None, "GOMAXPROCS", True,
                              gc_trace_env=["GODEBUG=gctrace=1"], gc_trace_regex=GO_REGEX)
        on = type("C", (), {"gc_trace": True})()
        off = type("C", (), {"gc_trace": False})()
        self.assertEqual(executor.gc_trace_prefix(on, server), ["env", "GODEBUG=gctrace=1"])
        self.assertEqual(executor.gc_trace_prefix(off, server), [])


class ServerLifecycleTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
    def test_every_instance_gets_its_queue_log_path(self):
        started = []
        orig = executor.start_program
        executor.start_program = lambda m, name, words, *rest: started.append(words)
        try:
            server = MachineReady(None, "server", "/w", "/w/repo", {"server": "/b/server"}, None, "GOMAXPROCS", True)
            config = SimpleNamespace(listen_port=8080, gc_trace=False,
                                     run=SimpleNamespace(server_ready_timeout_s=1.0))
            p = point(instances=2)
            p.server_args = [("response_rate", 10)]
            executor.start_servers(p, config, server, "/rd")
            server.gc_trace_env = ["GODEBUG=gctrace=1"]
            config.gc_trace = True
            executor.start_servers(p, config, server, "/rd")
        finally:
            executor.start_program = orig
        self.assertEqual(started[1][:5], ["/b/server", "--listen_addr", "0.0.0.0:8081",
                                          "--queue_log_path", "/rd/server_1.queue.csv"])
        self.assertIn("--response_rate", started[1])
        self.assertEqual(started[2][:3], ["env", "GODEBUG=gctrace=1", "/b/server"])  # GcTraceHook prefix

    # Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
    def test_local_index_worked_example(self):
        for hosts, want in (([0, 0], [0, 1]), ([0, 1], [0, 0]), ([0, 0, 1, 1], [0, 1, 0, 1])):
            self.assertEqual([executor.local_index(hosts, k) for k in range(len(hosts))], want)

    # Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
    def test_instances_across_server_machines(self):
        started, stopped = [], []
        orig_start, orig_stop = executor.start_program, executor.stop_program
        executor.start_program = lambda m, name, words, log, pid_file, *rest: started.append((m.role, words[:3], log,
                                                                                              pid_file))
        executor.stop_program = lambda m, pid_file, timeout: stopped.append((m.role, pid_file)) or True
        try:
            a = MachineReady(None, "a", "/wa", "/wa/repo", {"server": "/a/server"}, "10.0.0.1", "GOMAXPROCS", True)
            b = MachineReady(None, "b", "/wb", "/wb/repo", {"server": "/b/server"}, "10.0.0.2", "GOMAXPROCS", True)
            config = SimpleNamespace(listen_port=8080, gc_trace=False, run=SimpleNamespace(server_ready_timeout_s=1.0))
            p = point(instances=4)
            p.server_hosts = [0, 0, 1, 1]
            executor.start_servers(p, config, [a, b], ["/rda", "/rdb"])
            self.assertTrue(executor.stop_servers([a, b], p.server_hosts, 1.0))
        finally:
            executor.start_program, executor.stop_program = orig_start, orig_stop
        self.assertEqual(started, [
            ("a", ["/a/server", "--listen_addr", "0.0.0.0:8080"], "/rda/server_0.out", "/wa/server_0.pid"),
            ("a", ["/a/server", "--listen_addr", "0.0.0.0:8081"], "/rda/server_1.out", "/wa/server_1.pid"),
            ("b", ["/b/server", "--listen_addr", "0.0.0.0:8080"], "/rdb/server_2.out", "/wb/server_2.pid"),
            ("b", ["/b/server", "--listen_addr", "0.0.0.0:8081"], "/rdb/server_3.out", "/wb/server_3.pid")])
        self.assertEqual(stopped, [("a", "/wa/server_0.pid"), ("a", "/wa/server_1.pid"),
                                   ("b", "/wb/server_2.pid"), ("b", "/wb/server_3.pid")])

    # Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
    def test_freeze_target_only_on_its_instance(self):
        started = []
        orig_start = executor.start_program
        executor.start_program = lambda m, name, words, *rest: started.append(words)
        try:
            a = MachineReady(None, "a", "/wa", "/wa/repo", {"server": "/a/server"}, "10.0.0.1", "GOMAXPROCS", True)
            b = MachineReady(None, "b", "/wb", "/wb/repo", {"server": "/b/server"}, "10.0.0.2", "GOMAXPROCS", True)
            config = SimpleNamespace(listen_port=8080, gc_trace=False, run=SimpleNamespace(server_ready_timeout_s=1.0))
            p = point(instances=2)
            p.server_hosts = [0, 1]
            p.freeze = {"instance": 0, "freeze_after_ms": 60000}
            executor.start_servers(p, config, [a, b], ["/rda", "/rdb"])
        finally:
            executor.start_program = orig_start
        self.assertEqual(started[0][-2:], ["--freeze_after_ms", "60000"])
        self.assertNotIn("--freeze_after_ms", started[1])

    # Design: abstraction/benchmark_runner/experiment_executor/design.md#point-outputs
    def test_outputs_fetched_from_each_instances_machine(self):
        with tempfile.TemporaryDirectory() as ra, tempfile.TemporaryDirectory() as rb, \
                tempfile.TemporaryDirectory() as pdir:
            a, b = LocalMachine(ra), LocalMachine(rb)
            for d, k, c in ((ra, 0, (1, 0, 2)), (rb, 1, (3, 1, 4))):
                with open(os.path.join(d, f"server_{k}.out"), "w") as f:
                    f.write(json.dumps(dict(zip(executor.COUNTER_KEYS, c))) + "\n")
            p = point(instances=2)
            p.server_hosts = [0, 1]
            with contextlib.redirect_stdout(io.StringIO()):
                executor.collect_outputs(p, [a, b], a, [ra, rb], ra, pdir)
            with open(os.path.join(pdir, "server_counters.json")) as f:
                self.assertEqual(json.load(f), dict(zip(executor.COUNTER_KEYS, (4, 1, 6))))


if __name__ == "__main__":
    unittest.main()
