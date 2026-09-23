//! Transaction-signing policy: what this service is willing to sign at all.
//! Secure by default — with no `SIGNER_TX_CHAINS` it signs no transactions.

use crate::config::ConfigError;
use chaincore::tx::LegacyTx;
use std::collections::BTreeSet;

/// Why a transaction was refused.
#[derive(Debug, PartialEq, Eq)]
pub enum Violation {
    /// Transaction signing is not enabled.
    Disabled,
    /// Chain id not in the allowlist.
    Chain,
    /// Recipient not in the allowlist.
    Recipient,
    /// Value above the cap.
    Value,
    /// Gas limit above the cap.
    GasLimit,
    /// Gas price above the cap.
    GasPrice,
    /// Calldata larger than allowed.
    Data,
}

impl Violation {
    /// Human-readable reason (safe to return to callers).
    pub fn reason(&self) -> &'static str {
        match self {
            Self::Disabled => "transaction signing is disabled",
            Self::Chain => "chain id not allowed",
            Self::Recipient => "recipient not allowed",
            Self::Value => "value above policy cap",
            Self::GasLimit => "gas limit above policy cap",
            Self::GasPrice => "gas price above policy cap",
            Self::Data => "calldata too large",
        }
    }
}

/// Signing limits.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TxPolicy {
    /// Allowed EIP-155 chain ids (empty = disabled).
    pub chains: BTreeSet<u64>,
    /// Maximum value per transaction, wei.
    pub max_value: u128,
    /// Maximum gas limit.
    pub max_gas_limit: u64,
    /// Maximum gas price, wei.
    pub max_gas_price: u128,
    /// Allowed recipients (empty = any).
    pub allow_to: BTreeSet<[u8; 20]>,
}

fn parse_num<T: std::str::FromStr>(
    get: &dyn Fn(&str) -> Option<String>,
    key: &'static str,
    default: T,
) -> Result<T, ConfigError> {
    get(key).map_or(Ok(default), |v| v.trim().parse().map_err(|_| ConfigError::BadPolicy(key)))
}

impl TxPolicy {
    /// Reads `SIGNER_TX_*` settings.
    pub fn from_env(get: &dyn Fn(&str) -> Option<String>) -> Result<Self, ConfigError> {
        let mut chains = BTreeSet::new();
        for c in get("SIGNER_TX_CHAINS").unwrap_or_default().split(',').map(str::trim).filter(|c| !c.is_empty()) {
            chains.insert(c.parse().map_err(|_| ConfigError::BadPolicy("SIGNER_TX_CHAINS"))?);
        }
        let mut allow_to = BTreeSet::new();
        for a in get("SIGNER_TX_ALLOW_TO").unwrap_or_default().split(',').map(str::trim).filter(|a| !a.is_empty()) {
            allow_to.insert(chaincore::parse_address(a).map_err(|_| ConfigError::BadPolicy("SIGNER_TX_ALLOW_TO"))?);
        }
        Ok(Self {
            chains,
            max_value: parse_num(get, "SIGNER_TX_MAX_VALUE", 0)?,
            max_gas_limit: parse_num(get, "SIGNER_TX_MAX_GAS", 1_000_000)?,
            max_gas_price: parse_num(get, "SIGNER_TX_MAX_GAS_PRICE", 500_000_000_000)?,
            allow_to,
        })
    }

    /// Whether any transaction can be signed.
    pub fn enabled(&self) -> bool {
        !self.chains.is_empty()
    }

    /// Checks a transaction against the policy. Contract creation is never allowed.
    pub fn check(&self, tx: &LegacyTx, max_data: usize) -> Result<(), Violation> {
        if !self.enabled() {
            return Err(Violation::Disabled);
        }
        if !self.chains.contains(&tx.chain_id) {
            return Err(Violation::Chain);
        }
        match tx.to {
            None => return Err(Violation::Recipient),
            Some(to) if !self.allow_to.is_empty() && !self.allow_to.contains(&to) => return Err(Violation::Recipient),
            Some(_) => {}
        }
        if tx.value > self.max_value {
            return Err(Violation::Value);
        }
        if tx.gas_limit > self.max_gas_limit {
            return Err(Violation::GasLimit);
        }
        if tx.gas_price > self.max_gas_price {
            return Err(Violation::GasPrice);
        }
        if tx.data.len() > max_data {
            return Err(Violation::Data);
        }
        Ok(())
    }
}

