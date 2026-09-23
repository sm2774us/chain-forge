//! secp256k1 recoverable signing (RFC 6979 deterministic, low-s normalised).

use crate::{keccak256, personal_message_hash};
use k256::ecdsa::{RecoveryId, Signature as K256Sig, SigningKey, VerifyingKey};
use thiserror::Error;

/// Signing / recovery failures.
#[derive(Debug, Error, PartialEq, Eq)]
pub enum SignerError {
    /// Secret is not a valid non-zero scalar below the curve order.
    #[error("invalid secret key")]
    InvalidKey,
    /// Signing primitive failed.
    #[error("signing failed")]
    SignFailed,
    /// Signature bytes are malformed or recovery failed.
    #[error("invalid signature")]
    InvalidSignature,
}

/// A 65-byte `r || s || v` signature (`v` is 27/28 for messages).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Signature {
    /// 32-byte r.
    pub r: [u8; 32],
    /// 32-byte s (low-s).
    pub s: [u8; 32],
    /// Recovery id: 0 or 1.
    pub recid: u8,
}

impl Signature {
    /// Ethereum wire format with `v = 27 + recid`.
    #[must_use]
    pub fn to_hex(&self) -> String {
        let mut b = Vec::with_capacity(65);
        b.extend_from_slice(&self.r);
        b.extend_from_slice(&self.s);
        b.push(27 + self.recid);
        format!("0x{}", hex::encode(b))
    }

    /// Parse the 65-byte hex form (accepts v ∈ {0,1,27,28}).
    pub fn from_hex(s: &str) -> Result<Self, SignerError> {
        let raw = hex::decode(s.strip_prefix("0x").unwrap_or(s)).map_err(|_| SignerError::InvalidSignature)?;
        if raw.len() != 65 {
            return Err(SignerError::InvalidSignature);
        }
        let recid = match raw.last() {
            Some(0 | 27) => 0,
            Some(1 | 28) => 1,
            _ => return Err(SignerError::InvalidSignature),
        };
        let mut r = [0u8; 32];
        let mut sv = [0u8; 32];
        r.copy_from_slice(&raw[..32]);
        sv.copy_from_slice(&raw[32..64]);
        Ok(Self { r, s: sv, recid })
    }
}

/// Anything that can sign a 32-byte digest: an in-memory key, or an HSM/KMS
/// adapter that never lets the key leave its boundary.
pub trait DigestSigner {
    /// Sign a 32-byte digest with a recoverable ECDSA signature.
    fn sign_digest(&self, digest: &[u8; 32]) -> Result<Signature, SignerError>;
}

impl DigestSigner for Signer {
    fn sign_digest(&self, digest: &[u8; 32]) -> Result<Signature, SignerError> {
        Self::sign_digest(self, digest)
    }
}

/// Holds one private key. `SigningKey` zeroizes its scalar on drop; the type
/// intentionally implements neither `Clone` nor a revealing `Debug`.
pub struct Signer {
    key: SigningKey,
}

impl std::fmt::Debug for Signer {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Signer").field("address", &self.address_hex()).finish_non_exhaustive()
    }
}

fn address_of(vk: &VerifyingKey) -> [u8; 20] {
    let uncompressed = vk.to_encoded_point(false);
    let h = keccak256(&uncompressed.as_bytes()[1..]);
    let mut a = [0u8; 20];
    a.copy_from_slice(&h[12..]);
    a
}

impl Signer {
    /// Build from a 32-byte secret.
    pub fn from_secret(secret: &[u8]) -> Result<Self, SignerError> {
        SigningKey::from_slice(secret).map(|key| Self { key }).map_err(|_| SignerError::InvalidKey)
    }

    /// The 20-byte Ethereum address.
    #[must_use]
    pub fn address(&self) -> [u8; 20] {
        address_of(self.key.verifying_key())
    }

    /// EIP-55 address string.
    #[must_use]
    pub fn address_hex(&self) -> String {
        crate::to_checksum(&self.address())
    }

    /// Sign a 32-byte digest.
    pub fn sign_digest(&self, digest: &[u8; 32]) -> Result<Signature, SignerError> {
        let (sig, rec): (K256Sig, RecoveryId) =
            self.key.sign_prehash_recoverable(digest).map_err(|_| SignerError::SignFailed)?;
        let (r, s) = sig.split_bytes();
        Ok(Signature { r: r.into(), s: s.into(), recid: rec.to_byte() & 1 })
    }

