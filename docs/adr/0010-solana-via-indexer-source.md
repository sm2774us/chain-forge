# ADR-0010: Solana through the existing indexer Source and JSON-RPC, not a second stack
**Status:** accepted
**Decision:** Map Solana block height→number, blockhash→hash, previousBlockhash→parentHash (slots resolve through `getBlocks` because slots skip). Simulate via `simulateTransaction` (sigVerify off), submit via a Jito-style relay. Custodial signing stays EVM-only.
**Consequences:** Reorg handling, streaming, webhooks and the UI work unchanged for Solana. There is no local SVM; simulation depends on the node.
