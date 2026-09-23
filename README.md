# ⛓ ChainForge

A polyglot blockchain-infrastructure platform: a **Go** RPC/indexing gateway, a **Rust** revm simulation engine, a **Rust** custody-signing service and protocol library, and a **React/TypeScript** operations console — orchestrated by **Nx**, gated at **100% test coverage**, deployable with **Docker** or **OpenTofu**, and released with semantic versioning from Conventional Commits.

## 1. Synopsis
| Piece | What it does |
|---|---|
| `services/gateway` (Go) | JSON-RPC 2.0 edge proxy (batching, load-balancing, circuit breakers, failover, TTL/LRU cache, token-bucket rate limiting), reorg-aware block indexer, Kafka-style partitioned event log, SSE stream, HMAC-signed webhooks with SSRF hygiene, Prometheus metrics, health/readiness |
| `services/gateway/cmd/chainsim` | Deterministic simulated EVM node with **forced reorgs** (for tests, e2e, demos) |
| `services/engine` (Rust) | **Pre-trade simulation core.** Real EVM (`revm`) over gRPC on a Unix socket or mTLS. Executes a transaction in memory against caller-supplied state and returns outcome, gas, logs, revert reason and balance deltas in microseconds. Holds no keys, makes no outbound calls |
| `services/signer` (Rust) | Custody signer: EIP-191 `personal_sign` and policy-gated EIP-155 transaction signing (default-deny: chain/value/gas/recipient caps), HMAC + timestamp-window auth, bounded audit log. Signing goes through a `KeyBackend` trait so an HSM/KMS adapter drops in; secrets are zeroized and never printed |
| `crates/chaincore` (Rust) | Keccak-256, EIP-55, EIP-191, RLP, EIP-155 legacy tx signing, secp256k1 recoverable ECDSA — verified against the official EIP-155 vector |
| `apps/web` (TS/React) | "ChainForge Console": live head, upstream health, streaming block table, custody signing UI |
| `contracts/` | Cross-language golden vectors (`vectors.json`) and the gateway `openapi.yaml` |

## 2. Directory structure
```
chainforge/
├─ apps/web/                 React 19 + Vite + Tailwind v4 + shadcn/Radix + TanStack + Zustand + Motion
│  ├─ src/{components,routes,lib,hooks.ts,store.ts,…}   unit/integration tests beside code
│  └─ e2e/                   Playwright specs (full stack)
├─ services/
│  ├─ gateway/               Go module: cmd/{gateway,chainsim}, internal/{jsonrpc,keccak,ratelimit,breaker,cache,
│  │                         metrics,upstream,chainsim,eventlog,indexer,webhook,signerclient,server,app,
│  │                         pbwire,grpcx,engineclient,intent,relay,solana,rpcx}
│  ├─ engine/                Rust bin+lib (tonic + revm): sim, service, transport (mTLS / UDS), config
│  └─ signer/                Rust bin+lib (axum): auth, backend (KMS/HSM seam), policy, config, routes
├─ crates/chaincore/         Rust protocol library
├─ contracts/                vectors.json (Keccak/EIP-55), wire_golden.json (Go↔Rust protobuf bytes), proto/…/engine.proto, openapi.yaml
├─ docker/  docker-compose.yml   multi-stage, distroless/unprivileged images
├─ infra/tofu/               OpenTofu (Docker provider) — no license, no cloud bill
├─ tools/                    100%-coverage gate scripts (Go, Rust)
├─ docs/adr/                 Architecture Decision Records
├─ .github/                  workflows (ci, release), CODEOWNERS, dependabot, PR template, copilot-instructions
├─ .husky/ .lintstagedrc.json commitlint.config.js      local branch defense
├─ CLAUDE.md AGENTS.md .claude/                          AI-assistant tooling
└─ nx.json + */project.json  Nx targets for every language
```

## 3. Build & run
**Prereqs:** Go ≥1.24 (stdlib HTTP/2 over Unix sockets), Rust ≥1.85 (revm/tonic; `llvm-tools-preview` for the coverage gate), Node ≥22, Docker (optional). The protobuf compiler is vendored — no `protoc` needed.

