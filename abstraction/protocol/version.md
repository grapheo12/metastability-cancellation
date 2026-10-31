# Version — protocol

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: RequestId, PaddedJsonBody, parsing, status codes |
| 0.2.0 | 2026-10-30T00:00:00Z | RequestId layout flipped to `(client_id << 32) \| seq`. Added `/cancel` route, CancelBody, canceller status classification. 503 now means "cancelled by `/cancel`" |
