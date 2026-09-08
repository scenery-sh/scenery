// This executable owns real SQL/HTTP auth journeys for the release verifier.
// It deliberately uses public auth/runtime entrypoints, not private test hooks.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"scenery.sh/auth"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/runtime"
)

const (
	gmailModify      = "https://www.googleapis.com/auth/gmail.modify"
	gmailRead        = "https://www.googleapis.com/auth/gmail.readonly"
	calendarEvents   = "https://www.googleapis.com/auth/calendar.events"
	configuredTenant = "d0540000-0000-0000-0000-0000000000aa"
)

type probe struct {
	ctx        context.Context
	db         *sql.DB
	q          *authdb.Queries
	client     *http.Client
	google     *fakeGoogle
	assertions []string
}

type failure struct{ err error }

func take[T any](value T, err error) T {
	if err != nil {
		panic(failure{err})
	}
	return value
}

func must(err error) {
	if err != nil {
		panic(failure{err})
	}
}

func (p *probe) check(ok bool, name string) {
	if !ok {
		panic(failure{fmt.Errorf("assertion failed: %s", name)})
	}
	p.assertions = append(p.assertions, name)
}

func main() {
	name := flag.String("case", "", "one release journey")
	report := flag.String("report", "", "owned JSON report path")
	list := flag.Bool("list", false, "list release journeys without starting resources")
	flag.Parse()
	if *list {
		names := make([]string, 0, len(journeys))
		for name := range journeys {
			names = append(names, name)
		}
		sort.Strings(names)
		must(json.NewEncoder(os.Stdout).Encode(names))
		return
	}
	if *name == "" || *report == "" {
		fmt.Fprintln(os.Stderr, "case and report are required")
		os.Exit(2)
	}
	p := &probe{}
	err := p.run(*name)
	result := struct {
		Case       string   `json:"case"`
		OK         bool     `json:"ok"`
		Assertions []string `json:"assertions"`
		Error      string   `json:"error,omitempty"`
	}{Case: *name, OK: err == nil, Assertions: p.assertions}
	if err != nil {
		result.Error = err.Error()
	}
	encoded, encodeErr := json.MarshalIndent(result, "", "  ")
	if encodeErr == nil {
		encodeErr = os.WriteFile(*report, append(encoded, '\n'), 0600)
	}
	if err != nil || encodeErr != nil {
		fmt.Fprintln(os.Stderr, errors.Join(err, encodeErr))
		os.Exit(1)
	}
}

