// server.rs — NodeAgentService gRPC implementation.
// Each RPC validates input, delegates to adapters, returns typed responses.
// Never returns raw shell output. All mutating RPCs are idempotent.
use std::net::IpAddr;

use tonic::{Request, Response, Status};
use tracing::info;

use crate::adapters::{ipset, iptables, wireguard};

pub mod pb {
    pub mod nodeagent {
        pub mod v1 {
            tonic::include_proto!("nexus.nodeagent.v1");
        }
    }
}

use pb::nodeagent::v1::{
    node_agent_service_server::NodeAgentService,
    EnsureUserIsolationRequest, EnsureUserIsolationResponse,
    EnsureWireGuardPeerRequest, EnsureWireGuardPeerResponse,
    GetWireGuardStatusRequest, GetWireGuardStatusResponse,
    GrantPodAccessRequest, GrantPodAccessResponse,
    HealthRequest, HealthResponse,
    RevokeUserIsolationRequest, RevokeUserIsolationResponse,
    RevokeWireGuardPeerRequest, RevokeWireGuardPeerResponse,
    RevokePodAccessRequest, RevokePodAccessResponse,
    WireGuardPeer,
};

/// NodeAgent is the gRPC service implementation.
#[derive(Default)]
pub struct NodeAgent {
    pub version: String,
}

#[tonic::async_trait]
impl NodeAgentService for NodeAgent {
    // ── Health ────────────────────────────────────────────────────────────────
    async fn health(
        &self,
        _req: Request<HealthRequest>,
    ) -> Result<Response<HealthResponse>, Status> {
        let (cpu, mem, disk) = system_metrics();
        Ok(Response::new(HealthResponse {
            healthy: true,
            message: "ok".to_string(),
            version: self.version.clone(),
            cpu_percent: cpu,
            mem_percent: mem,
            disk_percent: disk,
            uptime_seconds: uptime_secs(),
        }))
    }

    // ── EnsureUserIsolation ───────────────────────────────────────────────────
    async fn ensure_user_isolation(
        &self,
        req: Request<EnsureUserIsolationRequest>,
    ) -> Result<Response<EnsureUserIsolationResponse>, Status> {
        let r = req.into_inner();
        validate_not_empty(&r.user_id, "user_id")?;
        let vpn_ip = validate_ipv4(&r.vpn_ip, "vpn_ip")?;

        let safe_user = ipset::sanitize_user_id(&r.user_id);
        let set_name = ipset::ipset_name(&safe_user);

        // 1. Ensure ipset exists.
        ipset::create_set(&set_name)?;

        // 2. Ensure ACCEPT rule: VPN IP → any IP in user's ipset.
        let check_accept = ["-C", "FORWARD", "-s", &vpn_ip, "-m", "set",
            "--match-set", &set_name, "dst", "-j", "ACCEPT"];
        let insert_accept = ["-I", "FORWARD", "1", "-s", &vpn_ip, "-m", "set",
            "--match-set", &set_name, "dst", "-j", "ACCEPT"];
        iptables::ensure_rule(&check_accept, &insert_accept, "user ACCEPT")?;

        // 3. Ensure DROP rule: VPN IP → everything else (after the ACCEPT).
        let check_drop = ["-C", "FORWARD", "-s", &vpn_ip, "-j", "DROP"];
        let insert_drop = ["-I", "FORWARD", "2", "-s", &vpn_ip, "-j", "DROP"];
        iptables::ensure_rule(&check_drop, &insert_drop, "user DROP")?;

        info!(user_id = %safe_user, vpn_ip = %vpn_ip, "ensure_user_isolation applied");

        Ok(Response::new(EnsureUserIsolationResponse {
            applied: true,
            message: "user isolation ensured".to_string(),
        }))
    }

