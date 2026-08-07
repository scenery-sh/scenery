package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"scenery.sh/errs"
	"scenery.sh/internal/assistantapi"
	"scenery.sh/internal/assistantcontrol"
	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/assistanttoken"
)

// AssistantClient is the provider-neutral managed-helper boundary consumed by
// the public Scenery assistant gateway.
type AssistantClient = assistantruntime.Client

// AssistantRegistration is emitted by generated application composition. The
// registration is applied before the contract registry seals, so the normal
// runtime snapshot/rollback transaction also covers assistant routes.
type AssistantRegistration struct {
	Address            string
	Name               string
	Path               string
	Access             Access
	Policy             *ContractHTTPPolicy
	AssistantAddress   string
	RuntimeRevision    string
	CapabilityRevision string
	// Required defaults to true for every declared assistant. The private
	// bootstrap may still report an unavailable child without terminating the
	// Go app; this flag is for provider-neutral inspection and orchestration.
	Required        bool
	Client          AssistantClient
	TokenManager    assistanttoken.Manager
	InitiatorSigner assistanttoken.InitiatorSigner
	// RunIDReader and Random are testable entropy sources. Random is used for
	// request IDs when RunIDReader is not supplied.
	RunIDReader io.Reader
	Random      io.Reader

	gateway *assistantGateway
}

// AssistantRegistration names the five public routes below. Keeping route
// names stable makes diagnostics and request tracing deterministic.
const (
	assistantCreateRoute   = "conversation.create"
	assistantTurnRoute     = "conversation.turn"
	assistantEventsRoute   = "conversation.events"
	assistantApprovalRoute = "conversation.approval"
	assistantCancelRoute   = "conversation.cancel"
)

type assistantGateway struct {
	registration AssistantRegistration
	mu           sync.Mutex
	approvals    map[string]assistantApprovalState
	continuation map[string]string
	publicEvents map[string][]assistantapi.Event
	publicRuns   map[string][]string
	privateRuns  map[string]map[string]string
	cancelled    map[string]map[string]bool
}

type assistantApprovalState struct {
	PrivateID       string
	ConversationID  string
	RunID           string
	OwnerDigest     string
	DecisionContext string
}

// RegisterAssistant registers a generated assistant and its five raw public
// endpoints. It panics on invalid generated composition, matching the other
// convenience registration functions in this package.
func RegisterAssistant(registration AssistantRegistration) {
	if err := RegisterAssistantChecked(registration); err != nil {
		panic(err)
	}
}

// RegisterAssistantChecked validates and registers an assistant atomically at
// the runtime registry level. The endpoint registration is part of the same
// ContractRegistry snapshot taken during Seal.
func RegisterAssistantChecked(registration AssistantRegistration) error {
	registration, gateway, err := normalizeAssistantRegistration(registration)
	if err != nil {
		return err
	}
	global.mu.Lock()
	if global.assistants == nil {
		global.assistants = map[string]AssistantRegistration{}
	}
	if global.assistantClients == nil {
		global.assistantClients = map[string]AssistantClient{}
	}
	if _, exists := global.assistants[registration.Address]; exists {
		global.mu.Unlock()
		return fmt.Errorf("runtime: duplicate assistant registration %s", registration.Address)
	}
	if existing := global.assistantClients[registration.Address]; existing != nil && registration.Client == nil {
		registration.Client = existing
	}
	registration.gateway = gateway
	// Register the endpoints while holding the registry lock. RegisterEndpointChecked
	// also acquires this lock, so use the internal map directly and perform the
	// same route-conflict checks here.
	endpoints := assistantEndpoints(registration)
	for _, endpoint := range endpoints {
		key := endpointKey(endpoint.Service, endpoint.Name)
		if _, exists := global.endpoints[key]; exists {
			global.mu.Unlock()
			return fmt.Errorf("runtime: duplicate endpoint registration for %s", key)
		}
		if err := validateAssistantEndpointConflict(endpoint); err != nil {
			global.mu.Unlock()
			return err
		}
	}
	for _, endpoint := range endpoints {
		global.endpoints[endpointKey(endpoint.Service, endpoint.Name)] = endpoint
	}
	global.assistants[registration.Address] = registration
	global.assistantClients[registration.Address] = registration.Client
	global.mu.Unlock()
	ensureAssistantBootstrapService()
	return nil
}

