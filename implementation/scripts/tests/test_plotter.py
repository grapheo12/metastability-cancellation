# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/result_plotter/design.md (result_plotter v0.12.0)
import csv
import json
import os
import tempfile
import unittest

from benchrunner import plotter


def write_point(out, pid, swept, done=True, ok=90, retries=True):
    pdir = os.path.join(out, "points", f"point_{pid:04d}")
    os.makedirs(pdir)
    with open(os.path.join(pdir, "params.json"), "w") as f:
        json.dump({"id": pid, "swept": swept, "server_args": [], "loadgen_args": [], "canceller_args": None}, f)
    if not done:
        open(os.path.join(pdir, "failed"), "w").write("boom\n")
        return
    summary = {"counts": {"scheduled": 100, "sent": 100, "ok": ok, "rejected": 10, "cancelled": 0, "error": 0,
                          "not_sent": 0},
               "rates_per_s": {"offered": 50.0, "goodput": ok / 2},
               "e2e_latency_us": {"p50": 100.0, "p99": 900.5, "p999": None},
               "service_latency_us": {"p50": 80.0, "p99": 700.0}}
    if retries:  # runs made before retries have neither count
        summary["counts"].update(dropped=4, copies_sent=130)
    json.dump(summary, open(os.path.join(pdir, "loadgen.summary.json"), "w"))
    json.dump({"cancelled_before_processing": 7, "cancelled_during_processing": 3, "completed_before_cancel": 1},
              open(os.path.join(pdir, "server_counters.json"), "w"))
    open(os.path.join(pdir, "done"), "w").close()


class PointTableTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/result_plotter/design.md#pointtable
    def test_table(self):
        with tempfile.TemporaryDirectory() as out:
            write_point(out, 0, {"load_gen.global_rate": 1000, "server.cancel_mode": "ignore"})
            write_point(out, 1, {"load_gen.global_rate": 2000, "server.cancel_mode": "ignore"}, done=False)
            rows, keys = plotter.build_table(out)
            self.assertEqual(keys, ["load_gen.global_rate", "server.cancel_mode"])
            with open(os.path.join(out, "all_points.csv")) as f:
                table = list(csv.DictReader(f))
            self.assertEqual([r["status"] for r in table], ["done", "failed"])
            self.assertEqual(table[0]["e2e_p99_us"], "900.5")
            self.assertEqual(table[0]["e2e_p999_us"], "")
            self.assertEqual(table[0]["srv_cancelled_before_processing"], "7")
            self.assertEqual(table[0]["cx_sent"], "")
            self.assertEqual(list(table[0])[:4], ["point", "status", "load_gen.global_rate", "server.cancel_mode"])
            cols = list(table[0])
            self.assertEqual(cols[cols.index("cx_accepted") + 1], "cx_offered_fraction")
            self.assertEqual(table[0]["cx_offered_fraction"], "")  # canceller disabled in this fixture
            self.assertEqual((table[0]["lg_dropped"], table[0]["lg_copies_sent"]), ("4", "130"))
            self.assertEqual(cols[cols.index("lg_cancelled") + 1], "lg_dropped")
            self.assertEqual(cols[cols.index("lg_not_sent") + 1], "lg_copies_sent")

    # Design: abstraction/benchmark_runner/result_plotter/design.md#pointtable
    def test_table_before_retries(self):
        with tempfile.TemporaryDirectory() as out:
            write_point(out, 0, {"load_gen.global_rate": 1000}, retries=False)
            plotter.build_table(out)
            with open(os.path.join(out, "all_points.csv")) as f:
                row = next(csv.DictReader(f))
            self.assertEqual((row["lg_dropped"], row["lg_copies_sent"]), ("", ""))

    # Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
    def test_plots(self):
        try:
            import plotly  # noqa: F401
        except ImportError:
            self.skipTest("plotly not installed")
        with tempfile.TemporaryDirectory() as out:
            pid = 0
            for rate in (1000, 2000):
                for mode in ("ignore", "cooperative"):
                    write_point(out, pid, {"load_gen.global_rate": rate, "server.cancel_mode": mode}, ok=80 + pid)
                    pid += 1
            plotter.plot(out)
            for name in ("latency_e2e", "goodput", "outcomes", "server_counters", "throughput_latency",
                         "server_counters_normalized", "throughput_latency_normalized"):
                page = os.path.join(out, "plots", f"{name}.html")
                self.assertTrue(0 < os.path.getsize(page) < 1_000_000)  # the library is not embedded
                with open(page) as f:
                    self.assertIn('src="plotly.min.js"', f.read())
            self.assertTrue(os.path.exists(os.path.join(out, "plots", "plotly.min.js")))  # shared, offline

    # Design: abstraction/benchmark_runner/result_plotter/design.md#throughputlatencyplot
    def test_throughput_latency_line_keeps_swept_order(self):
        rows = [{"load_gen.global_rate": 1000, "lg_goodput": 950.0, "e2e_p99_us": 4000},
                {"load_gen.global_rate": 10, "lg_goodput": 9.9, "e2e_p99_us": 1100},
                {"load_gen.global_rate": 2000, "lg_goodput": 600.0, "e2e_p99_us": 9000},  # past saturation
                {"load_gen.global_rate": 100, "lg_goodput": None, "e2e_p99_us": 1300}]  # left out
        position = {r["load_gen.global_rate"]: r["load_gen.global_rate"] for r in rows}
        line = plotter.throughput_latency_line(rows, "load_gen.global_rate", position, lambda r: r["e2e_p99_us"])
        self.assertEqual(line, [(9.9, 1100), (950.0, 4000), (600.0, 9000)])

    # Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
    def test_server_counters_normalized_values(self):
        fig = next(f for f in plotter.FIGURES if f[0] == "server_counters_normalized")
        row = {"cx_sent": 1000, "srv_cancelled_before_processing": 600, "srv_cancelled_during_processing": 50,
               "srv_completed_before_cancel": 300}
        self.assertEqual([value(row) for _, value in fig[2]], [0.6, 0.05, 0.3])
        for cx in (0, None):
            self.assertEqual([value(dict(row, cx_sent=cx)) for _, value in fig[2]], [None, None, None])
        self.assertEqual((fig[1], fig[3], fig[4]), ("x_key", "linear", (0, 1)))

    # Design: abstraction/benchmark_runner/result_plotter/design.md#cancelofferedfraction
    def test_cancel_offered_fraction(self):
        p = {"loadgen_args": [["global_rate", 1000]], "canceller_args": [["cancel_rate", 200.0]]}
        self.assertEqual(plotter.cancel_offered_fraction(p), 0.2)  # a run made before the Ingress
        self.assertEqual(plotter.cancel_offered_fraction({"loadgen_args": [["global_rate", 1000]],
                                                          "ingress_args": [["lb_policy", "round_robin"]]}), 0.0)
        self.assertEqual(plotter.cancel_offered_fraction({"loadgen_args": [["global_rate", 500]],
                                                          "canceller_args": [["cancel_rate", 5.0]]}), 0.01)
        self.assertIsNone(plotter.cancel_offered_fraction({"loadgen_args": [["global_rate", 1000]],
                                                           "canceller_args": None}))
        self.assertIsNone(plotter.cancel_offered_fraction({"loadgen_args": [["global_rate", 0]],
                                                           "canceller_args": [["cancel_rate", 1.0]]}))

    # Design: abstraction/benchmark_runner/result_plotter/design.md#throughputlatencynormalizedplot
    def test_normalized_goodput(self):
        n = plotter.normalized_goodput
        self.assertEqual(n({"lg_goodput": 819.0, "cx_offered_fraction": 0.2}), 1023.75)
        self.assertAlmostEqual(n({"lg_goodput": 491.0, "cx_offered_fraction": 0.01}), 495.959595959596)
        self.assertEqual(n({"lg_goodput": 992.0, "cx_offered_fraction": None}), 992.0)
        self.assertIsNone(n({"lg_goodput": 50.0, "cx_offered_fraction": 1.0}))
        self.assertIsNone(n({"lg_goodput": None, "cx_offered_fraction": 0.1}))

    # Design: abstraction/benchmark_runner/result_plotter/design.md#lbimbalance
    def test_lb_imbalance(self):
        f = lambda *xs: plotter.lb_imbalance({"backends": [{"forwarded": x} for x in xs]})
        self.assertEqual(f(15000, 15000), 1.0)
        self.assertAlmostEqual(f(15300, 14700), 15300 / 14700)
        self.assertIsNone(f(30000))
        self.assertIsNone(f(30000, 0))
        self.assertIsNone(plotter.lb_imbalance(None))

    # Design: abstraction/benchmark_runner/result_plotter/design.md#pointtable
    def test_ingress_sources(self):
        with tempfile.TemporaryDirectory() as out:
            write_point(out, 0, {"load_gen.global_rate": 1000})
            pdir = os.path.join(out, "points", "point_0000")
            with open(os.path.join(pdir, "params.json"), "w") as f:
                json.dump({"id": 0, "swept": {"load_gen.global_rate": 1000}, "server_args": [],
                           "loadgen_args": [["global_rate", 1000]], "ingress_args": [["cancel_rate", 200.0]]}, f)
            with open(os.path.join(pdir, "ingress.summary.json"), "w") as f:
                json.dump({"counts": {"sent": 180, "accepted": 179},
                           "backends": [{"forwarded": 15300}, {"forwarded": 14700}]}, f)
            rows, _ = plotter.build_table(out)
            r = rows[0]
            self.assertEqual((r["cx_sent"], r["cx_accepted"], r["cx_offered_fraction"]), (180, 179, 0.2))
            self.assertAlmostEqual(r["lb_imbalance"], 15300 / 14700)
            with open(os.path.join(out, "all_points.csv")) as f:
                cols = next(csv.reader(f))
            i = cols.index("cx_accepted")
            self.assertEqual(cols[i + 1:i + 3], ["cx_offered_fraction", "lb_imbalance"])

    def test_nothing_to_plot(self):
        try:
            import plotly  # noqa: F401
        except ImportError:
            self.skipTest("plotly not installed")
        with tempfile.TemporaryDirectory() as out:
            write_point(out, 0, {}, done=False)
            with self.assertRaises(plotter.NothingToPlot):
                plotter.plot(out)