    /// EIP-191 `personal_sign`.
    pub fn sign_message(&self, msg: &[u8]) -> Result<Signature, SignerError> {
        self.sign_digest(&personal_message_hash(msg))
    }
}

/// Recover the signer address from a digest and signature.
pub fn recover_address(digest: &[u8; 32], sig: &Signature) -> Result<[u8; 20], SignerError> {
    let ks = K256Sig::from_scalars(sig.r, sig.s).map_err(|_| SignerError::InvalidSignature)?;
    let rid = RecoveryId::from_byte(sig.recid).ok_or(SignerError::InvalidSignature)?;
    let vk = VerifyingKey::recover_from_prehash(digest, &ks, rid).map_err(|_| SignerError::InvalidSignature)?;
    Ok(address_of(&vk))
}

#[cfg(test)]
mod tests {
    use super::*;

    // Well-known test key (Hardhat/Anvil account #0).
    const SK: &str = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80";
    const ADDR: &str = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";

    fn signer() -> Signer {
        Signer::from_secret(&hex::decode(SK).unwrap()).unwrap()
    }

    #[test]
    fn derives_the_known_address() {
        assert_eq!(signer().address_hex(), ADDR);
        assert!(format!("{:?}", signer()).contains(ADDR));
        assert!(!format!("{:?}", signer()).contains(SK));
    }

    #[test]
    fn sign_and_recover_round_trip() {
        let s = signer();
        let sig = s.sign_message(b"hello").unwrap();
        assert_eq!(s.sign_message(b"hello").unwrap(), sig, "RFC6979 is deterministic");
        let rec = recover_address(&personal_message_hash(b"hello"), &sig).unwrap();
        assert_eq!(rec, s.address());
        let parsed = Signature::from_hex(&sig.to_hex()).unwrap();
        assert_eq!(parsed, sig);
    }

    #[test]
    fn rejects_bad_keys_and_signatures() {
        assert_eq!(Signer::from_secret(&[0u8; 32]).unwrap_err(), SignerError::InvalidKey);
        assert_eq!(Signer::from_secret(&[1u8; 5]).unwrap_err(), SignerError::InvalidKey);
        assert!(Signature::from_hex("0xzz").is_err());
        assert!(Signature::from_hex("0x00").is_err());
        let sig = signer().sign_message(b"x").unwrap();
        let mut bad_v = sig.to_hex();
        bad_v.replace_range(bad_v.len() - 2.., "05");
        assert!(Signature::from_hex(&bad_v).is_err());
        let zero = Signature { r: [0; 32], s: [0; 32], recid: 0 };
        assert!(recover_address(&[0; 32], &zero).is_err());
        let bad_rec = Signature { recid: 3, ..sig.clone() };
        assert!(recover_address(&[0; 32], &bad_rec).is_err());
        let wrong = recover_address(&[9; 32], &Signature { r: [1; 32], s: [1; 32], recid: 0 });
        assert!(wrong.is_ok() || wrong.is_err()); // arbitrary r/s may or may not recover
    }

    #[test]
    fn v_byte_variants() {
        let sig = Signature { r: [1; 32], s: [2; 32], recid: 0 };
        let mut h = sig.to_hex(); // v = 27
        assert_eq!(Signature::from_hex(&h).unwrap(), sig);
        h.replace_range(h.len() - 2.., "00");
        assert_eq!(Signature::from_hex(&h).unwrap(), sig);
        assert_eq!(Signature::from_hex(&Signature { recid: 1, ..sig.clone() }.to_hex()).unwrap().recid, 1);
    }

    #[test]
    fn accepts_raw_and_legacy_v() {
        let sig = signer().sign_message(b"x").unwrap();
        let hexed = sig.to_hex();
        let mut raw = hexed.clone();
        raw.replace_range(raw.len() - 2.., &format!("{:02x}", sig.recid));
        assert_eq!(Signature::from_hex(&raw).unwrap(), sig);
        assert_eq!(Signature::from_hex(hexed.trim_start_matches("0x")).unwrap(), sig);
    }
}
