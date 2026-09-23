//! Environment-driven configuration with fail-fast validation.

use crate::policy::TxPolicy;
use std::collections::BTreeMap;
use zeroize::Zeroize;

/// Configuration errors (all fatal at startup).
#[derive(Debug, PartialEq, Eq)]
pub enum ConfigError {
    /// Shared secret absent or shorter than 16 bytes.
    WeakSecret,
    /// `SIGNER_KEYS` missing or malformed.
    BadKeys(String),
    /// Numeric setting failed to parse.
    BadNumber(&'static str),
    /// A transaction-policy setting failed to parse.
    BadPolicy(&'static str),
}

impl std::fmt::Display for ConfigError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::WeakSecret => write!(f, "SIGNER_SHARED_SECRET must be at least 16 characters"),
            Self::BadKeys(why) => write!(f, "SIGNER_KEYS invalid: {why}"),
            Self::BadNumber(k) => write!(f, "{k} must be a positive integer"),
            Self::BadPolicy(k) => write!(f, "{k} is invalid"),
        }
    }
}

impl std::error::Error for ConfigError {}

/// Parsed configuration. Secrets are zeroized on drop and never printed.
pub struct Config {
    /// Listen address.
    pub addr: String,
    /// HMAC shared secret.
    pub secret: Vec<u8>,
    /// `key_id` → 32-byte secret.
    pub keys: BTreeMap<String, Vec<u8>>,
    /// Maximum message size in bytes.
    pub max_message: usize,
    /// Transaction-signing policy (empty chain list = tx signing disabled).
    pub tx: TxPolicy,
}

impl std::fmt::Debug for Config {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Config")
            .field("addr", &self.addr)
            .field("key_ids", &self.keys.keys().collect::<Vec<_>>())
            .field("max_message", &self.max_message)
            .field("tx", &self.tx)
            .finish_non_exhaustive()
    }
}

impl Drop for Config {
    fn drop(&mut self) {
        self.secret.zeroize();
        for k in self.keys.values_mut() {
            k.zeroize();
        }
    }
}

impl Config {
    /// Build from an env lookup function.
    pub fn from_env(get: &dyn Fn(&str) -> Option<String>) -> Result<Self, ConfigError> {
        let secret = get("SIGNER_SHARED_SECRET").filter(|s| s.len() >= 16).ok_or(ConfigError::WeakSecret)?;
        let raw = get("SIGNER_KEYS").ok_or_else(|| ConfigError::BadKeys("not set".into()))?;
        let mut keys = BTreeMap::new();
        for pair in raw.split(',').map(str::trim).filter(|p| !p.is_empty()) {
            let (id, hexkey) =
                pair.split_once('=').ok_or_else(|| ConfigError::BadKeys(format!("`{pair}` is not id=hex")))?;
            let bytes = hex::decode(hexkey).map_err(|_| ConfigError::BadKeys(format!("`{id}` is not hex")))?;
            if id.is_empty() || bytes.len() != 32 {
                return Err(ConfigError::BadKeys(format!("`{id}` needs a 32-byte key")));
            }
            keys.insert(id.to_string(), bytes);
        }
        if keys.is_empty() {
            return Err(ConfigError::BadKeys("no keys".into()));
        }
        let max_message = get("SIGNER_MAX_MESSAGE")
            .map_or(Ok(4096), |v| v.parse::<usize>())
            .ok()
            .filter(|n| *n > 0)
            .ok_or(ConfigError::BadNumber("SIGNER_MAX_MESSAGE"))?;
        Ok(Self {
            addr: get("SIGNER_ADDR").unwrap_or_else(|| "0.0.0.0:8081".into()),
            secret: secret.into_bytes(),
            keys,
            max_message,
            tx: TxPolicy::from_env(get)?,
        })
    }
}

#[cfg(test)]
#[allow(clippy::unwrap_used)]
mod tests {
    use super::*;

    const K: &str = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80";

    fn env(pairs: &[(&str, &str)]) -> impl Fn(&str) -> Option<String> {
        let m: BTreeMap<String, String> = pairs.iter().map(|(k, v)| ((*k).to_string(), (*v).to_string())).collect();
        move |k| m.get(k).cloned()
    }

    #[test]
    fn policy_errors_display() {
        assert_eq!(ConfigError::BadPolicy("X").to_string(), "X is invalid");
    }

    #[test]
    fn parses_valid_config() {
        let keys = format!("alice={K}, bob={K}");
        let c =
            Config::from_env(&env(&[("SIGNER_SHARED_SECRET", "0123456789abcdef"), ("SIGNER_KEYS", &keys)])).unwrap();
        assert_eq!(c.keys.len(), 2);
        assert_eq!(c.max_message, 4096);
        assert_eq!(c.addr, "0.0.0.0:8081");
        let c = Config::from_env(&env(&[
            ("SIGNER_SHARED_SECRET", "0123456789abcdef"),
            ("SIGNER_KEYS", &keys),
            ("SIGNER_MAX_MESSAGE", "10"),
            ("SIGNER_ADDR", "x:1"),
        ]))
        .unwrap();
        assert_eq!((c.max_message, c.addr.as_str()), (10, "x:1"));
    }

    #[test]
    fn rejects_bad_config() {
        let good = format!("a={K}");
        let s = "0123456789abcdef";
        assert_eq!(Config::from_env(&env(&[("SIGNER_KEYS", &good)])).unwrap_err(), ConfigError::WeakSecret);
        assert_eq!(
            Config::from_env(&env(&[("SIGNER_SHARED_SECRET", "short"), ("SIGNER_KEYS", &good)])).unwrap_err(),
            ConfigError::WeakSecret
        );
        for keys in ["", "novalue", "a=zz", "a=abcd", &format!("=({K})"), &format!("={K}")] {
            let e = Config::from_env(&env(&[("SIGNER_SHARED_SECRET", s), ("SIGNER_KEYS", keys)])).unwrap_err();
            assert!(matches!(e, ConfigError::BadKeys(_)), "{keys}");
            assert!(e.to_string().contains("SIGNER_KEYS"));
        }
        assert!(Config::from_env(&env(&[("SIGNER_SHARED_SECRET", s)])).is_err());
        for n in ["0", "x"] {
            let e = Config::from_env(&env(&[
                ("SIGNER_SHARED_SECRET", s),
                ("SIGNER_KEYS", &good),
                ("SIGNER_MAX_MESSAGE", n),
            ]))
            .unwrap_err();
            assert_eq!(e, ConfigError::BadNumber("SIGNER_MAX_MESSAGE"));
            assert!(e.to_string().contains("positive"));
        }
        assert!(ConfigError::WeakSecret.to_string().contains("16"));
        let c = Config::from_env(&env(&[("SIGNER_SHARED_SECRET", s), ("SIGNER_KEYS", &good)])).unwrap();
        assert!(format!("{c:?}").contains("max_message"));
    }
}
