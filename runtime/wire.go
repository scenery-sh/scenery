package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"onlava.com/errs"
	"onlava.com/internal/wire"
	"onlava.com/runtime/shared"
)

type wireRecoveryRecord struct {
	StoredAt time.Time      `json:"stored_at"`
	Status   int            `json:"status"`
	Headers  map[string]any `json:"headers,omitempty"`
	Result   any            `json:"result,omitempty"`
	Error    any            `json:"error,omitempty"`
}

type wireRequest struct {
	SchemaHash  string
	Method      string
	PathParams  map[string]any
	Payload     any
	PayloadJSON []byte
	BinaryFrame bool
}

func newWireRecoveryStore() map[string]wireRecoveryRecord {
	return make(map[string]wireRecoveryRecord)
}

func (s *server) registerWire() {
	s.public.Handle([]string{http.MethodGet}, wire.CapabilitiesPath, func(w http.ResponseWriter, req *http.Request, _ routeParams) {
		s.serveWireCapabilities(w)
	})
	s.public.Handle([]string{http.MethodGet}, wire.RecoverPathPrefix+":call_id", func(w http.ResponseWriter, req *http.Request, params routeParams) {
		s.serveWireRecovery(w, req, params.ByName("call_id"))
	})
	for endpointID, ep := range s.wireEndpoints {
		endpointID := endpointID
		ep := ep
		s.public.Handle([]string{http.MethodPost}, wire.CallPathPrefix+endpointID, func(w http.ResponseWriter, req *http.Request, _ routeParams) {
			s.serveWireEndpointCall(w, req, ep)
		})
	}
	s.public.Handle([]string{http.MethodPost}, wire.CallPathPrefix+":endpoint_id", func(w http.ResponseWriter, req *http.Request, params routeParams) {
		s.serveWireCall(w, req, params.ByName("endpoint_id"))
	})
}

func (s *server) serveWireCapabilities(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(s.wireCaps)
}

func buildWireCapabilities(endpoints []*Endpoint) wire.Capabilities {
	items := make([]wire.Endpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		item := wire.Endpoint{
			ID:                  endpointWireID(ep),
			Service:             ep.Service,
			Endpoint:            ep.Name,
			Path:                ep.Path,
			Methods:             append([]string(nil), ep.Methods...),
			Available:           ep.WireAvailable,
			UnsupportedReason:   ep.WireUnsupportedReason,
			SchemaHash:          ep.WireSchemaHash,
			SafeJSONRetry:       wireMethodsSafe(ep.Methods),
			WirePath:            wire.CallPathPrefix + ep.WireID,
			RecoveryPathPattern: wire.RecoverPathPrefix + "{call_id}",
		}
		items = append(items, item)
	}
	return wire.NewCapabilities(wire.HashEndpoints(items), items)
}

func (s *server) serveWireRecovery(w http.ResponseWriter, req *http.Request, callID string) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		errs.HTTPError(w, errs.B().Code(errs.InvalidArgument).Msg("missing call id").Err())
		return
	}
	record, ok := s.lookupWireRecovery(callID)
	if !ok {
		errs.HTTPError(w, errs.B().Code(errs.NotFound).Msg("wire call result not found").Err())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(record.Status)
	_ = json.NewEncoder(w).Encode(record)
}

func (s *server) serveWireCall(w http.ResponseWriter, req *http.Request, endpointID string) {
	ep, ok := s.lookupWireEndpoint(endpointID)
	if !ok || ep.Access == Private {
		s.writeWireFallback(w, errs.B().Code(errs.NotFound).Msg("wire endpoint not found").Err())
		return
	}
	if !ep.WireAvailable {
		msg := strings.TrimSpace(ep.WireUnsupportedReason)
		if msg == "" {
			msg = "wire transport unavailable for endpoint"
		}
		s.writeWireFallback(w, errs.B().Code(errs.Unimplemented).Msg(msg).Err())
		return
	}
	s.serveWireEndpointCall(w, req, ep)
}

