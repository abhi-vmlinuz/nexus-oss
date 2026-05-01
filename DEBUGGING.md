# Nexus OSS Debugging Guide

This document covers common issues encountered during the setup and operation of the Nexus Engine, Node Agent, and CLI.

## 1. Engine Connectivity Issues (404/Not Found)

If the CLI TUI shows `404 page not found` for specific tabs (Cluster, Registry, etc.):
- **Cause**: The `nexus-engine` service might be running an outdated binary.
- **Solution**: Rebuild the engine from source and restart the service.
  ```bash
  cd nexus-engine
  go build -o nexus-engine ./cmd/
  sudo mv nexus-engine /usr/local/bin/nexus-engine
  sudo systemctl restart nexus-engine
  ```

## 2. Service Startup Failures (status=203/EXEC)

If `systemctl status nexus-engine` shows `failed (Result: exit-code)` with `status=203/EXEC` and the logs mention `Permission denied`:
- **Cause**: On systems like Fedora, SELinux prevents systemd from executing binaries that inherit security labels from the user's home directory (e.g., `user_home_t`).
- **Solution**: Relabel the binary to the correct security context.
  ```bash
  sudo restorecon -v /usr/local/bin/nexus-engine
  sudo systemctl restart nexus-engine
  ```

## 3. Redis Connectivity (IPv6 Loopback)

If the Engine fails to connect to Redis with "connection refused":
- **Cause**: Some distributions prioritize `[::1]` (IPv6) for `localhost`, but Redis may only be listening on `127.0.0.1` (IPv4).
- **Solution**: Explicitly use `127.0.0.1` in your `nexus-engine.service` or configuration files.

## 4. TUI Loading Hang (Infinite Loading Screen)

If a tab in the TUI shows "loading..." indefinitely but the API returns 200 OK:
- **Cause**: The API might be returning `null` for a collection instead of an empty slice `[]`, causing the TUI rendering logic to wait indefinitely.
- **Solution**: Check the API logs and ensure the backend initializes response slices: `var result = []Type{}`.

## 5. Node Agent RPC Errors

If the Metrics tab shows "Node Agent RPC errors":
- **Cause**: The engine cannot reach the `nexus-node-agent` on port `50051`.
- **Solution**: 
  - Ensure the agent is running: `sudo systemctl status nexus-node-agent`
  - Verify the agent address in the engine service is set to `127.0.0.1:50051`.
