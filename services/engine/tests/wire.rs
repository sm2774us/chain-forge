#![allow(clippy::unwrap_used, clippy::expect_used, clippy::panic, missing_docs)]
//! Cross-language wire conformance: these bytes are also asserted by the Go
//! client (`engineclient`), so prost and the hand-written Go codec cannot drift.

use engine_service::pb::{AccountState, BalanceChange, Log, Outcome, SimulateRequest, SimulateResponse, StorageSlot};
use prost::Message;

fn golden(key: &str) -> Vec<u8> {
    let raw = include_str!("../../../contracts/wire_golden.json");
    let v: serde_json::Value = serde_json::from_str(raw).unwrap();
    hex::decode(v[key].as_str().unwrap()).unwrap()
}

fn be(n: u128, width: usize) -> Vec<u8> {
    let b = n.to_be_bytes();
    let first = b.iter().position(|&x| x != 0).unwrap_or(b.len());
    let min = &b[first..];
    let mut out = vec![0; width.saturating_sub(min.len())];
    out.extend_from_slice(min);
    out
}

#[test]
fn request_bytes_match_the_go_encoder() {
    let mut key = vec![0u8; 32];
    key[31] = 1;
    let req = SimulateRequest {
        chain_id: 1337,
        from: vec![0x11; 20],
        to: vec![0x22; 20],
        value: be(1000, 0),
        data: vec![0xde, 0xad, 0xbe, 0xef],
        gas_limit: 50_000,
        gas_price: be(1_000_000_000, 0),
        state: vec![AccountState {
            address: vec![0x11; 20],
            balance: be(1_000_000_000_000_000_000, 0),
            nonce: 5,
            code: vec![0x60, 0x01],
            storage: vec![StorageSlot { key, value: vec![0x2a] }],
        }],
        block_number: 19_000_000,
        timestamp: 1_700_000_000,
    };
    assert_eq!(req.encode_to_vec(), golden("simulate_request_hex"));
    assert_eq!(SimulateRequest::decode(golden("simulate_request_hex").as_slice()).unwrap(), req);
}

#[test]
fn response_bytes_match_the_go_decoder_fixture() {
    let resp = SimulateResponse {
        outcome: Outcome::Success as i32,
        gas_used: 21_000,
        output: vec![0x2a],
        reason: "ok".into(),
        logs: vec![Log { address: vec![0x22; 20], topics: vec![vec![0xaa; 32]], data: vec![1] }],
        balance_changes: vec![BalanceChange {
            address: vec![0x11; 20],
            before: be(1_000_000_000_000_000_000, 32),
            after: be(1_000_000_000_000_000_000 - 1000, 32),
        }],
        elapsed_us: 42,
        created_address: vec![0x33; 20],
    };
    assert_eq!(resp.encode_to_vec(), golden("simulate_response_hex"));
}
