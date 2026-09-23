# ADR-0001: Go for the gateway, Rust for the signer
**Status:** accepted
**Context:** The edge is I/O-heavy and needs fast iteration by many engineers; the custody service holds key material and must be maximally hard to get wrong.
**Decision:** Gateway in Go (goroutine-per-request concurrency, stdlib-first, tiny static binaries). Signer in Rust (`unsafe_code=forbid`, no `unwrap` lint, ownership-based secret handling, `zeroize`-ready).
**Consequences:** Two toolchains, unified by Nx and one contract. Security-sensitive code is small, isolated and independently reviewable (CODEOWNERS → @security).