### Docker (fastest)
```bash
docker compose up --build        # console → http://localhost:8088   API key: dev-key-change-me
curl -H 'x-api-key: dev-key-change-me' localhost:8080/v1/status
```
### Local processes
```bash
npm ci
SIM_BLOCK_MS=500 SIM_REORG_EVERY=8 go run ./services/gateway/cmd/chainsim &     # run from services/gateway: cd services/gateway
SIGNER_SHARED_SECRET=dev-shared-secret-change-me-please \
SIGNER_KEYS=alice=ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
SIGNER_TX_CHAINS=1337 SIGNER_TX_MAX_VALUE=1000000000000000 cargo run -p signer &
ENGINE_LISTEN=unix:///tmp/engine.sock cargo run --release -p engine &            # or tcp://… with mTLS (see below)
GATEWAY_UPSTREAMS=http://localhost:8545 GATEWAY_API_KEYS=dev=dev-key-change-me \
SIGNER_SHARED_SECRET=dev-shared-secret-change-me-please \
ENGINE_ADDR=unix:///tmp/engine.sock RELAY_URLS=http://localhost:8545 go run ./cmd/gateway &   # from services/gateway
npx nx run web:dev                                                              # http://localhost:5173
```
### Quality gates (identical locally and in CI)
```bash
npx nx run-many -t lint test coverage build     # everything
npx nx affected -t lint test coverage build     # only what changed
npx nx run web:e2e                              # Playwright (boots the stack; or E2E_BASE_URL=… to reuse one)
npx nx graph
```
### OpenTofu
```bash
cd infra/tofu && cp terraform.tfvars.example terraform.tfvars && tofu init && tofu apply
```

## 4. Solution walkthrough
```
 Browser ──HTTP/SSE──▶ web (nginx) ──/api──▶ GATEWAY (Go) ──JSON-RPC──▶ node pool (chainsim / real nodes)
                                              │  ▲ breaker+LB+cache+ratelimit          
                                              │  └─ indexer ─▶ eventlog ─▶ SSE, webhooks
                                              └──HMAC(ts.body)──▶ SIGNER (Rust, private net) ─ keys never leave
```
1. **Edge:** `/rpc` decodes single/batch JSON-RPC (≤100), rate-limits per client, serves cacheable methods (`eth_chainId`, `eth_getBlockByHash`) from a TTL+LRU cache, otherwise round-robins healthy upstreams; each has a circuit breaker (closed → open → single-probe half-open) with failover.
2. **Indexing:** the indexer follows head through a `Source` interface, keeps a sliding window, re-verifies its tip hash each poll, and on divergence unwinds and re-syncs, emitting `block.reorged` then `block.new`.
3. **Streaming:** events are appended to a keyed, partitioned log (per-key ordering, offsets, consumer-group commits). Live subscribers evict on backpressure. SSE and the webhook dispatcher are consumers.
4. **Webhooks:** HMAC-SHA256 signatures, exponential-backoff retries, URLs validated against loopback/private/link-local (SSRF).
5. **Custody:** the gateway authenticates the caller, then calls the signer with `X-Timestamp` and `X-Signature = HMAC(secret, ts.body)` (±30 s). The signer enforces key allowlist, size policy, strict schema, constant-time MAC compare, and audits digests only.
6. **Observability:** structured `slog`, request IDs, Prometheus counters/gauges with a method-label cardinality guard, `/healthz` and `/readyz`.

### 4a. Simulation engine, intents and private relays
```
 UI ──intent (JSON)──▶ GATEWAY ─load pre-state (eth_getBalance/Nonce/Code)─▶ nodes
                          │
                          ├──gRPC, unix socket / mTLS──▶ ENGINE (Rust, revm)   predicts: outcome, gas, logs, Δbalances, revert reason
                          │◀───────── SimulateResponse ──────────────────────
                          ├─(only if simulation succeeded, policy allows)─HMAC─▶ SIGNER  signs the exact simulated tx
                          └─ raw signed tx ──▶ PRIVATE RELAY (Flashbots-style / Jito-style), never the public mempool
```
- **Security:** the gateway (public internet) has no keys and no EVM; the engine (executes arbitrary bytecode) has no keys and no network at all — in Docker/OpenTofu it runs with `network_mode: none`, a read-only filesystem and dropped capabilities, reachable only through a 0600 Unix socket. Cross-host deployments use **mutual TLS** (TLS 1.3, client cert required); plaintext TCP is refused unless `ENGINE_ALLOW_INSECURE=true`. The signer holds the keys and is default-deny: it signs only allow-listed chains, value/gas/price caps, optional recipient allowlist, never contract creation. The gateway signs only after a *successful* simulation of the identical transaction.
- **Latency:** no TCP or TLS on the hot path (Unix socket + h2c), connection reuse, protobuf bytes (never JSON/floats between services), microsecond in-memory EVM execution with the engine reporting `elapsed_us` on every call. Measured on the authoring sandbox (release build, sequential calls over one Unix-socket connection, `go test -tags interop -bench Simulate`): **≈60 µs per full Go → gRPC → revm → Go round trip** for a plain transfer (the engine itself reports `elapsed_us` per call). Your hardware and heavier contracts will differ — reproduce with `ENGINE_BIN=target/release/engine go test -tags interop -bench Simulate ./internal/engineclient`.
- **Ease of use:** the UI is a chain-agnostic *intent signer*: one form, one round trip returns the prediction and the exact transaction; amounts are decimal strings (no floats), errors map to honest statuses (400 invalid, 422 engine-rejected, 503 engine down).
- **Why gRPC-wire on the stdlib:** `grpcx` implements unary gRPC (HTTP/2 + length-prefixed protobuf + trailers) with Go 1.24's `net/http`, so no third-party module is in the request path; `contracts/wire_golden.json` proves byte-for-byte agreement with Rust's prost in both languages.
- **Solana:** `internal/solana` adapts a Solana node to the indexer (`GATEWAY_CHAIN=solana`), so the same reorg-aware index, event log, SSE and webhooks work; Solana transactions are simulated through the node's `simulateTransaction` (no signature needed) and submitted through a Jito-style relay. Custodial signing is EVM-only — Solana signs in the wallet.