// RegisterAssistantClient replaces the helper client used by an already
// registered assistant. Supervision uses this hook to swap a restarted child
// without rebuilding the public router.
func RegisterAssistantClient(address string, client AssistantClient) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return errors.New("runtime: assistant client address is required")
	}
	if client == nil {
		return errors.New("runtime: assistant client is required")
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.assistantClients == nil {
		global.assistantClients = map[string]AssistantClient{}
	}
	if _, exists := global.assistants[address]; !exists {
		// Keep a pending client so generated registration can arrive later in the
		// same composition transaction.
		global.assistantClients[address] = client
		return nil
	}
	global.assistantClients[address] = client
	registration := global.assistants[address]
	registration.Client = client
	global.assistants[address] = registration
	return nil
}

// swapAssistantClients replaces every currently registered assistant client
// under one registry lock. Bootstrap uses this bulk boundary so a concurrent
// request observes either the old helper generation or the new one, never a
// mixed multi-assistant map. The returned map contains the exact prior client
// pointers for caller-owned shutdown.
func swapAssistantClients(clients map[string]AssistantClient) (map[string]AssistantClient, error) {
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.assistantClients == nil {
		global.assistantClients = map[string]AssistantClient{}
	}
	for address := range clients {
		if _, exists := global.assistants[address]; !exists {
			return nil, fmt.Errorf("runtime: assistant client address %s is not registered", address)
		}
	}
	previous := make(map[string]AssistantClient, len(global.assistants))
	for address, registration := range global.assistants {
		previous[address] = global.assistantClients[address]
		client := clients[address]
		if client == nil {
			delete(global.assistantClients, address)
		} else {
			global.assistantClients[address] = client
		}
		registration.Client = client
		global.assistants[address] = registration
	}
	return previous, nil
}

// UnregisterAssistantClient removes the private helper client while keeping
// the public assistant endpoints registered. When expected is supplied, the
// removal is compare-and-swap semantics: a newer child installed by another
// supervisor lane is never removed by a stale cleanup callback.
func UnregisterAssistantClient(address string, expected ...AssistantClient) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return errors.New("runtime: assistant client address is required")
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	current := global.assistantClients[address]
	if len(expected) > 0 && expected[0] != nil && !sameAssistantClient(current, expected[0]) {
		return nil
	}
	delete(global.assistantClients, address)
	if registration, exists := global.assistants[address]; exists {
		registration.Client = nil
		global.assistants[address] = registration
	}
	return nil
}

