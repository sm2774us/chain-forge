#![allow(clippy::unwrap_used, clippy::expect_used, clippy::panic, missing_docs)]
//! Real gRPC round-trips over mTLS TCP and Unix sockets.

use engine_service::{
    config::{Config, Listen, TlsPaths},
    pb::{engine_client::EngineClient, HealthRequest, Outcome, SimulateRequest},
    service::EngineSvc,
    transport::{load_tls, run, serve_tcp, serve_uds},
};
use hyper_util::rt::TokioIo;
use std::{path::Path, time::Duration};
use tokio::{
    net::{TcpListener, UnixStream},
    sync::oneshot,
};
use tonic::transport::{Certificate, Channel, ClientTlsConfig, Endpoint, Identity, Uri};
use tonic::Code;

struct Pki {
    dir: tempfile::TempDir,
    ca_pem: String,
    client_cert: String,
    client_key: String,
}
impl Pki {
    fn paths(&self) -> TlsPaths {
        TlsPaths {
            cert: self.dir.path().join("server.pem"),
            key: self.dir.path().join("server.key"),
            client_ca: self.dir.path().join("ca.pem"),
        }
    }
}

fn pki() -> Pki {
    use rcgen::{BasicConstraints, CertificateParams, IsCa, KeyPair};
    let ca_key = KeyPair::generate().unwrap();
    let mut ca_params = CertificateParams::new(vec![]).unwrap();
    ca_params.is_ca = IsCa::Ca(BasicConstraints::Unconstrained);
    let ca = ca_params.self_signed(&ca_key).unwrap();
    let leaf = |names: Vec<String>| {
        let k = KeyPair::generate().unwrap();
        let c = CertificateParams::new(names).unwrap().signed_by(&k, &ca, &ca_key).unwrap();
        (c.pem(), k.serialize_pem())
    };
    let (server_cert, server_key) = leaf(vec!["localhost".into()]);
    let (client_cert, client_key) = leaf(vec!["gateway".into()]);
    let dir = tempfile::tempdir().unwrap();
    std::fs::write(dir.path().join("server.pem"), server_cert).unwrap();
    std::fs::write(dir.path().join("server.key"), server_key).unwrap();
    std::fs::write(dir.path().join("ca.pem"), ca.pem()).unwrap();
    Pki { dir, ca_pem: ca.pem(), client_cert, client_key }
}

fn transfer() -> SimulateRequest {
    SimulateRequest {
        chain_id: 1,
        from: vec![1; 20],
        to: vec![2; 20],
        value: vec![0],
        gas_limit: 100_000,
        ..Default::default()
    }
}

async fn uds_client(path: &Path) -> EngineClient<Channel> {
    let p = path.to_owned();
    let ch = Endpoint::try_from("http://engine.local")
        .unwrap()
        .connect_with_connector(tower::service_fn(move |_: Uri| {
            let p = p.clone();
            async move { Ok::<_, std::io::Error>(TokioIo::new(UnixStream::connect(p).await?)) }
        }))
        .await
        .unwrap();
    EngineClient::new(ch)
}

