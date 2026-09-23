//! Compiles the shared contract (`contracts/proto`) with a vendored protoc so
//! builds are hermetic on any machine or CI runner.
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let protoc = protoc_bin_vendored::protoc_bin_path()?;
    std::env::set_var("PROTOC", protoc);
    let proto = "../../contracts/proto/chainforge/engine/v1/engine.proto";
    println!("cargo:rerun-if-changed={proto}");
    tonic_build::configure().build_client(true).compile_protos(&[proto], &["../../contracts/proto"])?;
    Ok(())
}
