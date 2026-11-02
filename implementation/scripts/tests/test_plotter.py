# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/result_plotter/design.md (result_plotter v0.3.0)
import csv
import json
import os
import tempfile
import unittest

from benchrunner import plotter


def write_point(out, pid, swept, done=True, ok=90):
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

    # Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
    def test_plots(self):
        try:
            import matplotlib  # noqa: F401
        except ImportError:
            self.skipTest("matplotlib not installed")
        with tempfile.TemporaryDirectory() as out:
            pid = 0
            for rate in (1000, 2000):
                for mode in ("ignore", "cooperative"):
                    write_point(out, pid, {"load_gen.global_rate": rate, "server.cancel_mode": mode}, ok=80 + pid)
                    pid += 1
            plotter.plot(out)
            for name in ("latency_e2e", "goodput", "outcomes", "server_counters", "throughput_latency",
                         "server_counters_normalized"):
                self.assertTrue(os.path.getsize(os.path.join(out, "plots", f"{name}.png")) > 0)

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

    def test_nothing_to_plot(self):
        try:
            import matplotlib  # noqa: F401
        except ImportError:
            self.skipTest("matplotlib not installed")
        with tempfile.TemporaryDirectory() as out:
            write_point(out, 0, {}, done=False)
            with self.assertRaises(plotter.NothingToPlot):
                plotter.plot(out)


if __name__ == "__main__":
    unittest.main()
