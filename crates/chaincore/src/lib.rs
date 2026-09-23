//! Ethereum-compatible cryptographic primitives with a deliberately small,
//! `unsafe`-free surface: hashing, address encoding, message and transaction
//! signing, and public-key recovery.

#![cfg_attr(test, allow(clippy::unwrap_used))]

pub mod address;
pub mod rlp;
pub mod signer;
pub mod tx;

pub use address::{parse_address, to_checksum};
pub use signer::{recover_address, DigestSigner, Signature, Signer, SignerError};

use sha3::{Digest, Keccak256};

/// Legacy Keccak-256 (Ethereum's hash, *not* NIST SHA3-256).
#[must_use]
pub fn keccak256(data: &[u8]) -> [u8; 32] {
    Keccak256::digest(data).into()
}

/// EIP-191 `personal_sign` digest: `keccak256("\x19Ethereum Signed Message:\n" + len + msg)`.
#[must_use]
pub fn personal_message_hash(msg: &[u8]) -> [u8; 32] {
    let mut buf = format!("\x19Ethereum Signed Message:\n{}", msg.len()).into_bytes();
    buf.extend_from_slice(msg);
    keccak256(&buf)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keccak_matches_shared_vectors() {
        let v: serde_json::Value = serde_json::from_str(include_str!("../../../contracts/vectors.json")).unwrap();
        for c in v["keccak256"].as_array().unwrap() {
            let input = hex::decode(c["input_hex"].as_str().unwrap()).unwrap();
            assert_eq!(hex::encode(keccak256(&input)), c["digest"].as_str().unwrap());
        }
    }

    #[test]
    fn personal_message_hash_known_answer() {
        // Widely published vector for the message "hello world".
        assert_eq!(
            hex::encode(personal_message_hash(b"hello world")),
            "d9eba16ed0ecae432b71fe008c98cc872bb4cc214d3220a36f365326cf807d68"
        );
    }
}
