package inspect

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/model"
	"github.com/pbrazdil/onlava/internal/standardauthmeta"
	"github.com/pbrazdil/onlava/internal/wire"
	"github.com/pbrazdil/onlava/internal/wiremodel"
)

type AppRef struct {
	Name       string `json:"name"`
	ID         string `json:"id,omitempty"`
	Root       string `json:"root"`
	ConfigPath string `json:"config_path"`
	ModulePath string `json:"module_path,omitempty"`
}

type AppResponse struct {
	SchemaVersion string         `json:"schema_version"`
	App           AppRef         `json:"app"`
	Config        appcfg.Config  `json:"config"`
	Counts        AppCounts      `json:"counts"`
	Services      []string       `json:"services"`
	AuthHandler   *AuthBriefInfo `json:"auth_handler,omitempty"`
}

type AppCounts struct {
	Packages            int `json:"packages"`
	Services            int `json:"services"`
	Endpoints           int `json:"endpoints"`
	Middleware          int `json:"middleware"`
	AuthHandler         int `json:"auth_handler"`
	RuntimeDeclarations int `json:"runtime_declarations"`
}

type AuthBriefInfo struct {
	Service string `json:"service"`
	Name    string `json:"name"`
}

type ServicesResponse struct {
	SchemaVersion string           `json:"schema_version"`
	App           AppRef           `json:"app"`
	Services      []ServiceDetails `json:"services"`
}

type ServiceDetails struct {
	Name          string          `json:"name"`
	RootRelDir    string          `json:"root_rel_dir"`
	RootAbsDir    string          `json:"root_abs_dir"`
	PackageDirs   []string        `json:"package_dirs"`
	Endpoints     []string        `json:"endpoints"`
	Middleware    []string        `json:"middleware"`
	ServiceStruct *ServiceStruct  `json:"service_struct,omitempty"`
	AuthHandler   *AuthHandlerRef `json:"auth_handler,omitempty"`
}

type ServiceStruct struct {
	TypeName string `json:"type_name"`
	InitFunc string `json:"init_func,omitempty"`
	Shutdown string `json:"shutdown,omitempty"`
}

type AuthHandlerRef struct {
	Name     string `json:"name"`
	Package  string `json:"package"`
	Receiver string `json:"receiver,omitempty"`
}

type RoutesResponse struct {
	SchemaVersion string        `json:"schema_version"`
	App           AppRef        `json:"app"`
	Routes        []RouteRecord `json:"routes"`
}

type EndpointsResponse struct {
	SchemaVersion string           `json:"schema_version"`
	App           AppRef           `json:"app"`
	Endpoints     []EndpointRecord `json:"endpoints"`
	Wire          WireSummary      `json:"wire"`
}

type WireSummary struct {
	SchemaHash  string `json:"wire_schema_hash"`
	Available   int    `json:"available"`
	Unsupported int    `json:"unsupported"`
}

type RouteRecord struct {
	ID         string   `json:"id"`
	Service    string   `json:"service"`
	Endpoint   string   `json:"endpoint"`
	Package    string   `json:"package"`
	File       string   `json:"file"`
	Access     string   `json:"access"`
	Raw        bool     `json:"raw"`
	Path       string   `json:"path"`
	Methods    []string `json:"methods"`
	Tags       []string `json:"tags,omitempty"`
	Receiver   string   `json:"receiver,omitempty"`
	HasPayload bool     `json:"has_payload"`
	Wire       WireInfo `json:"wire"`
}

type EndpointRecord struct {
	ID         string   `json:"id"`
	Service    string   `json:"service"`
	Endpoint   string   `json:"endpoint"`
	Access     string   `json:"access"`
	Raw        bool     `json:"raw"`
	Path       string   `json:"path"`
	Methods    []string `json:"methods"`
	HasPayload bool     `json:"has_payload"`
	Wire       WireInfo `json:"wire"`
}

type WireInfo struct {
	Available         bool   `json:"available"`
	UnsupportedReason string `json:"unsupported_reason,omitempty"`
	SchemaHash        string `json:"schema_hash,omitempty"`
	Path              string `json:"path,omitempty"`
}