async fn wait_for(path: &Path) {
    for _ in 0..200 {
        if path.exists() {
            return;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    panic!("socket never appeared");
}

#[tokio::test]
async fn unix_socket_serves_simulate_and_health_and_maps_errors() {
    let dir = tempfile::tempdir().unwrap();
    let sock = dir.path().join("e.sock");
    std::fs::write(&sock, b"stale").unwrap(); // stale file must be replaced
    let (tx, rx) = oneshot::channel::<()>();
    let s2 = sock.clone();
    let h = tokio::spawn(async move {
        serve_uds(&s2, EngineSvc::new(1_000_000), async {
            let _ = rx.await;
        })
        .await
    });
    for _ in 0..200 {
        if std::fs::metadata(&sock).map(|m| !m.is_file()).unwrap_or(false) {
            break;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    wait_for(&sock).await;
    {
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(std::fs::metadata(&sock).unwrap().permissions().mode() & 0o777, 0o600);
    }
    let mut c = uds_client(&sock).await;
    assert!(!c.health(HealthRequest {}).await.unwrap().into_inner().version.is_empty());
    let mut req = transfer();
    req.state = vec![engine_service::pb::AccountState { address: vec![1; 20], balance: vec![9], ..Default::default() }];
    req.value = vec![1];
    let ok = c.simulate(req).await.unwrap().into_inner();
    assert_eq!(ok.outcome, Outcome::Success as i32);
    let mut bad = transfer();
    bad.from = vec![1];
    assert_eq!(c.simulate(bad).await.unwrap_err().code(), Code::InvalidArgument);
    let mut hot = transfer();
    hot.gas_limit = 2_000_000;
    assert_eq!(c.simulate(hot).await.unwrap_err().code(), Code::ResourceExhausted);
    let mut poor = transfer();
    poor.value = vec![5];
    assert_eq!(c.simulate(poor).await.unwrap_err().code(), Code::FailedPrecondition);
    tx.send(()).unwrap();
    h.await.unwrap().unwrap();
}

#[tokio::test]
async fn mtls_accepts_authorised_clients_and_rejects_others() {
    let p = pki();
    let tls = load_tls(&p.paths()).await.unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let (tx, rx) = oneshot::channel::<()>();
    let h = tokio::spawn(serve_tcp(listener, Some(tls), EngineSvc::new(30_000_000), async {
        let _ = rx.await;
    }));

    let base = ClientTlsConfig::new().ca_certificate(Certificate::from_pem(&p.ca_pem)).domain_name("localhost");
    let url = format!("https://127.0.0.1:{port}");

    let good = Endpoint::from_shared(url.clone())
        .unwrap()
        .tls_config(base.clone().identity(Identity::from_pem(&p.client_cert, &p.client_key)))
        .unwrap()
        .connect()
        .await
        .unwrap();
    let mut c = EngineClient::new(good);
    assert!(c.simulate(transfer()).await.is_ok());

    // No client certificate: the handshake or first call must fail.
    let anon = Endpoint::from_shared(url).unwrap().tls_config(base).unwrap().connect().await;
    let denied = match anon {
        Err(_) => true,
        Ok(ch) => EngineClient::new(ch).health(HealthRequest {}).await.is_err(),
    };
    assert!(denied, "client without a certificate must be refused");
    tx.send(()).unwrap();
    h.await.unwrap().unwrap();
}

#[tokio::test]
async fn plaintext_tcp_is_available_for_local_dev_only_via_serve_tcp() {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let (tx, rx) = oneshot::channel::<()>();
    let h = tokio::spawn(serve_tcp(listener, None, EngineSvc::new(1), async {
        let _ = rx.await;
    }));
    let mut c = EngineClient::connect(format!("http://127.0.0.1:{port}")).await.unwrap();
    assert!(c.health(HealthRequest {}).await.is_ok());
    tx.send(()).unwrap();
    h.await.unwrap().unwrap();
}

#[tokio::test]
async fn load_tls_reports_missing_files_and_bad_pem() {
    let p = pki();
    let mut paths = p.paths();
    paths.cert = "/definitely/missing.pem".into();
    assert!(load_tls(&paths).await.is_err());
    // Garbage identity: rejected when the server is configured.
    std::fs::write(p.dir.path().join("server.pem"), "garbage").unwrap();
    std::fs::write(p.dir.path().join("server.key"), "garbage").unwrap();
    let tls = load_tls(&p.paths()).await.unwrap();
    let l = TcpListener::bind("127.0.0.1:0").await.unwrap();
    assert!(serve_tcp(l, Some(tls), EngineSvc::new(1), async {}).await.is_err());
}

#[tokio::test]
async fn uds_errors_are_reported() {
    let dir = tempfile::tempdir().unwrap();
    // A directory where the socket should be: remove_file fails with a non-NotFound error.
    assert!(serve_uds(dir.path(), EngineSvc::new(1), async {}).await.is_err());
    // Parent directory does not exist: bind fails.
    assert!(serve_uds(&dir.path().join("no/such/dir/e.sock"), EngineSvc::new(1), async {}).await.is_err());
}

#[tokio::test]
async fn run_dispatches_on_listener_kind() {
    let dir = tempfile::tempdir().unwrap();
    let unix = Config { listen: Listen::Unix(dir.path().join("r.sock")), tls: None, max_gas: 1 };
    run(unix, async {}).await.unwrap();

    let tcp = Config { listen: Listen::Tcp("127.0.0.1:0".parse().unwrap()), tls: None, max_gas: 1 };
    run(tcp, async {}).await.unwrap();

    let p = pki();
    let secure = Config { listen: Listen::Tcp("127.0.0.1:0".parse().unwrap()), tls: Some(p.paths()), max_gas: 1 };
    run(secure, async {}).await.unwrap();

    let mut missing = p.paths();
    missing.key = "/nope".into();
    let broken = Config { listen: Listen::Tcp("127.0.0.1:0".parse().unwrap()), tls: Some(missing), max_gas: 1 };
    assert!(run(broken, async {}).await.is_err());
}
