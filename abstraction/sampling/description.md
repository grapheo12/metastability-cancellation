# Sampling — Description

Shared leaf component for reproducible randomness:

- A **seeded pseudo-random generator** with a fixed, language-independent algorithm, so that every
  implementation with the same seed produces the same sequence.
- An **ExponentialSampler** used for both Poisson inter-arrival gaps (load generator) and
  ServiceTime (server).
- **SeedDerivation**, which gives each client and each server connection its own independent stream
  from one base `seed` parameter.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
