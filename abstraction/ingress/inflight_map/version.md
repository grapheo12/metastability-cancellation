# Version — ingress/inflight_map

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-02T00:00:00Z | Initial spec: InFlightMap array of atomic pointers, publish, identity-checked clear, snapshot |
| 0.2.0 | 2026-11-04T00:00:00Z | Prefer reusable lock-free map libraries; representation is abstract, with identity-safe clear and ordered weak snapshot |
| 0.3.0 | 2026-11-10T00:00:00Z | Keyed by RequestId: `publish(map, entry)`, `clear(map, entry)`; the snapshot has one pair per in-flight request, any number per client, sorted by RequestId; worked tables |
