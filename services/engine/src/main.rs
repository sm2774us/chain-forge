//! Process wiring only; all logic lives in the library (and is tested there).
use engine_service::{config::Config, transport};

#[tokio::main]
async fn main() {
    let cfg = match Config::from_env(&|k| std::env::var(k).ok()) {
        Ok(c) => c,
        Err(e) => {
            eprintln!("engine: {e}");
            std::process::exit(2);
        }
    };
    eprintln!("engine: listening {:?}", cfg.listen);
    let stop = async {
        let _ = tokio::signal::ctrl_c().await;
    };
    if let Err(e) = transport::run(cfg, stop).await {
        eprintln!("engine: {e}");
        std::process::exit(1);
    }
}
