//! Custody signing service. Private keys live only in this process's memory
//! (loaded from env in dev). Signing goes through the [`backend::KeyBackend`]
//! trait, so an HSM/KMS adapter can replace the in-memory key with no change
//! to the HTTP layer or policy engine. Every request is authenticated with HMAC-SHA256 over
//! `"<unix-ts>.<body>"` within a ±30 s window, policy-checked and audited.

pub mod auth;
pub mod backend;
pub mod config;
pub mod policy;
pub mod routes;

pub use config::{Config, ConfigError};
pub use routes::{router, AppState};
