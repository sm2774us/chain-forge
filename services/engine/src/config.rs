//! Environment-driven configuration with secure defaults.

use std::{fmt, net::SocketAddr, path::PathBuf};

/// Where the engine listens.
#[derive(Debug, PartialEq, Eq)]
pub enum Listen {
    /// TCP socket (mTLS required unless `ENGINE_ALLOW_INSECURE=true`).
    Tcp(SocketAddr),
    /// Unix domain socket (filesystem permissions are the access control).
    Unix(PathBuf),
}

/// mTLS material locations.
#[derive(Debug, PartialEq, Eq)]
pub struct TlsPaths {
    /// Server certificate chain (PEM).
    pub cert: PathBuf,
    /// Server private key (PEM).
    pub key: PathBuf,
    /// CA that must have signed every client certificate (PEM).
    pub client_ca: PathBuf,
}

/// Validated engine configuration.
#[derive(Debug, PartialEq, Eq)]
pub struct Config {
    /// Listener.
    pub listen: Listen,
    /// mTLS material (TCP only).
    pub tls: Option<TlsPaths>,
    /// Per-request gas ceiling.
    pub max_gas: u64,
}

/// Configuration problems.
#[derive(Debug, PartialEq, Eq)]
pub enum ConfigError {
    /// A value could not be parsed.
    Bad(&'static str),
    /// TCP without mTLS and without the explicit dev opt-out.
    InsecureTcp,
    /// Only some of cert/key/client-CA were provided.
    PartialTls,
}

impl fmt::Display for ConfigError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Bad(w) => write!(f, "invalid {w}"),
            Self::InsecureTcp => write!(
                f,
                "TCP listener requires ENGINE_TLS_CERT/KEY/CLIENT_CA (or ENGINE_ALLOW_INSECURE=true for local dev)"
            ),
            Self::PartialTls => {
                write!(f, "ENGINE_TLS_CERT, ENGINE_TLS_KEY and ENGINE_TLS_CLIENT_CA must be set together")
            }
        }
    }
}

impl Config {
    /// Reads configuration through `get` (injectable for tests).
    pub fn from_env(get: &dyn Fn(&str) -> Option<String>) -> Result<Self, ConfigError> {
        let raw = get("ENGINE_LISTEN").unwrap_or_else(|| "tcp://127.0.0.1:50051".into());
        let listen = if let Some(p) = raw.strip_prefix("unix://") {
            Listen::Unix(PathBuf::from(p))
        } else if let Some(a) = raw.strip_prefix("tcp://") {
            Listen::Tcp(a.parse().map_err(|_| ConfigError::Bad("ENGINE_LISTEN address"))?)
        } else {
            return Err(ConfigError::Bad("ENGINE_LISTEN scheme"));
        };
        let max_gas = match get("ENGINE_MAX_GAS") {
            None => 30_000_000,
            Some(v) => v.parse().map_err(|_| ConfigError::Bad("ENGINE_MAX_GAS"))?,
        };
        let parts = [get("ENGINE_TLS_CERT"), get("ENGINE_TLS_KEY"), get("ENGINE_TLS_CLIENT_CA")];
        let tls = match parts {
            [None, None, None] => None,
            [Some(cert), Some(key), Some(ca)] => {
                Some(TlsPaths { cert: cert.into(), key: key.into(), client_ca: ca.into() })
            }
            _ => return Err(ConfigError::PartialTls),
        };
        if matches!(listen, Listen::Tcp(_)) && tls.is_none() && get("ENGINE_ALLOW_INSECURE").as_deref() != Some("true")
        {
            return Err(ConfigError::InsecureTcp);
        }
        Ok(Self { listen, tls, max_gas })
    }
}
