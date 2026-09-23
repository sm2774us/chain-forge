//! EIP-155 legacy transaction signing.

use crate::keccak256;
use crate::rlp::{encode_bytes, encode_list, encode_uint};
use crate::signer::{DigestSigner, SignerError};

/// A legacy (type-0) transaction.
#[derive(Debug, Clone)]
pub struct LegacyTx {
    /// Sender nonce.
    pub nonce: u64,
    /// Gas price in wei.
    pub gas_price: u128,
    /// Gas limit.
    pub gas_limit: u64,
    /// Recipient (None = contract creation).
    pub to: Option<[u8; 20]>,
    /// Value in wei.
    pub value: u128,
    /// Calldata.
    pub data: Vec<u8>,
    /// EIP-155 chain id.
    pub chain_id: u64,
}

impl LegacyTx {
    fn base_fields(&self) -> Vec<Vec<u8>> {
        vec![
            encode_uint(u128::from(self.nonce)),
            encode_uint(self.gas_price),
            encode_uint(u128::from(self.gas_limit)),
            encode_bytes(self.to.as_ref().map_or(&[][..], |a| &a[..])),
            encode_uint(self.value),
            encode_bytes(&self.data),
        ]
    }

    /// Hash that gets signed (EIP-155 preimage).
    #[must_use]
    pub fn signing_hash(&self) -> [u8; 32] {
        let mut f = self.base_fields();
        f.extend([encode_uint(u128::from(self.chain_id)), encode_uint(0), encode_uint(0)]);
        keccak256(&encode_list(&f))
    }

    /// Sign and return the raw RLP-encoded transaction.
    pub fn sign<S: DigestSigner + ?Sized>(&self, signer: &S) -> Result<Vec<u8>, SignerError> {
        let sig = signer.sign_digest(&self.signing_hash())?;
        let v = u128::from(self.chain_id) * 2 + 35 + u128::from(sig.recid);
        let mut f = self.base_fields();
        let strip = |b: &[u8; 32]| {
            let i = b.iter().position(|&x| x != 0).unwrap_or(32);
            encode_bytes(&b[i..])
        };
        f.extend([encode_uint(v), strip(&sig.r), strip(&sig.s)]);
        Ok(encode_list(&f))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::signer::Signer;

    /// The worked example from the EIP-155 specification.
    fn spec_tx() -> LegacyTx {
        LegacyTx {
            nonce: 9,
            gas_price: 20_000_000_000,
            gas_limit: 21_000,
            to: Some([0x35; 20]),
            value: 1_000_000_000_000_000_000,
            data: vec![],
            chain_id: 1,
        }
    }

    #[test]
    fn eip155_spec_vector() {
        let tx = spec_tx();
        assert_eq!(hex::encode(tx.signing_hash()), "daf5a779ae972f972197303d7b574746c7ef83eadac0f2791ad23db92e4c8e53");
        let signer = Signer::from_secret(&[0x46; 32]).unwrap();
        assert_eq!(
            hex::encode(tx.sign(&signer).unwrap()),
            "f86c098504a817c800825208943535353535353535353535353535353535353535880de0b6b3a76400008025a028ef61340bd939bc2195fe537567866003e1a15d3c71ff63e1590620aa636276a067cbe9d8997f761aecb703304b3800ccf555c9f3dc64214b297fb1966a3b6d83"
        );
    }

    #[test]
    fn contract_creation_encodes_empty_to() {
        let mut tx = spec_tx();
        tx.to = None;
        tx.data = vec![1, 2, 3];
        let signer = Signer::from_secret(&[0x46; 32]).unwrap();
        assert!(!tx.sign(&signer).unwrap().is_empty());
        assert!(format!("{:?}", tx.clone()).contains("chain_id"));
    }
}
