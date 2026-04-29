// adapters/wireguard.rs — WireGuard peer management adapter.
// Manages /etc/wireguard/wg0.conf peer blocks and syncs runtime config via wg-quick.
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::process::Command;
use std::time::{SystemTime, UNIX_EPOCH};

use tonic::Status;
use tracing::warn;

const WG_CONFIG_PATH: &str = "/etc/wireguard/wg0.conf";
const HANDSHAKE_ACTIVE_SECS: i64 = 180;

/// Ensure a WireGuard peer exists (idempotent: removes old block if present, then appends).
pub fn ensure_peer(user_id: &str, public_key: &str, vpn_ip: &str) -> Result<(), Status> {
    remove_peer_block(user_id)?;
    append_peer_block(user_id, public_key, vpn_ip)?;
    reload_wireguard()
}

/// Revoke a WireGuard peer (idempotent).
pub fn revoke_peer(user_id: &str, public_key: &str) -> Result<(), Status> {
    remove_peer_block(user_id)?;
    // Also remove from runtime config.
    let out = Command::new("wg")
        .args(["set", "wg0", "peer", public_key, "remove"])
        .output()
        .map_err(|e| Status::internal(format!("wg set peer remove: {e}")))?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr).to_lowercase();
        if !stderr.contains("not found") && !stderr.contains("no peer") {
            return Err(Status::internal(format!(
                "wg remove peer failed: {}",
                String::from_utf8_lossy(&out.stderr).trim()
            )));
        }
    }
    reload_wireguard()
}

#[derive(Debug)]
pub struct WgStatus {
    pub interface_up: bool,
    pub total_peers: i32,
    pub active_peers: i32, // peers with handshake within HANDSHAKE_ACTIVE_SECS
    pub peers: Vec<WgPeer>,
}

#[derive(Debug)]
pub struct WgPeer {
    pub public_key: String,
    pub vpn_ip: String,
    pub endpoint: String,
    pub last_handshake_unix: i64,
    pub rx_bytes: i64,
    pub tx_bytes: i64,
    pub connected: bool,
}

/// Return WireGuard interface status and peer list.
pub fn get_status() -> Result<WgStatus, Status> {
    let out = Command::new("wg")
        .args(["show", "wg0", "dump"])
        .output()
        .map_err(|e| Status::internal(format!("wg show dump: {e}")))?;

    if !out.status.success() {
        return Ok(WgStatus {
            interface_up: false,
            total_peers: 0,
            active_peers: 0,
            peers: vec![],
        });
    }

    let output = String::from_utf8_lossy(&out.stdout);
    let now = current_unix();
    let mut peers = Vec::new();

    for (i, line) in output.lines().enumerate() {
        if i == 0 || line.trim().is_empty() {
            continue; // First line is the interface row.
        }
        let parts: Vec<&str> = line.split('\t').collect();
        if parts.len() < 8 {
            continue;
        }
        let public_key = parts[0].to_string();
        let endpoint = parts[2].to_string();
        let vpn_ip = parts[3]
            .split(',')
            .next()
            .unwrap_or_default()
            .trim_end_matches("/32")
            .to_string();
        let handshake = parts[4].parse::<i64>().unwrap_or_default();
        let rx = parts[5].parse::<i64>().unwrap_or_default();
        let tx = parts[6].parse::<i64>().unwrap_or_default();
        let connected = handshake > 0 && (now - handshake) <= HANDSHAKE_ACTIVE_SECS;

        peers.push(WgPeer {
            public_key,
            vpn_ip,
            endpoint,
            last_handshake_unix: handshake,
            rx_bytes: rx,
            tx_bytes: tx,
            connected,
        });
    }

    let active = peers.iter().filter(|p| p.connected).count() as i32;
    let total = peers.len() as i32;

    Ok(WgStatus {
        interface_up: true,
        total_peers: total,
        active_peers: active,
        peers,
    })
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

fn remove_peer_block(user_id: &str) -> Result<(), Status> {
    let safe = crate::adapters::ipset::sanitize_user_id(user_id);
    // Use sed to delete the "[Peer]" block tagged with "# User: <id>"
    let expr = format!("/# User: {}/,+2d", safe);
    let out = Command::new("sed")
        .args(["-i", &expr, WG_CONFIG_PATH])
        .output()
        .map_err(|e| Status::internal(format!("sed remove peer block: {e}")))?;
    if !out.status.success() {
        warn!(user_id = %safe, "sed remove peer block returned non-zero (possibly no match)");
    }
    Ok(())
}

fn append_peer_block(user_id: &str, public_key: &str, vpn_ip: &str) -> Result<(), Status> {
    let safe = crate::adapters::ipset::sanitize_user_id(user_id);
    let block = format!(
        "\n[Peer]\n# User: {safe}\nPublicKey = {public_key}\nAllowedIPs = {vpn_ip}/32\n"
    );
    let mut file = OpenOptions::new()
        .append(true)
        .open(WG_CONFIG_PATH)
        .map_err(|e| Status::internal(format!("open {WG_CONFIG_PATH}: {e}")))?;
    file.write_all(block.as_bytes())
        .map_err(|e| Status::internal(format!("write {WG_CONFIG_PATH}: {e}")))?;
    Ok(())
}

fn reload_wireguard() -> Result<(), Status> {
    // Strip and sync rather than restart (avoids dropping active handshakes).
    let stripped = run_capture("wg-quick", &["strip", "wg0"])?;
    let tmp = format!(
        "/tmp/nexus-wg0-{}.conf",
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_millis()
    );
    fs::write(&tmp, stripped)
        .map_err(|e| Status::internal(format!("write tmp wg config: {e}")))?;
    let result = run_ok("wg", &["syncconf", "wg0", &tmp]);
    let _ = fs::remove_file(&tmp);
    result
}

fn run_capture(program: &str, args: &[&str]) -> Result<String, Status> {
    let out = Command::new(program)
        .args(args)
        .output()
        .map_err(|e| Status::internal(format!("exec {program}: {e}")))?;
    if !out.status.success() {
        return Err(Status::internal(format!(
            "{program} failed: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        )));
    }
    Ok(String::from_utf8_lossy(&out.stdout).into_owned())
}

fn run_ok(program: &str, args: &[&str]) -> Result<(), Status> {
    let out = Command::new(program)
        .args(args)
        .output()
        .map_err(|e| Status::internal(format!("exec {program}: {e}")))?;
    if !out.status.success() {
        return Err(Status::internal(format!(
            "{program} failed: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        )));
    }
    Ok(())
}

fn current_unix() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64
}
