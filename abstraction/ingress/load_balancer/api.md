# Load Balancer — API

## Types

### `LbPolicy`

`round_robin` \| `least_outstanding` \| `power_of_two`, from the Ingress parameter `lb_policy`
([../api.md#program-parameters](../api.md#program-parameters)). See [design.md#lbpolicy](design.md#lbpolicy).

### `ForwardOutcome`

How a forward ended: `ok` (`202` relayed), `rejected` (`503` relayed), `bad_request` (`400` relayed),
`bad_gateway` (`502` sent), `reset` (the client reset its stream). Each maps to the BackendStats field of the same
name ([../api.md#backendstats](../api.md#backendstats)).

### `LoadBalancer`

The policy, the BackendStats array, the round-robin counter and the policy's random stream. Safe for concurrent
callers. See [design.md#loadbalancer](design.md#loadbalancer).

## Functions

### `lb_new(config: IngressConfig) -> LoadBalancer`
`B = len(config.backends)` BackendStats, all zero. The random stream is
`rng_new(derive_seed(config.seed, STREAM_INGRESS_LB, 0))`. See [design.md#loadbalancer](design.md#loadbalancer).

### `pick(lb) -> server_id: u32`
Chooses a backend by the policy, then increments its `outstanding` and `forwarded`. Every `pick` must be followed by
exactly one `finish` for the same backend. See [design.md#lbpolicy](design.md#lbpolicy).

### `finish(lb, server_id: u32, outcome: ForwardOutcome) -> nothing`
Decrements `outstanding` and increments the outcome's counter. See [design.md#loadbalancer](design.md#loadbalancer).

### `stats_snapshot(lb) -> list of BackendStats values`
Reads every counter (acquire), in `server_id` order, for the summary. See [design.md#loadbalancer](design.md#loadbalancer).

## Uses

- [../../sampling/api.md](../../sampling/api.md): `derive_seed`, `rng_new`, `uniform_int`, `STREAM_INGRESS_LB`