func BuildAppResponse(appRoot string, cfg appcfg.Config, app *model.App) AppResponse {
	resp := AppResponse{
		SchemaVersion: "onlava.inspect.app.v1",
		App:           appInfo(appRoot, cfg, app),
		Config:        cfg,
		Counts: AppCounts{
			Packages:            len(relevantAppPackageDirs(app)),
			Middleware:          len(app.Middleware),
			RuntimeDeclarations: len(app.Runtime),
		},
	}
	for _, svc := range filteredModelServices(app.Services) {
		resp.Counts.Services++
		resp.Services = append(resp.Services, svc.Name)
		resp.Counts.Endpoints += len(svc.Endpoints)
		if svc.AuthHandler != nil {
			resp.Counts.AuthHandler++
			resp.AuthHandler = &AuthBriefInfo{Service: svc.Name, Name: svc.AuthHandler.Name}
		}
	}
	if cfg.Auth.Enabled {
		resp.Services, resp.Counts.Services, resp.Counts.Endpoints = appendStandardAuthSummary(resp.Services, resp.Counts.Services, resp.Counts.Endpoints)
		resp.Counts.AuthHandler = 1
		resp.AuthHandler = &AuthBriefInfo{Service: "auth", Name: "AuthHandler"}
	}
	sort.Strings(resp.Services)
	return resp
}

func BuildServicesResponse(appRoot string, cfg appcfg.Config, app *model.App) ServicesResponse {
	services := make([]ServiceDetails, 0, len(app.Services))
	for _, svc := range filteredModelServices(app.Services) {
		item := ServiceDetails{
			Name:        svc.Name,
			RootRelDir:  filepath.ToSlash(svc.RootRelDir),
			RootAbsDir:  svc.RootAbsDir,
			PackageDirs: []string{},
			Endpoints:   []string{},
			Middleware:  []string{},
		}
		item.PackageDirs = relevantServicePackageDirs(svc)
		for _, ep := range svc.Endpoints {
			item.Endpoints = append(item.Endpoints, ep.Name)
		}
		for _, mw := range svc.Middleware {
			item.Middleware = append(item.Middleware, mw.Name)
		}
		sort.Strings(item.PackageDirs)
		sort.Strings(item.Endpoints)
		sort.Strings(item.Middleware)
		if svc.Struct != nil {
			item.ServiceStruct = &ServiceStruct{
				TypeName: svc.Struct.TypeName,
				InitFunc: svc.Struct.InitFunc,
				Shutdown: svc.Struct.Shutdown,
			}
		}
		if svc.AuthHandler != nil {
			item.AuthHandler = &AuthHandlerRef{
				Name:    svc.AuthHandler.Name,
				Package: filepath.ToSlash(svc.AuthHandler.Package.RelDir),
			}
			if svc.AuthHandler.Receiver != nil {
				item.AuthHandler.Receiver = svc.AuthHandler.Receiver.TypeName
			}
		}
		services = append(services, item)
	}
	if cfg.Auth.Enabled {
		services = append(services, standardAuthServiceDetails()...)
	}
	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})
	return ServicesResponse{
		SchemaVersion: "onlava.inspect.services.v1",
		App:           appInfo(appRoot, cfg, app),
		Services:      services,
	}
}

