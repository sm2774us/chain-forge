//! Request authentication: timestamped HMAC-SHA256, constant-time verification.

use hmac::{Hmac, Mac};
use sha2::Sha256;

/// Maximum tolerated clock skew / replay window in seconds.
pub const MAX_SKEW_SECS: u64 = 30;

/// Why authentication failed. Deliberately coarse: callers must not learn which
/// check tripped beyond "missing" vs "rejected".
#[derive(Debug, PartialEq, Eq)]
pub enum AuthError {
    /// Header absent or unparsable.
    Missing,
    /// Timestamp outside the window or MAC mismatch.
    Rejected,
}

/// Compute the hex MAC for `ts.body` (used by tests and reference clients).
pub fn mac_hex(secret: &[u8], ts: &str, body: &[u8]) -> Result<String, AuthError> {
    Ok(hex::encode(keyed(secret, ts, body)?.finalize().into_bytes()))
}

fn keyed(secret: &[u8], ts: &str, body: &[u8]) -> Result<Hmac<Sha256>, AuthError> {
    let mut m = Hmac::<Sha256>::new_from_slice(secret).map_err(|_| AuthError::Rejected)?;
    m.update(ts.as_bytes());
    m.update(b".");
    m.update(body);
    Ok(m)
}

/// Verify `ts_hdr` / `sig_hdr` against `body` at time `now`.
pub fn verify(
    secret: &[u8],
    ts_hdr: Option<&str>,
    sig_hdr: Option<&str>,
    body: &[u8],
    now: u64,
) -> Result<(), AuthError> {
    let (ts, sig) = ts_hdr.zip(sig_hdr).ok_or(AuthError::Missing)?;
    let ts_num: u64 = ts.parse().map_err(|_| AuthError::Missing)?;
    if now.abs_diff(ts_num) > MAX_SKEW_SECS {
        return Err(AuthError::Rejected);
    }
    let want = hex::decode(sig).map_err(|_| AuthError::Rejected)?;
    keyed(secret, ts, body)?.verify_slice(&want).map_err(|_| AuthError::Rejected)
    // constant-time
}

#[cfg(test)]
#[allow(clippy::unwrap_used)]
mod tests {
    use super::*;

    const S: &[u8] = b"0123456789abcdef";

    #[test]
    fn accepts_valid_and_rejects_tampering() {
        let sig = mac_hex(S, "1000", b"body").unwrap();
        assert_eq!(verify(S, Some("1000"), Some(&sig), b"body", 1010), Ok(()));
        assert_eq!(verify(S, Some("1000"), Some(&sig), b"other", 1010), Err(AuthError::Rejected));
        assert_eq!(verify(b"wrong-secret-xxxx", Some("1000"), Some(&sig), b"body", 1010), Err(AuthError::Rejected));
        assert_eq!(verify(S, Some("1001"), Some(&sig), b"body", 1010), Err(AuthError::Rejected));
    }

    #[test]
    fn enforces_window_both_directions() {
        let sig = mac_hex(S, "1000", b"").unwrap();
        assert_eq!(verify(S, Some("1000"), Some(&sig), b"", 1031), Err(AuthError::Rejected));
        assert_eq!(verify(S, Some("1000"), Some(&sig), b"", 969), Err(AuthError::Rejected));
        assert_eq!(verify(S, Some("1000"), Some(&sig), b"", 1030), Ok(()));
    }

    #[test]
    fn missing_and_malformed_headers() {
        assert_eq!(verify(S, None, Some("aa"), b"", 0), Err(AuthError::Missing));
        assert_eq!(verify(S, Some("1"), None, b"", 0), Err(AuthError::Missing));
        assert_eq!(verify(S, Some("x"), Some("aa"), b"", 0), Err(AuthError::Missing));
        assert_eq!(verify(S, Some("1"), Some("zz"), b"", 1), Err(AuthError::Rejected));
    }
}
