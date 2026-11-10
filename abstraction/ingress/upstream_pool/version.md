# Version — ingress/upstream_pool

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-02T00:00:00Z | Initial spec: one connection per (client, backend), single dial per pair, identity-checked discard, close_all |
| 0.1.1 | 2026-11-10T00:00:00Z | Wording: the server binds each connection to one client; it no longer serves connections serially. No behavior change |
| 0.1.2 | 2026-11-10T00:00:00Z | Wording: a pair connection carries up to `n_waiting_requests` concurrent streams. No behavior change |
