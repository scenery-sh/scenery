package main

import (
	"crypto/tls"
	"errors"
	"net"
	"time"

	localagent "scenery.sh/internal/agent"
)

// edgeTLSProbeOutcome classifies one loopback TLS reachability probe. The
// distinction that matters for readiness is whether TLS-level bytes came back
// at all: a listener that accepts TCP and then closes without any TLS reply
// is the signature of a privileged helper that refuses its target metadata.
type edgeTLSProbeOutcome string

const (
	// edgeTLSProbeUnreachable: the TCP dial itself failed.
	edgeTLSProbeUnreachable edgeTLSProbeOutcome = "unreachable"
	// edgeTLSProbeDropped: TCP connected but the connection closed or timed
	// out without any TLS-level reply.
	edgeTLSProbeDropped edgeTLSProbeOutcome = "dropped"
	// edgeTLSProbeForwarded: the far end answered at the TLS layer (alert or
	// non-TLS bytes), proving forwarding works even though the handshake for
	// this server name did not complete.
	edgeTLSProbeForwarded edgeTLSProbeOutcome = "forwarded"
	// edgeTLSProbeHandshakeOK: a full TLS handshake completed.
	edgeTLSProbeHandshakeOK edgeTLSProbeOutcome = "handshake_ok"
)

type edgeTLSProbeResult struct {
	Outcome edgeTLSProbeOutcome
	Error   string
}

func (r edgeTLSProbeResult) reachedTLSServer() bool {
	return r.Outcome == edgeTLSProbeHandshakeOK || r.Outcome == edgeTLSProbeForwarded
}

var (
	edgeTLSProbeFunc    = probeEdgeTLS
	edgeTLSProbeTimeout = 1500 * time.Millisecond
)

func probeEdgeTLS(addr, serverName string, timeout time.Duration) edgeTLSProbeResult {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return edgeTLSProbeResult{Outcome: edgeTLSProbeUnreachable, Error: err.Error()}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	client := tls.Client(conn, &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true, // reachability probe, not trust validation
		MinVersion:         tls.VersionTLS12,
	})
	err = client.Handshake()
	if err == nil {
		return edgeTLSProbeResult{Outcome: edgeTLSProbeHandshakeOK}
	}
	if edgeTLSServerReplied(err) {
		return edgeTLSProbeResult{Outcome: edgeTLSProbeForwarded, Error: err.Error()}
	}
	return edgeTLSProbeResult{Outcome: edgeTLSProbeDropped, Error: err.Error()}
}

func edgeTLSServerReplied(err error) bool {
	var record tls.RecordHeaderError
	if errors.As(err, &record) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "remote error" {
		return true
	}
	return false
}

// edgeProbeServerName picks the SNI for end-to-end probes through the
// privileged listener: a domain Caddy actually serves proves the most, so an
// enabled deploy domain wins over the synthetic local probe host.
func edgeProbeServerName(paths localagent.Paths) string {
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err == nil {
		for _, target := range registry.Targets {
			if target.Enabled {
				return target.Domain
			}
		}
	}
	return "scenery-edge-probe." + defaultEdgeDNSDomain
}
