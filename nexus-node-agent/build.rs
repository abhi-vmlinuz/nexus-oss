fn main() {
    let proto = "../nexus-engine/contracts/nexus/nodeagent/v1/nodeagent.proto";
    let includes = &["../nexus-engine/contracts"];
    tonic_build::configure()
        .build_server(true)
        .build_client(false)
        .compile_protos(&[proto], includes)
        .unwrap_or_else(|e| panic!("failed to compile proto: {e}"));
}
