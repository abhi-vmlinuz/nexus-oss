// adapters/iptables.rs — iptables FORWARD chain adapter for nexus-node-agent.
// All operations are idempotent: check-before-insert, loop-until-absent on delete.
use std::process::Command;

use tonic::Status;

/// Check if an iptables rule exists (-C), then insert (-I) if absent.
pub fn ensure_rule(check_args: &[&str], insert_args: &[&str], desc: &str) -> Result<(), Status> {
    let check = Command::new("iptables")
        .args(check_args)
        .output()
        .map_err(|e| Status::internal(format!("iptables check exec: {e}")))?;

    if check.status.success() {
        return Ok(()); // Rule already exists.
    }

    // Rule absent — insert.
    let insert = Command::new("iptables")
        .args(insert_args)
        .output()
        .map_err(|e| Status::internal(format!("iptables insert exec: {e}")))?;

    if !insert.status.success() {
        return Err(Status::internal(format!(
            "iptables insert {desc} failed: {}",
            String::from_utf8_lossy(&insert.stderr).trim()
        )));
    }
    Ok(())
}

/// Delete an iptables rule (-D) repeatedly until absent.
pub fn remove_rule(delete_args: &[&str], desc: &str) -> Result<(), Status> {
    loop {
        let out = Command::new("iptables")
            .args(delete_args)
            .output()
            .map_err(|e| Status::internal(format!("iptables delete exec: {e}")))?;

        if out.status.success() {
            continue; // Deleted one; loop to remove duplicates.
        }

        let stderr = String::from_utf8_lossy(&out.stderr).to_lowercase();
        if stderr.contains("no chain") || stderr.contains("bad rule") || stderr.contains("does not exist") {
            break; // Rule is gone — success.
        }
        if stderr.contains("permission denied") {
            return Err(Status::permission_denied(format!(
                "iptables delete {desc}: permission denied"
            )));
        }
        break; // Unknown error, stop looping.
    }
    Ok(())
}
