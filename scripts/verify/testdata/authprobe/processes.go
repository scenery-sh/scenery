package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"time"

	"scenery.sh/auth"
	"scenery.sh/errs"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/runtime"
)

// Worker output is private parent/child IPC, never an artifact or browser
// response. Reports record only comparison assertions, never the bearer.
type workerResult struct {
	Token string       `json:"token,omitempty"`
	Code  errs.ErrCode `json:"code,omitempty"`
}

func runConnectionWorker(user, endpoint string) {
	must(deliverAuthConfiguration(false))
	must(auth.RegisterStandard(auth.StandardConfig{Enabled: true, Connections: []auth.ConnectionProvider{{ID: "independent", Protocol: "oauth2", Issuer: endpoint, AuthorizeURL: endpoint + "/authorize", TokenURL: endpoint + "/token", AccountURL: endpoint + "/account", Resource: "https://files.example", AllowedScopes: []string{"files.read", "files.write"}}}}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	service, ready, stop := registerConnectionWorker()
	defer stop()
	must(envpolicy.Set("SCENERY_ROLE", "worker"))
	done := make(chan error, 1)
	go func() { done <- runtime.Main(runtime.AppConfig{Name: "auth-token-worker"}) }()
	ctx = connectionWorkerContext(ctx, service, ready, done)
	token, err := auth.ConnectionTokenForUser(ctx, user, "independent", "files.read")
	result := workerResult{Token: token}
	if err != nil {
		result.Code = errs.Code(err)
	}
	stop()
	process := take(os.FindProcess(os.Getpid()))
	must(process.Signal(os.Interrupt))
	must(<-done)
	must(json.NewEncoder(os.Stdout).Encode(result))
}

type tokenWorker struct {
	command             *exec.Cmd
	output, diagnostics bytes.Buffer
	waited              bool
}

func (p *probe) startWorker(user string) *tokenWorker {
	executable := take(os.Executable())
	w := &tokenWorker{command: exec.CommandContext(p.ctx, executable, "--connection-worker", user, "--provider-url", p.independent.server.URL)}
	w.command.Env = envpolicy.Environ()
	w.command.Stdout = &w.output
	w.command.Stderr = &w.diagnostics
	must(w.command.Start())
	return w
}
func (w *tokenWorker) finish() (workerResult, error) {
	if w.waited {
		return workerResult{}, errors.New("token worker already joined")
	}
	w.waited = true
	if err := w.command.Wait(); err != nil {
		return workerResult{}, errors.New("owned token worker failed")
	}
	var result workerResult
	if json.Unmarshal(w.output.Bytes(), &result) != nil {
		return result, errors.New("owned token worker returned invalid private IPC")
	}
	return result, nil
}
func (w *tokenWorker) stop() {
	if w.waited {
		return
	}
	_ = w.command.Process.Kill()
	_ = w.command.Wait()
	w.waited = true
}

func (p *probe) prepareIndependent() auth.AuthSessionResponse {
	session, _ := p.signin()
	start, _ := call[auth.ConnectionStartResponse](p, http.MethodPost, "/auth/connections/independent/start", auth.ConnectionStartParams{Scopes: []string{"files.read"}}, session.Token, "")
	callback := p.independent.authorize(start.AuthorizeURL)
	response, _ := p.request(http.MethodGet, callback, nil, "", "")
	p.check(response.StatusCode == 302, "independent connection prepared through public callback")
	_, err := p.db.ExecContext(p.ctx, `UPDATE scenery.scenery_auth_connections SET access_token_expires_at=now()-interval '1 minute' WHERE user_id=$1 AND provider_id='independent'`, id(session.User.ID))
	must(err)
	return session
}
func (p *probe) blockRefresh() (started chan struct{}, release chan struct{}) {
	started, release = make(chan struct{}, 1), make(chan struct{})
	p.independent.beforeRefresh = func(req *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-req.Context().Done():
		}
	}
	return started, release
}
func (p *probe) awaitRefresh(started <-chan struct{}) {
	select {
	case <-started:
	case <-p.ctx.Done():
		panic(failure{p.ctx.Err()})
	}
}

func connectionProcessRefreshJourney(p *probe) {
	session := p.prepareIndependent()
	started, release := p.blockRefresh()
	first := p.startWorker(session.User.ID)
	defer first.stop()
	p.awaitRefresh(started)
	second := p.startWorker(session.User.ID)
	defer second.stop()
	close(release)
	a := take(first.finish())
	b := take(second.finish())
	p.check(a.Code == "" && b.Code == "" && a.Token == "independent-rotated-access" && b.Token == a.Token, "independent processes receive the same rotated bearer")
	p.check(p.independent.refreshCalls.Load() == 1, "independent processes serialize one remote refresh through the SQL lease and row lock")
}

func connectionProcessDisconnectJourney(p *probe) {
	session := p.prepareIndependent()
	started, release := p.blockRefresh()
	worker := p.startWorker(session.User.ID)
	defer worker.stop()
	p.awaitRefresh(started)
	type answer struct {
		status int
		body   []byte
	}
	done := make(chan answer, 1)
	// Use an independent HTTP call while the other native process owns refresh.
	go func() {
		response, body := p.request(http.MethodPost, "/auth/connections/independent/disconnect", nil, session.Token, "")
		done <- answer{response.StatusCode, body}
	}()
	close(release)
	result := take(worker.finish())
	p.check(result.Code == "", "refresh owner completed before serialized disconnect")
	var disconnected answer
	select {
	case disconnected = <-done:
	case <-p.ctx.Done():
		panic(failure{p.ctx.Err()})
	}
	var status auth.ConnectionResponse
	must(json.Unmarshal(disconnected.body, &status))
	p.check(disconnected.status == 200 && status.Status == "disconnected", "disconnect waits for the independent refresh writer and wins the final state")
	after := p.startWorker(session.User.ID)
	defer after.stop()
	denied := take(after.finish())
	p.check(denied.Code == errs.ConnectionReauthRequired && denied.Token == "", "a late token writer cannot reactivate or issue a disconnected connection")
}

func connectionProcessCrashJourney(p *probe) {
	session := p.prepareIndependent()
	started, release := p.blockRefresh()
	worker := p.startWorker(session.User.ID)
	defer worker.stop()
	p.awaitRefresh(started)
	var status string
	must(p.db.QueryRowContext(p.ctx, `SELECT status FROM scenery.scenery_auth_connections WHERE user_id=$1 AND provider_id='independent'`, id(session.User.ID)).Scan(&status))
	p.check(status == "refresh_pending", "refresh intent is durable before the provider finishes")
	worker.stop()
	close(release)
	after := p.startWorker(session.User.ID)
	defer after.stop()
	result := take(after.finish())
	p.check(result.Code == errs.ConnectionReauthRequired && result.Token == "", "a crashed process leaves the potentially rotated credential unusable")
	p.check(p.independent.refreshCalls.Load() == 1, "unknown refresh outcome does not trigger another provider request")
}
