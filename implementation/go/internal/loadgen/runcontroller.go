// Source: abstraction/load_generator/run_controller/design.md (run_controller v0.1.1)
package loadgen

import (
	"errors"
	"math"
	"net"
	"strconv"
	"sync"
	"time"

	"testbed/internal/h2client"
	"testbed/internal/protocol"
	"testbed/internal/sampling"
)

// Validate turns Params into a Config with derived values, or a one-line error.
//
// Design: abstraction/load_generator/run_controller/design.md#configuration-validation
func Validate(p Params) (Config, error) {
	var c Config
	n, err := strconv.ParseUint(p.NumClients, 10, 32)
	if p.NumClients == "" || err != nil || n < 1 {
		return c, errors.New("num_clients must be an integer in 1 ..= 2^32-1")
	}
	c.NumClients = uint32(n)
	rate, err := parsePositive(p.GlobalRate)
	if err != nil {
		return c, errors.New("global_rate " + err.Error())
	}
	c.GlobalRate = rate
	rb, err := strconv.Atoi(p.RequestBytes)
	if p.RequestBytes == "" || err != nil {
		return c, errors.New("request_bytes must be an integer (required)")
	}
	if err := protocol.ValidateBodySize(rb); err != nil {
		return c, errors.New("request_bytes: " + err.Error())
	}
	c.RequestBytes = rb
	if c.SloMs, err = parsePositive(p.SloMs); err != nil {
		return c, errors.New("slo_ms " + err.Error())
	}
	if c.DurationS, err = parsePositive(p.DurationS); err != nil {
		return c, errors.New("duration_s " + err.Error())
	}
	if c.Seed, err = strconv.ParseUint(p.Seed, 10, 64); err != nil {
		return c, errors.New("seed must be an unsigned 64-bit integer")
	}
	if _, port, err := net.SplitHostPort(p.Target); err != nil || port == "" {
		return c, errors.New("target must be host:port")
	}
	c.Target = p.Target
	c.OutputPath = p.OutputPath
	c.PerClientRate = c.GlobalRate / float64(c.NumClients)
	c.SloNs = int64(math.Round(c.SloMs * 1e6))
	c.DurationNs = int64(math.Round(c.DurationS * 1e9))
	return c, nil
}

func parsePositive(s string) (float64, error) {
	if s == "" {
		return 0, errors.New("is required")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || !(v > 0) || math.IsInf(v, 0) {
		return 0, errors.New("must be a finite number > 0")
	}
	return v, nil
}

// Run opens N connections in client-id order, runs all clients from a shared
// run_start, waits for them (active phase + DrainPhase), closes the
// connections and writes both outputs.
//
// Design: abstraction/load_generator/run_controller/design.md#startup
// Design: abstraction/load_generator/design.md#structure
// Design: abstraction/load_generator/design.md#phases
func Run(cfg Config) error {
	conns := make([]*h2client.Conn, 0, cfg.NumClients)
	for i := uint32(0); i < cfg.NumClients; i++ {
		c, err := h2client.Dial(cfg.Target)
		if err != nil {
			for _, c := range conns {
				c.Close()
			}
			return err
		}
		conns = append(conns, c)
	}

	// Design: abstraction/load_generator/design.md#concurrency
	runStart := time.Now()
	results := make([][]RequestRecord, cfg.NumClients)
	var wg sync.WaitGroup
	for i := uint32(0); i < cfg.NumClients; i++ {
		rng := sampling.NewRng(sampling.DeriveSeed(cfg.Seed, sampling.StreamClientArrivals, i))
		wg.Add(1)
		go func(i uint32) {
			defer wg.Done()
			results[i] = RunClient(i, conns[i], cfg, rng, runStart)
		}(i)
	}
	// Design: abstraction/load_generator/run_controller/design.md#drainphase
	wg.Wait()
	runEnd := nowNs(runStart)

	// Design: abstraction/load_generator/run_controller/design.md#shutdown-ordering
	for _, c := range conns {
		c.Close()
	}
	var all []RequestRecord
	for _, r := range results {
		all = append(all, r...)
	}
	report := Aggregate(all, cfg, runEnd)
	if err := WriteRequestsCSV(cfg.OutputPath+".requests.csv", all); err != nil {
		return err
	}
	return WriteSummaryJSON(cfg.OutputPath+".summary.json", report)
}