    // ── RevokeUserIsolation ───────────────────────────────────────────────────
    async fn revoke_user_isolation(
        &self,
        req: Request<RevokeUserIsolationRequest>,
    ) -> Result<Response<RevokeUserIsolationResponse>, Status> {
        let r = req.into_inner();
        validate_not_empty(&r.user_id, "user_id")?;
        let vpn_ip = validate_ipv4(&r.vpn_ip, "vpn_ip")?;

        let safe_user = ipset::sanitize_user_id(&r.user_id);
        let set_name = ipset::ipset_name(&safe_user);

        // Remove ACCEPT rule.
        let del_accept = ["-D", "FORWARD", "-s", &vpn_ip, "-m", "set",
            "--match-set", &set_name, "dst", "-j", "ACCEPT"];
        iptables::remove_rule(&del_accept, "user ACCEPT")?;

        // Remove DROP rule.
        let del_drop = ["-D", "FORWARD", "-s", &vpn_ip, "-j", "DROP"];
        iptables::remove_rule(&del_drop, "user DROP")?;

        // Destroy ipset.
        ipset::destroy_set(&set_name)?;

        info!(user_id = %safe_user, vpn_ip = %vpn_ip, "revoke_user_isolation applied");

        Ok(Response::new(RevokeUserIsolationResponse {
            revoked: true,
            message: "user isolation revoked".to_string(),
        }))
    }

    // ── GrantPodAccess ────────────────────────────────────────────────────────
    async fn grant_pod_access(
        &self,
        req: Request<GrantPodAccessRequest>,
    ) -> Result<Response<GrantPodAccessResponse>, Status> {
        let r = req.into_inner();
        validate_not_empty(&r.user_id, "user_id")?;
        let pod_ip = validate_ipv4(&r.pod_ip, "pod_ip")?;

        let safe_user = ipset::sanitize_user_id(&r.user_id);
        let set_name = ipset::ipset_name(&safe_user);

        // Ensure ipset exists (may not yet if VPN not connected).
        ipset::create_set(&set_name)?;
        // Add pod_ip to the user's allowed set.
        ipset::add_ip(&set_name, &pod_ip)?;

        info!(user_id = %safe_user, pod_ip = %pod_ip, "grant_pod_access applied");

        Ok(Response::new(GrantPodAccessResponse {
            applied: true,
            message: "pod access granted".to_string(),
        }))
    }

    // ── RevokePodAccess ───────────────────────────────────────────────────────
    async fn revoke_pod_access(
        &self,
        req: Request<RevokePodAccessRequest>,
    ) -> Result<Response<RevokePodAccessResponse>, Status> {
        let r = req.into_inner();
        validate_not_empty(&r.user_id, "user_id")?;
        let pod_ip = validate_ipv4(&r.pod_ip, "pod_ip")?;

        let safe_user = ipset::sanitize_user_id(&r.user_id);
        let set_name = ipset::ipset_name(&safe_user);

        // Remove pod_ip from the user's allowed set (idempotent).
        ipset::del_ip(&set_name, &pod_ip)?;

        info!(user_id = %safe_user, pod_ip = %pod_ip, "revoke_pod_access applied");

        Ok(Response::new(RevokePodAccessResponse {
            revoked: true,
            message: "pod access revoked".to_string(),
        }))
    }

    // ── EnsureWireGuardPeer ───────────────────────────────────────────────────
    async fn ensure_wire_guard_peer(
        &self,
        req: Request<EnsureWireGuardPeerRequest>,
    ) -> Result<Response<EnsureWireGuardPeerResponse>, Status> {
        let r = req.into_inner();
        validate_not_empty(&r.user_id, "user_id")?;
        validate_not_empty(&r.public_key, "public_key")?;
        let vpn_ip = validate_ipv4(&r.vpn_ip, "vpn_ip")?;
        let public_key = validate_public_key(&r.public_key)?;

        wireguard::ensure_peer(&r.user_id, &public_key, &vpn_ip)?;

        info!(user_id = %r.user_id, vpn_ip = %vpn_ip, "ensure_wire_guard_peer applied");

        Ok(Response::new(EnsureWireGuardPeerResponse {
            applied: true,
            message: "WireGuard peer ensured".to_string(),
        }))
    }

    // ── RevokeWireGuardPeer ───────────────────────────────────────────────────
    async fn revoke_wire_guard_peer(
        &self,
        req: Request<RevokeWireGuardPeerRequest>,
    ) -> Result<Response<RevokeWireGuardPeerResponse>, Status> {
        let r = req.into_inner();
        validate_not_empty(&r.user_id, "user_id")?;
        let public_key = validate_public_key(&r.public_key)?;

        wireguard::revoke_peer(&r.user_id, &public_key)?;

        info!(user_id = %r.user_id, "revoke_wire_guard_peer applied");

        Ok(Response::new(RevokeWireGuardPeerResponse {
            revoked: true,
            message: "WireGuard peer revoked".to_string(),
        }))
    }