func BuildRoutesResponse(appRoot string, cfg appcfg.Config, app *model.App) RoutesResponse {
	routes := make([]RouteRecord, 0)
	for _, svc := range filteredModelServices(app.Services) {
		for _, ep := range svc.Endpoints {
			filePath := filepath.ToSlash(relOrSelf(appRoot, ep.File.Path))
			item := RouteRecord{
				ID:         svc.Name + "." + ep.Name,
				Service:    svc.Name,
				Endpoint:   ep.Name,
				Package:    filepath.ToSlash(ep.Package.RelDir),
				File:       filePath,
				Access:     string(ep.Access),
				Raw:        ep.Raw,
				Path:       ep.Path,
				Methods:    append([]string(nil), ep.Methods...),
				HasPayload: ep.Payload != nil,
			}
			wireInfo := wiremodel.Endpoint(ep)
			item.Wire = WireInfo{
				Available:         wireInfo.Available,
				UnsupportedReason: wireInfo.UnsupportedReason,
				SchemaHash:        wireInfo.SchemaHash,
				Path:              wireInfo.WirePath,
			}
			if len(ep.Tags) > 0 {
				item.Tags = slices.Clone(ep.Tags)
				sort.Strings(item.Tags)
			}
			if ep.Receiver != nil {
				item.Receiver = ep.Receiver.TypeName
			}
			routes = append(routes, item)
		}
	}
	if cfg.Auth.Enabled {
		for _, ep := range standardauthmeta.Endpoints() {
			routes = append(routes, RouteRecord{
				ID:         ep.Service + "." + ep.Name,
				Service:    ep.Service,
				Endpoint:   ep.Name,
				Package:    "github.com/pbrazdil/onlava/auth",
				File:       "github.com/pbrazdil/onlava/auth",
				Access:     string(ep.Access),
				Raw:        ep.Raw,
				Path:       ep.Path,
				Methods:    append([]string(nil), ep.Methods...),
				HasPayload: ep.HasPayload,
				Wire:       WireInfo{Available: false, UnsupportedReason: "standard auth endpoints use JSON transport"},
			})
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Service != routes[j].Service {
			return routes[i].Service < routes[j].Service
		}
		if routes[i].Endpoint != routes[j].Endpoint {
			return routes[i].Endpoint < routes[j].Endpoint
		}
		return routes[i].Path < routes[j].Path
	})
	return RoutesResponse{
		SchemaVersion: "onlava.inspect.routes.v1",
		App:           appInfo(appRoot, cfg, app),
		Routes:        routes,
	}
}

func BuildEndpointsResponse(appRoot string, cfg appcfg.Config, app *model.App) EndpointsResponse {
	capabilities := wiremodel.AppCapabilities(app)
	endpoints := make([]EndpointRecord, 0)
	var available int
	var unsupported int
	for _, svc := range filteredModelServices(app.Services) {
		for _, ep := range svc.Endpoints {
			wireInfo := wiremodel.Endpoint(ep)
			if wireInfo.Available {
				available++
			} else {
				unsupported++
			}
			endpoints = append(endpoints, EndpointRecord{
				ID:         svc.Name + "." + ep.Name,
				Service:    svc.Name,
				Endpoint:   ep.Name,
				Access:     string(ep.Access),
				Raw:        ep.Raw,
				Path:       ep.Path,
				Methods:    append([]string(nil), ep.Methods...),
				HasPayload: ep.Payload != nil,
				Wire: WireInfo{
					Available:         wireInfo.Available,
					UnsupportedReason: wireInfo.UnsupportedReason,
					SchemaHash:        wireInfo.SchemaHash,
					Path:              wireInfo.WirePath,
				},
			})
		}
	}
	if cfg.Auth.Enabled {
		for _, ep := range standardauthmeta.Endpoints() {
			unsupported++
			endpoints = append(endpoints, EndpointRecord{
				ID:         ep.Service + "." + ep.Name,
				Service:    ep.Service,
				Endpoint:   ep.Name,
				Access:     string(ep.Access),
				Raw:        ep.Raw,
				Path:       ep.Path,
				Methods:    append([]string(nil), ep.Methods...),
				HasPayload: ep.HasPayload,
				Wire:       WireInfo{Available: false, UnsupportedReason: "standard auth endpoints use JSON transport"},
			})
		}
	}
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Service != endpoints[j].Service {
			return endpoints[i].Service < endpoints[j].Service
		}
		return endpoints[i].Endpoint < endpoints[j].Endpoint
	})
	return EndpointsResponse{
		SchemaVersion: "onlava.inspect.endpoints.v1",
		App:           appInfo(appRoot, cfg, app),
		Endpoints:     endpoints,
		Wire: WireSummary{
			SchemaHash:  capabilities.SchemaHash,
			Available:   available,
			Unsupported: unsupported,
		},
	}
}