func (p *probe) run(name string) (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if f, ok := recovered.(failure); ok {
				returnErr = f.err
			} else {
				panic(recovered)
			}
		}
	}()
	run, ok := journeys[name]
	if !ok {
		return fmt.Errorf("unknown auth journey %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p.ctx = ctx
	base := strings.TrimSpace(envpolicy.Get("DATABASE_URL"))
	if base == "" {
		return errors.New("owned DATABASE_URL is required; auth proof cannot skip")
	}
	p.db = take(postgresdb.Open(ctx, take(postgresdb.ServiceURL(base, "scenery"))))
	defer func() { returnErr = errors.Join(returnErr, p.db.Close()) }()
	p.q = authdb.New(p.db)
	p.google = newFakeGoogle()
	defer p.google.server.Close()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = p.google.transport(originalTransport)
	defer func() { http.DefaultTransport = originalTransport }()
	state := take(os.MkdirTemp("", "scenery-auth-probe-runtime-"))
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(state)) }()
	socket := filepath.Join(state, "api.sock")
	must(envpolicy.Set("SCENERY_LISTEN_NETWORK", "unix"))
	must(envpolicy.Set("JWT_SECRET", "owned-auth-release-secret"))
	must(envpolicy.Set("GOOGLE_OAUTH_CLIENT_ID", "client-id"))
	must(envpolicy.Set("GOOGLE_OAUTH_CLIENT_SECRET", "client-secret"))
	must(envpolicy.Set("AUTH_TOKEN_CIPHER_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI="))
	must(envpolicy.Set("SCENERY_PUBLIC_APP_URL", "https://app.example.test"))
	must(envpolicy.Set("SCENERY_API_BASE_URL", "https://api.example.test"))
	config := auth.StandardConfig{Enabled: true, AutoBootstrapDatabase: true, GoogleOAuth: auth.GoogleOAuthConfig{Enabled: true, AllowedScopes: []string{gmailModify, gmailRead}}}
	if name == "dev-new" || name == "dev-existing" {
		config.DevBootstrap = auth.DevBootstrapConfig{Enabled: true, DefaultUserEmail: "petr@example.test", DefaultTenantID: configuredTenant}
	}
	must(auth.RegisterStandard(config))
	done := make(chan error, 1)
	exited := false
	go func() { done <- runtime.Main(runtime.AppConfig{Name: "auth-release-probe", ListenAddr: socket}) }()
	defer func() {
		if exited {
			return
		}
		select {
		case err := <-done:
			returnErr = errors.Join(returnErr, err)
		default:
			process, err := os.FindProcess(os.Getpid())
			if err == nil {
				err = process.Signal(os.Interrupt)
			}
			returnErr = errors.Join(returnErr, err)
			select {
			case err := <-done:
				returnErr = errors.Join(returnErr, err)
			case <-time.After(5 * time.Second):
				returnErr = errors.Join(returnErr, errors.New("auth runtime did not stop"))
			}
		}
	}()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	p.client = &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		request := take(http.NewRequestWithContext(ctx, http.MethodGet, "http://api.example.test/auth/google/start", nil))
		response, err := p.client.Do(request)
		if err == nil {
			_, copyErr := io.Copy(io.Discard, response.Body)
			must(errors.Join(copyErr, response.Body.Close()))
			p.check(response.StatusCode == http.StatusFound, "standard auth initialized through public Google start")
			break
		}
		select {
		case err := <-done:
			exited = true
			return fmt.Errorf("auth runtime exited before readiness: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	run(p)
	return nil
}

func (p *probe) request(method, path string, input any, token, refresh string) (*http.Response, []byte) {
	var body io.Reader
	if input != nil {
		body = bytes.NewReader(take(json.Marshal(input)))
	}
	request := take(http.NewRequestWithContext(p.ctx, method, "http://api.example.test"+path, body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if refresh != "" {
		request.AddCookie(&http.Cookie{Name: "scenery_refresh", Value: refresh})
	}
	response := take(p.client.Do(request))
	encoded := take(io.ReadAll(response.Body))
	must(response.Body.Close())
	return response, encoded
}

func call[T any](p *probe, method, path string, input any, token, refresh string) (T, *http.Response) {
	response, body := p.request(method, path, input, token, refresh)
	if response.StatusCode != http.StatusOK {
		panic(failure{fmt.Errorf("%s %s returned %d: %s", method, path, response.StatusCode, body)})
	}
	var value T
	must(json.Unmarshal(body, &value))
	return value, response
}

func cookie(response *http.Response) string {
	for _, c := range response.Cookies() {
		if c.Name == "scenery_refresh" {
			return c.Value
		}
	}
	panic(failure{errors.New("refresh cookie missing")})
}

func id(value string) authdb.UUID       { var result authdb.UUID; must(result.Scan(value)); return result }
func newID() authdb.UUID                { return id(uuid.NewString()) }
func idString(value authdb.UUID) string { return uuid.UUID(value.Bytes).String() }

func (p *probe) redirect(response *http.Response, want string) {
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != want {
		panic(failure{fmt.Errorf("redirect status=%d location=%q, want 302 %q", response.StatusCode, response.Header.Get("Location"), want)})
	}
	p.check(response.StatusCode == http.StatusFound && response.Header.Get("Location") == want, "redirect "+want)
}

func (p *probe) signin() (auth.AuthSessionResponse, string) {
	callback, response := p.googleFlow("/")
	_ = callback
	return p.refresh(cookie(response))
}

func (p *probe) refresh(value string) (auth.AuthSessionResponse, string) {
	result, response := call[auth.AuthSessionResponse](p, http.MethodPost, "/auth/refresh", nil, "", value)
	return result, cookie(response)
}

func (p *probe) googleFlow(redirect string) (string, *http.Response) {
	start, _ := p.request(http.MethodGet, "/auth/google/start?redirect_path="+url.QueryEscape(redirect), nil, "", "")
	p.check(start.StatusCode == http.StatusFound, "Google start issues authorization redirect")
	callback := p.google.authorize(start.Header.Get("Location"))
	u := take(url.Parse(callback))
	response, _ := p.request(http.MethodGet, u.RequestURI(), nil, "", "")
	return u.RequestURI(), response
}

func (p *probe) signup(email string, verify bool) authdb.UUID {
	result, _ := call[auth.EmailSignupResponse](p, http.MethodPost, "/auth/signup/email", auth.EmailSignupParams{Email: email, Password: "correct horse battery staple", DisplayName: email}, "", "")
	if verify {
		p.check(result.DevVerificationToken != "", "signup exposes local verification token")
		_, _ = call[auth.AuthSessionResponse](p, http.MethodPost, "/auth/email-verification/confirm", auth.EmailVerificationConfirmParams{Token: result.DevVerificationToken}, "", "")
	}
	return take(p.q.GetUserByNormalizedEmail(p.ctx, strings.ToLower(email))).ID
}

var journeys = map[string]func(*probe){
	"schema": schemaJourney, "dev-new": devNewJourney, "dev-existing": devExistingJourney,
	"oauth-browser": oauthBrowserJourney, "connection-redirect": connectionRedirectJourney,
	"connection-store": connectionStoreJourney, "connection-error": connectionErrorJourney,
	"token-rotation": tokenRotationJourney, "token-retry": tokenRetryJourney,
	"token-missing-scope": tokenMissingScopeJourney, "token-disallowed-scope": tokenDisallowedScopeJourney,
	"impersonation": impersonationJourney, "impersonation-privilege": impersonationPrivilegeJourney,
	"refresh-replay": refreshReplayJourney, "user-lifecycle": userLifecycleJourney,
}
