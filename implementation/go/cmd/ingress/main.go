// Command ingress runs the Ingress: a load-balancing /ping proxy with an
// in-process LoadCanceller.
//
// Source: abstraction/ingress/api.md (ingress v0.13.0)
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"testbed/internal/ingress"
)

// API: abstraction/ingress/api.md#lifecycle
func main() {
	p := ingress.DefaultParams()
	fs := flag.NewFlagSet("ingress", flag.ContinueOnError)
	fs.StringVar(&p.ListenAddr, "listen_addr", p.ListenAddr, "host:port to accept client connections on")
	fs.StringVar(&p.Backends, "backends", p.Backends, "comma-separated host:port of the servers (required)")
	fs.StringVar(&p.NumClients, "num_clients", p.NumClients, "number of load-generator clients (required)")
	fs.StringVar(&p.LbPolicy, "lb_policy", p.LbPolicy, "round_robin | least_outstanding | power_of_two | ewma")
	fs.StringVar(&p.LbRetryPolicy, "lb_retry_policy", p.LbRetryPolicy, "RetryPolicy for LbRetries (registered name; none: never retry)")
	fs.StringVar(&p.LbRetryTimeoutMs, "lb_retry_timeout_ms", p.LbRetryTimeoutMs, "timeout RetryPolicy: retry once the newest attempt has run this long, ms (> 0)")
	fs.StringVar(&p.LbRetryAnxietyRatio, "lb_retry_anxiety_ratio", p.LbRetryAnxietyRatio, "anxious RetryPolicy: retry when moving is expected to finish at least this fraction sooner (>= 0)")
	fs.StringVar(&p.LbRetryLateFactor, "lb_retry_late_factor", p.LbRetryLateFactor, "anxious RetryPolicy: retry only if CurrentExpFinish > this x time left (>= 0)")
	fs.StringVar(&p.LbMaxRetries, "lb_max_retries", p.LbMaxRetries, "most LbRetries per client stream (>= 0; 0: no LB retries)")
	fs.StringVar(&p.LbFilter, "lb_filter", p.LbFilter, "none | deadline_mean | deadline_mean_per_worker | deadline_mean_queue_plus_share | deadline_mmk (DeadlineFilter strategy; no backend passes: 503)")
	fs.StringVar(&p.EwmaBeta, "ewma_beta", p.EwmaBeta, "EwmaLatency weight of the old value, in [0, 1]")
	fs.StringVar(&p.CancelRate, "cancel_rate", p.CancelRate, "LoadCanceller rate in cancels/s (0 = off)")
	fs.StringVar(&p.DurationS, "duration_s", p.DurationS, "LoadCanceller sending phase in s (required if cancel_rate > 0)")
	fs.StringVar(&p.Seed, "seed", p.Seed, "base seed")
	fs.StringVar(&p.HeartbeatTimeoutMs, "heartbeat_timeout_ms", p.HeartbeatTimeoutMs, "ms without a HeartbeatLine before a backend gets no new requests (>= 0; 0: off)")
	fs.StringVar(&p.InitialStallMs, "initial_stall_ms", p.InitialStallMs, "ms after the first /ping before the first cancel (>= 0)")
	fs.StringVar(&p.OutputPath, "output_path", p.OutputPath, "output path prefix")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	cfg, err := ingress.Validate(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
	ing, err := ingress.Listen(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
	go ing.Serve()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	if err := ing.Shutdown(); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}
