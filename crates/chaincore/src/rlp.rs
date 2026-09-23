//! Minimal RLP encoder (strings and lists) — enough for legacy transactions.

/// Encode a byte string.
#[must_use]
pub fn encode_bytes(b: &[u8]) -> Vec<u8> {
    if b.len() == 1 && b[0] < 0x80 {
        return vec![b[0]];
    }
    let mut out = header(0x80, b.len());
    out.extend_from_slice(b);
    out
}

/// Encode an unsigned integer as a minimal big-endian byte string.
#[must_use]
pub fn encode_uint(v: u128) -> Vec<u8> {
    let be = v.to_be_bytes();
    let first = be.iter().position(|&x| x != 0).unwrap_or(be.len());
    encode_bytes(&be[first..])
}

/// Encode a list from already-encoded items.
#[must_use]
pub fn encode_list(items: &[Vec<u8>]) -> Vec<u8> {
    let payload: Vec<u8> = items.concat();
    let mut out = header(0xc0, payload.len());
    out.extend_from_slice(&payload);
    out
}

fn header(base: u8, len: usize) -> Vec<u8> {
    if let Some(short) = u8::try_from(len).ok().filter(|n| *n < 56) {
        return vec![base + short];
    }
    let be = (len as u64).to_be_bytes();
    let first = be.iter().position(|&x| x != 0).unwrap_or(7);
    let n = u8::try_from(be.len() - first).unwrap_or(8);
    let mut out = vec![base + 55 + n];
    out.extend_from_slice(&be[first..]);
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn canonical_examples_from_the_spec() {
        assert_eq!(encode_bytes(b"dog"), [0x83, b'd', b'o', b'g']);
        assert_eq!(encode_bytes(&[]), [0x80]);
        assert_eq!(encode_bytes(&[0x0f]), [0x0f]);
        assert_eq!(encode_uint(0), [0x80]);
        assert_eq!(encode_uint(1024), [0x82, 0x04, 0x00]);
        assert_eq!(encode_list(&[]), [0xc0]);
        assert_eq!(
            encode_list(&[encode_bytes(b"cat"), encode_bytes(b"dog")]),
            [0xc8, 0x83, b'c', b'a', b't', 0x83, b'd', b'o', b'g']
        );
    }

    #[test]
    fn long_payloads_use_length_of_length() {
        let s = vec![7u8; 56];
        let e = encode_bytes(&s);
        assert_eq!(&e[..2], [0xb8, 56]);
        assert_eq!(e.len(), 58);
        let big = vec![7u8; 300];
        assert_eq!(&encode_bytes(&big)[..3], [0xb9, 0x01, 0x2c]);
    }
}