func (s *server) serveWireEndpointCall(w http.ResponseWriter, req *http.Request, ep *Endpoint) {
	fastJSON := requestUsesWireJSON(req)
	var wireReq wireRequest
	var err error
	if fastJSON {
		wireReq, err = decodeWireJSONRequest(req)
	} else {
		wireReq, err = decodeWireRequest(req.Body)
	}
	if err != nil {
		s.writeWireFallback(w, errs.B().Code(errs.InvalidArgument).Msgf("invalid wire request: %v", err).Err())
		return
	}
	if wireReq.SchemaHash == "" {
		wireReq.SchemaHash = strings.TrimSpace(req.Header.Get(wire.SchemaHashHeader))
	}
	if wireReq.SchemaHash != "" && ep.WireSchemaHash != "" && wireReq.SchemaHash != ep.WireSchemaHash {
		s.writeWireFallback(w, errs.B().Code(errs.FailedPrecondition).Msg("wire schema mismatch").Err())
		return
	}

	pathValues, pathParams, err := decodeWirePathParams(ep, wireReq.PathParams)
	if err != nil {
		s.writeWireFallback(w, err)
		return
	}
	fastWire := canUseFastWireInvoke(ep, wireReq)
	var payload any
	if !fastWire {
		payload, err = decodeWirePayload(wireReq, ep.PayloadType)
		if err != nil {
			s.writeWireFallback(w, err)
			return
		}
	}
	method := strings.ToUpper(strings.TrimSpace(wireReq.Method))
	if method == "" {
		method = preferredRuntimeMethod(ep.Methods)
	}
	wireRequestForState := cloneWireRequestForState(req)
	wireRequestForState.Method = method
	wireRequestForState.URL.Path = renderWireRequestPath(ep.Path, pathParams)

	state := newExternalState(ep, wireRequestForState, pathParams, payload, AuthInfo{})
	ctx := withState(req.Context(), state)
	restore := enterState(state)
	defer restore()
	startRequestTrace(state)

	authInfo, err := authenticateRequest(wireRequestForState.WithContext(ctx), ep)
	if err != nil {
		logRequestStart(state)
		finishRequestTrace(state, errs.HTTPStatus(err), nil, err)
		s.writeWireAppError(w, req, err, errs.HTTPStatus(err), fastJSON, wireReq.BinaryFrame)
		return
	}
	state.auth = authInfo
	logRequestStart(state)

	if canUseFastWireJSON(ep, wireReq, req) {
		data, callErr := ep.WireInvokeJSON(ctx, pathValues, wireReq.PayloadJSON)
		defer finishRequestTrace(state, errs.HTTPStatus(callErr), nil, callErr)
		if callErr != nil {
			s.writeWireAppError(w, req, callErr, errs.HTTPStatus(callErr), fastJSON, wireReq.BinaryFrame)
			return
		}
		s.writeWireJSONBytes(w, req, http.StatusOK, data)
		return
	}

	resp, status, headers, callErr := s.executeWireEndpoint(ep, ctx, pathValues, payload, wireReq, fastWire)
	applyHeaders(w.Header(), headers)
	defer finishRequestTrace(state, status, resp, callErr)
	if callErr != nil {
		s.writeWireAppError(w, req, callErr, status, fastJSON, wireReq.BinaryFrame)
		return
	}

	body, bodyStatus, err := wireResponseBody(resp, w.Header())
	if err != nil {
		errs.HTTPError(w, errs.Wrap(err, "encode wire response"))
		return
	}
	if status == 0 {
		status = bodyStatus
	}
	if status == 0 {
		status = http.StatusOK
	}
	s.writeWireSuccess(w, req, status, body, len(wireReq.PayloadJSON) != 0, fastJSON, wireReq.BinaryFrame)
}

func (s *server) executeWireEndpoint(ep *Endpoint, ctx context.Context, pathValues []any, payload any, wireReq wireRequest, fastWire bool) (any, int, http.Header, error) {
	if fastWire {
		resp, err := ep.WireInvoke(ctx, pathValues, wireReq.PayloadJSON)
		if err != nil {
			return nil, errs.HTTPStatus(err), nil, err
		}
		return resp, 0, nil, nil
	}
	return executeTypedEndpoint(ep, ctx, pathValues, payload)
}

func canUseFastWireInvoke(ep *Endpoint, wireReq wireRequest) bool {
	if ep == nil || ep.WireInvoke == nil {
		return false
	}
	if !wireReq.BinaryFrame || ep.Access != Public || len(ep.MiddlewareIDs) != 0 {
		return false
	}
	_, mocked := lookupEndpointMock(ep)
	return !mocked
}