def write_queue_log(pdir, k, rows):
    with open(os.path.join(pdir, f"server_{k}.queue.csv"), "w") as f:
        f.write("t_ms,queue_len\n" + "".join(f"{t},{n}\n" for t, n in rows))


class QueueLengthPlotTest(unittest.TestCase):
    # Worked example: 2 instances with 3 and 4 rows give 3 summed points.
    #
    # Design: abstraction/benchmark_runner/result_plotter/design.md#queuelengthplot
    def test_worked_example(self):
        with tempfile.TemporaryDirectory() as pdir:
            write_queue_log(pdir, 0, [(0, 0), (10, 2), (20, 5)])
            write_queue_log(pdir, 1, [(0, 1), (10, 0), (21, 1), (30, 4)])
            self.assertEqual(plotter.point_queue_series(pdir, {"server_instances": 2}),
                             [(0.0, 1), (0.01, 2), (0.02, 6)])

    # Design: abstraction/benchmark_runner/result_plotter/design.md#queuelengthplot
    def test_edge_cases(self):
        with tempfile.TemporaryDirectory() as pdir:
            self.assertIsNone(plotter.point_queue_series(pdir, {"server_instances": 2}))  # no logs
            write_queue_log(pdir, 1, [(0, 3), (10, 4)])  # instance 0 missing
            self.assertEqual(plotter.point_queue_series(pdir, {"server_instances": 2}), [(0.0, 3), (0.01, 4)])
            self.assertIsNone(plotter.point_queue_series(pdir, {}))  # old run: one instance, k = 0 only
            write_queue_log(pdir, 0, [])  # header only
            self.assertIsNone(plotter.point_queue_series(pdir, {"server_instances": 2}))
            with open(os.path.join(pdir, "server_0.queue.csv"), "w") as f:
                f.write("t_ms,queue_len\n0,1\n10,2\n2")  # truncated last line
            self.assertEqual(plotter.read_queue_log(os.path.join(pdir, "server_0.queue.csv")), [(0, 1), (10, 2)])

    # Design: abstraction/benchmark_runner/result_plotter/design.md#queuelengthplot
    def test_plot_file(self):
        try:
            import plotly  # noqa: F401
        except ImportError:
            self.skipTest("plotly not installed")
        with tempfile.TemporaryDirectory() as out:
            write_point(out, 0, {"server.n_worker_threads": 1})
            write_point(out, 1, {"server.n_worker_threads": 4})
            write_point(out, 2, {"server.n_worker_threads": 8})  # no queue log: left out
            write_queue_log(os.path.join(out, "points", "point_0000"), 0, [(0, 0), (10, 5), (20, 9)])
            write_queue_log(os.path.join(out, "points", "point_0001"), 0, [(0, 0), (10, 0), (20, 1)])
            plotter.plot(out)
            path = os.path.join(out, "plots", "queue_length.html")
            self.assertTrue(os.path.getsize(path) > 0)
            for pid in (0, 1):
                os.remove(os.path.join(out, "points", f"point_{pid:04d}", "server_0.queue.csv"))
            plotter.plot(out)
            self.assertFalse(os.path.exists(path))  # no logs left: stale figure removed



