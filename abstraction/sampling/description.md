# Sampling — Description

Leaf component, shared. It provides reproducible randomness:

- A **seeded pseudo-random generator** with a fixed, language-independent algorithm, so that every
  implementation with the same seed produces the same sequence.
- An **ExponentialSampler** used for Poisson inter-arrival gaps (load generator and load canceller)
  and for ServiceTime (server).
- A **UniformIntSampler** for unbiased integers in a closed range (load canceller's client choice
  and counter step).
- **SeedDerivation**, which gives each client, each server connection and the canceller its own
  independent stream from one base `seed` parameter.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
