# Retry Policy — Description

Leaf component. It decides **when the Ingress retries a request itself** (an **LbRetry**), independently of the
load-generator client's own RetryTimers:

- **RetryPolicy**: a named decision function, `decide(state, event) -> Retry | Wait`. The Frontend consults it for a
  ForwardTask when the newest attempt ends without a usable answer and on a periodic tick while the newest attempt is
  still running ([../frontend/design.md#forwardtask](../frontend/design.md#forwardtask)).
- **RetryState**: everything a policy may look at: the request's attempts so far, its time left before the
  RequestDeadline, and a read-only view of the Load Balancer's per-backend state.
- **RetryPolicyRegistry**: maps the `lb_retry_policy` name to a policy. New policies are added by name, without
  touching the Frontend ([design.md#adding-a-retrypolicy](design.md#adding-a-retrypolicy)).

The policy only says *whether* to retry now. *How many* retries a request may have is the Ingress parameter
`lb_max_retries`, enforced by the Frontend, and *where* a retry goes is the Load Balancer's normal `pick`. The policies
shipped are `none`, which never retries, and `timeout`, which mimics the client's RetryTimer; with it, or with `lb_max_retries = 0`, the Ingress behaves exactly as
it did before LB retries existed.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
