# ADR-0009: KeyBackend seam and default-deny transaction policy
**Status:** accepted
**Decision:** Signing goes through `KeyBackend: DigestSigner`; the in-memory key is the dev implementation, and HSM/KMS/MPC adapters implement the same two methods so private keys need never enter the process. Transaction signing is disabled unless `SIGNER_TX_CHAINS` is set and enforces value/gas/gas-price/recipient/calldata caps and forbids contract creation. Config secrets zeroize on drop and are redacted from `Debug`.
**Consequences:** A compromised gateway can at worst request policy-compliant transactions from the signer, never arbitrary ones.
