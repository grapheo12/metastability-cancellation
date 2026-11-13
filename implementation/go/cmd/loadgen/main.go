// Command loadgen runs the closed-loop Poisson load generator.
//
// Source: abstraction/load_generator/api.md (load_generator v0.5.1)
package main

import (
	"flag"
	"fmt"
	"os"

	"testbed/internal/loadgen"
)

// API: abstraction/load_generator/run_controller/api.md#mainparams---exit-status
func main() {
	p := loadgen.DefaultParams()
	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	fs.StringVar(&p.Target, "target", p.Target, "server host:port")
	fs.StringVar(&p.NumClients, "num_clients", p.NumClients, "number of clients N (required)")
	fs.StringVar(&p.GlobalRate, "global_rate", p.GlobalRate, "total offered rate in requests/s (required)")
	fs.StringVar(&p.RequestBytes, "request_bytes", p.RequestBytes, "exact request body size (required, >= 76)")
	fs.StringVar(&p.SloMs, "slo_ms", p.SloMs, "SLO in ms (required)")
	fs.StringVar(&p.DurationS, "duration_s", p.DurationS, "active phase length in s (required)")
	fs.StringVar(&p.Seed, "seed", p.Seed, "base seed")
	fs.StringVar(&p.NWaitingRequests, "n_waiting_requests", p.NWaitingRequests, "most requests one client keeps outstanding (1..=1000)")
	fs.StringVar(&p.RetryTimeoutMs, "retry_timeout_ms", p.RetryTimeoutMs, "RetryTimer of each request copy in ms (> 0)")
	fs.StringVar(&p.MaxRetries, "max_retries", p.MaxRetries, "request copies sent after the first (>= 0)")
	fs.StringVar(&p.InitialStallMs, "initial_stall_ms", p.InitialStallMs, "StallWindow in ms: requests first sent earlier never retry and are left out of the metrics")
	fs.StringVar(&p.OutputPath, "output_path", p.OutputPath, "output path prefix")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	cfg, err := loadgen.Validate(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
	if err := loadgen.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}
