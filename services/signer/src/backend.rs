//! The seam between the service and wherever key material actually lives.
//!
//! Production deployments implement [`KeyBackend`] over a PKCS#11 HSM, AWS KMS
//! (`ECC_SECG_P256K1`), GCP KMS or a threshold/MPC signer. The service only ever
//! asks for `sign_digest(digest)` and the public address, so private keys never
//! have to enter this process at all.

use chaincore::{DigestSigner, Signer};

/// A named signing key living somewhere safe.
pub trait KeyBackend: DigestSigner + Send + Sync {
    /// The Ethereum address controlled by this key.
    fn address(&self) -> [u8; 20];
}

/// Development backend: the key is held in process memory (zeroized on drop).
impl KeyBackend for Signer {
    fn address(&self) -> [u8; 20] {
        Self::address(self)
    }
}
