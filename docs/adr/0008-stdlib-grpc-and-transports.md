# ADR-0008: gRPC on the Go standard library; Unix socket first, mTLS across hosts
**Status:** accepted
**Context:** Lowest latency and smallest attack surface for the Go↔Rust hop. `grpc-go` could not be fetched in the build sandbox, and a dependency-free request path is attractive for a security boundary anyway.
**Decision:** `grpcx` implements unary gRPC (HTTP/2, 5-byte message frames, trailers) on Go 1.24 `net/http` (which supports unencrypted HTTP/2 for Unix sockets); `pbwire` is a tiny proto3 codec. Transports: Unix socket (no network exposure), mTLS TCP 1.3 with client-certificate authentication, or explicit h2c for local dev. The Rust side is stock tonic/prost.
**Consequences:** Byte compatibility is proven by shared golden vectors and a real cross-process interop test in CI. Only unary calls are supported (streaming would require growing `grpcx` or adopting grpc-go).