func listAssistants() []AssistantRegistration {
	global.mu.RLock()
	defer global.mu.RUnlock()
	result := make([]AssistantRegistration, 0, len(global.assistants))
	for _, registration := range global.assistants {
		result = append(result, registration)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result
}

func normalizeAssistantRegistration(registration AssistantRegistration) (AssistantRegistration, *assistantGateway, error) {
	registration.Address = strings.TrimSpace(registration.Address)
	registration.Name = strings.TrimSpace(registration.Name)
	registration.Path = strings.TrimSpace(registration.Path)
	registration.AssistantAddress = strings.TrimSpace(registration.AssistantAddress)
	if registration.Address == "" {
		registration.Address = registration.AssistantAddress
	}
	if registration.Address == "" {
		return AssistantRegistration{}, nil, errors.New("runtime: assistant registration requires address")
	}
	if registration.Name == "" {
		registration.Name = assistantNameFromAddress(registration.Address)
	}
	if registration.Name == "" {
		return AssistantRegistration{}, nil, fmt.Errorf("runtime: assistant %s has no name", registration.Address)
	}
	if registration.Path == "" {
		registration.Path = "/assistants/" + registration.Name
	}
	if !strings.HasPrefix(registration.Path, "/") || strings.HasSuffix(registration.Path, "/") || strings.ContainsAny(registration.Path, "?#") {
		return AssistantRegistration{}, nil, fmt.Errorf("runtime: assistant %s has invalid public path %q", registration.Address, registration.Path)
	}
	if registration.AssistantAddress == "" {
		registration.AssistantAddress = registration.Name
	}
	if registration.RuntimeRevision == "" {
		registration.RuntimeRevision = "runtime-1"
	}
	if registration.CapabilityRevision == "" {
		registration.CapabilityRevision = "capability-1"
	}
	if !registration.Required {
		registration.Required = true
	}
	if registration.Access != Public && registration.Access != Auth && registration.Access != Private {
		registration.Access = Public
	}
	if registration.Access == Private {
		return AssistantRegistration{}, nil, fmt.Errorf("runtime: assistant %s cannot use private public surface", registration.Address)
	}
	if err := validateContractHTTPPolicy(registration.Policy); err != nil {
		return AssistantRegistration{}, nil, fmt.Errorf("runtime: assistant %s policy: %w", registration.Address, err)
	}
	if registration.TokenManager.Keys == nil {
		registration.TokenManager = defaultAssistantTokenManager(registration.TokenManager)
	}
	if registration.InitiatorSigner.Keys == nil && len(registration.InitiatorSigner.Key) == 0 {
		registration.InitiatorSigner = defaultAssistantInitiatorSigner(registration.InitiatorSigner)
	}
	gateway := &assistantGateway{registration: registration, approvals: map[string]assistantApprovalState{}, continuation: map[string]string{}, publicEvents: map[string][]assistantapi.Event{}, publicRuns: map[string][]string{}, privateRuns: map[string]map[string]string{}, cancelled: map[string]map[string]bool{}}
	return registration, gateway, nil
}

func assistantNameFromAddress(address string) string {
	address = strings.TrimSuffix(strings.TrimSpace(address), "/")
	if index := strings.LastIndexByte(address, '/'); index >= 0 {
		address = address[index+1:]
	}
	return address
}

func assistantEndpoints(registration AssistantRegistration) []*Endpoint {
	service := "assistant/" + registration.Name
	base := strings.TrimSuffix(registration.Path, "/") + "/v1/conversations"
	gateway := registration.gateway
	return []*Endpoint{
		{Service: service, Name: assistantCreateRoute, Access: registration.Access, ContractPolicy: registration.Policy, Raw: true, Path: base, Methods: []string{http.MethodPost}, RawHandler: gateway.handleCreate},
		{Service: service, Name: assistantTurnRoute, Access: registration.Access, ContractPolicy: registration.Policy, Raw: true, Path: base + "/:conversation_id/turns", Methods: []string{http.MethodPost}, RawHandler: gateway.handleTurn},
		{Service: service, Name: assistantEventsRoute, Access: registration.Access, ContractPolicy: registration.Policy, Raw: true, Path: base + "/:conversation_id/events", Methods: []string{http.MethodGet}, RawHandler: gateway.handleEvents},
		{Service: service, Name: assistantApprovalRoute, Access: registration.Access, ContractPolicy: registration.Policy, Raw: true, Path: base + "/:conversation_id/approvals/:approval_id", Methods: []string{http.MethodPost}, RawHandler: gateway.handleApproval},
		{Service: service, Name: assistantCancelRoute, Access: registration.Access, ContractPolicy: registration.Policy, Raw: true, Path: base + "/:conversation_id/runs/:run_id/cancel", Methods: []string{http.MethodPost}, RawHandler: gateway.handleCancel},
	}
}

func validateAssistantEndpointConflict(endpoint *Endpoint) error {
	for key, existing := range global.endpoints {
		if contractRouteConflict(endpoint, existing) {
			return fmt.Errorf("runtime: assistant endpoint %s conflicts with route registered by %s", endpointKey(endpoint.Service, endpoint.Name), key)
		}
	}
	return nil
}

func defaultAssistantTokenManager(manager assistanttoken.Manager) assistanttoken.Manager {
	if manager.Keys == nil {
		if key := assistantTokenKeyFromRuntime(); len(key) > 0 {
			manager.Keys = assistanttoken.NewStaticKeyring("runtime", key, nil)
		}
	}
	return manager
}

func defaultAssistantInitiatorSigner(signer assistanttoken.InitiatorSigner) assistanttoken.InitiatorSigner {
	if len(signer.Key) == 0 && signer.Keys == nil {
		if key := assistantTokenKeyFromRuntime(); len(key) > 0 {
			signer.KeyID = "runtime"
			signer.Key = key
		}
	}
	return signer
}

type assistantIdentity struct {
	Principal string
	Owner     string
	Cookie    *http.Cookie
	Data      any
}

// resolveIdentity accepts an authenticated principal when one is available;
// otherwise it verifies (or, on conversation creation, issues) Scenery's
// signed anonymous initiator cookie. Invalid cookies are intentionally
// indistinguishable from unknown conversation handles on non-create routes.
func (g *assistantGateway) resolveIdentity(req *http.Request, issue bool) (assistantIdentity, error) {
	if req == nil {
		return assistantIdentity{}, errors.New("request is required")
	}
	principal, err := g.authenticate(req)
	if err != nil {
		return assistantIdentity{}, err
	}
	if principal != "" {
		var data any
		if auth := CurrentAuth(); auth != nil {
			data = auth.Data
		}
		return assistantIdentity{Principal: principal, Owner: assistanttoken.OwnerDigest(principal), Data: data}, nil
	}
	cookie, _ := req.Cookie(assistanttoken.CookieName)
	if cookie != nil {
		identity, replacement, verifyErr := g.registration.InitiatorSigner.VerifyOrRotate(cookie)
		if verifyErr == nil {
			return assistantIdentity{Principal: "anonymous_" + identity.ID, Owner: assistanttoken.OwnerDigest(identity.ID), Cookie: replacement, Data: map[string]any{"anonymous_id": identity.ID}}, nil
		}
		if !issue {
			return assistantIdentity{}, assistanttoken.ErrNotFound
		}
	}
	if !issue {
		return assistantIdentity{}, assistanttoken.ErrNotFound
	}
	identity, value, issueErr := g.registration.InitiatorSigner.IssueIdentity()
	if issueErr != nil {
		return assistantIdentity{}, issueErr
	}
	return assistantIdentity{Principal: "anonymous_" + identity.ID, Owner: assistanttoken.OwnerDigest(identity.ID), Cookie: identity.Cookie(value, g.registration.InitiatorSigner), Data: map[string]any{"anonymous_id": identity.ID}}, nil
}

func (g *assistantGateway) authenticate(req *http.Request) (string, error) {
	if auth := CurrentAuth(); auth != nil && strings.TrimSpace(auth.UID) != "" {
		return strings.TrimSpace(auth.UID), nil
	}
	// Raw routes are admitted by the native Endpoint.Access gate before this
	// handler runs. If an authenticated route somehow reaches the handler
	// without an auth state, fail closed instead of treating the request as
	// anonymous.
	if g.registration.Access == Auth {
		return "", errs.B().Code(errs.Unauthenticated).Msg("assistant authentication missing").Err()
	}
	return "", nil
}

func (g *assistantGateway) currentClient() AssistantClient {
	global.mu.RLock()
	client := global.assistantClients[g.registration.Address]
	if client == nil {
		client = g.registration.Client
	}
	global.mu.RUnlock()
	return client
}

func (g *assistantGateway) randomReader(run bool) io.Reader {
	if run && g.registration.RunIDReader != nil {
		return g.registration.RunIDReader
	}
	if g.registration.Random != nil {
		return g.registration.Random
	}
	return rand.Reader
}

func (g *assistantGateway) runID() (string, error) {
	return assistantapi.NewRunID(g.randomReader(true))
}

func (g *assistantGateway) requestID(prefix string) string {
	bytesValue := make([]byte, 12)
	if _, err := io.ReadFull(g.randomReader(false), bytesValue); err != nil {
		// Request IDs are diagnostic only. A fixed fallback keeps control
		// requests valid even when a test entropy source is exhausted.
		return prefix + "_000000000000000000000000"
	}
	return prefix + "_" + hex.EncodeToString(bytesValue)
}

func (g *assistantGateway) requestMetadata(req *http.Request, identity assistantIdentity, conversationDigest string) assistantruntime.RequestMetadata {
	return assistantruntime.RequestMetadata{
		RequestID:          g.requestID("request"),
		AssistantAddress:   g.registration.AssistantAddress,
		RuntimeRevision:    g.registration.RuntimeRevision,
		CapabilityRevision: g.registration.CapabilityRevision,
		Principal:          identity.Principal,
		ConversationDigest: conversationDigest,
	}
}

func (g *assistantGateway) withAuth(req *http.Request, identity assistantIdentity) context.Context {
	ctx := context.Background()
	if req != nil {
		ctx = req.Context()
	}
	state := stateFromContext(ctx)
	data := identity.Data
	if data == nil {
		data = map[string]any{}
	}
	if values, ok := data.(map[string]any); ok {
		copied := make(map[string]any, len(values)+1)
		for key, value := range values {
			copied[key] = value
		}
		copied["owner_digest"] = identity.Owner
		data = copied
	}
	if state != nil {
		state.auth = AuthInfo{UID: identity.Principal, Data: data}
		if g.registration.Policy != nil && g.registration.Policy.BindingAddress != "" {
			state.request.CallerBinding = g.registration.Policy.BindingAddress
		}
		return withRuntimeInvocation(ctx, state)
	}
	return WithAuthContext(ctx, AuthInfo{UID: identity.Principal, Data: data})
}

func (g *assistantGateway) invoke(ctx context.Context, req *http.Request, identity assistantIdentity, fn func(context.Context) (any, error)) (any, error) {
	return InvokeContractPolicy(g.withAuth(req, identity), g.registration.Policy, nil, fn)
}

const assistantRequestLimit int64 = 1 << 20

func readAssistantBody(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, errors.New("request body is required")
	}
	limited := io.LimitReader(req.Body, assistantRequestLimit+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > assistantRequestLimit {
		return nil, errors.New("request body exceeds assistant limit")
	}
	return data, nil
}

func writeAssistantJSON(w http.ResponseWriter, value any) {
	if w == nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		writeAssistantError(w, http.StatusInternalServerError, assistantapi.NewError(assistantapi.ErrorInternal, "assistant response encoding failed"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func writeAssistantError(w http.ResponseWriter, status int, value assistantapi.Error) {
	if w == nil {
		return
	}
	if err := value.Validate(); err != nil {
		value = assistantapi.NewError(assistantapi.ErrorInternal, "assistant request failed")
		status = http.StatusInternalServerError
	}
	encoded, err := assistantapi.MarshalError(value)
	if err != nil {
		encoded = []byte(`{"code":"internal","message":"assistant request failed"}`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

func assistantErrorFor(err error) (assistantapi.Error, int) {
	if err == nil {
		return assistantapi.NewError(assistantapi.ErrorInternal, "assistant request failed"), http.StatusInternalServerError
	}
	if errors.Is(err, assistanttoken.ErrNotFound) || errors.Is(err, assistantruntime.ErrConversation) || errors.Is(err, assistantruntime.ErrRun) || errors.Is(err, assistantruntime.ErrApproval) {
		return assistantapi.NewError(assistantapi.ErrorNotFound, "assistant resource not found"), http.StatusNotFound
	}
	if errors.Is(err, assistanttoken.ErrKeyUnavailable) {
		return assistantapi.NewError(assistantapi.ErrorUnavailable, "assistant runtime unavailable"), http.StatusServiceUnavailable
	}
	if errors.Is(err, assistantruntime.ErrUnavailable) || errors.Is(err, assistantruntime.ErrStopped) || errors.Is(err, assistantruntime.ErrNotStarted) || errors.Is(err, assistantruntime.ErrRevisionMismatch) {
		return assistantapi.NewError(assistantapi.ErrorUnavailable, "assistant runtime unavailable"), http.StatusServiceUnavailable
	}
	if errors.Is(err, assistantruntime.ErrMalformedEvent) {
		return assistantapi.NewError(assistantapi.ErrorInternal, "assistant runtime returned an invalid event"), http.StatusInternalServerError
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, assistantruntime.ErrCancelled) {
		return assistantapi.NewError(assistantapi.ErrorCancelled, "assistant run cancelled"), http.StatusConflict
	}
	if errs.Code(err) == errs.PermissionDenied {
		return assistantapi.NewError(assistantapi.ErrorForbidden, "assistant request forbidden"), http.StatusForbidden
	}
	// Runtime authorization errors are represented by errs.PermissionDenied but
	// avoid importing the error package's implementation details here.
	if strings.Contains(strings.ToLower(err.Error()), "permission denied") || strings.Contains(strings.ToLower(err.Error()), "authorization denied") {
		return assistantapi.NewError(assistantapi.ErrorForbidden, "assistant request forbidden"), http.StatusForbidden
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "invalid") || strings.Contains(message, "required") || strings.Contains(message, "body") || strings.Contains(message, "must be user") || strings.Contains(message, "json") || strings.Contains(message, "cursor") || strings.Contains(message, "approval decision") {
		return assistantapi.NewError(assistantapi.ErrorInvalidRequest, "invalid assistant request"), http.StatusBadRequest
	}
	return assistantapi.NewError(assistantapi.ErrorInternal, "assistant request failed"), http.StatusInternalServerError
}

func (g *assistantGateway) conversationClaims(conversationID string, identity assistantIdentity) (assistanttoken.ConversationClaims, error) {
	if err := assistantapi.ValidateConversationID(conversationID); err != nil {
		return assistanttoken.ConversationClaims{}, assistanttoken.ErrNotFound
	}
	claims, err := g.registration.TokenManager.UnsealConversation(conversationID, assistanttoken.ConversationExpectation{AssistantAddress: g.registration.AssistantAddress, OwnerDigest: identity.Owner})
	if err != nil {
		return assistanttoken.ConversationClaims{}, assistanttoken.ErrNotFound
	}
	g.mu.Lock()
	if continuation := g.continuation[conversationID]; continuation != "" {
		claims.ContinuationToken = continuation
	}
	g.mu.Unlock()
	return claims, nil
}

func (g *assistantGateway) rememberContinuation(conversationID, continuation string) {
	if conversationID == "" || continuation == "" {
		return
	}
	g.mu.Lock()
	if g.continuation == nil {
		g.continuation = map[string]string{}
	}
	g.continuation[conversationID] = continuation
	g.trimConversationCachesLocked()
	g.mu.Unlock()
}

func (g *assistantGateway) rememberRun(conversationID, publicRunID string) {
	if conversationID == "" || publicRunID == "" {
		return
	}
	g.mu.Lock()
	if g.publicRuns == nil {
		g.publicRuns = make(map[string][]string)
	}
	g.publicRuns[conversationID] = append(g.publicRuns[conversationID], publicRunID)
	g.trimConversationCachesLocked()
	g.mu.Unlock()
}

func (g *assistantGateway) wasCancelled(conversationID, runID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cancelled[conversationID][runID]
}

func (g *assistantGateway) rememberCancelled(conversationID, runID string) {
	if conversationID == "" || runID == "" {
		return
	}
	g.mu.Lock()
	if g.cancelled == nil {
		g.cancelled = make(map[string]map[string]bool)
	}
	if g.cancelled[conversationID] == nil {
		g.cancelled[conversationID] = map[string]bool{}
	}
	g.cancelled[conversationID][runID] = true
	g.trimConversationCachesLocked()
	g.mu.Unlock()
}

func (g *assistantGateway) trimConversationCachesLocked() {
	keysSet := make(map[string]struct{}, len(g.continuation)+len(g.publicEvents)+len(g.publicRuns)+len(g.privateRuns)+len(g.cancelled))
	for key := range g.continuation {
		keysSet[key] = struct{}{}
	}
	for key := range g.publicEvents {
		keysSet[key] = struct{}{}
	}
	for key := range g.publicRuns {
		keysSet[key] = struct{}{}
	}
	for key := range g.privateRuns {
		keysSet[key] = struct{}{}
	}
	for key := range g.cancelled {
		keysSet[key] = struct{}{}
	}
	if len(keysSet) <= 256 {
		return
	}
	keys := make([]string, 0, len(keysSet))
	for key := range keysSet {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys[:len(keys)-256] {
		delete(g.continuation, key)
		delete(g.publicEvents, key)
		delete(g.publicRuns, key)
		delete(g.privateRuns, key)
		delete(g.cancelled, key)
	}
}

func (g *assistantGateway) publicRunID(conversationID, privateRunID, eventType, previous string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.privateRuns == nil {
		g.privateRuns = make(map[string]map[string]string)
	}
	if g.publicRuns == nil {
		g.publicRuns = make(map[string][]string)
	}
	if privateRunID != "" {
		if mapped := g.privateRuns[conversationID][privateRunID]; mapped != "" {
			return mapped, nil
		}
	}
	if eventType == assistantcontrol.EventRuntimeCrashed || eventType == assistantcontrol.EventRuntimeRestarting {
		if err := assistantapi.ValidateRunID(previous); err != nil {
			return "", assistantruntime.ErrMalformedEvent
		}
		return previous, nil
	}
	if eventType == assistantcontrol.EventRunStarted {
		used := make(map[string]bool, len(g.privateRuns[conversationID]))
		for _, mapped := range g.privateRuns[conversationID] {
			used[mapped] = true
		}
		for _, public := range g.publicRuns[conversationID] {
			if !used[public] {
				if g.privateRuns[conversationID] == nil {
					g.privateRuns[conversationID] = map[string]string{}
				}
				if privateRunID == "" {
					return "", assistantruntime.ErrMalformedEvent
				}
				g.privateRuns[conversationID][privateRunID] = public
				return public, nil
			}
		}
	}
	if err := assistantapi.ValidateRunID(privateRunID); err != nil {
		return "", assistantruntime.ErrMalformedEvent
	}
	return privateRunID, nil
}

func (g *assistantGateway) noClientError() error {
	if g.currentClient() == nil {
		return assistantruntime.ErrUnavailable
	}
	return nil
}

func (g *assistantGateway) writeError(w http.ResponseWriter, err error) {
	value, status := assistantErrorFor(err)
	writeAssistantError(w, status, value)
}

func writeAssistantIdentityCookie(w http.ResponseWriter, identity assistantIdentity) {
	if w != nil && identity.Cookie != nil {
		http.SetCookie(w, identity.Cookie)
	}
}

func (g *assistantGateway) handleCreate(w http.ResponseWriter, req *http.Request) {
	identity, err := g.resolveIdentity(req, true)
	if err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantIdentityCookie(w, identity)
	body, err := readAssistantBody(req)
	if err != nil {
		g.writeError(w, err)
		return
	}
	request, err := assistantapi.DecodeCreateConversationRequest(body)
	if err != nil {
		g.writeError(w, err)
		return
	}
	if err := g.noClientError(); err != nil {
		g.writeError(w, err)
		return
	}
	runID, err := g.runID()
	if err != nil {
		g.writeError(w, err)
		return
	}
	conversationDigest := assistanttoken.ConversationDigest(runID)
	startRequest := assistantruntime.StartRequest{
		RequestMetadata: g.requestMetadata(req, identity, conversationDigest),
		RunID:           runID,
		Message:         request.Message.Content,
	}
	client := g.currentClient()
	value, err := g.invoke(req.Context(), req, identity, func(ctx context.Context) (any, error) {
		return client.StartConversation(ctx, startRequest)
	})
	if err != nil {
		g.writeError(w, err)
		return
	}
	result, ok := value.(assistantruntime.StartResult)
	if !ok {
		g.writeError(w, assistantruntime.ErrMalformedEvent)
		return
	}
	if result.RunID != runID || result.PrivateSessionID == "" || result.ContinuationToken == "" {
		g.writeError(w, assistantruntime.ErrMalformedEvent)
		return
	}
	// The helper's private session and continuation are sealed directly into
	// the public conv1 handle. The token itself is already a canonical conv1_
	// value; re-encoding it would create a second, incompatible envelope.
	sealed, err := g.registration.TokenManager.SealConversation(assistanttoken.ConversationClaims{
		AssistantAddress: g.registration.AssistantAddress, OwnerDigest: identity.Owner,
		ConversationDigest: conversationDigest,
		PrivateSessionID:   result.PrivateSessionID, ContinuationToken: result.ContinuationToken,
	})
	if err != nil {
		g.writeError(w, err)
		return
	}
	g.rememberContinuation(sealed, result.ContinuationToken)
	g.rememberRun(sealed, runID)
	eventsURL := strings.TrimSuffix(g.registration.Path, "/") + "/v1/conversations/" + sealed + "/events"
	if err := assistantapi.ValidateEventsURLForSurface(eventsURL, g.registration.Path, sealed); err != nil {
		g.writeError(w, assistantruntime.ErrMalformedEvent)
		return
	}
	public := assistantapi.CreateConversationResponse{ConversationID: sealed, RunID: runID, EventsURL: eventsURL}
	if err := public.Validate(); err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantJSON(w, public)
}

func (g *assistantGateway) handleTurn(w http.ResponseWriter, req *http.Request) {
	identity, err := g.resolveIdentity(req, false)
	if err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantIdentityCookie(w, identity)
	conversationID := CurrentRequest().PathParams.Get("conversation_id")
	claims, err := g.conversationClaims(conversationID, identity)
	if err != nil {
		g.writeError(w, err)
		return
	}
	body, err := readAssistantBody(req)
	if err != nil {
		g.writeError(w, err)
		return
	}
	request, err := assistantapi.DecodeSendTurnRequest(body)
	if err != nil {
		g.writeError(w, err)
		return
	}
	if err := g.noClientError(); err != nil {
		g.writeError(w, err)
		return
	}
	runID, err := g.runID()
	if err != nil {
		g.writeError(w, err)
		return
	}
	turnRequest := assistantruntime.TurnRequest{
		RequestMetadata:   g.requestMetadata(req, identity, claims.ConversationDigest),
		PrivateSessionID:  claims.PrivateSessionID,
		ContinuationToken: claims.ContinuationToken,
		RunID:             runID,
		Message:           request.Message.Content,
	}
	client := g.currentClient()
	value, err := g.invoke(req.Context(), req, identity, func(ctx context.Context) (any, error) {
		return client.SendTurn(ctx, turnRequest)
	})
	if err != nil {
		g.writeError(w, err)
		return
	}
	result, ok := value.(assistantruntime.TurnResult)
	if !ok {
		g.writeError(w, assistantruntime.ErrMalformedEvent)
		return
	}
	if result.RunID != runID || (result.PrivateSessionID != "" && result.PrivateSessionID != claims.PrivateSessionID) || result.ContinuationToken == "" {
		g.writeError(w, assistantruntime.ErrMalformedEvent)
		return
	}
	g.rememberContinuation(conversationID, result.ContinuationToken)
	g.rememberRun(conversationID, runID)
	public := assistantapi.SendTurnResponse{RunID: runID}
	if err := public.Validate(); err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantJSON(w, public)
}

func (g *assistantGateway) handleApproval(w http.ResponseWriter, req *http.Request) {
	identity, err := g.resolveIdentity(req, false)
	if err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantIdentityCookie(w, identity)
	conversationID := CurrentRequest().PathParams.Get("conversation_id")
	approvalToken := CurrentRequest().PathParams.Get("approval_id")
	claims, err := g.conversationClaims(conversationID, identity)
	if err != nil {
		g.writeError(w, err)
		return
	}
	approvalClaims, err := g.registration.TokenManager.UnsealApproval(approvalToken, assistanttoken.ApprovalExpectation{
		AssistantAddress: g.registration.AssistantAddress,
		OwnerDigest:      identity.Owner,
		ConversationID:   conversationID,
	})
	if err != nil {
		g.writeError(w, assistanttoken.ErrNotFound)
		return
	}
	body, err := readAssistantBody(req)
	if err != nil {
		g.writeError(w, err)
		return
	}
	request, err := assistantapi.DecodeResolveApprovalRequest(body)
	if err != nil {
		g.writeError(w, err)
		return
	}
	if err := g.noClientError(); err != nil {
		g.writeError(w, err)
		return
	}
	decision := assistantcontrol.DecisionDeny
	if request.Decision == "approve" {
		decision = assistantcontrol.DecisionAllow
	}
	approvalRequest := assistantruntime.ApprovalRequest{
		RequestMetadata:   g.requestMetadata(req, identity, claims.ConversationDigest),
		PrivateSessionID:  claims.PrivateSessionID,
		ContinuationToken: claims.ContinuationToken,
		RunID:             approvalClaims.RunID,
		ApprovalID:        approvalClaims.ApprovalID,
		Decision:          decision,
	}
	client := g.currentClient()
	_, err = g.invoke(req.Context(), req, identity, func(ctx context.Context) (any, error) {
		return nil, client.ResolveApproval(ctx, approvalRequest)
	})
	if err != nil {
		g.writeError(w, err)
		return
	}
	public := assistantapi.ResolveApprovalResponse{ApprovalID: approvalToken, Decision: request.Decision}
	if err := public.Validate(); err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantJSON(w, public)
}

func (g *assistantGateway) handleCancel(w http.ResponseWriter, req *http.Request) {
	identity, err := g.resolveIdentity(req, false)
	if err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantIdentityCookie(w, identity)
	conversationID := CurrentRequest().PathParams.Get("conversation_id")
	runID := CurrentRequest().PathParams.Get("run_id")
	if err := assistantapi.ValidateRunID(runID); err != nil {
		g.writeError(w, err)
		return
	}
	claims, err := g.conversationClaims(conversationID, identity)
	if err != nil {
		g.writeError(w, err)
		return
	}
	if g.wasCancelled(conversationID, runID) {
		writeAssistantJSON(w, assistantapi.CancelRunResponse{RunID: runID, State: "cancelled"})
		return
	}
	if err := g.noClientError(); err != nil {
		g.writeError(w, err)
		return
	}
	cancelRequest := assistantruntime.CancelRequest{
		RequestMetadata:   g.requestMetadata(req, identity, claims.ConversationDigest),
		PrivateSessionID:  claims.PrivateSessionID,
		ContinuationToken: claims.ContinuationToken,
		RunID:             runID,
	}
	client := g.currentClient()
	_, err = g.invoke(req.Context(), req, identity, func(ctx context.Context) (any, error) {
		return nil, client.CancelRun(ctx, cancelRequest)
	})
	if err != nil {
		g.writeError(w, err)
		return
	}
	g.rememberCancelled(conversationID, runID)
	public := assistantapi.CancelRunResponse{RunID: runID, State: "cancelled"}
	if err := public.Validate(); err != nil {
		g.writeError(w, err)
		return
	}
	writeAssistantJSON(w, public)
}
