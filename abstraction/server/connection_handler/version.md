# Version — server/connection_handler

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: FrameReader, SerialStreamWorker, routing, response writing, close |
| 0.2.0 | 2026-10-30T00:00:00Z | `DELETE /cancel` routing, per-connection cancel context, 503 responses, monotonic-id assumption |
| 0.2.1 | 2026-10-30T00:00:00Z | API style: Types / Functions sections, explicit `Response` type, design links. No behavior change |
| 0.3.0 | 2026-11-10T00:00:00Z | Shared WorkQueue: SerialStreamWorker and the per-connection queue removed. The FrameReader routes at once: `/ping` is admitted (`admit_ping` + `queue_push`, one at a time per connection) and answered by a stream responder; `/cancel`, `400`, `404`, `405` answered inline. New sections Admission, Routing table; state machine, concurrency requirement and connection close updated |