def write_forward_log(pdir, rows, backends=None):
    with open(os.path.join(pdir, "ingress.forwards.csv"), "w") as f:
        f.write(plotter.FORWARD_HEADER + "\n" + "".join(",".join(str(x) for x in r) + "\n" for r in rows))
    if backends is not None:
        json.dump({"backends": [{"server_id": k} for k in range(backends)]},
                  open(os.path.join(pdir, "ingress.summary.json"), "w"))


# The ForwardTimelinePlots worked example (result_plotter/design.md).
FWD_EXAMPLE = [(1_000_000, 0, "ok", 2_000_000, 1300000), (5_000_000, 0, "ok", 4_000_000, 2110000),
               (19_999_999, 0, "rejected", 1_000_000, 2110000), (20_000_000, 0, "ok", 3_000_000, 2377000),
               (65_000_000, 0, "ok", 10_000_000, 4663900)]


class ForwardTimelinePlotsTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/result_plotter/design.md#forwardtimelineplots
    def test_worked_example(self):
        with tempfile.TemporaryDirectory() as out:
            write_point(out, 0, {"load_gen.global_rate": 500})
            write_forward_log(os.path.join(out, "points", "point_0000"), FWD_EXAMPLE, backends=1)
            rows, _ = plotter.build_table(out)
            figs = plotter.forward_timelines(out, rows, ["load_gen.global_rate"])
            self.assertEqual([f[0] for f in figs], ["forward_latency", "ewma_latency"])
            (label, med, p99, gc, color), = figs[0][2]
            self.assertEqual((gc, color), (None, 0))  # no gc.csv: no overlay
            self.assertEqual(label, "load_gen.global_rate=500")
            self.assertEqual(med, [(0.0, 2.0), (0.02, 3.0), (0.06, 10.0)])
            self.assertEqual(p99, [(0.0, 4.0), (0.02, 3.0), (0.06, 10.0)])
            _, med, p99, gc, _ = figs[1][2][0]
            self.assertIsNone(gc)  # ewma_latency never has an overlay
            self.assertEqual(med, [(0.0, 2.11), (0.02, 2.377), (0.06, 4.6639)])
            self.assertEqual(p99, med)

    # Design: abstraction/benchmark_runner/result_plotter/design.md#forwardtimelineplots
    def test_edge_cases(self):
        self.assertEqual(plotter.percentile([5, 1, 3, 2, 4, 6, 7, 8, 9, 10], 50), 5)
        self.assertEqual(plotter.percentile([5, 1, 3, 2, 4, 6, 7, 8, 9, 10], 99), 10)
        with tempfile.TemporaryDirectory() as out:
            for pid in range(4):
                write_point(out, pid, {"ingress.lb_policy": f"p{pid}"})
            pd = lambda pid: os.path.join(out, "points", f"point_{pid:04d}")
            write_forward_log(pd(0), FWD_EXAMPLE, backends=1)                         # B = 1
            write_forward_log(pd(1), [(1, 0, "ok", 5, 5.0), (2, 1, "ok", 7, 7.0)], backends=2)
            write_forward_log(pd(2), [(1, 0, "reset", 5, 5.0)])                       # no ok row
            # point 3: no ForwardLog
            with open(os.path.join(pd(0), "ingress.forwards.csv"), "a") as f:
                f.write("70000000,0,o")                                               # truncated last line
            self.assertEqual(len(plotter.read_forward_log(os.path.join(pd(0), "ingress.forwards.csv"))), 5)
            rows, keys = plotter.build_table(out)
            figs = {f[0]: f[2] for f in plotter.forward_timelines(out, rows, keys)}
            self.assertEqual(sorted(figs), ["ewma_latency_server_0", "ewma_latency_server_1",
                                            "forward_latency_server_0", "forward_latency_server_1"])
            self.assertEqual([l[0] for l in figs["forward_latency_server_0"]],
                             ["ingress.lb_policy=p0", "ingress.lb_policy=p1"])        # p2 has no ok row
            self.assertEqual([l[0] for l in figs["forward_latency_server_1"]], ["ingress.lb_policy=p1"])
            self.assertEqual([l[0] for l in figs["ewma_latency_server_0"]],
                             ["ingress.lb_policy=p0", "ingress.lb_policy=p1", "ingress.lb_policy=p2"])
            self.assertEqual(figs["forward_latency_server_1"][0][1], [(0.0, 7e-6)])
            self.assertEqual(plotter.forward_timelines(out, [r for r in rows if r["point"] == 3], keys), [])

    # Design: abstraction/benchmark_runner/result_plotter/design.md#forwardtimelineplots
    def test_plot_files(self):
        try:
            import plotly  # noqa: F401
        except ImportError:
            self.skipTest("plotly not installed")
        with tempfile.TemporaryDirectory() as out:
            write_point(out, 0, {"load_gen.global_rate": 500})
            write_point(out, 1, {"load_gen.global_rate": 1000})
            write_forward_log(os.path.join(out, "points", "point_0000"), FWD_EXAMPLE, backends=2)
            write_forward_log(os.path.join(out, "points", "point_0001"), FWD_EXAMPLE, backends=1)
            plotter.plot(out)
            plots = os.path.join(out, "plots")
            self.assertTrue(os.path.getsize(os.path.join(plots, "forward_latency_server_0.html")) > 0)
            self.assertTrue(os.path.exists(os.path.join(plots, "ewma_latency_server_0.html")))
            self.assertFalse(os.path.exists(os.path.join(plots, "forward_latency_server_1.html")))  # no rows
            write_forward_log(os.path.join(out, "points", "point_0000"), FWD_EXAMPLE, backends=1)
            plotter.plot(out)
            self.assertTrue(os.path.exists(os.path.join(plots, "forward_latency.html")))
            self.assertFalse(os.path.exists(os.path.join(plots, "ewma_latency_server_0.html")))  # stale, removed
            for pid in (0, 1):
                os.remove(os.path.join(out, "points", f"point_{pid:04d}", "ingress.forwards.csv"))
            plotter.plot(out)
            self.assertFalse(os.path.exists(os.path.join(plots, "forward_latency.html")))


