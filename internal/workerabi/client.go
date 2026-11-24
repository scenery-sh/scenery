package workerabi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"time"

	"scenery.sh/internal/contract"
)

// Client addresses one immutable worker instance. Its transport does not
// follow redirects, negotiate HTTP/1, or use the ambient HTTP proxy.
type Client struct {
	identity  Identity
	token     string
	transport *http.Transport
}

type dialError struct{ cause error }

func (e *dialError) Error() string { return e.cause.Error() }
func (e *dialError) Unwrap() error { return e.cause }

func NewClient(socket, token string, identity Identity) *Client {
	return &Client{identity: identity, token: token, transport: NewTransport(socket)}
}

// NewTransport pools private HTTP/2 connections to one Unix listener.
func NewTransport(socket string) *http.Transport {
	dialer := &net.Dialer{}
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, "unix", socket)
			if err != nil {
				return nil, &dialError{cause: err}
			}
			return conn, nil
		},
		Protocols:          Protocols(),
		DisableCompression: true,
		IdleConnTimeout:    90 * time.Second,
	}
}

// WasNotSent reports whether a failed dial prevented delivery.
func WasNotSent(err error) bool {
	_, ok := errors.AsType[*dialError](err)
	return ok
}

// Protocols is also used by the Host's capability socket.
func Protocols() *http.Protocols {
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return protocols
}

func (c *Client) Close() { c.transport.CloseIdleConnections() }

func (c *Client) Ready(ctx context.Context) error {
	response, err := c.request(ctx, http.MethodGet, ReadyPath, nil)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if err := c.verifyResponse(response); err != nil {
		return err
	}
	data, err := readMetadata(response.Body)
	if err != nil {
		return err
	}
	var actual Identity
	members, err := contract.DecodeJSONObject(data)
	if err != nil {
		return err
	}
	if len(members) != 4 || json.Unmarshal(data, &actual) != nil || actual != c.identity {
		return fmt.Errorf("worker readiness identity mismatch")
	}
	return nil
}

// Result owns the HTTP/2 stream until Stream is closed when the worker streams
// bytes. The caller must close it even when it rejects the outcome.
type Result struct {
	Outcome                json.RawMessage
	Stream                 io.ReadCloser
	StreamSize             int64
	ProcessID              int
	ImplementationRevision string
}

func (c *Client) Invoke(ctx context.Context, invocation Invocation) (Result, error) {
	encoded, err := json.Marshal(invocation)
	if err != nil {
		return Result{}, &DeliveryError{Delivery: NotSent, Cause: err}
	}
	if len(encoded) > MaxMetadataBytes {
		return Result{}, &DeliveryError{Delivery: NotSent, Cause: fmt.Errorf("invocation exceeds metadata limit")}
	}
	response, err := c.request(ctx, http.MethodPost, InvokePath, encoded)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		delivery := Unknown
		if WasNotSent(err) {
			delivery = NotSent
		}
		return Result{}, &DeliveryError{Delivery: delivery, Cause: err}
	}
	if response.StatusCode == http.StatusInternalServerError && c.verifyIdentity(response) == nil {
		_ = response.Body.Close()
		return Result{}, ErrImplementation
	}
	result, err := c.readInvocationResult(response)
	if result.Stream == nil {
		_ = response.Body.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, &DeliveryError{Delivery: Unknown, Cause: err}
	}
	return result, nil
}

func (c *Client) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://worker"+path, reader)
	if err != nil {
		return nil, err
	}
	// A fresh request body is unavailable after the transport starts to send
	// it. An ambiguous failure must never cause a transport replay.
	request.GetBody = nil
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return c.transport.RoundTrip(request)
}

func (c *Client) verifyResponse(response *http.Response) error {
	if err := c.verifyIdentity(response); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("worker returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (c *Client) verifyIdentity(response *http.Response) error {
	if response.Header.Get(ContractHeader) != c.identity.ContractRevision ||
		response.Header.Get(ImplementationHeader) != c.identity.ImplementationRevision {
		return fmt.Errorf("worker response identity mismatch")
	}
	return nil
}

func (c *Client) readInvocationResult(response *http.Response) (Result, error) {
	if err := c.verifyResponse(response); err != nil {
		return Result{}, err
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		return Result{}, fmt.Errorf("read worker content type: %w", err)
	}
	var data []byte
	switch mediaType {
	case "application/json":
		data, err = readMetadata(response.Body)
	case StreamMediaType:
		data, err = readStreamMetadata(response.Body)
	default:
		return Result{}, fmt.Errorf("unsupported worker content type %q", mediaType)
	}
	if err != nil {
		return Result{}, err
	}
	members, err := contract.DecodeJSONObject(data)
	if err != nil {
		return Result{}, err
	}
	outcome := members["outcome"]
	if len(members) != 1 || len(outcome) == 0 {
		return Result{}, fmt.Errorf("worker response must contain one outcome")
	}
	if _, _, _, err := contract.DecodeContractOutcomeEnvelope(outcome); err != nil {
		return Result{}, err
	}
	result := Result{Outcome: outcome}
	if mediaType == StreamMediaType {
		size, err := strconv.ParseInt(response.Header.Get(StreamSizeHeader), 10, 64)
		if err != nil || size < 0 {
			return Result{}, fmt.Errorf("worker streamed response has no exact length")
		}
		result.Stream = response.Body
		result.StreamSize = size
	}
	return result, nil
}

func readMetadata(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, MaxMetadataBytes+1))
	if err == nil && len(data) > MaxMetadataBytes {
		return nil, fmt.Errorf("worker response exceeds metadata limit")
	}
	return data, err
}

func readStreamMetadata(body io.Reader) ([]byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(body, prefix[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length > MaxMetadataBytes {
		return nil, fmt.Errorf("worker stream exceeds metadata limit")
	}
	data := make([]byte, int(length))
	_, err := io.ReadFull(body, data)
	return data, err
}
