//! Signer entrypoint (excluded from coverage: it only wires env → lib → socket).

use signer_service::{router, AppState, Config};
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = Config::from_env(&|k| std::env::var(k).ok())?;
    let now = Arc::new(|| SystemTime::now().duration_since(UNIX_EPOCH).map_or(0, |d| d.as_secs()));
    let state = AppState::new(&cfg, now)?;
    let listener = tokio::net::TcpListener::bind(&cfg.addr).await?;
    eprintln!("{{\"msg\":\"signer listening\",\"addr\":\"{}\",\"keys\":{}}}", cfg.addr, cfg.keys.len());
    axum::serve(listener, router(state))
        .with_graceful_shutdown(async {
            let _ = tokio::signal::ctrl_c().await;
        })
        .await?;
    Ok(())
}
