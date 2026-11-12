// Command ingress runs the Ingress: a load-balancing /ping proxy with an
// in-process LoadCanceller.
//
// Source: abstraction/ingress/api.md (ingress v0.1.17)
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
	fs.StringVar(&p.EwmaBeta, "ewma_beta", p.EwmaBeta, "EwmaLatency weight of the old value, in [0, 1]")
	fs.StringVar(&p.CancelRate, "cancel_rate", p.CancelRate, "LoadCanceller rate in cancels/s (0 = off)")
	fs.StringVar(&p.DurationS, "duration_s", p.DurationS, "LoadCanceller sending phase in s (required if cancel_rate > 0)")
	fs.StringVar(&p.Seed, "seed", p.Seed, "base seed")
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