## 5. UI/UX wireframes
**Overview `/`**
```
┌──────────────────────────────────────────────────────────────────────────────┐
│ ⛓ ChainForge   Overview  Blocks  Sign            API key [••••••••]   (☾/☀) │
├──────────────────────────────────────────────────────────────────────────────┤
│ ┌─CHAIN HEAD───────┐ ┌─UPSTREAM NODES────────────┐ ┌─EDGE─────────────────┐   │
│ │ 1,234,567        │ │ http://n1:8545 [closed]   │ │ 12 cached responses  │   │
│ │ 0x9f3a1c2b…e41d  │ │ http://n2:8545 [half-open]│ │ 2 webhooks registered│   │
│ └──────────────────┘ └───────────────────────────┘ └──────────────────────┘   │
│ ┌─LATEST BLOCKS──────────────────────────────────────── [stream: live] ────┐  │
│ │ Height ⇅   Hash            Txs   Time                                    │  │
│ │ 1,234,567  0x9f3a1c2b…e41d   3   14:02:11 UTC   ← animates in on new     │  │
│ │ 1,234,566  0x77d0aa19…02bc   1   14:02:09 UTC   ← fades out on reorg     │  │
│ └──────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────┘
```
**Blocks `/blocks`**
```
┌─ nav ────────────────────────────────────────────────────────────────────────┐
│ ┌─INDEXED BLOCKS (REORG-AWARE)─────────────────────────────────────────────┐ │
│ │ Height ⇅ (click to sort)   Hash   Txs   Time        (25 rows, live)     │ │
│ └──────────────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────────────┘
```
**Simulate `/simulate`**
```
┌─ nav: Overview Blocks [Simulate] Sign ───────────────────────────────────────┐
│ ┌─INTENT (EVM)───────────────────────┐ ┌─PREDICTED OUTCOME──────── [success] ┐│
│ │ From   [0xf39F…2266            ]   │ │ gas used   engine time   logs       ││
│ │ To     [0x7099…79C8            ]   │ │ 21,000     812 µs        0          ││
│ │ Value (wei) [1000]  Calldata [0x]  │ │ Account            Δ wei            ││
│ │ Custody key [alice]                │ │ 0xf39F…2266        -1000  (red)     ││
│ │ [ Simulate ] [ Simulate & sign ]   │ │ 0x7099…79C8        +1000  (green)   ││
│ └────────────────────────────────────┘ │ tx: nonce 0 · gas limit 25,200 …    ││
│                                        │ signed by 0xf39F… tx hash 0x…       ││
│                                        │ [ Broadcast privately ] → relay id  ││
│                                        └──────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────────────────────┘
```
**Sign `/sign`**
```
┌─ nav ────────────────────────────────────────────────────────────────────────┐
│ ┌─CUSTODY SIGNING (EIP-191 personal_sign)───────────────────────┐            │
│ │ The gateway forwards to the Rust signer. Keys never leave it. │            │
│ │ Key ID  [alice        ]                                       │            │
│ │ Message [gm chainforge                                      ] │            │
│ │ [ Sign message ]   (disabled while empty / “Signing…”)        │            │
│ │ address   0xf39F…2266                                         │            │
│ │ digest    0x…                                                 │            │
│ │ signature 0x… (65 bytes)        or ⚠ inline error alert       │            │
│ └───────────────────────────────────────────────────────────────┘            │
└──────────────────────────────────────────────────────────────────────────────┘
```
Design: dark-first OKLCH token system (`index.css`), light theme toggle, keyboard-focus outlines, ARIA-labelled tables/regions, `role="alert"` errors.

