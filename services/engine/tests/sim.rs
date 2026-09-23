#![allow(clippy::unwrap_used, clippy::expect_used, clippy::panic, missing_docs)]
//! Simulation semantics against hand-assembled EVM bytecode.

use engine_service::pb::{AccountState, Outcome, SimulateRequest, StorageSlot};
use engine_service::sim::{revert_reason, simulate, SimError};

const MAX: u64 = 30_000_000;
const ALICE: [u8; 20] = [0xaa; 20];
const BOB: [u8; 20] = [0xbb; 20];
const CONTRACT: [u8; 20] = [0xcc; 20];

fn wei(n: u128) -> Vec<u8> {
    n.to_be_bytes().to_vec()
}
fn funded(addr: [u8; 20], bal: u128) -> AccountState {
    AccountState { address: addr.to_vec(), balance: wei(bal), ..Default::default() }
}
fn contract(code: &[u8]) -> AccountState {
    AccountState { address: CONTRACT.to_vec(), code: code.to_vec(), ..Default::default() }
}
fn call(to: &[u8], value: u128, state: Vec<AccountState>) -> SimulateRequest {
    SimulateRequest {
        chain_id: 1,
        from: ALICE.to_vec(),
        to: to.to_vec(),
        value: wei(value),
        gas_limit: 1_000_000,
        state,
        block_number: 7,
        timestamp: 1_700_000_000,
        ..Default::default()
    }
}
fn u256(b: &[u8]) -> u128 {
    assert_eq!(b.len(), 32);
    u128::from_be_bytes(b[16..].try_into().unwrap())
}

/// PUSH1 0x2a PUSH1 0 MSTORE PUSH1 0x20 PUSH1 0 RETURN
const RETURN_42: [u8; 10] = [0x60, 0x2a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3];
/// PUSH1 0xaa PUSH1 0 PUSH1 0 LOG1 STOP
const EMIT_LOG: [u8; 8] = [0x60, 0xaa, 0x60, 0x00, 0x60, 0x00, 0xa1, 0x00];
/// PUSH1 0 SLOAD PUSH1 0 MSTORE PUSH1 0x20 PUSH1 0 RETURN
const READ_SLOT0: [u8; 11] = [0x60, 0x00, 0x54, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3];

/// Runtime that copies its trailing `payload` to memory and REVERTs with it.
fn revert_with(payload: &[u8]) -> Vec<u8> {
    let len = u16::try_from(payload.len()).unwrap().to_be_bytes();
    let mut c = vec![0x61, len[0], len[1], 0x60, 14, 0x60, 0x00, 0x39, 0x61, len[0], len[1], 0x60, 0x00, 0xfd];
    assert_eq!(c.len(), 14);
    c.extend_from_slice(payload);
    c
}
fn error_string(msg: &str) -> Vec<u8> {
    let mut p = vec![0x08, 0xc3, 0x79, 0xa0];
    let mut off = [0u8; 32];
    off[31] = 0x20;
    let mut len = [0u8; 32];
    len[31] = u8::try_from(msg.len()).unwrap();
    p.extend_from_slice(&off);
    p.extend_from_slice(&len);
    p.extend_from_slice(msg.as_bytes());
    p
}

#[test]
fn plain_transfer_reports_both_balance_changes_in_address_order() {
    let r = simulate(&call(&BOB, 1_000, vec![funded(ALICE, 10_000)]), MAX).unwrap();
    assert_eq!(r.outcome, Outcome::Success as i32);
    assert_eq!(r.gas_used, 21_000);
    assert_eq!(r.balance_changes.len(), 2);
    assert_eq!(r.balance_changes[0].address, ALICE);
    assert_eq!((u256(&r.balance_changes[0].before), u256(&r.balance_changes[0].after)), (10_000, 9_000));
    assert_eq!(r.balance_changes[1].address, BOB);
    assert_eq!((u256(&r.balance_changes[1].before), u256(&r.balance_changes[1].after)), (0, 1_000));
}

#[test]
fn contract_call_returns_data() {
    let r = simulate(&call(&CONTRACT, 0, vec![funded(ALICE, 1), contract(&RETURN_42)]), MAX).unwrap();
    assert_eq!(r.outcome, Outcome::Success as i32);
    assert_eq!(r.output.len(), 32);
    assert_eq!(r.output[31], 0x2a);
    assert!(r.balance_changes.is_empty());
}

#[test]
fn logs_are_surfaced() {
    let r = simulate(&call(&CONTRACT, 0, vec![contract(&EMIT_LOG)]), MAX).unwrap();
    assert_eq!(r.logs.len(), 1);
    assert_eq!(r.logs[0].address, CONTRACT);
    assert_eq!(r.logs[0].topics[0][31], 0xaa);
}

#[test]
fn storage_overrides_are_visible_to_the_contract() {
    let mut c = contract(&READ_SLOT0);
    c.storage.push(StorageSlot { key: vec![0; 32], value: vec![0x07] });
    let r = simulate(&call(&CONTRACT, 0, vec![c]), MAX).unwrap();
    assert_eq!(r.output[31], 7);
}

