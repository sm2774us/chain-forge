#![allow(clippy::result_large_err)] // Err is a ready-to-send HTTP response by design
//! HTTP surface: `/healthz`, `POST /v1/sign`, `POST /v1/sign-tx`, `GET /v1/audit`.

use crate::auth::{self, AuthError};
use crate::backend::KeyBackend;
use crate::config::Config;
use crate::policy::TxPolicy;
use axum::body::Bytes;
use axum::extract::State;
use axum::http::{HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use chaincore::tx::LegacyTx;
use chaincore::{keccak256, personal_message_hash, to_checksum, Signer};
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::collections::{BTreeMap, VecDeque};
use std::sync::{Arc, Mutex, PoisonError};
use zeroize::Zeroizing;

const AUDIT_CAP: usize = 100;

/// One audit record — never contains key material or message contents.
#[derive(Debug, Clone, Serialize)]
pub struct AuditEntry {
    /// Which key signed.
    pub key_id: String,
    /// Digest that was signed.
    pub digest: String,
    /// Unix seconds.
    pub at: u64,
}

/// Shared state.
pub struct AppState {
    secret: Zeroizing<Vec<u8>>,
    backends: BTreeMap<String, Arc<dyn KeyBackend>>,
    policy: TxPolicy,
    max_message: usize,
    now: Arc<dyn Fn() -> u64 + Send + Sync>,
    audit: Mutex<VecDeque<AuditEntry>>,
}

impl AppState {
    /// Build state from validated config using in-memory (development) keys.
    /// `now` is injectable for tests.
    pub fn new(cfg: &Config, now: Arc<dyn Fn() -> u64 + Send + Sync>) -> Result<Arc<Self>, chaincore::SignerError> {
        let mut backends: BTreeMap<String, Arc<dyn KeyBackend>> = BTreeMap::new();
        for (id, secret) in &cfg.keys {
            backends.insert(id.clone(), Arc::new(Signer::from_secret(secret)?));
        }
        Ok(Self::with_backends(&cfg.secret, backends, cfg.tx.clone(), cfg.max_message, now))
    }

    /// Build state over arbitrary key backends (HSM, KMS, MPC…).
    pub fn with_backends(
        secret: &[u8],
        backends: BTreeMap<String, Arc<dyn KeyBackend>>,
        policy: TxPolicy,
        max_message: usize,
        now: Arc<dyn Fn() -> u64 + Send + Sync>,
    ) -> Arc<Self> {
        Arc::new(Self {
            secret: Zeroizing::new(secret.to_vec()),
            backends,
            policy,
            max_message,
            now,
            audit: Mutex::new(VecDeque::new()),
        })
    }

    fn record(&self, key_id: String, digest: String) {
        let mut log = self.audit.lock().unwrap_or_else(PoisonError::into_inner);
        if log.len() == AUDIT_CAP {
            log.pop_front();
        }
        log.push_back(AuditEntry { key_id, digest, at: (self.now)() });
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SignReq {
    key_id: String,
    message: String,
}

#[derive(Serialize)]
struct SignResp {
    address: String,
    digest: String,
    signature: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SignTxReq {
    key_id: String,
    chain_id: u64,
    nonce: u64,
    gas_price: String,
    gas_limit: u64,
    to: String,
    value: String,
    data: String,
}

#[derive(Serialize)]
struct SignTxResp {
    address: String,
    signing_hash: String,
    raw_tx: String,
    tx_hash: String,
}

fn err(status: StatusCode, msg: &str) -> Response {
    (status, Json(json!({ "error": msg }))).into_response()
}

fn authenticate(st: &AppState, h: &HeaderMap, body: &[u8]) -> Result<(), Response> {
    let get = |k: &str| h.get(k).and_then(|v| v.to_str().ok());
    auth::verify(&st.secret, get("x-timestamp"), get("x-signature"), body, (st.now)()).map_err(|e| match e {
        AuthError::Missing => err(StatusCode::UNAUTHORIZED, "missing credentials"),
        AuthError::Rejected => err(StatusCode::UNAUTHORIZED, "invalid credentials"),
    })
}

fn sign_inner(st: &AppState, headers: &HeaderMap, body: &[u8]) -> Result<SignResp, Response> {
    authenticate(st, headers, body)?;
    let req = serde_json::from_slice::<SignReq>(body)
        .map_err(|_| err(StatusCode::BAD_REQUEST, "body must be {key_id, message}"))?;
    if req.message.len() > st.max_message {
        return Err(err(StatusCode::PAYLOAD_TOO_LARGE, "message too large"));
    }
    let key = st.backends.get(&req.key_id).ok_or_else(|| err(StatusCode::FORBIDDEN, "unknown key"))?;
    let digest = personal_message_hash(req.message.as_bytes());
    let sig = key.sign_digest(&digest).map_err(|_| err(StatusCode::INTERNAL_SERVER_ERROR, "signing failed"))?;
    let digest_hex = format!("0x{}", hex::encode(digest));
    st.record(req.key_id, digest_hex.clone());
    Ok(SignResp { address: to_checksum(&key.address()), digest: digest_hex, signature: sig.to_hex() })
}

fn parse_hex(s: &str) -> Option<Vec<u8>> {
    hex::decode(s.strip_prefix("0x").unwrap_or(s)).ok()
}

fn sign_tx_inner(st: &AppState, headers: &HeaderMap, body: &[u8]) -> Result<SignTxResp, Response> {
    authenticate(st, headers, body)?;
    let bad = |m: &str| err(StatusCode::BAD_REQUEST, m);
    let req = serde_json::from_slice::<SignTxReq>(body).map_err(|_| bad("body must be a transaction request"))?;
    let tx = LegacyTx {
        nonce: req.nonce,
        gas_price: req.gas_price.parse().map_err(|_| bad("gas_price must be a decimal integer"))?,
        gas_limit: req.gas_limit,
        to: Some(chaincore::parse_address(&req.to).map_err(|_| bad("to must be a 20-byte 0x address"))?),
        value: req.value.parse().map_err(|_| bad("value must be a decimal integer"))?,
        data: parse_hex(&req.data).ok_or_else(|| bad("data must be hex"))?,
        chain_id: req.chain_id,
    };
    let key = st.backends.get(&req.key_id).ok_or_else(|| err(StatusCode::FORBIDDEN, "unknown key"))?;
    st.policy.check(&tx, st.max_message).map_err(|v| err(StatusCode::FORBIDDEN, v.reason()))?;
    let raw = tx.sign(key.as_ref()).map_err(|_| err(StatusCode::INTERNAL_SERVER_ERROR, "signing failed"))?;
    let signing_hash = format!("0x{}", hex::encode(tx.signing_hash()));
    st.record(req.key_id, signing_hash.clone());
    Ok(SignTxResp {
        address: to_checksum(&key.address()),
        signing_hash,
        tx_hash: format!("0x{}", hex::encode(keccak256(&raw))),
        raw_tx: format!("0x{}", hex::encode(raw)),
    })
}

async fn sign(State(st): State<Arc<AppState>>, headers: HeaderMap, body: Bytes) -> Response {
    sign_inner(&st, &headers, &body).map_or_else(|r| r, |ok| Json(ok).into_response())
}

async fn sign_tx(State(st): State<Arc<AppState>>, headers: HeaderMap, body: Bytes) -> Response {
    sign_tx_inner(&st, &headers, &body).map_or_else(|r| r, |ok| Json(ok).into_response())
}

async fn audit(State(st): State<Arc<AppState>>, headers: HeaderMap) -> Response {
    if let Err(r) = authenticate(&st, &headers, b"") {
        return r;
    }
    let entries: Vec<AuditEntry> = st.audit.lock().unwrap_or_else(PoisonError::into_inner).iter().cloned().collect();
    Json(json!({ "entries": entries })).into_response()
}

async fn healthz() -> Json<serde_json::Value> {
    Json(json!({ "status": "ok" }))
}

/// Build the router.
pub fn router(state: Arc<AppState>) -> Router {
    Router::new()
        .route("/healthz", get(healthz))
        .route("/v1/sign", post(sign))
        .route("/v1/sign-tx", post(sign_tx))
        .route("/v1/audit", get(audit))
        .with_state(state)
}

#[cfg(test)]
#[allow(clippy::unwrap_used)]
mod tests {
    use super::*;
    use axum::body::Body;
    use axum::http::Request;
    use chaincore::{recover_address, Signature};
    use http_body_util::BodyExt;
    use tower::ServiceExt;

    const SK: &str = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80";
    const SECRET: &str = "0123456789abcdef";

    fn app(max: usize) -> (Router, Arc<AppState>) {
        let keys = format!("alice={SK}");
        let env = move |k: &str| match k {
            "SIGNER_SHARED_SECRET" => Some(SECRET.to_string()),
            "SIGNER_KEYS" => Some(keys.clone()),
            "SIGNER_MAX_MESSAGE" => Some(max.to_string()),
            "SIGNER_TX_CHAINS" => Some("1337".to_string()),
            "SIGNER_TX_MAX_VALUE" => Some("1000".to_string()),
            _ => None,
        };
        let cfg = Config::from_env(&env).unwrap();
        let st = AppState::new(&cfg, Arc::new(|| 1000)).unwrap();
        (router(st.clone()), st)
    }

    async fn call(
        app: &Router,
        method: &str,
        uri: &str,
        body: &str,
        sig_body: Option<&str>,
    ) -> (StatusCode, serde_json::Value) {
        let mut b = Request::builder().method(method).uri(uri);
        if let Some(sb) = sig_body {
            b = b
                .header("x-timestamp", "1000")
                .header("x-signature", auth::mac_hex(SECRET.as_bytes(), "1000", sb.as_bytes()).unwrap());
        }
        let resp = app.clone().oneshot(b.body(Body::from(body.to_string())).unwrap()).await.unwrap();
        let status = resp.status();
        let bytes = resp.into_body().collect().await.unwrap().to_bytes();
        (status, serde_json::from_slice(&bytes).unwrap())
    }

    #[tokio::test]
    async fn health_is_open() {
        let (a, _) = app(100);
        assert_eq!(call(&a, "GET", "/healthz", "", None).await.0, StatusCode::OK);
    }

    #[tokio::test]
    async fn signs_and_the_signature_recovers_to_the_key_address() {
        let (a, st) = app(100);
        let body = r#"{"key_id":"alice","message":"hello"}"#;
        let (code, v) = call(&a, "POST", "/v1/sign", body, Some(body)).await;
        assert_eq!(code, StatusCode::OK);
        assert_eq!(v["address"], "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266");
        let sig = Signature::from_hex(v["signature"].as_str().unwrap()).unwrap();
        let rec = recover_address(&personal_message_hash(b"hello"), &sig).unwrap();
        assert_eq!(chaincore::to_checksum(&rec), v["address"]);
        assert_eq!(st.audit.lock().unwrap().len(), 1);
    }

    #[tokio::test]
    async fn rejects_unauthenticated_and_tampered_requests() {
        let (a, _) = app(100);
        let body = r#"{"key_id":"alice","message":"x"}"#;
        assert_eq!(call(&a, "POST", "/v1/sign", body, None).await.0, StatusCode::UNAUTHORIZED);
        let (code, _) = call(&a, "POST", "/v1/sign", body, Some(r#"{"key_id":"alice","message":"y"}"#)).await;
        assert_eq!(code, StatusCode::UNAUTHORIZED);
        assert_eq!(call(&a, "GET", "/v1/audit", "", None).await.0, StatusCode::UNAUTHORIZED);
    }

    #[tokio::test]
    async fn enforces_policy() {
        let (a, _) = app(4);
        for (body, want) in [
            ("nope", StatusCode::BAD_REQUEST),
            (r#"{"key_id":"alice","message":"x","extra":1}"#, StatusCode::BAD_REQUEST),
            (r#"{"key_id":"mallory","message":"x"}"#, StatusCode::FORBIDDEN),
            (r#"{"key_id":"alice","message":"too long"}"#, StatusCode::PAYLOAD_TOO_LARGE),
        ] {
            assert_eq!(call(&a, "POST", "/v1/sign", body, Some(body)).await.0, want, "{body}");
        }
    }

    #[tokio::test]
    async fn audit_is_bounded_and_secret_free() {
        let (a, _) = app(100);
        let body = r#"{"key_id":"alice","message":"m"}"#;
        for _ in 0..(AUDIT_CAP + 5) {
            call(&a, "POST", "/v1/sign", body, Some(body)).await;
        }
        let (code, v) = call(&a, "GET", "/v1/audit", "", Some("")).await;
        assert_eq!(code, StatusCode::OK);
        assert_eq!(v["entries"].as_array().unwrap().len(), AUDIT_CAP);
        assert!(!v.to_string().contains(SK));
    }

    #[test]
    fn state_rejects_invalid_key_material() {
        let mut cfg = Config::from_env(&|k| match k {
            "SIGNER_SHARED_SECRET" => Some(SECRET.into()),
            "SIGNER_KEYS" => Some(format!("a={SK}")),
            _ => None,
        })
        .unwrap();
        cfg.keys.insert("zero".into(), vec![0; 32]);
        assert!(AppState::new(&cfg, Arc::new(|| 0)).is_err());
    }

    const BOB: &str = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8";

    fn tx_body(over: &[(&str, serde_json::Value)]) -> String {
        let mut v = json!({"key_id":"alice","chain_id":1337,"nonce":3,"gas_price":"1000000000","gas_limit":21000,"to":BOB,"value":"500","data":"0x"});
        for (k, val) in over {
            v[*k] = val.clone();
        }
        v.to_string()
    }

    #[tokio::test]
    async fn signs_a_transaction_that_recovers_to_the_key_address() {
        let (a, st) = app(100);
        let body = tx_body(&[]);
        let (code, v) = call(&a, "POST", "/v1/sign-tx", &body, Some(&body)).await;
        assert_eq!(code, StatusCode::OK, "{v}");
        assert_eq!(v["address"], "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266");
        let tx = LegacyTx {
            nonce: 3,
            gas_price: 1_000_000_000,
            gas_limit: 21_000,
            to: Some(chaincore::parse_address(BOB).unwrap()),
            value: 500,
            data: vec![],
            chain_id: 1337,
        };
        let want = tx.sign(&Signer::from_secret(&hex::decode(SK).unwrap()).unwrap()).unwrap();
        assert_eq!(v["raw_tx"], format!("0x{}", hex::encode(&want)));
        assert_eq!(v["tx_hash"], format!("0x{}", hex::encode(keccak256(&want))));
        assert_eq!(v["signing_hash"], format!("0x{}", hex::encode(tx.signing_hash())));
        assert_eq!(st.audit.lock().unwrap().len(), 1);
    }

    #[tokio::test]
    async fn tx_requests_are_validated_and_policy_checked() {
        let (a, _) = app(4);
        let cases: Vec<(String, StatusCode)> = vec![
            ("nope".into(), StatusCode::BAD_REQUEST),
            (tx_body(&[("gas_price", json!("x"))]), StatusCode::BAD_REQUEST),
            (tx_body(&[("value", json!("-1"))]), StatusCode::BAD_REQUEST),
            (tx_body(&[("to", json!(""))]), StatusCode::BAD_REQUEST),
            (tx_body(&[("data", json!("0xzz"))]), StatusCode::BAD_REQUEST),
            (tx_body(&[("key_id", json!("mallory"))]), StatusCode::FORBIDDEN),
            (tx_body(&[("chain_id", json!(1))]), StatusCode::FORBIDDEN),
            (tx_body(&[("value", json!("1001"))]), StatusCode::FORBIDDEN),
            (tx_body(&[("data", json!("0x0102030405"))]), StatusCode::FORBIDDEN),
        ];
        for (body, want) in cases {
            assert_eq!(call(&a, "POST", "/v1/sign-tx", &body, Some(&body)).await.0, want, "{body}");
        }
        let body = tx_body(&[]);
        assert_eq!(call(&a, "POST", "/v1/sign-tx", &body, None).await.0, StatusCode::UNAUTHORIZED);
    }

    #[tokio::test]
    async fn tx_signing_is_off_unless_a_chain_is_allowed() {
        let cfg = Config::from_env(&|k| match k {
            "SIGNER_SHARED_SECRET" => Some(SECRET.into()),
            "SIGNER_KEYS" => Some(format!("alice={SK}")),
            _ => None,
        })
        .unwrap();
        let a = router(AppState::new(&cfg, Arc::new(|| 1000)).unwrap());
        let body = tx_body(&[]);
        let (code, v) = call(&a, "POST", "/v1/sign-tx", &body, Some(&body)).await;
        assert_eq!(code, StatusCode::FORBIDDEN);
        assert_eq!(v["error"], "transaction signing is disabled");
    }

    struct Broken;
    impl chaincore::DigestSigner for Broken {
        fn sign_digest(&self, _: &[u8; 32]) -> Result<Signature, chaincore::SignerError> {
            Err(chaincore::SignerError::SignFailed)
        }
    }
    impl KeyBackend for Broken {
        fn address(&self) -> [u8; 20] {
            [7; 20]
        }
    }

    #[tokio::test]
    async fn backend_failures_surface_as_500_and_are_not_audited() {
        let mut b: BTreeMap<String, Arc<dyn KeyBackend>> = BTreeMap::new();
        b.insert("alice".into(), Arc::new(Broken));
        let mut policy = TxPolicy::from_env(&|_| None).unwrap();
        policy.chains.insert(1337);
        policy.max_value = 1000;
        let st = AppState::with_backends(SECRET.as_bytes(), b, policy, 100, Arc::new(|| 1000));
        let a = router(st.clone());
        let m = r#"{"key_id":"alice","message":"x"}"#;
        assert_eq!(call(&a, "POST", "/v1/sign", m, Some(m)).await.0, StatusCode::INTERNAL_SERVER_ERROR);
        let t = tx_body(&[]);
        assert_eq!(call(&a, "POST", "/v1/sign-tx", &t, Some(&t)).await.0, StatusCode::INTERNAL_SERVER_ERROR);
        assert_eq!(st.audit.lock().unwrap().len(), 0);
        assert_eq!(KeyBackend::address(&Broken), [7; 20]);
    }

    #[test]
    fn config_debug_never_prints_secrets() {
        let cfg = Config::from_env(&|k| match k {
            "SIGNER_SHARED_SECRET" => Some(SECRET.into()),
            "SIGNER_KEYS" => Some(format!("alice={SK}")),
            _ => None,
        })
        .unwrap();
        let d = format!("{cfg:?}");
        assert!(d.contains("alice"));
        assert!(!d.contains(SK) && !d.contains(SECRET));
    }
}
