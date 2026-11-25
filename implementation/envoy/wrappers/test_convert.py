"""Unit tests for the impl/envoy wrappers' conversions (python3 -m unittest discover -s implementation/envoy/wrappers)."""
import csv
import json
import os
import tempfile
import unittest

import convert


class IngressConversion(unittest.TestCase):
    def test_outcomes(self):
        self.assertEqual(convert.forward_outcome(202, "-"), "ok")
        self.assertEqual(convert.forward_outcome(503, "-"), "rejected")
        self.assertEqual(convert.forward_outcome(400, "-"), "bad_request")
        self.assertEqual(convert.forward_outcome(503, "UF"), "bad_gateway")  # upstream connection failure
        self.assertEqual(convert.forward_outcome(0, "DC"), "reset")          # client went away
        self.assertEqual(convert.forward_outcome(502, "-"), "bad_gateway")

    def test_access_log_to_forwards_and_summary(self):
        lines = [
            "1700000000.100000000,400000000,399000000,10.0.0.1:8080,202,-,1",
            "1700000000.000000000,500000000,498000000,10.0.0.2:8080,503,-,1",
            "1700000001.000000000,1000,-,-,503,UH,0",                    # no backend reached: not a forward
            "1700000002.000000000,7000000,6000000,10.0.0.1:8080,0,DC,1",  # client reset
            "garbage",
        ]
        backends = ["10.0.0.1:8080", "10.0.0.2:8080"]
        with tempfile.TemporaryDirectory() as d:
            raw = os.path.join(d, "a.raw")
            open(raw, "w").write("\n".join(lines) + "\n")
            rows = convert.parse_access_log(raw)
            self.assertEqual(len(rows), 4)
            out = os.path.join(d, "f.csv")
            run_start = convert.write_forwards(rows, backends, out)
            self.assertEqual(run_start, 1_700_000_000_000_000_000)
            got = list(csv.reader(open(out)))
            self.assertEqual(",".join(got[0]), convert.FORWARDS_HEADER)
            # sorted by end time: server 0's ok ends at 0.5 s, server 1's rejected at 0.5 s too (stable), reset last
            # EwmaLatency (beta 0.7, start 1 ms): server 0's ok moves it, the rejected and the reset do not
            self.assertEqual([r[4] for r in got[1:]], [repr(1e6 * 0.7 + 399e6 * (1 - 0.7)), "1000000.0",
                                                       repr(1e6 * 0.7 + 399e6 * (1 - 0.7))])
            self.assertEqual([(r[0], r[1], r[2], r[3]) for r in got[1:]],
                             [("500000000", "0", "ok", "399000000"), ("500000000", "1", "rejected", "498000000"),
                              ("2007000000", "0", "reset", "6000000")])
            s = convert.ingress_summary(rows, backends, {"x": 1}, run_start, 3.5)
            self.assertEqual([(b["forwarded"], b["ok"], b["rejected"], b["reset"]) for b in s["backends"]],
                             [(2, 1, 0, 1), (1, 0, 1, 0)])
            self.assertIsNone(s["backends"][0]["ewma_exec_ns"])
            self.assertEqual(s["lb_retries"], 0)


class PeakEwmaTraceMerge(unittest.TestCase):
    def test_merge(self):
        backends = ["10.0.0.1:8080", "10.0.0.2:8080"]
        with tempfile.TemporaryDirectory() as d:
            pre = os.path.join(d, "ing.peak_ewma")
            open(pre + ".ewma.0.csv", "w").write(
                "t_unix_ns,host,rtt_ms,dt_ns,alpha,ewma_before_ms,ewma_after_ms\n"
                "1000000300,10.0.0.2:8080,400,25000000,0.0025,0,400\n"
                "1000000100,10.0.0.1:8080,390,-5,1,401,390\n"
                "1000000200,9.9.9.9:1,1,1,1,1,1\n"          # not a backend: dropped
                "1000000400,10.0.0.1:8080,3")                # truncated last line: dropped
            open(pre + ".ewma.1.csv", "w").write(
                "t_unix_ns,host,rtt_ms,dt_ns,alpha,ewma_before_ms,ewma_after_ms\n"
                "1000000200,10.0.0.1:8080,410,100,1e-08,390,390.000004\n")
            open(pre + ".picks.0.csv", "w").write(
                "t_unix_ns,host_a,active_a,ewma_a_ms,cost_a,host_b,active_b,ewma_b_ms,cost_b,chosen\n"
                "1000000500,10.0.0.2:8080,3,400,1600,10.0.0.1:8080,1,390,780,b\n")
            open(pre + ".picks.1.csv", "w").write(
                "t_unix_ns,host_a,active_a,ewma_a_ms,cost_a,host_b,active_b,ewma_b_ms,cost_b,chosen\n")
            out = os.path.join(d, "ing")
            self.assertEqual(convert.merge_peak_ewma(pre, backends, 1_000_000_000, out), (3, 1))
            up = open(out + ".peak_ewma_updates.csv").read().splitlines()
            self.assertEqual(up, [convert.PEAK_EWMA_UPDATES_HEADER,
                                  "100,0,390,-5,1,401,390",
                                  "200,0,410,100,1e-08,390,390.000004",
                                  "300,1,400,25000000,0.0025,0,400"])
            pk = open(out + ".peak_ewma_picks.csv").read().splitlines()
            self.assertEqual(pk, [convert.PEAK_EWMA_PICKS_HEADER, "500,1,3,400,1600,0,1,390,780,b"])
            self.assertEqual(sorted(f for f in os.listdir(d) if ".peak_ewma." in f), [])  # raw files removed

    def test_no_trace(self):
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "ing")
            self.assertEqual(convert.merge_peak_ewma(out + ".peak_ewma", ["a:1"], 0, out), (0, 0))
            self.assertEqual(os.listdir(d), [])  # no outputs without trace files


