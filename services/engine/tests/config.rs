#![allow(clippy::unwrap_used, clippy::expect_used, clippy::panic, missing_docs)]
use engine_service::config::{Config, ConfigError, Listen};
use std::collections::HashMap;

fn cfg(pairs: &[(&str, &str)]) -> Result<Config, ConfigError> {
    let m: HashMap<String, String> = pairs.iter().map(|(k, v)| ((*k).into(), (*v).into())).collect();
    Config::from_env(&|k| m.get(k).cloned())
}

#[test]
fn defaults_refuse_plaintext_tcp() {
    assert_eq!(cfg(&[]), Err(ConfigError::InsecureTcp));
}

#[test]
fn dev_opt_out_and_defaults() {
    let c = cfg(&[("ENGINE_ALLOW_INSECURE", "true")]).unwrap();
    assert_eq!(c.max_gas, 30_000_000);
    assert!(c.tls.is_none());
    assert!(matches!(c.listen, Listen::Tcp(_)));
}

#[test]
fn unix_needs_no_tls() {
    let c = cfg(&[("ENGINE_LISTEN", "unix:///tmp/e.sock"), ("ENGINE_MAX_GAS", "1000")]).unwrap();
    assert_eq!(c.listen, Listen::Unix("/tmp/e.sock".into()));
    assert_eq!(c.max_gas, 1000);
}

#[test]
fn full_tls_is_accepted_and_partial_is_not() {
    let c = cfg(&[
        ("ENGINE_LISTEN", "tcp://0.0.0.0:50051"),
        ("ENGINE_TLS_CERT", "c"),
        ("ENGINE_TLS_KEY", "k"),
        ("ENGINE_TLS_CLIENT_CA", "ca"),
    ])
    .unwrap();
    assert_eq!(c.tls.unwrap().client_ca, std::path::PathBuf::from("ca"));
    assert_eq!(cfg(&[("ENGINE_TLS_CERT", "c")]), Err(ConfigError::PartialTls));
}

#[test]
fn bad_values() {
    assert_eq!(cfg(&[("ENGINE_LISTEN", "http://x")]), Err(ConfigError::Bad("ENGINE_LISTEN scheme")));
    assert_eq!(cfg(&[("ENGINE_LISTEN", "tcp://nope")]), Err(ConfigError::Bad("ENGINE_LISTEN address")));
    assert_eq!(
        cfg(&[("ENGINE_LISTEN", "unix:///x"), ("ENGINE_MAX_GAS", "abc")]),
        Err(ConfigError::Bad("ENGINE_MAX_GAS"))
    );
}

#[test]
fn errors_display() {
    assert_eq!(ConfigError::Bad("x").to_string(), "invalid x");
    assert!(
        ConfigError::InsecureTcp.to_string().contains("mTLS")
            || ConfigError::InsecureTcp.to_string().contains("ENGINE_TLS_CERT")
    );
    assert!(ConfigError::PartialTls.to_string().contains("together"));
}