class GcOverlayTest(unittest.TestCase):
    # Worked example of result_plotter/design.md#gcoverlay.
    #
    # Design: abstraction/benchmark_runner/result_plotter/design.md#gcoverlay
    def test_worked_example(self):
        with tempfile.TemporaryDirectory() as pdir:
            json.dump({"run_start_unix_ns": 1_700_000_001_000_000_000},
                      open(os.path.join(pdir, "ingress.summary.json"), "w"))
            with open(os.path.join(pdir, "server_0.gc.csv"), "w") as f:
                f.write(plotter.GC_HEADER + "\n1700000000500000000,1,0.01,1,0.02\n"
                        "1700000001000000000,2,0.01,2,0.02\n1700000004507000000,3,0.018,5.1,0.042\n"
                        "1700000031100000000,4,0.01,1,0.02\n17000000")                    # truncated last line
            start = plotter.origin(pdir, "ingress.summary.json")
            self.assertEqual(plotter.gc_events(pdir, [0], start, 30.02),
                             [(0.0, 2, 0.01, 2.0, 0.02), (3.507, 3, 0.018, 5.1, 0.042)])
            self.assertIsNone(plotter.gc_events(pdir, [1], start, 30.02))                 # no file for k = 1
            with open(os.path.join(pdir, "server_1.gc.csv"), "w") as f:
                f.write(plotter.GC_HEADER + "\n1700000002000000000,9,0.01,1,0.02\n")
            self.assertEqual([e[1] for e in plotter.gc_events(pdir, [0, 1], start, 30.02)], [2, 9, 3])  # merged by x
            json.dump({"run_start_unix_ns": 0}, open(os.path.join(pdir, "ingress.summary.json"), "w"))
            self.assertIsNone(plotter.origin(pdir, "ingress.summary.json"))               # no /ping
            self.assertIsNone(plotter.gc_events(pdir, [0], None, 30.02))
            os.remove(os.path.join(pdir, "ingress.summary.json"))
            self.assertIsNone(plotter.origin(pdir, "ingress.summary.json"))               # older run

    # Design: abstraction/benchmark_runner/result_plotter/design.md#gcoverlay
    def test_overlay_in_figures(self):
        with tempfile.TemporaryDirectory() as out:
            write_point(out, 0, {"load_gen.global_rate": 500})
            pdir = os.path.join(out, "points", "point_0000")
            write_forward_log(pdir, FWD_EXAMPLE, backends=1)
            summary = json.load(open(os.path.join(pdir, "ingress.summary.json")))
            summary["run_start_unix_ns"] = 10**18
            json.dump(summary, open(os.path.join(pdir, "ingress.summary.json"), "w"))
            with open(os.path.join(pdir, "server_0.gc.csv"), "w") as f:
                f.write(plotter.GC_HEADER + f"\n{10**18 + 30_000_000},7,0.01,4,0.02\n{10**18 + 90_000_000},8,1,1,1\n")
            rows, keys = plotter.build_table(out)
            figs = {f[0]: f[2] for f in plotter.forward_timelines(out, rows, keys)}
            # last bin starts at 0.06 s, so t_end_s = 0.08: the cycle at 0.09 s is dropped
            self.assertEqual(figs["forward_latency"][0][3], [(0.03, 7, 0.01, 4.0, 0.02)])
            self.assertIsNone(figs["ewma_latency"][0][3])
            try:
                import plotly  # noqa: F401
            except ImportError:
                self.skipTest("plotly not installed")
            plotter.plot(out)
            with open(os.path.join(out, "plots", "forward_latency.html")) as f:
                page = f.read()
            self.assertIn("GC 7 at 0.030 s: 0.01 + 4 + 0.02 ms", page)
            with open(os.path.join(out, "plots", "ewma_latency.html")) as f:
                self.assertNotIn("GC 7", f.read())