    // ── GetWireGuardStatus ────────────────────────────────────────────────────
    async fn get_wire_guard_status(
        &self,
        _req: Request<GetWireGuardStatusRequest>,
    ) -> Result<Response<GetWireGuardStatusResponse>, Status> {
        let status = wireguard::get_status()?;
        let peers = status
            .peers
            .into_iter()
            .map(|p| WireGuardPeer {
                public_key: p.public_key,
                vpn_ip: p.vpn_ip,
                endpoint: p.endpoint,
                last_handshake_unix: p.last_handshake_unix,
                rx_bytes: p.rx_bytes,
                tx_bytes: p.tx_bytes,
                connected: p.connected,
            })
            .collect();

        Ok(Response::new(GetWireGuardStatusResponse {
            interface_up: status.interface_up,
            active_peers: status.active_peers,
            total_peers: status.total_peers,
            peers,
        }))
    }
}

// ─── Input validation helpers ─────────────────────────────────────────────────

fn validate_not_empty(value: &str, field: &str) -> Result<(), Status> {
    if value.trim().is_empty() {
        return Err(Status::invalid_argument(format!("{field} is required")));
    }
    Ok(())
}

fn validate_ipv4(ip: &str, field: &str) -> Result<String, Status> {
    let ip = ip.trim();
    match ip.parse::<IpAddr>() {
        Ok(IpAddr::V4(_)) => Ok(ip.to_string()),
        _ => Err(Status::invalid_argument(format!(
            "{field} must be a valid IPv4 address, got {ip:?}"
        ))),
    }
}

fn validate_public_key(key: &str) -> Result<String, Status> {
    let key = key.trim();
    if key.is_empty() {
        return Err(Status::invalid_argument("public_key is required"));
    }
    let valid = key.chars().all(|c| c.is_ascii_alphanumeric() || c == '+' || c == '/' || c == '=');
    if !valid {
        return Err(Status::invalid_argument("public_key contains invalid characters"));
    }
    if key.len() < 32 || key.len() > 128 {
        return Err(Status::invalid_argument("public_key length is invalid"));
    }
    Ok(key.to_string())
}

// ─── System metrics (best-effort) ─────────────────────────────────────────────

fn system_metrics() -> (f64, f64, f64) {
    // Read /proc/stat for CPU, /proc/meminfo for memory, /proc/mounts for disk.
    // Returns (cpu_percent, mem_percent, disk_percent) — all 0.0 if unavailable.
    let cpu = read_cpu_percent().unwrap_or(0.0);
    let mem = read_mem_percent().unwrap_or(0.0);
    (cpu, mem, 0.0) // disk omitted for now
}

fn read_cpu_percent() -> Option<f64> {
    // Simple idle percentage from /proc/stat first line.
    let content = std::fs::read_to_string("/proc/stat").ok()?;
    let line = content.lines().next()?;
    let nums: Vec<u64> = line.split_whitespace()
        .skip(1)
        .filter_map(|s| s.parse().ok())
        .collect();
    if nums.len() < 5 {
        return None;
    }
    let total: u64 = nums.iter().sum();
    let idle = nums[3];
    if total == 0 { return None; }
    Some(100.0 - (idle as f64 / total as f64 * 100.0))
}

fn read_mem_percent() -> Option<f64> {
    let content = std::fs::read_to_string("/proc/meminfo").ok()?;
    let mut total = 0u64;
    let mut available = 0u64;
    for line in content.lines() {
        if line.starts_with("MemTotal:") {
            total = line.split_whitespace().nth(1)?.parse().ok()?;
        } else if line.starts_with("MemAvailable:") {
            available = line.split_whitespace().nth(1)?.parse().ok()?;
        }
    }
    if total == 0 { return None; }
    Some((total - available) as f64 / total as f64 * 100.0)
}

fn uptime_secs() -> i64 {
    std::fs::read_to_string("/proc/uptime")
        .ok()
        .and_then(|s| s.split_whitespace().next()?.parse::<f64>().ok())
        .map(|f| f as i64)
        .unwrap_or(0)
}
