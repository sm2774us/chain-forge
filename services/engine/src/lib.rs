//! Pre-trade simulation core. Executes EVM transactions in memory with `revm`
//! against caller-supplied state, exposed over gRPC. The process holds **no
//! keys** and makes **no outbound network calls**; it listens on a Unix socket
//! or an mTLS-only TCP port.

/// Generated protobuf/gRPC bindings for `chainforge.engine.v1`.
#[allow(missing_docs, clippy::all, clippy::pedantic)]
pub mod pb {
    tonic::include_proto!("chainforge.engine.v1");
}

pub mod config;
pub mod service;
pub mod sim;
pub mod transport;
