# Version — protocol

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: RequestId, PaddedJsonBody, parsing, status codes |
| 0.2.0 | 2026-10-30T00:00:00Z | RequestId layout flipped to `(client_id << 32) \| seq`. Added `/cancel` route, CancelBody, canceller status classification. 503 now means "cancelled by `/cancel`" |
| 0.2.1 | 2026-10-30T00:00:00Z | API style fixes: every constant and function links where it is defined. No behavior change |
| 0.3.0 | 2026-11-02T00:00:00Z | CancelReplyBody: `202` answers to `/cancel` carry the FinishedWatermark (`build_cancel_reply`, `parse_cancel_reply`) |
| 0.3.1 | 2026-11-02T00:00:00Z | `STATUS_BAD_GATEWAY` (502), sent only by an Ingress; the load-generator client classifies it as `error` |
