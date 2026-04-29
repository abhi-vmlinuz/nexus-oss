// adapters/ipset.rs — ipset kernel subsystem adapter for nexus-node-agent.
// All operations are idempotent: create if not exists, add if not member, etc.
use std::process::Command;

use tonic::Status;
use tracing::{info, warn};

fn run_expecting_success(program: &str, args: &[&str], ctx: &str) -> Result<(), Status> {
    let out = Command::new(program)
        .args(args)
        .output()
        .map_err(|e| Status::internal(format!("exec {program}: {e}")))?;

    if out.status.success() {
        return Ok(());
    }

    let stderr = String::from_utf8_lossy(&out.stderr);
    Err(Status::internal(format!(
        "{ctx} failed (exit {}): {}",
        out.status.code().unwrap_or(-1),
        stderr.trim()
    )))
}

/// Create an ipset (hash:ip type) if it doesn't already exist.
pub fn create_set(name: &str) -> Result<(), Status> {
    run_expecting_success("ipset", &["create", name, "hash:ip", "-exist"], "ipset create")
}

/// Add an IP to a set. Idempotent via -exist flag.
pub fn add_ip(set_name: &str, ip: &str) -> Result<(), Status> {
    run_expecting_success("ipset", &["add", set_name, ip, "-exist"], "ipset add")
}

/// Remove an IP from a set. Returns Ok if IP is not in the set.
pub fn del_ip(set_name: &str, ip: &str) -> Result<(), Status> {
    let out = Command::new("ipset")
        .args(["del", set_name, ip])
        .output()
        .map_err(|e| Status::internal(format!("exec ipset: {e}")))?;

    if out.status.success() {
        return Ok(());
    }
    let stderr = String::from_utf8_lossy(&out.stderr).to_lowercase();
    // "element not found" means it was already absent — that's fine.
    if stderr.contains("element not found") || stderr.contains("does not exist") {
        return Ok(());
    }
    Err(Status::internal(format!(
        "ipset del failed: {}",
        String::from_utf8_lossy(&out.stderr).trim()
    )))
}

/// Destroy a set. Returns Ok if set doesn't exist.
pub fn destroy_set(name: &str) -> Result<(), Status> {
    let out = Command::new("ipset")
        .args(["destroy", name])
        .output()
        .map_err(|e| Status::internal(format!("exec ipset: {e}")))?;

    if out.status.success() {
        return Ok(());
    }
    let stderr = String::from_utf8_lossy(&out.stderr).to_lowercase();
    if stderr.contains("does not exist") {
        return Ok(());
    }
    // "set cannot be destroyed" means it still has members — not an error in revoke context.
    if stderr.contains("set cannot be destroyed") {
        warn!(set = name, "ipset not empty during destroy, skipping");
        return Ok(());
    }
    Err(Status::internal(format!(
        "ipset destroy {name} failed: {}",
        String::from_utf8_lossy(&out.stderr).trim()
    )))
}

/// Build the canonical ipset name for a user.
pub fn ipset_name(user_id: &str) -> String {
    format!("nexus-user-{}", sanitize_user_id(user_id))
}

/// Sanitize user_id for use in ipset names (max 26 chars, alphanumeric + hyphen/underscore).
pub fn sanitize_user_id(user_id: &str) -> String {
    let mut s: String = user_id
        .to_lowercase()
        .chars()
        .map(|c| if c == ' ' { '-' } else { c })
        .filter(|c| c.is_ascii_alphanumeric() || *c == '_' || *c == '-')
        .collect();
    s.truncate(26);
    if s.is_empty() {
        "unknown".to_string()
    } else {
        s
    }
}