func canUseFastWireJSON(ep *Endpoint, wireReq wireRequest, req *http.Request) bool {
	if !canUseFastWireInvoke(ep, wireReq) || ep.WireInvokeJSON == nil {
		return false
	}
	if req.Header.Get(wire.CallIDHeader) != "" {
		return false
	}
	return !hasResponseShapeTags(ep.ResponseType)
}

func (s *server) writeWireJSONBytes(w http.ResponseWriter, req *http.Request, status int, data []byte) {
	w.Header().Set("Content-Type", wire.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(wire.SchemaHashHeader, s.wireCaps.SchemaHash)
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func cloneWireRequestForState(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}
	cloned := new(http.Request)
	*cloned = *req
	if req.URL != nil {
		urlCopy := *req.URL
		cloned.URL = &urlCopy
	}
	return cloned
}

func decodeWireRequest(body io.Reader) (wireRequest, error) {
	if body == nil {
		return wireRequest{}, nil
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return wireRequest{}, err
	}
	if frame, ok, err := wire.DecodeRequestFrame(data); ok {
		if err != nil {
			return wireRequest{}, err
		}
		req := wireRequest{
			SchemaHash:  strings.TrimSpace(frame.SchemaHash),
			PayloadJSON: frame.PayloadJSON,
			BinaryFrame: true,
		}
		if len(bytes.TrimSpace(frame.PathParamsJSON)) != 0 {
			req.PathParams = map[string]any{}
			if err := json.Unmarshal(frame.PathParamsJSON, &req.PathParams); err != nil {
				return wireRequest{}, fmt.Errorf("invalid path params: %w", err)
			}
		}
		return req, nil
	}
	data = bytes.TrimSpace(data)
	value, err := wire.Decode(data)
	if err != nil {
		return wireRequest{}, err
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return wireRequest{}, fmt.Errorf("request envelope must be an object")
	}
	req := wireRequest{
		SchemaHash: stringValue(obj["schema_hash"]),
		Method:     stringValue(obj["method"]),
		PathParams: objectValue(obj["path_params"]),
		Payload:    obj["payload"],
	}
	if payloadJSON := stringValue(obj["payload_json"]); payloadJSON != "" {
		req.PayloadJSON = []byte(payloadJSON)
	}
	return req, nil
}

func decodeWireJSONRequest(req *http.Request) (wireRequest, error) {
	if req == nil {
		return wireRequest{}, nil
	}
	defer req.Body.Close()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return wireRequest{}, err
	}
	var pathParams map[string]any
	if raw := strings.TrimSpace(req.Header.Get(wire.PathParamsHeader)); raw != "" {
		pathParams = map[string]any{}
		if err := json.Unmarshal([]byte(raw), &pathParams); err != nil {
			return wireRequest{}, fmt.Errorf("invalid path params: %w", err)
		}
	}
	return wireRequest{
		SchemaHash:  strings.TrimSpace(req.Header.Get(wire.SchemaHashHeader)),
		Method:      strings.TrimSpace(req.Header.Get(wire.MethodHeader)),
		PathParams:  pathParams,
		PayloadJSON: body,
	}, nil
}

func decodeWirePathParams(ep *Endpoint, raw map[string]any) ([]any, shared.PathParams, error) {
	if len(ep.PathParams) == 0 {
		return nil, nil, nil
	}
	values := make([]any, 0, len(ep.PathParams))
	decoded := make(shared.PathParams, 0, len(ep.PathParams))
	for _, spec := range ep.PathParams {
		rawValue, ok := raw[spec.Name]
		if !ok {
			return nil, nil, errs.B().Code(errs.InvalidArgument).Msgf("missing path param %q", spec.Name).Err()
		}
		asString := fmt.Sprint(rawValue)
		value, err := decodeScalar(spec.Kind, asString)
		if err != nil {
			return nil, nil, errs.B().Code(errs.InvalidArgument).Msgf("invalid path param %q: %v", spec.Name, err).Err()
		}
		values = append(values, value)
		decoded = append(decoded, shared.PathParam{Name: spec.Name, Value: asString})
	}
	return values, decoded, nil
}

