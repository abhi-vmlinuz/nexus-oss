// Package nodeagent provides the gRPC client for nexus-node-agent.
// The engine calls this client to delegate privileged host operations.
package nodeagent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/nexus-oss/nexus/nexus-engine/internal/config"
	pb "github.com/nexus-oss/nexus/nexus-engine/gen/contracts/nexus/nodeagent/v1"
)

const defaultCallTimeout = 10 * time.Second

// Client wraps the gRPC connection to nexus-node-agent.
type Client struct {
	conn   *grpc.ClientConn
	client pb.NodeAgentServiceClient
	cfg    config.NodeAgentConfig
}

// New dials the node agent and returns a ready Client.
func New(cfg config.NodeAgentConfig) (*Client, error) {
	var creds credentials.TransportCredentials

	if cfg.Insecure {
		creds = insecure.NewCredentials()
	} else {
		tlsCfg, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("node agent TLS config: %w", err)
		}
		creds = credentials.NewTLS(tlsCfg)
	}

	conn, err := grpc.NewClient(cfg.Addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("dial node agent %s: %w", cfg.Addr, err)
	}

	return &Client{
		conn:   conn,
		client: pb.NewNodeAgentServiceClient(conn),
		cfg:    cfg,
	}, nil
}

// Close releases the gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Health checks node agent reachability and returns health metrics.
func (c *Client) Health(ctx context.Context) (*pb.HealthResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	return c.client.Health(ctx, &pb.HealthRequest{})
}

// EnsureUserIsolation creates an ipset for user_id and adds vpn_ip.
// Idempotent: safe to call multiple times.
func (c *Client) EnsureUserIsolation(ctx context.Context, userID, vpnIP string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	resp, err := c.client.EnsureUserIsolation(ctx, &pb.EnsureUserIsolationRequest{
		UserId: userID,
		VpnIp:  vpnIP,
	})
	if err != nil {
		return fmt.Errorf("EnsureUserIsolation(%s, %s): %w", userID, vpnIP, err)
	}
	if !resp.Applied {
		return fmt.Errorf("EnsureUserIsolation(%s, %s): not applied: %s", userID, vpnIP, resp.Message)
	}
	return nil
}

// RevokeUserIsolation removes vpn_ip from the user's ipset.
// Idempotent: returns nil if already absent.
func (c *Client) RevokeUserIsolation(ctx context.Context, userID, vpnIP string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	_, err := c.client.RevokeUserIsolation(ctx, &pb.RevokeUserIsolationRequest{
		UserId: userID,
		VpnIp:  vpnIP,
	})
	return err
}

// GrantPodAccess adds pod_ip to the user's ipset.
// Idempotent: safe to call multiple times.
func (c *Client) GrantPodAccess(ctx context.Context, userID, podIP string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	resp, err := c.client.GrantPodAccess(ctx, &pb.GrantPodAccessRequest{
		UserId: userID,
		PodIp:  podIP,
	})
	if err != nil {
		return fmt.Errorf("GrantPodAccess(%s, %s): %w", userID, podIP, err)
	}
	if !resp.Applied {
		return fmt.Errorf("GrantPodAccess(%s, %s): not applied: %s", userID, podIP, resp.Message)
	}
	return nil
}

// RevokePodAccess removes pod_ip from the user's ipset.
// Idempotent: returns nil if already absent.
func (c *Client) RevokePodAccess(ctx context.Context, userID, podIP string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	_, err := c.client.RevokePodAccess(ctx, &pb.RevokePodAccessRequest{
		UserId: userID,
		PodIp:  podIP,
	})
	return err
}

// EnsureWireGuardPeer adds or updates a WireGuard peer. Idempotent.
func (c *Client) EnsureWireGuardPeer(ctx context.Context, userID, publicKey, vpnIP string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	resp, err := c.client.EnsureWireGuardPeer(ctx, &pb.EnsureWireGuardPeerRequest{
		UserId:    userID,
		PublicKey: publicKey,
		VpnIp:     vpnIP,
	})
	if err != nil {
		return fmt.Errorf("EnsureWireGuardPeer(%s): %w", userID, err)
	}
	if !resp.Applied {
		return fmt.Errorf("EnsureWireGuardPeer(%s): not applied: %s", userID, resp.Message)
	}
	return nil
}

// RevokeWireGuardPeer removes a WireGuard peer. Idempotent.
func (c *Client) RevokeWireGuardPeer(ctx context.Context, userID, publicKey string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	_, err := c.client.RevokeWireGuardPeer(ctx, &pb.RevokeWireGuardPeerRequest{
		UserId:    userID,
		PublicKey: publicKey,
	})
	return err
}

// GetWireGuardStatus returns WireGuard interface and peer status.
func (c *Client) GetWireGuardStatus(ctx context.Context) (*pb.GetWireGuardStatusResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	return c.client.GetWireGuardStatus(ctx, &pb.GetWireGuardStatusRequest{})
}

// buildTLSConfig constructs mTLS credentials from cert files.
func buildTLSConfig(cfg config.NodeAgentConfig) (*tls.Config, error) {
	caCert, err := os.ReadFile(cfg.TLSCA)
	if err != nil {
		return nil, fmt.Errorf("read CA cert %s: %w", cfg.TLSCA, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA cert")
	}
	clientCert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		return nil, fmt.Errorf("load client cert/key: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      pool,
	}, nil
}
