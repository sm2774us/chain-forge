//! Pure simulation logic: request → revm → response. No I/O, fully deterministic.

use crate::pb::{AccountState, BalanceChange, Log, Outcome, SimulateRequest, SimulateResponse};
use revm::{
    db::{CacheDB, EmptyDB},
    primitives::{AccountInfo, Address, Bytecode, Bytes, ExecutionResult, Output, TxKind, B256, U256},
    Evm,
};
use std::{collections::BTreeMap, fmt, time::Instant};

/// Why a request could not be simulated.
#[derive(Debug, PartialEq, Eq)]
pub enum SimError {
    /// A field had the wrong shape (length, range).
    Invalid(String),
    /// Gas limit above the operator-configured ceiling.
    GasTooHigh(u64),
    /// revm rejected the transaction before execution (bad nonce, no funds for gas…).
    Rejected(String),
}

impl fmt::Display for SimError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Invalid(m) => write!(f, "invalid request: {m}"),
            Self::GasTooHigh(max) => write!(f, "gas_limit exceeds engine ceiling {max}"),
            Self::Rejected(m) => write!(f, "transaction rejected: {m}"),
        }
    }
}

fn addr(field: &str, b: &[u8]) -> Result<Address, SimError> {
    <[u8; 20]>::try_from(b).map(Address::from).map_err(|_| SimError::Invalid(format!("{field} must be 20 bytes")))
}

fn word(field: &str, b: &[u8]) -> Result<U256, SimError> {
    if b.len() > 32 {
        return Err(SimError::Invalid(format!("{field} must be at most 32 bytes")));
    }
    Ok(U256::from_be_slice(b))
}

fn slot_key(b: &[u8]) -> Result<U256, SimError> {
    <[u8; 32]>::try_from(b)
        .map(|a| U256::from_be_bytes(a))
        .map_err(|_| SimError::Invalid("storage key must be 32 bytes".into()))
}

/// Decodes a Solidity `Error(string)` revert payload, if that is what it is.
pub fn revert_reason(output: &[u8]) -> Option<String> {
    const SELECTOR: [u8; 4] = [0x08, 0xc3, 0x79, 0xa0];
    let body = output.strip_prefix(&SELECTOR)?;
    let len = usize::try_from(U256::from_be_slice(body.get(32..64)?)).ok()?;
    let text = body.get(64..64usize.checked_add(len)?)?;
    Some(String::from_utf8_lossy(text).into_owned())
}

fn load_state(db: &mut CacheDB<EmptyDB>, state: &[AccountState]) -> Result<BTreeMap<Address, U256>, SimError> {
    let mut pre = BTreeMap::new();
    for a in state {
        let address = addr("state.address", &a.address)?;
        let balance = word("state.balance", &a.balance)?;
        let code = (!a.code.is_empty()).then(|| Bytecode::new_raw(Bytes::copy_from_slice(&a.code)));
        let code_hash = code.as_ref().map_or(revm::primitives::KECCAK_EMPTY, Bytecode::hash_slow);
        db.insert_account_info(address, AccountInfo { balance, nonce: a.nonce, code_hash, code });
        for s in &a.storage {
            let _ = db.insert_account_storage(address, slot_key(&s.key)?, word("storage.value", &s.value)?);
        }
        pre.insert(address, balance);
    }
    Ok(pre)
}

/// Simulates one transaction against the supplied state. `max_gas` bounds the work.
pub fn simulate(req: &SimulateRequest, max_gas: u64) -> Result<SimulateResponse, SimError> {
    if req.gas_limit > max_gas {
        return Err(SimError::GasTooHigh(max_gas));
    }
    let started = Instant::now();
    let caller = addr("from", &req.from)?;
    let kind = if req.to.is_empty() { TxKind::Create } else { TxKind::Call(addr("to", &req.to)?) };
    let value = word("value", &req.value)?;
    let gas_price = word("gas_price", &req.gas_price)?;

    let mut db = CacheDB::new(EmptyDB::default());
    let pre = load_state(&mut db, &req.state)?;

    let mut evm = Evm::builder()
        .with_db(db)
        .modify_cfg_env(|c| c.chain_id = req.chain_id)
        .modify_block_env(|b| {
            b.number = U256::from(req.block_number);
            b.timestamp = U256::from(req.timestamp);
        })
        .modify_tx_env(|t| {
            t.caller = caller;
            t.transact_to = kind;
            t.value = value;
            t.data = Bytes::copy_from_slice(&req.data);
            t.gas_limit = req.gas_limit;
            t.gas_price = gas_price;
            t.nonce = None;
        })
        .build();
    let out = evm.transact().map_err(|e| SimError::Rejected(e.to_string()))?;

    let mut resp = SimulateResponse::default();
    match out.result {
        ExecutionResult::Success { gas_used, logs, output, .. } => {
            resp.outcome = Outcome::Success as i32;
            resp.gas_used = gas_used;
            resp.logs = logs
                .iter()
                .map(|l| Log {
                    address: l.address.to_vec(),
                    topics: l.data.topics().iter().map(|t| t.to_vec()).collect(),
                    data: l.data.data.to_vec(),
                })
                .collect();
            match output {
                Output::Call(b) => resp.output = b.to_vec(),
                Output::Create(b, a) => {
                    resp.output = b.to_vec();
                    resp.created_address = a.map(|a| a.to_vec()).unwrap_or_default();
                }
            }
        }
        ExecutionResult::Revert { gas_used, output } => {
            resp.outcome = Outcome::Revert as i32;
            resp.gas_used = gas_used;
            resp.reason = revert_reason(&output).unwrap_or_default();
            resp.output = output.to_vec();
        }
        ExecutionResult::Halt { reason, gas_used } => {
            resp.outcome = Outcome::Halt as i32;
            resp.gas_used = gas_used;
            resp.reason = format!("{reason:?}");
        }
    }

    // Deterministic order: BTreeMap keyed by address.
    let mut after: BTreeMap<Address, U256> = BTreeMap::new();
    for (a, acct) in &out.state {
        after.insert(*a, acct.info.balance);
    }
    for (a, new) in after {
        let old = pre.get(&a).copied().unwrap_or(U256::ZERO);
        if old != new {
            resp.balance_changes.push(BalanceChange {
                address: a.to_vec(),
                before: B256::from(old).to_vec(),
                after: B256::from(new).to_vec(),
            });
        }
    }
    resp.elapsed_us = u64::try_from(started.elapsed().as_micros()).unwrap_or(u64::MAX);
    Ok(resp)
}
