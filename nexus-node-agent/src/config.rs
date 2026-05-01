// config.rs — environment-based configuration for nexus-node-agent.
use std::env;

pub struct Config {
    pub listen_addr: String,
    pub insecure: bool,
    pub tls_cert: String,
    pub tls_key: String,
    pub tls_ca: String,
    pub mode: String,
}

impl Config {
    pub fn from_env() -> anyhow::Result<Self> {
        let mode = env::var("NEXUS_MODE").unwrap_or_else(|_| "dev".to_string());
        if mode != "dev" && mode != "prod" {
            anyhow::bail!("NEXUS_MODE must be 'dev' or 'prod', got {:?}", mode);
        }

        let insecure_default = mode == "dev";
        let insecure = env::var("NODE_AGENT_INSECURE")
            .ok()
            .and_then(|v| v.parse::<bool>().ok())
            .unwrap_or(insecure_default);

        Ok(Config {
            listen_addr: env::var("NODE_AGENT_LISTEN_ADDR")
                .unwrap_or_else(|_| "0.0.0.0:50051".to_string()),
            insecure,
            tls_cert: env::var("NODE_AGENT_TLS_CERT")
                .unwrap_or_else(|_| "/etc/nexus/agent.crt".to_string()),
            tls_key: env::var("NODE_AGENT_TLS_KEY")
                .unwrap_or_else(|_| "/etc/nexus/agent.key".to_string()),
            tls_ca: env::var("NODE_AGENT_CA_CERT")
                .unwrap_or_else(|_| "/etc/nexus/ca.crt".to_string()),
            mode,
        })
    }
}
