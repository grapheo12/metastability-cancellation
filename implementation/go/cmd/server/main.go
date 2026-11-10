// Command server runs the HTTP/2 /ping + /cancel server.
//
// Source: abstraction/server/api.md (server v0.5.0)
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"testbed/internal/server"
)

// API: abstraction/server/api.md#lifecycle
func main() {
	p := server.DefaultParams()
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.StringVar(&p.ListenAddr, "listen_addr", p.ListenAddr, "host:port to accept connections on")
	fs.StringVar(&p.ResponseRate, "response_rate", p.ResponseRate, "service rate in requests/s (required)")
	fs.StringVar(&p.ResponseBytes, "response_bytes", p.ResponseBytes, "exact /ping response body size (required, >= 42)")
	fs.StringVar(&p.CancelMode, "cancel_mode", p.CancelMode, "ignore | cooperative")
	fs.StringVar(&p.CancelCheckIntervalUs, "cancel_check_interval_us", p.CancelCheckIntervalUs, "RST check interval in µs")
	fs.StringVar(&p.Seed, "seed", p.Seed, "base seed")
	fs.StringVar(&p.NWorkerThreads, "n_worker_threads", p.NWorkerThreads, "number of WorkerPool workers (>= 1)")
	fs.StringVar(&p.InitialStallMs, "initial_stall_ms", p.InitialStallMs, "ms after the first /ping before any /ping runs (>= 0)")
	fs.StringVar(&p.QueueLogPath, "queue_log_path", p.QueueLogPath, "QueueLengthLog CSV path (empty: no log)")
	fs.StringVar(&p.QueueSampleIntervalMs, "queue_sample_interval_ms", p.QueueSampleIntervalMs, "QueueLengthLog sample interval in ms (>= 1)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	cfg, err := server.Validate(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
	s, err := server.Listen(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
	go s.Serve()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	s.Shutdown()
	os.Exit(0)
}
