# ChainForge — GitHub Copilot instructions
Polyglot Nx monorepo: **Go** gateway (`services/gateway`), **Rust** simulation engine (`services/engine`, revm over gRPC), **Rust** custody signer (`services/signer`) + protocol lib (`crates/chaincore`), **React/TS** console (`apps/web`). Shared conformance data: `contracts/`.

## Commands (always via Nx)
- All checks on changed code: `npx nx affected -t lint test coverage build`
- One project: `npx nx run gateway:coverage` | `signer:coverage` | `web:coverage` | `web:e2e` | `engine:interop` (real Go client ↔ real Rust engine)
- Full stack: `docker compose up --build` → http://localhost:8088 (key `dev-key-change-me`)

## Non-negotiables
1. **100% coverage** is a CI gate on all three stacks. New code ships with tests; never exclude files to pass.
2. **Signing boundary:** private keys live only in `services/signer`. Never log, return, serialize or test-print key material. Go talks to it only through `internal/signerclient` (HMAC + ±30s timestamp). Changes there need @security review.
3. **Engine boundary:** `services/engine` must stay keyless and network-free; the gateway↔engine contract is `contracts/proto/**/engine.proto` and its bytes are pinned by `contracts/wire_golden.json` (Go `engineclient` + Rust `tests/wire.rs`). Change the proto, regenerate the golden from Go, and make both tests pass. Only sign a transaction the engine just simulated successfully.
4. **Cross-language conformance:** Keccak/EIP-55 behaviour is pinned by `contracts/vectors.json`, read by Go and Rust tests. Change the vectors first, then both implementations.
5. Conventional Commits (`feat:`, `fix:`, `feat!:`); PRs into `main` only — hooks block direct commits.

## Conventions
- **Go:** stdlib-first (Go 1.24; gRPC is hand-rolled in `internal/grpcx` on net/http), one concern per `internal/` package, inject clocks/HTTP for tests, wrap errors with `%w`, no globals, `-race` clean.
- **Rust:** `unsafe_code = forbid`; no `unwrap/expect/panic` outside tests; clippy pedantic; return `Result`, inject time/env for testability.
- **TS/React:** shadcn-style components in `components/ui`, Tailwind v4 tokens in `index.css`, TanStack Query for server state, Zustand for UI state only, Motion for animation, path alias `@/`.
- Prefer small PRs; record significant decisions in `docs/adr/`.
