# ADR-0002: Authenticated HTTP+JSON (HMAC, ±30 s window) across the Go↔Rust boundary
**Status:** accepted
**Context:** Options were FFI, gRPC, or HTTP. FFI merges the trust domains; gRPC adds codegen for a single endpoint.
**Decision:** Separate processes, `POST /v1/sign` with `X-Timestamp` and `X-Signature = HMAC-SHA256(secret, "<ts>.<body>")`, constant-time compare, bounded replay window, deny-unknown-fields, message-size policy, per-key allowlist, bounded audit log with digests only.
**Consequences:** Process isolation (network-private, read-only container), trivially testable contract. Rotating to mTLS or KMS/HSM only changes the signer's internals and transport auth.