class LoadgenConversion(unittest.TestCase):
    def write_logs(self, d):
        header = "id,result,http_status,scheduled_unix_ns,send_unix_ns,end_unix_ns,exec_ns,start_slack_ns\n"
        c0 = 0 << 32
        c1 = 1 << 32
        open(os.path.join(d, "lg.nh.0.csv"), "w").write(header + "\n".join([
            f"{c0 | 1},response,202,1000000000,1000000100,1400000100,400000000,4600000000",
            f"{c0 | 0},response,503,500000000,500000050,500001050,1000,-5",
            f"{c0 | 2},reset,0,9000000000,9000000100,40000000000,,",
        ]) + "\n")
        open(os.path.join(d, "lg.nh.1.csv"), "w").write(header + "\n".join([
            f"{c1 | 0},response,202,200000000,200000000,7200000000,1,2",   # 7 s: late against a 5 s SLO
            f"{c1 | 1},pool_failure,0,300000000,0,300000010,,",
        ]) + "\n")
        return os.path.join(d, "lg.nh")

    def test_records_and_summary(self):
        with tempfile.TemporaryDirectory() as d:
            rows = convert.read_request_logs(self.write_logs(d))
            recs, run_start = convert.requests_records(rows, initial_stall_ms=0)
            self.assertEqual(run_start, 200_000_000)
            self.assertEqual([(r["client_id"], r["seq"], r["outcome"]) for r in recs],
                             [(0, 0, "rejected"), (0, 1, "ok"), (0, 2, "cancelled"), (1, 0, "ok"), (1, 1, "error")])
            ok = recs[1]
            self.assertEqual((ok["scheduled_ns"], ok["send_ns"], ok["end_ns"]), (800_000_000, 800_000_100,
                                                                               1_200_000_100))
            self.assertEqual((ok["e2e_latency_ns"], ok["service_latency_ns"]), (400_000_100, 400_000_000))
            self.assertEqual((ok["exec_ns"], ok["start_slack_ns"]), ("400000000", "4600000000"))
            pf = recs[4]
            self.assertIsNone(pf["send_ns"])
            self.assertEqual(pf["copies"], 0)
            out = os.path.join(d, "r.csv")
            convert.write_requests(recs, out)
            self.assertEqual(open(out).readline().strip(), convert.REQUESTS_HEADER)
            s = convert.loadgen_summary(recs, {"duration_s": 10, "slo_ms": 5000, "initial_stall_ms": 0}, run_start,
                                        41.0)
            c = s["counts"]
            self.assertEqual((c["scheduled"], c["sent"], c["not_sent"], c["ok"], c["ok_late"], c["rejected"],
                              c["cancelled"], c["error"]), (5, 4, 1, 2, 1, 1, 1, 0))
            self.assertAlmostEqual(s["rates_per_s"]["goodput"], 0.1)  # (2 ok - 1 late) / 10 s
            self.assertEqual(s["e2e_latency_us"]["count"], 2)
            json.dumps(s)

    def test_stall_window_and_percentiles(self):
        recs = [{"in_stall_window": 1, "send_ns": 5, "outcome": "ok"}]
        s = convert.loadgen_summary(recs, {"duration_s": 2, "slo_ms": 1, "initial_stall_ms": 1000}, 0, 1.0)
        self.assertEqual((s["counts"]["stall_window"], s["counts"]["scheduled"]), (1, 0))
        st = convert.latency_stats([i * 1000 for i in range(1, 11)])  # 1..10 µs
        self.assertEqual((st["p50"], st["p90"], st["p99"], st["p999"], st["max"]), (5, 9, 10, 10, 10))
        self.assertIsNone(convert.latency_stats([])["p50"])


if __name__ == "__main__":
    unittest.main()
