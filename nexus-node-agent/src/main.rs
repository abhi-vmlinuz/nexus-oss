// main.rs — nexus-node-agent entrypoint.
mod adapters;
mod config;
mod server;

use anyhow::Context;
use server::pb::nodeagent::v1::node_agent_service_server::NodeAgentServiceServer;
use server::NodeAgent;
use tokio::signal;
use tonic::transport::Server;
use tracing::info;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    // Structured JSON logging.
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(
            std::env::var("RUST_LOG")
                .unwrap_or_else(|_| "nexus_node_agent=info,info".to_string()),
        )
        .init();

    let cfg = config::Config::from_env()?;

    let addr = cfg
        .listen_addr
        .parse()
        .with_context(|| format!("invalid listen address: {}", cfg.listen_addr))?;

    let service = NodeAgent {
        version: env!("CARGO_PKG_VERSION").to_string(),
    };

    info!(
        listen_addr = %addr,
        mode = %cfg.mode,
        insecure = cfg.insecure,
        "starting nexus-node-agent"
    );

    if cfg.insecure {
        info!("⚠️  mTLS disabled (insecure mode — dev only)");
        Server::builder()
            .add_service(NodeAgentServiceServer::new(service))
            .serve_with_shutdown(addr, shutdown_signal())
            .await
            .context("gRPC server failed")?;
    } else {
        // Load mTLS certificates.
        let cert = std::fs::read(&cfg.tls_cert)
            .with_context(|| format!("read TLS cert {}", cfg.tls_cert))?;
        let key = std::fs::read(&cfg.tls_key)
            .with_context(|| format!("read TLS key {}", cfg.tls_key))?;
        let ca = std::fs::read(&cfg.tls_ca)
            .with_context(|| format!("read CA cert {}", cfg.tls_ca))?;

        let identity = tonic::transport::Identity::from_pem(cert, key);
        let ca_cert = tonic::transport::Certificate::from_pem(ca);
        let tls = tonic::transport::ServerTlsConfig::new()
            .identity(identity)
            .client_ca_root(ca_cert);

        info!("🔒 mTLS enabled");
        Server::builder()
            .tls_config(tls)
            .context("TLS config failed")?
            .add_service(NodeAgentServiceServer::new(service))
            .serve_with_shutdown(addr, shutdown_signal())
            .await
            .context("gRPC server failed")?;
    }

    info!("nexus-node-agent stopped");
    Ok(())
}

async fn shutdown_signal() {
    if let Err(e) = signal::ctrl_c().await {
        tracing::error!("failed to listen for shutdown signal: {e}");
    }
    info!("shutdown signal received");
}