func decodeWirePayload(req wireRequest, typ reflect.Type) (any, error) {
	if typ == nil {
		return nil, nil
	}
	target := newValueForType(typ)
	if len(req.PayloadJSON) != 0 {
		if err := json.Unmarshal(req.PayloadJSON, target.Interface()); err != nil {
			return nil, errs.B().Code(errs.InvalidArgument).Msgf("invalid wire payload: %v", err).Err()
		}
		if err := maybeValidate(target.Interface()); err != nil {
			return nil, err
		}
		return finalizeValue(target, typ), nil
	}
	payload := req.Payload
	if payload == nil {
		return finalizeValue(target, typ), nil
	}
	if ok, err := assignWireValue(target.Elem(), payload); ok {
		if err != nil {
			return nil, errs.B().Code(errs.InvalidArgument).Msgf("invalid wire payload: %v", err).Err()
		}
		if err := maybeValidate(target.Interface()); err != nil {
			return nil, err
		}
		return finalizeValue(target, typ), nil
	}
	target = newValueForType(typ)
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errs.Wrap(err, "decode wire payload")
	}
	if err := json.Unmarshal(data, target.Interface()); err != nil {
		return nil, errs.B().Code(errs.InvalidArgument).Msgf("invalid wire payload: %v", err).Err()
	}
	if err := maybeValidate(target.Interface()); err != nil {
		return nil, err
	}
	return finalizeValue(target, typ), nil
}

func assignWireValue(dst reflect.Value, src any) (bool, error) {
	if !dst.CanSet() {
		return false, nil
	}
	if src == nil {
		dst.SetZero()
		return true, nil
	}
	for dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		dst = dst.Elem()
	}
	switch dst.Kind() {
	case reflect.Interface:
		dst.Set(reflect.ValueOf(src))
		return true, nil
	case reflect.Bool:
		value, ok := src.(bool)
		if !ok {
			return true, fmt.Errorf("want bool, got %T", src)
		}
		dst.SetBool(value)
		return true, nil
	case reflect.String:
		value, ok := src.(string)
		if !ok {
			return true, fmt.Errorf("want string, got %T", src)
		}
		dst.SetString(value)
		return true, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, ok := wireNumber(src)
		if !ok {
			return true, fmt.Errorf("want number, got %T", src)
		}
		asInt := int64(value)
		if float64(asInt) != value || dst.OverflowInt(asInt) {
			return true, fmt.Errorf("number %v overflows %s", value, dst.Type())
		}
		dst.SetInt(asInt)
		return true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		value, ok := wireNumber(src)
		if !ok {
			return true, fmt.Errorf("want number, got %T", src)
		}
		if value < 0 {
			return true, fmt.Errorf("negative number for unsigned field")
		}
		asUint := uint64(value)
		if float64(asUint) != value || dst.OverflowUint(asUint) {
			return true, fmt.Errorf("number %v overflows %s", value, dst.Type())
		}
		dst.SetUint(asUint)
		return true, nil
	case reflect.Float32, reflect.Float64:
		value, ok := wireNumber(src)
		if !ok {
			return true, fmt.Errorf("want number, got %T", src)
		}
		dst.SetFloat(value)
		return true, nil
	case reflect.Slice:
		items, ok := src.([]any)
		if !ok {
			return true, fmt.Errorf("want array, got %T", src)
		}
		next := reflect.MakeSlice(dst.Type(), len(items), len(items))
		for i, item := range items {
			ok, err := assignWireValue(next.Index(i), item)
			if !ok || err != nil {
				return ok, err
			}
		}
		dst.Set(next)
		return true, nil
	case reflect.Array:
		items, ok := src.([]any)
		if !ok {
			return true, fmt.Errorf("want array, got %T", src)
		}
		if len(items) != dst.Len() {
			return true, fmt.Errorf("array length %d does not match %d", len(items), dst.Len())
		}
		for i, item := range items {
			ok, err := assignWireValue(dst.Index(i), item)
			if !ok || err != nil {
				return ok, err
			}
		}
		return true, nil
	case reflect.Map:
		if dst.Type().Key().Kind() != reflect.String {
			return false, nil
		}
		obj, ok := src.(map[string]any)
		if !ok {
			return true, fmt.Errorf("want object, got %T", src)
		}
		next := reflect.MakeMapWithSize(dst.Type(), len(obj))
		for key, item := range obj {
			value := reflect.New(dst.Type().Elem()).Elem()
			ok, err := assignWireValue(value, item)
			if !ok || err != nil {
				return ok, err
			}
			next.SetMapIndex(reflect.ValueOf(key).Convert(dst.Type().Key()), value)
		}
		dst.Set(next)
		return true, nil
	case reflect.Struct:
		obj, ok := src.(map[string]any)
		if !ok {
			return true, fmt.Errorf("want object, got %T", src)
		}
		return assignWireStruct(dst, obj)
	default:
		return false, nil
	}
}