#[test]
fn revert_decodes_solidity_error_string() {
    let r = simulate(&call(&CONTRACT, 0, vec![contract(&revert_with(&error_string("nope")))]), MAX).unwrap();
    assert_eq!(r.outcome, Outcome::Revert as i32);
    assert_eq!(r.reason, "nope");
    assert!(!r.output.is_empty());
}

#[test]
fn revert_without_reason_has_empty_reason() {
    let r = simulate(&call(&CONTRACT, 0, vec![contract(&revert_with(&[1, 2, 3]))]), MAX).unwrap();
    assert_eq!(r.outcome, Outcome::Revert as i32);
    assert_eq!(r.reason, "");
}

#[test]
fn invalid_opcode_halts() {
    let r = simulate(&call(&CONTRACT, 0, vec![contract(&[0xfe])]), MAX).unwrap();
    assert_eq!(r.outcome, Outcome::Halt as i32);
    assert!(!r.reason.is_empty());
}

#[test]
fn contract_creation_returns_address() {
    // init code: PUSH1 1 PUSH1 0 RETURN  → runtime = 1 zero byte
    let mut req = call(&[], 0, vec![funded(ALICE, 1)]);
    req.data = vec![0x60, 0x01, 0x60, 0x00, 0xf3];
    let r = simulate(&req, MAX).unwrap();
    assert_eq!(r.outcome, Outcome::Success as i32);
    assert_eq!(r.created_address.len(), 20);
}

#[test]
fn rejects_bad_inputs() {
    let ok = || call(&BOB, 0, vec![]);
    let mut r = ok();
    r.gas_limit = MAX + 1;
    assert_eq!(simulate(&r, MAX), Err(SimError::GasTooHigh(MAX)));
    let mut r = ok();
    r.from = vec![1];
    assert!(matches!(simulate(&r, MAX), Err(SimError::Invalid(m)) if m.contains("from")));
    let mut r = ok();
    r.to = vec![1];
    assert!(matches!(simulate(&r, MAX), Err(SimError::Invalid(m)) if m.contains("to")));
    let mut r = ok();
    r.value = vec![1; 33];
    assert!(matches!(simulate(&r, MAX), Err(SimError::Invalid(m)) if m.contains("value")));
    let mut r = ok();
    r.gas_price = vec![1; 33];
    assert!(matches!(simulate(&r, MAX), Err(SimError::Invalid(m)) if m.contains("gas_price")));
    let mut r = ok();
    r.state = vec![AccountState { address: vec![1], ..Default::default() }];
    assert!(matches!(simulate(&r, MAX), Err(SimError::Invalid(m)) if m.contains("state.address")));
    let mut r = ok();
    r.state = vec![AccountState { address: ALICE.to_vec(), balance: vec![1; 33], ..Default::default() }];
    assert!(matches!(simulate(&r, MAX), Err(SimError::Invalid(m)) if m.contains("state.balance")));
    let mut r = ok();
    r.state = vec![AccountState {
        address: ALICE.to_vec(),
        storage: vec![StorageSlot { key: vec![1], value: vec![] }],
        ..Default::default()
    }];
    assert!(matches!(simulate(&r, MAX), Err(SimError::Invalid(m)) if m.contains("storage key")));
    let mut r = ok();
    r.state = vec![AccountState {
        address: ALICE.to_vec(),
        storage: vec![StorageSlot { key: vec![0; 32], value: vec![1; 33] }],
        ..Default::default()
    }];
    assert!(matches!(simulate(&r, MAX), Err(SimError::Invalid(m)) if m.contains("storage.value")));
}

#[test]
fn insufficient_funds_is_rejected_before_execution() {
    let err = simulate(&call(&BOB, 5, vec![funded(ALICE, 1)]), MAX).unwrap_err();
    assert!(matches!(err, SimError::Rejected(_)));
}

#[test]
fn errors_display() {
    assert_eq!(SimError::Invalid("x".into()).to_string(), "invalid request: x");
    assert_eq!(SimError::GasTooHigh(9).to_string(), "gas_limit exceeds engine ceiling 9");
    assert_eq!(SimError::Rejected("y".into()).to_string(), "transaction rejected: y");
}

#[test]
fn revert_reason_edge_cases() {
    assert_eq!(revert_reason(&error_string("hi")).as_deref(), Some("hi"));
    assert_eq!(revert_reason(&[]), None); // no selector
    assert_eq!(revert_reason(&[0x08, 0xc3, 0x79, 0xa0]), None); // truncated
    let mut huge = vec![0x08, 0xc3, 0x79, 0xa0];
    huge.extend_from_slice(&[0; 32]);
    huge.extend_from_slice(&[0xff; 32]); // length overflows usize
    assert_eq!(revert_reason(&huge), None);
    let mut short = error_string("abc");
    short.truncate(short.len() - 1); // length says 3, only 2 bytes present
    assert_eq!(revert_reason(&short), None);
    let mut bad_utf8 = error_string("a");
    *bad_utf8.last_mut().unwrap() = 0xff;
    assert_eq!(revert_reason(&bad_utf8).as_deref(), Some("\u{fffd}"));
}
