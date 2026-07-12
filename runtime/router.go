package runtime

import (
	"net/http"
	"slices"
	"strings"

	"scenery.sh/errs"
)

type routeHandle func(http.ResponseWriter, *http.Request, routeParams)

type routeTable struct {
	routes        []*route
	exact         map[string][]*route
	NotFound      http.Handler
	GlobalOPTIONS http.Handler
}

type route struct {
	methods []string
	pattern routePattern
	handler routeHandle
}

type routePattern struct {
	raw         string
	segments    []routeSegment
	literals    int
	hasParam    bool
	hasWildcard bool
	pathTail    bool
}

type routeSegment struct {
	kind  routeSegmentKind
	value string
}

type routeSegmentKind int

const (
	routeLiteral routeSegmentKind = iota
	routeParam
	routeWildcard
)

type routeParamValue struct {
	Key   string
	Value string
}

type routeParams []routeParamValue

func (p routeParams) ByName(name string) string {
	for _, param := range p {
		if param.Key == name {
			return param.Value
		}
	}
	return ""
}

func newRouteTable() *routeTable {
	return &routeTable{}
}

func (r *routeTable) Handle(methods []string, path string, handler routeHandle) {
	r.handle(methods, path, false, handler)
}

func (r *routeTable) HandlePathTail(methods []string, path string, handler routeHandle) {
	r.handle(methods, path, true, handler)
}

func (r *routeTable) handle(methods []string, path string, pathTail bool, handler routeHandle) {
	pattern := parseRoutePattern(path)
	pattern.pathTail = pathTail
	item := &route{
		methods: expandMethods(methods),
		pattern: pattern,
		handler: handler,
	}
	r.routes = append(r.routes, item)
	if !pattern.hasParam && !pattern.hasWildcard {
		if r.exact == nil {
			r.exact = make(map[string][]*route)
		}
		r.exact[pattern.raw] = append(r.exact[pattern.raw], item)
	}
	slices.SortStableFunc(r.routes, compareRoutes)
}

func (r *routeTable) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	requestPath := req.URL.EscapedPath()
	if req.Method != http.MethodOptions {
		if routes := r.exact[requestPath]; len(routes) > 0 {
			for _, route := range routes {
				if routeAllowsMethod(route.methods, req.Method) {
					w.Header().Set("Allow", strings.Join(expandedAllowedMethods(routes), ", "))
					route.handler(w, req, nil)
					return
				}
			}
		}
	}

	pathMatches := r.matchingRoutes(requestPath)
	if len(pathMatches) == 0 {
		r.serveNotFound(w, req)
		return
	}

	allow := allowedMethods(pathMatches, r.GlobalOPTIONS != nil)
	w.Header().Set("Allow", strings.Join(allow, ", "))

	if req.Method == http.MethodOptions && r.GlobalOPTIONS != nil {
		r.GlobalOPTIONS.ServeHTTP(w, req)
		return
	}

	for _, match := range pathMatches {
		if routeAllowsMethod(match.route.methods, req.Method) {
			match.route.handler(w, req, match.params)
			return
		}
	}

	errs.HTTPErrorWithCode(w, errs.B().Code(errs.InvalidArgument).Msg("method not allowed").Err(), http.StatusMethodNotAllowed)
}

func (r *routeTable) serveNotFound(w http.ResponseWriter, req *http.Request) {
	if r.NotFound != nil {
		r.NotFound.ServeHTTP(w, req)
		return
	}
	http.NotFound(w, req)
}

type routeMatch struct {
	route  *route
	params routeParams
}

func (r *routeTable) matchingRoutes(path string) []routeMatch {
	var matches []routeMatch
	for _, candidate := range r.routes {
		params, ok := candidate.pattern.match(path)
		if !ok {
			continue
		}
		matches = append(matches, routeMatch{route: candidate, params: params})
	}
	return matches
}

func allowedMethods(matches []routeMatch, includeOptions bool) []string {
	seen := make(map[string]bool)
	var methods []string
	for _, match := range matches {
		appendAllowedMethod(&methods, seen, match.route.methods...)
	}
	if includeOptions && !seen[http.MethodOptions] {
		methods = append(methods, http.MethodOptions)
	}
	slices.Sort(methods)
	return methods
}

func expandedAllowedMethods(routes []*route) []string {
	seen := make(map[string]bool)
	var methods []string
	for _, route := range routes {
		appendAllowedMethod(&methods, seen, route.methods...)
	}
	slices.Sort(methods)
	return methods
}

func appendAllowedMethod(methods *[]string, seen map[string]bool, values ...string) {
	for _, method := range values {
		if seen[method] {
			continue
		}
		seen[method] = true
		*methods = append(*methods, method)
		if method == http.MethodGet && !seen[http.MethodHead] {
			seen[http.MethodHead] = true
			*methods = append(*methods, http.MethodHead)
		}
	}
}

func routeAllowsMethod(methods []string, method string) bool {
	if slices.Contains(methods, method) {
		return true
	}
	return method == http.MethodHead && slices.Contains(methods, http.MethodGet)
}

func compareRoutes(a, b *route) int {
	for index := 0; index < len(a.pattern.segments) && index < len(b.pattern.segments); index++ {
		left, right := a.pattern.segments[index].kind, b.pattern.segments[index].kind
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
	}
	switch {
	case a.pattern.literals > b.pattern.literals:
		return -1
	case a.pattern.literals < b.pattern.literals:
		return 1
	}
	if a.pattern.hasWildcard != b.pattern.hasWildcard {
		if a.pattern.hasWildcard {
			return 1
		}
		return -1
	}
	switch {
	case len(a.pattern.segments) > len(b.pattern.segments):
		return -1
	case len(a.pattern.segments) < len(b.pattern.segments):
		return 1
	default:
		return 0
	}
}

func parseRoutePattern(path string) routePattern {
	pattern := routePattern{raw: path}
	for _, segment := range splitRoutePath(path) {
		switch {
		case strings.HasPrefix(segment, ":"):
			pattern.segments = append(pattern.segments, routeSegment{kind: routeParam, value: segment[1:]})
			pattern.hasParam = true
		case strings.HasPrefix(segment, "*"):
			pattern.segments = append(pattern.segments, routeSegment{kind: routeWildcard, value: segment[1:]})
			pattern.hasWildcard = true
		default:
			pattern.segments = append(pattern.segments, routeSegment{kind: routeLiteral, value: segment})
			pattern.literals++
		}
	}
	return pattern
}

func splitRoutePath(path string) []string {
	if path == "" || path == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}

func (p routePattern) match(path string) (routeParams, bool) {
	requestSegments := splitRoutePath(path)
	if p.pathTail {
		for _, segment := range requestSegments {
			if segment == "" {
				return nil, false
			}
		}
	}
	params := make(routeParams, 0, len(p.segments))

	i, j := 0, 0
	for i < len(p.segments) {
		segment := p.segments[i]
		switch segment.kind {
		case routeLiteral:
			if j >= len(requestSegments) || requestSegments[j] != segment.value {
				return nil, false
			}
			i++
			j++
		case routeParam:
			if j >= len(requestSegments) || requestSegments[j] == "" {
				return nil, false
			}
			params = append(params, routeParamValue{Key: segment.value, Value: requestSegments[j]})
			i++
			j++
		case routeWildcard:
			params = append(params, routeParamValue{Key: segment.value, Value: strings.Join(requestSegments[j:], "/")})
			return params, true
		}
	}
	return params, j == len(requestSegments)
}