func assignWireStruct(dst reflect.Value, obj map[string]any) (bool, error) {
	typ := dst.Type()
	for i := 0; i < dst.NumField(); i++ {
		field := typ.Field(i)
		if field.Anonymous {
			return false, nil
		}
		if !field.IsExported() {
			continue
		}
		name := jsonName(field)
		if name == "" {
			continue
		}
		raw, ok := obj[name]
		if !ok {
			continue
		}
		ok, err := assignWireValue(dst.Field(i), raw)
		if !ok || err != nil {
			return ok, err
		}
	}
	return true, nil
}

func wireNumber(src any) (float64, bool) {
	switch value := src.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func wireResponseBody(resp any, headers http.Header) (any, int, error) {
	if resp == nil {
		return nil, 0, nil
	}
	value := reflect.ValueOf(resp)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil, 0, nil
	}
	if isStructLike(value.Type()) {
		return splitResponse(resp, headers)
	}
	return resp, 0, nil
}

func (s *server) writeWireSuccess(w http.ResponseWriter, req *http.Request, status int, result any, resultJSON bool, responseJSON bool, binaryFrame bool) {
	if responseJSON {
		data, err := json.Marshal(result)
		if err != nil {
			errs.HTTPError(w, errs.Wrap(err, "encode wire response"))
			return
		}
		s.storeWireRecovery(req, status, result, nil)
		w.Header().Set("Content-Type", wire.JSONContentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set(wire.SchemaHashHeader, s.wireCaps.SchemaHash)
		if callID := req.Header.Get(wire.CallIDHeader); callID != "" {
			w.Header().Set(wire.CallIDHeader, callID)
		}
		w.WriteHeader(status)
		_, _ = w.Write(data)
		return
	}
	if binaryFrame {
		data, err := json.Marshal(result)
		if err != nil {
			errs.HTTPError(w, errs.Wrap(err, "encode wire response"))
			return
		}
		s.storeWireRecovery(req, status, result, nil)
		w.Header().Set("Content-Type", wire.ContentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set(wire.SchemaHashHeader, s.wireCaps.SchemaHash)
		if callID := req.Header.Get(wire.CallIDHeader); callID != "" {
			w.Header().Set(wire.CallIDHeader, callID)
		}
		w.WriteHeader(status)
		_, _ = w.Write(data)
		return
	}
	envelope := map[string]any{
		"ok":     true,
		"status": status,
	}
	if resultJSON {
		data, err := json.Marshal(result)
		if err != nil {
			errs.HTTPError(w, errs.Wrap(err, "encode wire response"))
			return
		}
		envelope["result_json"] = string(data)
	} else {
		envelope["result"] = result
	}
	s.storeWireRecovery(req, status, result, nil)
	data, err := wire.Encode(envelope)
	if err != nil {
		errs.HTTPError(w, errs.Wrap(err, "encode wire response"))
		return
	}
	w.Header().Set("Content-Type", wire.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(wire.SchemaHashHeader, s.wireCaps.SchemaHash)
	if callID := req.Header.Get(wire.CallIDHeader); callID != "" {
		w.Header().Set(wire.CallIDHeader, callID)
	}
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func (s *server) writeWireAppError(w http.ResponseWriter, req *http.Request, err error, status int, responseJSON bool, binaryFrame bool) {
	if status == 0 {
		status = errs.HTTPStatus(err)
	}
	payload := errorPayload(err)
	if responseJSON {
		data, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			errs.HTTPErrorWithCode(w, err, status)
			return
		}
		s.storeWireRecovery(req, status, nil, payload)
		w.Header().Set("Content-Type", wire.JSONContentType)
		w.Header().Set("Cache-Control", "no-store")
		if callID := req.Header.Get(wire.CallIDHeader); callID != "" {
			w.Header().Set(wire.CallIDHeader, callID)
		}
		w.WriteHeader(status)
		_, _ = w.Write(data)
		return
	}
	if binaryFrame {
		data, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			errs.HTTPErrorWithCode(w, err, status)
			return
		}
		s.storeWireRecovery(req, status, nil, payload)
		w.Header().Set("Content-Type", wire.ContentType)
		w.Header().Set("Cache-Control", "no-store")
		if callID := req.Header.Get(wire.CallIDHeader); callID != "" {
			w.Header().Set(wire.CallIDHeader, callID)
		}
		w.WriteHeader(status)
		_, _ = w.Write(data)
		return
	}
	envelope := map[string]any{
		"ok":     false,
		"status": status,
		"error":  payload,
	}
	s.storeWireRecovery(req, status, nil, payload)
	data, encodeErr := wire.Encode(envelope)
	if encodeErr != nil {
		errs.HTTPErrorWithCode(w, err, status)
		return
	}
	w.Header().Set("Content-Type", wire.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	if callID := req.Header.Get(wire.CallIDHeader); callID != "" {
		w.Header().Set(wire.CallIDHeader, callID)
	}
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func (s *server) writeWireFallback(w http.ResponseWriter, err error) {
	w.Header().Set(wire.FallbackHeader, "json")
	errs.HTTPError(w, err)
}

func requestUsesWireJSON(req *http.Request) bool {
	if req == nil {
		return false
	}
	contentType := req.Header.Get("Content-Type")
	return contentType == wire.JSONContentType || strings.HasPrefix(contentType, wire.JSONContentType+";")
}

func (s *server) storeWireRecovery(req *http.Request, status int, result any, errPayload any) {
	callID := req.Header.Get(wire.CallIDHeader)
	if callID == "" {
		return
	}
	record := wireRecoveryRecord{
		StoredAt: time.Now().UTC(),
		Status:   status,
		Result:   result,
		Error:    errPayload,
	}
	s.wireRecoveryMu.Lock()
	if s.wireRecovery == nil {
		s.wireRecovery = newWireRecoveryStore()
	}
	s.wireRecovery[callID] = record
	s.pruneWireRecoveryLocked(time.Now().Add(-10 * time.Minute))
	s.wireRecoveryMu.Unlock()
}

func (s *server) lookupWireRecovery(callID string) (wireRecoveryRecord, bool) {
	s.wireRecoveryMu.Lock()
	defer s.wireRecoveryMu.Unlock()
	s.pruneWireRecoveryLocked(time.Now().Add(-10 * time.Minute))
	record, ok := s.wireRecovery[callID]
	return record, ok
}

func (s *server) pruneWireRecoveryLocked(before time.Time) {
	for callID, record := range s.wireRecovery {
		if record.StoredAt.Before(before) {
			delete(s.wireRecovery, callID)
		}
	}
}

func (s *server) lookupWireEndpoint(id string) (*Endpoint, bool) {
	id = strings.TrimSpace(id)
	if id == "" || s == nil {
		return nil, false
	}
	ep, ok := s.wireEndpoints[id]
	return ep, ok
}

func endpointWireID(ep *Endpoint) string {
	if ep == nil {
		return ""
	}
	if strings.TrimSpace(ep.WireID) != "" {
		return ep.WireID
	}
	return wire.EndpointID(ep.Service, ep.Name)
}

func errorPayload(err error) map[string]any {
	payload := map[string]any{
		"code":    string(errs.Code(err)),
		"message": "",
	}
	if err != nil {
		payload["message"] = err.Error()
	}
	if details := errs.Details(err); details != nil {
		payload["details"] = details
	}
	if meta := errs.Meta(err); meta != nil {
		payload["meta"] = meta
	}
	return payload
}

func renderWireRequestPath(pattern string, params shared.PathParams) string {
	if len(params) == 0 {
		return pattern
	}
	path := pattern
	for _, param := range params {
		path = strings.ReplaceAll(path, ":"+param.Name, param.Value)
		path = strings.ReplaceAll(path, "*"+param.Name, param.Value)
	}
	return path
}

func preferredRuntimeMethod(methods []string) string {
	for _, method := range methods {
		if strings.EqualFold(method, http.MethodGet) {
			return http.MethodGet
		}
	}
	if len(methods) > 0 {
		return strings.ToUpper(methods[0])
	}
	return http.MethodPost
}

func wireMethodsSafe(methods []string) bool {
	if len(methods) == 0 {
		return false
	}
	for _, method := range methods {
		if method == "*" {
			return false
		}
		if !wire.IsSafeMethod(method) {
			return false
		}
	}
	return true
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func objectValue(value any) map[string]any {
	if value == nil {
		return nil
	}
	if obj, ok := value.(map[string]any); ok {
		return obj
	}
	return nil
}
