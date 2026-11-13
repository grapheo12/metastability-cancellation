// Source: abstraction/load_generator/metrics/design.md (metrics v0.3.0)
package loadgen

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strconv"
)

// RequestRecord is one output row per scheduled arrival. HTTPStatus 0 means
// "empty"; SendNs/EndNs are meaningful only when Sent is true.
//
// Design: abstraction/load_generator/metrics/design.md#requestrecord
type RequestRecord struct {
	RequestID   uint64
	ClientID    uint32
	Seq         uint32
	Outcome     Outcome
	HTTPStatus  int
	ScheduledNs int64
	SendNs      int64
	EndNs       int64
	Sent        bool
	Copies      uint32 // RequestCopies started; 0 for not_sent
}

// LatencyStats uses pointers so that an empty set serializes as null.
type LatencyStats struct {
	Count int      `json:"count"`
	Mean  *float64 `json:"mean"`
	P50   *float64 `json:"p50"`
	P90   *float64 `json:"p90"`
	P99   *float64 `json:"p99"`
	P999  *float64 `json:"p999"`
	Max   *float64 `json:"max"`
}

type summaryConfig struct {
	Target           string  `json:"target"`
	NumClients       uint32  `json:"num_clients"`
	GlobalRate       float64 `json:"global_rate"`
	RequestBytes     int     `json:"request_bytes"`
	SloMs            float64 `json:"slo_ms"`
	DurationS        float64 `json:"duration_s"`
	Seed             uint64  `json:"seed"`
	NWaitingRequests uint32  `json:"n_waiting_requests"`
	RetryTimeoutMs   float64 `json:"retry_timeout_ms"`
	MaxRetries       uint32  `json:"max_retries"`
	OutputPath       string  `json:"output_path"`
}

type summaryCounts struct {
	Scheduled  int `json:"scheduled"`
	Sent       int `json:"sent"`
	OK         int `json:"ok"`
	Rejected   int `json:"rejected"`
	Cancelled  int `json:"cancelled"`
	Dropped    int `json:"dropped"`
	Error      int `json:"error"`
	NotSent    int `json:"not_sent"`
	CopiesSent int `json:"copies_sent"`
}

type summaryRates struct {
	Offered float64 `json:"offered"`
	Sent    float64 `json:"sent"`
	Goodput float64 `json:"goodput"`
}

// SummaryReport has the field order of the summary JSON.
//
// API: abstraction/load_generator/metrics/api.md#summary-json
type SummaryReport struct {
	Config           summaryConfig `json:"config"`
	RunEndS          float64       `json:"run_end_s"`
	RunStartUnixNs   uint64        `json:"run_start_unix_ns"` // wall clock of run_start, reported only
	Counts           summaryCounts `json:"counts"`
	RatesPerS        summaryRates  `json:"rates_per_s"`
	E2ELatencyUs     LatencyStats  `json:"e2e_latency_us"`
	ServiceLatencyUs LatencyStats  `json:"service_latency_us"`
}

// Aggregate computes counts, rates (over duration_s) and latency stats over ok records.
//
// Design: abstraction/load_generator/metrics/design.md#collection
// Design: abstraction/load_generator/metrics/design.md#summary
func Aggregate(records []RequestRecord, cfg Config, runEndNs int64, runStartUnixNs uint64) SummaryReport {
	var r SummaryReport
	r.Config = summaryConfig{cfg.Target, cfg.NumClients, cfg.GlobalRate, cfg.RequestBytes, cfg.SloMs, cfg.DurationS, cfg.Seed, cfg.NWaitingRequests, cfg.RetryTimeoutMs, cfg.MaxRetries, cfg.OutputPath}
	r.RunEndS = float64(runEndNs) / 1e9
	r.RunStartUnixNs = runStartUnixNs
	var e2e, service []float64
	for _, rec := range records {
		r.Counts.CopiesSent += int(rec.Copies)
		switch rec.Outcome {
		case OutcomeOK:
			r.Counts.OK++
			e2e = append(e2e, float64(rec.EndNs-rec.ScheduledNs)/1000.0)
			service = append(service, float64(rec.EndNs-rec.SendNs)/1000.0)
		case OutcomeRejected:
			r.Counts.Rejected++
		case OutcomeCancelled:
			r.Counts.Cancelled++
		case OutcomeDropped:
			r.Counts.Dropped++
		case OutcomeError:
			r.Counts.Error++
		case OutcomeNotSent:
			r.Counts.NotSent++
		}
	}
	r.Counts.Scheduled = len(records)
	r.Counts.Sent = r.Counts.Scheduled - r.Counts.NotSent
	r.RatesPerS = summaryRates{
		Offered: float64(r.Counts.Scheduled) / cfg.DurationS,
		Sent:    float64(r.Counts.Sent) / cfg.DurationS,
		Goodput: float64(r.Counts.OK) / cfg.DurationS,
	}
	r.E2ELatencyUs = latencyStats(e2e)
	r.ServiceLatencyUs = latencyStats(service)
	return r
}

// latencyStats uses nearest-rank percentiles with exact integer rank arithmetic.
//
// Design: abstraction/load_generator/metrics/design.md#percentiles
func latencyStats(values []float64) LatencyStats {
	n := len(values)
	if n == 0 {
		return LatencyStats{}
	}
	v := append([]float64(nil), values...)
	sort.Float64s(v)
	pct := func(pMilli int) *float64 {
		x := v[(pMilli*n+99999)/100000-1]
		return &x
	}
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	mean, max := sum/float64(n), v[n-1]
	return LatencyStats{Count: n, Mean: &mean, P50: pct(50000), P90: pct(90000), P99: pct(99000), P999: pct(99900), Max: &max}
}

// WriteRequestsCSV writes records sorted by (client_id, seq).
//
// API: abstraction/load_generator/metrics/api.md#requests-csv
// Design: abstraction/load_generator/metrics/design.md#requestrecord
func WriteRequestsCSV(path string, records []RequestRecord) error {
	sorted := append([]RequestRecord(nil), records...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ClientID != sorted[j].ClientID {
			return sorted[i].ClientID < sorted[j].ClientID
		}
		return sorted[i].Seq < sorted[j].Seq
	})
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	w.WriteString("request_id,client_id,seq,outcome,http_status,scheduled_ns,send_ns,end_ns,e2e_latency_ns,service_latency_ns,copies\n")
	for _, r := range sorted {
		status, send, end, e2e, svc := "", "", "", "", ""
		if r.HTTPStatus != 0 {
			status = strconv.Itoa(r.HTTPStatus)
		}
		if r.Sent {
			send, end = strconv.FormatInt(r.SendNs, 10), strconv.FormatInt(r.EndNs, 10)
			e2e, svc = strconv.FormatInt(r.EndNs-r.ScheduledNs, 10), strconv.FormatInt(r.EndNs-r.SendNs, 10)
		}
		w.WriteString(strconv.FormatUint(r.RequestID, 10) + "," + strconv.FormatUint(uint64(r.ClientID), 10) + "," +
			strconv.FormatUint(uint64(r.Seq), 10) + "," + string(r.Outcome) + "," + status + "," +
			strconv.FormatInt(r.ScheduledNs, 10) + "," + send + "," + end + "," + e2e + "," + svc + "," +
			strconv.FormatUint(uint64(r.Copies), 10) + "\n")
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// WriteSummaryJSON writes the SummaryReport.
//
// API: abstraction/load_generator/metrics/api.md#summary-json
func WriteSummaryJSON(path string, report SummaryReport) error {
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
