package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func edgeTestTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "onlv.dev"},
		DNSNames:     []string{"onlv.dev"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestProbeEdgeTLSClassifiesOutcomes(t *testing.T) {
	timeout := 2 * time.Second

	// unreachable: nothing listens on the address.
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()
	if got := probeEdgeTLS(closedAddr, "onlv.dev", timeout); got.Outcome != edgeTLSProbeUnreachable {
		t.Fatalf("closed port probe = %+v", got)
	}

	// dropped: the listener accepts TCP and resets without any TLS reply —
	// the signature of a privileged helper refusing its target metadata.
	// This is what Cloudflare surfaces as error 525.
	dropper, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dropper.Close()
	go func() {
		for {
			conn, err := dropper.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	if got := probeEdgeTLS(dropper.Addr().String(), "onlv.dev", timeout); got.Outcome != edgeTLSProbeDropped {
		t.Fatalf("dropping listener probe = %+v", got)
	}

	// handshake_ok: a real TLS server completes the handshake.
	cert := edgeTestTLSCertificate(t)
	tlsListener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer tlsListener.Close()
	go func() {
		for {
			conn, err := tlsListener.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).Handshake()
				_ = conn.Close()
			}()
		}
	}()
	if got := probeEdgeTLS(tlsListener.Addr().String(), "onlv.dev", timeout); got.Outcome != edgeTLSProbeHandshakeOK {
		t.Fatalf("tls server probe = %+v", got)
	}

	// forwarded: the far end answers at the TLS layer with an alert (for
	// example strict SNI with no certificate for the probed host). Bytes
	// flowed end to end, so forwarding works even though the handshake
	// failed.
	alertListener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return nil, fmt.Errorf("no certificate for this server name")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer alertListener.Close()
	go func() {
		for {
			conn, err := alertListener.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).Handshake()
				_ = conn.Close()
			}()
		}
	}()
	if got := probeEdgeTLS(alertListener.Addr().String(), "unknown.example", timeout); got.Outcome != edgeTLSProbeForwarded {
		t.Fatalf("tls alert probe = %+v", got)
	}
}

func TestApplyEdgeHelperForwardingProbeRequiresTLSThroughHelper(t *testing.T) {
	oldProbe := edgeTLSProbeFunc
	t.Cleanup(func() { edgeTLSProbeFunc = oldProbe })

	probes := map[string]edgeTLSProbeResult{}
	edgeTLSProbeFunc = func(addr, serverName string, timeout time.Duration) edgeTLSProbeResult {
		return probes[addr]
	}

	// A live launchd PID plus a bound listener is not readiness: when port
	// 443 accepts TCP but drops TLS while Caddy answers TLS directly, the
	// helper itself is broken.
	probes["127.0.0.1:443"] = edgeTLSProbeResult{Outcome: edgeTLSProbeDropped, Error: "EOF"}
	probes["127.0.0.1:19443"] = edgeTLSProbeResult{Outcome: edgeTLSProbeHandshakeOK}
	status := edgeStatusPrivilegedListener{State: "running", Target: "127.0.0.1:19443"}
	applyEdgeHelperForwardingProbe(&status, "onlv.dev")
	if status.State != "unhealthy" {
		t.Fatalf("dropping helper state = %+v", status)
	}
	if !strings.Contains(status.Message, "deploy setup") {
		t.Fatalf("dropping helper message = %q", status.Message)
	}

	// Helper drops because Caddy is down: fail-closed is correct behavior,
	// so the helper stays running with an explanation.
	probes["127.0.0.1:19443"] = edgeTLSProbeResult{Outcome: edgeTLSProbeUnreachable, Error: "connection refused"}
	status = edgeStatusPrivilegedListener{State: "running", Target: "127.0.0.1:19443"}
	applyEdgeHelperForwardingProbe(&status, "onlv.dev")
	if status.State != "running" || !strings.Contains(status.Message, "fail-closed") {
		t.Fatalf("fail-closed state = %+v", status)
	}

	// A completed handshake through 443 is healthy.
	probes["127.0.0.1:443"] = edgeTLSProbeResult{Outcome: edgeTLSProbeHandshakeOK}
	status = edgeStatusPrivilegedListener{State: "running", Target: "127.0.0.1:19443"}
	applyEdgeHelperForwardingProbe(&status, "onlv.dev")
	if status.State != "running" || status.Message != "" {
		t.Fatalf("healthy state = %+v", status)
	}

	// A TLS alert through 443 proves forwarding even when the handshake for
	// the probe host does not complete.
	probes["127.0.0.1:443"] = edgeTLSProbeResult{Outcome: edgeTLSProbeForwarded, Error: "remote error: tls: internal error"}
	status = edgeStatusPrivilegedListener{State: "running", Target: "127.0.0.1:19443"}
	applyEdgeHelperForwardingProbe(&status, "onlv.dev")
	if status.State != "running" {
		t.Fatalf("forwarded state = %+v", status)
	}

	// No TCP accept on 443 is unhealthy.
	probes["127.0.0.1:443"] = edgeTLSProbeResult{Outcome: edgeTLSProbeUnreachable, Error: "connection refused"}
	status = edgeStatusPrivilegedListener{State: "running", Target: "127.0.0.1:19443"}
	applyEdgeHelperForwardingProbe(&status, "onlv.dev")
	if status.State != "unhealthy" {
		t.Fatalf("unreachable state = %+v", status)
	}
}
