package mcpfederation

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// authRoundTripper adds exactly the configured static credential to requests
// sent to one endpoint.  Redirects are disabled by newHTTPClient so the
// credential cannot be forwarded to another origin.
type authRoundTripper struct {
	base          http.RoundTripper
	auth          Auth
	maxBody       int64
	closeTimeout  time.Duration
	shutdown      <-chan struct{}
	cancelRequest func(*http.Request)
}

func (t authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.shutdown != nil {
		select {
		case <-t.shutdown:
			return nil, context.Canceled
		default:
		}
	}
	requestContext := req.Context()
	var cancel context.CancelFunc
	if t.shutdown != nil {
		requestContext, cancel = context.WithCancel(requestContext)
	}
	clone := req.Clone(requestContext)
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	if t.shutdown != nil {
		go func(request *http.Request) {
			select {
			case <-t.shutdown:
				if t.cancelRequest != nil {
					t.cancelRequest(request)
				}
				cancel()
			case <-requestContext.Done():
			}
		}(clone)
	}
	switch t.auth.Scheme {
	case "", AuthNone:
	case AuthBearer:
		clone.Header.Set("Authorization", "Bearer "+string(t.auth.Secret))
	case AuthHeader:
		clone.Header.Set(t.auth.Header, string(t.auth.Secret))
	}
	// Streamable HTTP's standalone GET is a long-lived notification stream and
	// must not be capped by the per-response JSON limit. DELETE only carries a
	// status response, while MCP requests use POST and are the bounded JSON
	// surfaces. Keep DELETE cancellation bounded so a dead remote cannot hold
	// Federation.Close forever.
	if clone.Method == http.MethodDelete && t.closeTimeout > 0 {
		ctx, cancel := context.WithTimeout(clone.Context(), t.closeTimeout)
		defer cancel()
		clone = clone.WithContext(ctx)
	}
	response, err := t.base.RoundTrip(clone)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	if cancel != nil {
		if response == nil || response.Body == nil {
			cancel()
		} else {
			response.Body = &cancelOnCloseBody{ReadCloser: response.Body, cancel: cancel}
		}
	}
	if response != nil && response.Body != nil && clone.Method == http.MethodPost && t.maxBody > 0 {
		response.Body = &limitedBody{ioReadCloser: response.Body, remaining: t.maxBody}
	}
	return response, nil
}

type cancelOnCloseBody struct {
	io.ReadCloser
	once   sync.Once
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.cancel)
	return err
}

type limitedBody struct {
	ioReadCloser
	remaining int64
}

// ioReadCloser keeps this file's transport surface small while still making
// the limit observable to the SDK decoder.
type ioReadCloser interface {
	Read([]byte) (int, error)
	Close() error
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		// Probe one byte after the configured limit. This distinguishes an
		// exactly-at-limit body (which must remain valid JSON) from a body that
		// has crossed the limit without buffering unbounded data.
		var one [1]byte
		for {
			n, err := b.ioReadCloser.Read(one[:])
			if n > 0 {
				return 0, ErrResponseTooLarge
			}
			if err != nil {
				return 0, err
			}
			// A zero-byte, nil-error read is permitted by io.Reader but is
			// unusual for HTTP bodies. Avoid spinning forever on a broken remote.
			return 0, ErrResponseTooLarge
		}
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ioReadCloser.Read(p)
	b.remaining -= int64(n)
	if err == nil && b.remaining == 0 {
		// Returning the final bytes with a nil error lets decoders accept a body
		// exactly at the configured limit; the next read fails closed.
		return n, nil
	}
	return n, err
}

func newHTTPClient(auth Auth, maxBody int64, shutdown ...<-chan struct{}) *http.Client {
	// Keep the HTTP transport's credential independent from the connection
	// config so final connection teardown can scrub its retained bytes without
	// racing a pending session DELETE.
	auth.Secret = append([]byte(nil), auth.Secret...)
	var base http.RoundTripper = http.DefaultTransport
	var cancelRequest func(*http.Request)
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		clone := transport.Clone()
		// External MCP credentials must not be sent through ambient proxy
		// configuration. Scenery connections own their endpoint and auth; a
		// caller that needs a proxy must provide an explicit transport later.
		clone.Proxy = nil
		// A connection cannot downgrade TLS. Preserve any explicitly supplied
		// roots/options while requiring TLS 1.2 or newer.
		tlsConfig := &tls.Config{}
		if transport.TLSClientConfig != nil {
			tlsConfig = transport.TLSClientConfig.Clone()
		}
		if tlsConfig.MinVersion < tls.VersionTLS12 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
		clone.TLSClientConfig = tlsConfig
		base = clone
		cancelRequest = clone.CancelRequest
	}
	var stop <-chan struct{}
	if len(shutdown) > 0 {
		stop = shutdown[0]
	}
	return &http.Client{
		Transport: authRoundTripper{base: base, auth: auth, maxBody: maxBody, closeTimeout: 2 * time.Second, shutdown: stop, cancelRequest: cancelRequest},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		// Keep the package's own network stack explicit. A custom default
		// RoundTripper is still honored, but ordinary transports cannot inherit
		// proxy settings or a global client timeout accidentally.
		Timeout: 0,
	}
}

func connect(ctx context.Context, cfg Connection, maxBody int64, notify chan<- struct{}, shutdown <-chan struct{}) (*mcp.Client, *mcp.ClientSession, error) {
	if maxBody <= 0 {
		maxBody = maxHTTPResponse
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "scenery", Version: "mcp-federation"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			select {
			case notify <- struct{}{}:
			default:
			}
		},
	})
	transport := &mcp.StreamableClientTransport{
		Endpoint:   cfg.URL,
		HTTPClient: newHTTPClient(cfg.Auth, maxBody, shutdown),
		MaxRetries: -1,
	}
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if errors.Is(err, ErrResponseTooLarge) {
			return nil, nil, ErrResponseTooLarge
		}
		return nil, nil, ErrRemoteUnavailable
	}
	if result := session.InitializeResult(); result == nil || result.ProtocolVersion != ProtocolVersion {
		_ = session.Close()
		return nil, nil, ErrProtocolVersion
	}
	return client, session, nil
}