## 6. Technology choices and why
- **Go (gateway):** cheap concurrency for many connections, stdlib-only `net/http` (Go 1.22 method+wildcard mux), single static binary; every subsystem is a small `internal/` package with injected clocks/HTTP for deterministic tests.
- **Rust (signer/protocol):** memory safety for secrets, `unsafe_code=forbid`, `unwrap/expect/panic` denied, clippy pedantic; `Debug` never prints key material; `zeroize`-ready for KMS/HSM.
- **HTTP+HMAC boundary, not FFI/gRPC:** preserves process isolation (ADR-0002).
- **Hand-written EIP primitives + shared golden vectors:** shows protocol depth and proves Go/Rust equivalence (ADR-0004).
- **Simulated chain:** deterministic reorg testing (ADR-0003).
- **Frontend:** shadcn/ui + Radix (accessible, owned components), Tailwind v4, TanStack Query (server state) / Table / Router, Zustand (UI-only state), Motion (layout animation). SSE patches the Query cache, so all views update without refetching.
- **Nx** drives everything with caching and `affected`; **release-please** does language-agnostic semver + changelog (ADR-0005).

## 7. Quality, security, delivery
- **Coverage = 100%:** Go (`tools/go-coverage.sh`, every `internal` statement), Rust (`tools/rust-coverage.sh`, LLVM lcov; thin `main.rs` wiring excluded), TS (vitest thresholds 100/100/100/100), plus Playwright e2e over the real stack.
- **Branch defense:** Husky `pre-commit` refuses commits on `main`/`master` and runs lint-staged (gofmt/vet, rustfmt/clippy, eslint/prettier); `commit-msg` = commitlint; `pre-push` = `nx affected lint test`.
- **CI (`ci.yml`):** commitlint → Nx affected lint/test/coverage/build → Docker-compose Playwright e2e → Trivy (vuln/secret/misconfig) → OpenTofu fmt/validate → single `gate` required check.
- **Release (`release.yml`):** feature → PR → `main`; release-please opens a release PR (semver from Conventional Commits, `CHANGELOG.md`); merging tags `vX.Y.Z`, builds GHCR images for gateway/chainsim/signer/web, attaches **Sigstore build-provenance**, Trivy-scans, and uploads signed binaries + checksums to the GitHub Release.
- **Setup:** enable branch protection on `main` requiring the `gate` check + PRs; allow Actions to create PRs (Settings → Actions).

## 8. Mapping to the QuickNode Senior Software Engineer (Go & Rust) role
| Role expectation | Where it's demonstrated |
|---|---|
| Design & build production Go/Rust backends for blockchain infrastructure | `services/gateway`, `services/signer`, `crates/chaincore` |
| Ethereum-style RPC, node, indexing, tx-processing architecture | `internal/{jsonrpc,upstream,indexer,chainsim}`, EIP-155 tx signing, reorg handling |
| Clean interfaces between Go services and Rust security-sensitive components | `signerclient` ↔ `services/signer`, `contracts/`, ADR-0002 |
| Wallet / signing / custody security review | Signer policy, HMAC window, audit log w/o secrets, tests that assert no key leakage, CODEOWNERS |
| Streaming / data infrastructure (Kafka-like) | `internal/eventlog` (partitions, offsets, consumer groups, backpressure) |
| Throughput, latency, fault tolerance | Cache, rate limiter, breakers + failover, metrics, `-race` tests |
| Cross-team frontend collaboration (TS/React) | `apps/web` with typed API client and OpenAPI contract |
| Cloud/infra & CI/CD | Docker, OpenTofu, GitHub Actions, GHCR, Sigstore, Trivy |
| Technical leadership, standards, mentorship | ADRs, CLAUDE.md/AGENTS.md, PR template, CODEOWNERS, enforced hooks/gates |

## 9. Limitations (honest)
- **Signer keys:** dev keys still come from env vars. The `KeyBackend` trait, zeroize-on-drop, redacted `Debug` and default-deny transaction policy are in place, but no real KMS/HSM adapter ships (that needs your cloud account); it is a ~50-line implementation of `KeyBackend`.
- **Engine state:** the engine executes real EVM bytecode (revm), but the gateway seeds only the *from/to accounts'* balance, nonce and code; contract storage the call reads from other accounts starts empty. A full shadow fork needs a lazy storage-fetch callback (roadmap). `chainsim` itself is still a simulator (used for tests/demos); against a real node the engine sees real pre-state.
- **Solana:** indexing, node-side simulation and relay submission are implemented and unit-tested against scripted RPC; there is no Solana simulator in the repo and no live-network test.
- **Playwright e2e** needs browsers (`npx playwright install --with-deps chromium`) and the built binaries or the compose stack; it was authored for both but has not been run in the authoring sandbox (no browser download). The Go↔Rust path *was* exercised for real (`tools/interop.sh`).
- **Latency numbers** are from one shared sandbox with a trivial transfer and no load test; treat them as an existence proof, not a capacity claim.

License: Apache-2.0
