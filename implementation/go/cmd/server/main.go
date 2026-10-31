// Command server runs the HTTP/2 /ping + /cancel server.
//
// Source: abstraction/server/api.md (server v0.2.0)
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
