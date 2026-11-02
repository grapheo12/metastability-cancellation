// Command canceller runs the LoadCanceller.
//
// Source: abstraction/load_canceller/api.md (load_canceller v0.2.0)
package main

import (
	"flag"
	"fmt"
	"os"

	"testbed/internal/canceller"
)

// API: abstraction/load_canceller/api.md#exit-status
func main() {
	p := canceller.DefaultParams()
	fs := flag.NewFlagSet("canceller", flag.ContinueOnError)
	fs.StringVar(&p.Target, "target", p.Target, "server host:port")
	fs.StringVar(&p.NumClients, "num_clients", p.NumClients, "number of load-generator clients N (required)")
	fs.StringVar(&p.CancelRate, "cancel_rate", p.CancelRate, "cancel arrival rate in cancels/s (required)")
	fs.StringVar(&p.IncrRandMax, "incr_rand_max", p.IncrRandMax, "largest step past the reported FinishedWatermark")
	fs.StringVar(&p.DurationS, "duration_s", p.DurationS, "sending phase length in s (required)")
	fs.StringVar(&p.Seed, "seed", p.Seed, "base seed")
	fs.StringVar(&p.OutputPath, "output_path", p.OutputPath, "output path prefix")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	cfg, err := canceller.Validate(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
	if err := canceller.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}
