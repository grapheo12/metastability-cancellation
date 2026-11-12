# Version — server/message_buffer

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-12T00:00:00Z | Initial spec: MessageBuffer window filled at startup, make_message, per-client MessageCursor with MESSAGE_CURSOR_STRIDE start, push_messages under one lock with messages created first, worked examples |
| 0.2.0 | 2026-11-12T00:00:00Z | Messages are MessageTrees: random binary trees of exactly `n_gc_binary_tree_size` nodes (u64 value, two child references), `make_tree` with a fixed pre-order draw order; `buffer_new(W, n, seed)` fills from STREAM_SERVER_PREFILL; `message_bytes` removed; shape worked example |
