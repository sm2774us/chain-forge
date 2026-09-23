//! Listeners: mTLS-only TCP, or a Unix domain socket with 0600 permissions.

use crate::{
    config::{Config, Listen, TlsPaths},
    pb::engine_server::EngineServer,
    service::EngineSvc,
};
use std::{future::Future, path::Path, time::Duration};
use tokio::net::{TcpListener, UnixListener};
use tokio_stream::wrappers::{TcpListenerStream, UnixListenerStream};
use tonic::transport::{Certificate, Identity, Server, ServerTlsConfig};

/// Transport failures.
pub type BoxError = Box<dyn std::error::Error + Send + Sync>;

/// Loads server identity + client CA; clients without a cert signed by that CA are refused.
pub async fn load_tls(p: &TlsPaths) -> Result<ServerTlsConfig, BoxError> {
    let cert = tokio::fs::read(&p.cert).await?;
    let key = tokio::fs::read(&p.key).await?;
    let ca = tokio::fs::read(&p.client_ca).await?;
    Ok(ServerTlsConfig::new().identity(Identity::from_pem(cert, key)).client_ca_root(Certificate::from_pem(ca)))
}

fn builder() -> Server {
    // Defence in depth against slow or abusive callers, even on a private network.
    Server::builder()
        .timeout(Duration::from_millis(500))
        .concurrency_limit_per_connection(256)
        .max_concurrent_streams(Some(256))
}

/// Serves on an already-bound TCP listener.
pub async fn serve_tcp(
    listener: TcpListener,
    tls: Option<ServerTlsConfig>,
    svc: EngineSvc,
    shutdown: impl Future<Output = ()>,
) -> Result<(), BoxError> {
    let mut b = builder();
    if let Some(t) = tls {
        b = b.tls_config(t)?;
    }
    b.add_service(EngineServer::new(svc))
        .serve_with_incoming_shutdown(TcpListenerStream::new(listener), shutdown)
        .await?;
    Ok(())
}

/// Serves on a Unix socket (created 0600, stale socket replaced).
pub async fn serve_uds(path: &Path, svc: EngineSvc, shutdown: impl Future<Output = ()>) -> Result<(), BoxError> {
    match tokio::fs::remove_file(path).await {
        Ok(()) => {}
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
        Err(e) => return Err(e.into()),
    }
    let listener = UnixListener::bind(path)?;
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o600))?;
    }
    builder()
        .add_service(EngineServer::new(svc))
        .serve_with_incoming_shutdown(UnixListenerStream::new(listener), shutdown)
        .await?;
    Ok(())
}

/// Runs the engine as configured until `shutdown` resolves.
pub async fn run(cfg: Config, shutdown: impl Future<Output = ()>) -> Result<(), BoxError> {
    let svc = EngineSvc::new(cfg.max_gas);
    match cfg.listen {
        Listen::Unix(p) => serve_uds(&p, svc, shutdown).await,
        Listen::Tcp(a) => {
            let tls = match &cfg.tls {
                Some(p) => Some(load_tls(p).await?),
                None => None,
            };
            serve_tcp(TcpListener::bind(a).await?, tls, svc, shutdown).await
        }
    }
}