func appendStandardAuthSummary(services []string, serviceCount int, endpointCount int) ([]string, int, int) {
	seen := make(map[string]bool, len(services)+2)
	for _, service := range services {
		seen[service] = true
	}
	for _, service := range []string{"auth", "users"} {
		if !seen[service] {
			services = append(services, service)
			serviceCount++
		}
	}
	endpointCount += len(standardauthmeta.Endpoints())
	return services, serviceCount, endpointCount
}

func standardAuthServiceDetails() []ServiceDetails {
	byService := map[string]*ServiceDetails{
		"auth": {
			Name:       "auth",
			RootRelDir: "github.com/pbrazdil/onlava/auth",
			RootAbsDir: "github.com/pbrazdil/onlava/auth",
			PackageDirs: []string{
				"github.com/pbrazdil/onlava/auth",
			},
			Endpoints:   []string{},
			Middleware:  []string{},
			AuthHandler: &AuthHandlerRef{Name: "AuthHandler", Package: "github.com/pbrazdil/onlava/auth"},
		},
		"users": {
			Name:       "users",
			RootRelDir: "github.com/pbrazdil/onlava/auth",
			RootAbsDir: "github.com/pbrazdil/onlava/auth",
			PackageDirs: []string{
				"github.com/pbrazdil/onlava/auth",
			},
			Endpoints:  []string{},
			Middleware: []string{},
		},
	}
	for _, ep := range standardauthmeta.Endpoints() {
		item := byService[ep.Service]
		item.Endpoints = append(item.Endpoints, ep.Name)
	}
	out := make([]ServiceDetails, 0, len(byService))
	for _, item := range byService {
		sort.Strings(item.Endpoints)
		out = append(out, *item)
	}
	return out
}

func appInfo(appRoot string, cfg appcfg.Config, app *model.App) AppRef {
	ref := AppRef{
		Name:       cfg.Name,
		ID:         cfg.ID,
		Root:       appRoot,
		ConfigPath: filepath.Join(appRoot, ".onlava.json"),
	}
	if app != nil {
		ref.ModulePath = app.ModulePath
	}
	return ref
}

func relOrSelf(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return target
	}
	return rel
}

func GeneratedAppPath(appRoot string) string {
	return filepath.Join(appRoot, ".onlava", "gen", "app.json")
}

func GeneratedRoutesPath(appRoot string) string {
	return filepath.Join(appRoot, ".onlava", "gen", "routes.json")
}

func GeneratedServicesPath(appRoot string) string {
	return filepath.Join(appRoot, ".onlava", "gen", "services.json")
}

func GeneratedEndpointsPath(appRoot string) string {
	return filepath.Join(appRoot, ".onlava", "gen", "endpoints.json")
}

func GeneratedWireCapabilitiesPath(appRoot string) string {
	return filepath.Join(appRoot, ".onlava", "gen", "wire", "capabilities.json")
}

func ReadGeneratedApp(appRoot string) (*AppResponse, bool, error) {
	var payload AppResponse
	ok, err := readJSONFile(GeneratedAppPath(appRoot), &payload)
	if err != nil || !ok {
		return nil, ok, err
	}
	if services, servicesOK, servicesErr := ReadGeneratedServices(appRoot); servicesErr != nil {
		return nil, true, servicesErr
	} else if servicesOK {
		payload.Services, payload.Counts.Endpoints, payload.Counts.AuthHandler, payload.AuthHandler = serviceSummary(services.Services)
		payload.Counts.Services = len(payload.Services)
		payload.Counts.Packages = len(relevantGeneratedPackageDirs(services.Services))
	}
	return &payload, true, nil
}

func ReadGeneratedRoutes(appRoot string) (*RoutesResponse, bool, error) {
	var payload RoutesResponse
	ok, err := readJSONFile(GeneratedRoutesPath(appRoot), &payload)
	if err != nil || !ok {
		return nil, ok, err
	}
	return &payload, true, nil
}

