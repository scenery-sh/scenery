package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"
)

// Dual-stack acceptance is an OS boundary. Bound both halves and exchange a
// nonce so connecting to a different listener can never count as success.
func runHarnessEdgeWildcardIPv4Probe(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	listener, err := listenEdgeHelperSpec(edgeHelperListenSpec{Addr: "[::]:0"})
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return err
	}
	const nonce = "scenery-ipv4-edge-probe"
	done := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = connection.Close() }()
		deadline, _ := ctx.Deadline()
		if err := connection.SetDeadline(deadline); err != nil {
			done <- err
			return
		}
		_, err = io.WriteString(connection, nonce)
		done <- err
	}()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp4", "127.0.0.1:"+port)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	deadline, _ := ctx.Deadline()
	if err := connection.SetReadDeadline(deadline); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(connection, int64(len(nonce)+1)))
	if err != nil {
		return err
	}
	if string(data) != nonce {
		return fmt.Errorf("IPv4 did not reach the exact dual-stack edge listener")
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
