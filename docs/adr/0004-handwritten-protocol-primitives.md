# ADR-0004: Hand-implemented Keccak/EIP-55/EIP-191/RLP/EIP-155 with shared golden vectors
**Status:** accepted
**Decision:** Implement primitives in both Go (stdlib) and Rust (minimal crates), pinned by `contracts/vectors.json` and the official EIP-155 worked example.
**Consequences:** Small audited surface, no heavyweight SDK in the custody path, proven cross-language equivalence. Trade-off: we own the maintenance; mitigated by vector tests.