func ReadGeneratedServices(appRoot string) (*ServicesResponse, bool, error) {
	var payload ServicesResponse
	ok, err := readJSONFile(GeneratedServicesPath(appRoot), &payload)
	if err != nil || !ok {
		return nil, ok, err
	}
	payload.Services = filterServiceDetails(payload.Services)
	return &payload, true, nil
}

func ReadGeneratedEndpoints(appRoot string) (*EndpointsResponse, bool, error) {
	var payload EndpointsResponse
	ok, err := readJSONFile(GeneratedEndpointsPath(appRoot), &payload)
	if err != nil || !ok {
		return nil, ok, err
	}
	return &payload, true, nil
}

func ReadGeneratedWireCapabilities(appRoot string) (*wire.Capabilities, bool, error) {
	var payload wire.Capabilities
	ok, err := readJSONFile(GeneratedWireCapabilitiesPath(appRoot), &payload)
	if err != nil || !ok {
		return nil, ok, err
	}
	return &payload, true, nil
}

func filteredModelServices(services []*model.Service) []*model.Service {
	filtered := make([]*model.Service, 0, len(services))
	for _, svc := range services {
		if svc == nil || len(svc.Endpoints) == 0 {
			continue
		}
		filtered = append(filtered, svc)
	}
	return filtered
}

func filterServiceDetails(services []ServiceDetails) []ServiceDetails {
	filtered := make([]ServiceDetails, 0, len(services))
	for _, svc := range services {
		if len(svc.Endpoints) == 0 {
			continue
		}
		filtered = append(filtered, svc)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Name < filtered[j].Name
	})
	return filtered
}

func serviceSummary(services []ServiceDetails) ([]string, int, int, *AuthBriefInfo) {
	names := make([]string, 0, len(services))
	var endpointCount int
	var authCount int
	var auth *AuthBriefInfo
	for _, svc := range services {
		names = append(names, svc.Name)
		endpointCount += len(svc.Endpoints)
		if svc.AuthHandler != nil {
			authCount++
			auth = &AuthBriefInfo{Service: svc.Name, Name: svc.AuthHandler.Name}
		}
	}
	sort.Strings(names)
	return names, endpointCount, authCount, auth
}

func relevantGeneratedPackageDirs(services []ServiceDetails) []string {
	seen := make(map[string]struct{})
	for _, svc := range services {
		for _, relDir := range svc.PackageDirs {
			seen[filepath.ToSlash(relDir)] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func relevantAppPackageDirs(app *model.App) []string {
	seen := make(map[string]struct{})
	for _, svc := range filteredModelServices(app.Services) {
		for _, relDir := range relevantServicePackageDirs(svc) {
			seen[relDir] = struct{}{}
		}
	}
	for _, mw := range app.Middleware {
		if mw == nil || mw.Package == nil {
			continue
		}
		seen[filepath.ToSlash(mw.Package.RelDir)] = struct{}{}
	}
	return sortedKeys(seen)
}

func relevantServicePackageDirs(svc *model.Service) []string {
	seen := make(map[string]struct{})
	if svc == nil {
		return nil
	}
	if svc.Struct != nil && svc.Struct.Package != nil {
		seen[filepath.ToSlash(svc.Struct.Package.RelDir)] = struct{}{}
	}
	if svc.AuthHandler != nil && svc.AuthHandler.Package != nil {
		seen[filepath.ToSlash(svc.AuthHandler.Package.RelDir)] = struct{}{}
	}
	for _, ep := range svc.Endpoints {
		if ep == nil || ep.Package == nil {
			continue
		}
		seen[filepath.ToSlash(ep.Package.RelDir)] = struct{}{}
	}
	for _, mw := range svc.Middleware {
		if mw == nil || mw.Package == nil {
			continue
		}
		seen[filepath.ToSlash(mw.Package.RelDir)] = struct{}{}
	}
	return sortedKeys(seen)
}

func sortedKeys(seen map[string]struct{}) []string {
	items := make([]string, 0, len(seen))
	for key := range seen {
		items = append(items, key)
	}
	sort.Strings(items)
	return items
}

func readJSONFile(path string, target any) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return false, err
	}
	return true, nil
}
