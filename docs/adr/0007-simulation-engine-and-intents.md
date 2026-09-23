# ADR-0007: A keyless Rust simulation engine (revm) behind the gateway; UI as an intent signer
**Status:** accepted
**Context:** Users need to know what a transaction will do *before* it is signed or sent, without waiting for blocks, paying fees, or trusting the browser to run an EVM. The public-facing gateway must not execute untrusted bytecode or hold keys.
**Decision:** `services/engine` embeds revm and exposes `Simulate` over gRPC. The gateway loads only the touched accounts' pre-state from the node pool, calls the engine, prices gas from the *measured* usage (+20%), and only then may ask the signer to sign the identical transaction. Broadcast goes through a private relay.
**Consequences:** Three trust zones (perimeter, execution, custody) that can each be compromised without yielding the others. The engine is stateless and horizontally scalable. Trade-off: storage of *other* contracts is not seeded (see README limitations); a lazy storage-fetch callback is the planned extension.