#[cfg(test)]
#[allow(clippy::unwrap_used)]
mod tests {
    use super::*;
    use std::collections::BTreeMap;

    const A: &str = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";

    fn env(p: &[(&str, &str)]) -> impl Fn(&str) -> Option<String> {
        let m: BTreeMap<String, String> = p.iter().map(|(k, v)| ((*k).into(), (*v).into())).collect();
        move |k| m.get(k).cloned()
    }
    fn tx() -> LegacyTx {
        LegacyTx { nonce: 0, gas_price: 1, gas_limit: 21_000, to: Some([1; 20]), value: 0, data: vec![], chain_id: 1 }
    }
    fn on() -> TxPolicy {
        TxPolicy::from_env(&env(&[("SIGNER_TX_CHAINS", "1, 5")])).unwrap()
    }

    #[test]
    fn defaults_are_locked_down() {
        let p = TxPolicy::from_env(&env(&[])).unwrap();
        assert!(!p.enabled());
        assert_eq!((p.max_value, p.max_gas_limit, p.max_gas_price), (0, 1_000_000, 500_000_000_000));
        assert_eq!(p.check(&tx(), 10), Err(Violation::Disabled));
    }

    #[test]
    fn parses_everything() {
        let p = TxPolicy::from_env(&env(&[
            ("SIGNER_TX_CHAINS", "1,5"),
            ("SIGNER_TX_ALLOW_TO", A),
            ("SIGNER_TX_MAX_VALUE", "7"),
            ("SIGNER_TX_MAX_GAS", "9"),
            ("SIGNER_TX_MAX_GAS_PRICE", "11"),
        ]))
        .unwrap();
        assert_eq!(p.chains.len(), 2);
        assert_eq!(p.allow_to.len(), 1);
        assert_eq!((p.max_value, p.max_gas_limit, p.max_gas_price), (7, 9, 11));
    }

    #[test]
    fn rejects_garbage_settings() {
        for (k, v) in [
            ("SIGNER_TX_CHAINS", "x"),
            ("SIGNER_TX_ALLOW_TO", "0x12"),
            ("SIGNER_TX_MAX_VALUE", "-1"),
            ("SIGNER_TX_MAX_GAS", "z"),
            ("SIGNER_TX_MAX_GAS_PRICE", "1.5"),
        ] {
            assert_eq!(TxPolicy::from_env(&env(&[(k, v)])).unwrap_err(), ConfigError::BadPolicy(k));
        }
    }

    #[test]
    fn checks_each_limit() {
        let p = on();
        assert_eq!(p.check(&tx(), 10), Ok(()));
        assert_eq!(p.check(&LegacyTx { chain_id: 2, ..tx() }, 10), Err(Violation::Chain));
        assert_eq!(p.check(&LegacyTx { to: None, ..tx() }, 10), Err(Violation::Recipient));
        assert_eq!(p.check(&LegacyTx { value: 1, ..tx() }, 10), Err(Violation::Value));
        assert_eq!(p.check(&LegacyTx { gas_limit: 2_000_000, ..tx() }, 10), Err(Violation::GasLimit));
        assert_eq!(p.check(&LegacyTx { gas_price: u128::MAX, ..tx() }, 10), Err(Violation::GasPrice));
        assert_eq!(p.check(&LegacyTx { data: vec![0; 11], ..tx() }, 10), Err(Violation::Data));
    }

    #[test]
    fn recipient_allowlist_is_enforced() {
        let mut p = on();
        p.allow_to.insert([9; 20]);
        assert_eq!(p.check(&tx(), 10), Err(Violation::Recipient));
        assert_eq!(p.check(&LegacyTx { to: Some([9; 20]), ..tx() }, 10), Ok(()));
    }

    #[test]
    fn every_violation_has_a_reason() {
        for v in [
            Violation::Disabled,
            Violation::Chain,
            Violation::Recipient,
            Violation::Value,
            Violation::GasLimit,
            Violation::GasPrice,
            Violation::Data,
        ] {
            assert!(!v.reason().is_empty());
        }
    }
}
