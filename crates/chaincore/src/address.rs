//! EIP-55 mixed-case checksum addresses.

use crate::keccak256;
use thiserror::Error;

/// Address parsing failures.
#[derive(Debug, Error, PartialEq, Eq)]
pub enum AddressError {
    /// Wrong length, prefix or non-hex characters.
    #[error("malformed address")]
    Malformed,
    /// Mixed-case input whose checksum does not verify.
    #[error("bad EIP-55 checksum")]
    BadChecksum,
}

/// Render `addr` with an EIP-55 checksum.
#[must_use]
pub fn to_checksum(addr: &[u8; 20]) -> String {
    let lower = hex::encode(addr);
    let h = keccak256(lower.as_bytes());
    let mut out = String::with_capacity(42);
    out.push_str("0x");
    for (i, c) in lower.chars().enumerate() {
        let nibble = if i % 2 == 0 { h[i / 2] >> 4 } else { h[i / 2] & 0x0f };
        out.push(if nibble >= 8 { c.to_ascii_uppercase() } else { c });
    }
    out
}

/// Parse a `0x…` address. Mixed-case input must carry a valid checksum.
pub fn parse_address(s: &str) -> Result<[u8; 20], AddressError> {
    let body = s.strip_prefix("0x").ok_or(AddressError::Malformed)?;
    let raw = hex::decode(body).map_err(|_| AddressError::Malformed)?;
    let addr: [u8; 20] = raw.try_into().map_err(|_| AddressError::Malformed)?;
    let uniform = body == body.to_lowercase() || body == body.to_uppercase();
    if !uniform && to_checksum(&addr) != s {
        return Err(AddressError::BadChecksum);
    }
    Ok(addr)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn vectors() -> Vec<String> {
        let v: serde_json::Value = serde_json::from_str(include_str!("../../../contracts/vectors.json")).unwrap();
        v["eip55"].as_array().unwrap().iter().map(|s| s.as_str().unwrap().to_string()).collect()
    }

    #[test]
    fn round_trips_shared_vectors() {
        for s in vectors() {
            let a = parse_address(&s).unwrap();
            assert_eq!(to_checksum(&a), s);
            assert!(parse_address(&s.to_lowercase()).is_ok());
        }
    }

    #[test]
    fn rejects_bad_input() {
        let good = &vectors()[0];
        assert_eq!(parse_address("5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"), Err(AddressError::Malformed));
        assert_eq!(parse_address("0xzz"), Err(AddressError::Malformed));
        assert_eq!(parse_address("0x1234"), Err(AddressError::Malformed));
        let flipped = good.replace("5aAe", "5AAe");
        assert_eq!(parse_address(&flipped), Err(AddressError::BadChecksum));
    }
}