REQ_HEADER = "request_id,client_id,seq,outcome,http_status,scheduled_ns,send_ns,end_ns,e2e_latency_ns,service_latency_ns"


def write_requests(pdir, rows, slo_ms=5, instances=1, run_start=None):
    """rows: (outcome, send_ns, end_ns), None for empty."""
    def cell(v):
        return "" if v is None else str(v)
    with open(os.path.join(pdir, "loadgen.requests.csv"), "w") as f:
        f.write(REQ_HEADER + "\n")
        for i, (o, s, e) in enumerate(rows):
            f.write(f"{i},0,{i},{o},,0,{cell(s)},{cell(e)},,\n")
    params = json.load(open(os.path.join(pdir, "params.json")))
    params["loadgen_args"] = [["slo_ms", slo_ms]]
    params["server_instances"] = instances
    json.dump(params, open(os.path.join(pdir, "params.json"), "w"))
    if run_start is not None:
        json.dump({**json.load(open(os.path.join(pdir, "loadgen.summary.json"))), "run_start_unix_ns": run_start},
                  open(os.path.join(pdir, "loadgen.summary.json"), "w"))


# The AvailabilityPlot worked example (result_plotter/design.md).
AVAIL_EXAMPLE = [("ok", 0, 10_000_000), ("ok", 0, 80_000_000), ("cancelled", 140_000_000, 190_000_000),
                 ("ok", 210_000_000, 250_000_000), ("ok", 250_000_000, 300_000_000), ("not_sent", None, None),
                 ("rejected", 660_000_000, 700_000_000)]


class AvailabilityPlotTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/result_plotter/design.md#availabilityplot
    def test_worked_example(self):
        with tempfile.TemporaryDirectory() as out:
            write_point(out, 0, {"load_gen.global_rate": 500})
            write_requests(os.path.join(out, "points", "point_0000"), AVAIL_EXAMPLE, slo_ms=50)
            rows = plotter.read_requests(os.path.join(out, "points", "point_0000", "loadgen.requests.csv"))
            self.assertEqual(len(rows), 6)  # not_sent ignored
            self.assertEqual(plotter.availability_series(rows, 50_000_000), [(0.1, 1 / 3), (0.3, 1.0), (0.7, 0.0)])
            table, keys = plotter.build_table(out)
            (label, s, gc, color), = plotter.availability_lines(out, table, keys)
            self.assertEqual((label, gc, color), ("load_gen.global_rate=500", None, 0))   # no run_start: no overlay

    # Design: abstraction/benchmark_runner/result_plotter/design.md#availabilityplot
    # Design: abstraction/benchmark_runner/result_plotter/design.md#gcoverlay
    def test_overlay_all_instances_and_edge_cases(self):
        with tempfile.TemporaryDirectory() as out:
            for pid in range(3):
                write_point(out, pid, {"load_gen.global_rate": 500 * (pid + 1)})
            pd = lambda pid: os.path.join(out, "points", f"point_{pid:04d}")
            write_requests(pd(0), AVAIL_EXAMPLE, slo_ms=50, instances=2, run_start=10**18)
            for k, t in ((0, 10**18 + 15_000_000), (1, 10**18 + 50_000_000)):
                with open(os.path.join(pd(0), f"server_{k}.gc.csv"), "w") as f:
                    f.write(plotter.GC_HEADER + f"\n{t},{k + 1},0.01,3,0.02\n{10**18 + 900_000_000},99,1,1,1\n")
            write_requests(pd(1), [("not_sent", None, None)])                             # nothing finished
            with open(os.path.join(pd(2), "loadgen.requests.csv"), "w") as f:
                f.write(REQ_HEADER + "\n0,0,0,ok,202,0,1,2,1,1\n0,0,1,o")                  # truncated, slo missing
            table, keys = plotter.build_table(out)
            lines = plotter.availability_lines(out, table, keys)
            self.assertEqual([l[0] for l in lines], ["load_gen.global_rate=500"])
            # t_end_s = 0.7 + 0.1 = 0.8: cycles at 0.015 s (instance 0) and 0.05 s (instance 1); 0.9 s dropped
            self.assertEqual([(round(e[0], 3), e[1]) for e in lines[0][2]], [(0.015, 1), (0.05, 2)])
            try:
                import plotly  # noqa: F401
            except ImportError:
                self.skipTest("plotly not installed")
            plotter.plot(out)
            path = os.path.join(out, "plots", "availability.html")
            with open(path) as f:
                self.assertIn("GC 2 at 0.050 s", f.read())
            for pid in range(3):
                os.remove(os.path.join(pd(pid), "loadgen.requests.csv"))
            plotter.plot(out)
            self.assertFalse(os.path.exists(path))                                         # stale page removed


if __name__ == "__main__":
    unittest.main()
