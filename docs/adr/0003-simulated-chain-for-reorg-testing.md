# ADR-0003: In-process deterministic chain simulator (`chainsim`)
**Status:** accepted
**Context:** Reorg handling is the hardest indexer behaviour and the least testable against public networks.
**Decision:** A deterministic EVM-style JSON-RPC node with forced `Reorg(depth)`; the indexer is tested against it over real HTTP, plus fake `Source`s for error paths. Also used in e2e and docker-compose.
**Consequences:** Reorg paths are covered deterministically at 100%. Real-node compatibility is exercised by the JSON-RPC layer's strict spec adherence, not by flaky integration tests.
