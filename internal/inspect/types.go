package inspect

import (
	appcfg "scenery.sh/internal/app"
)

var payloadSchemaRevisions = map[string]string{
	"scenery.inspect.app":       "sha256:b03f14d1a3f67697f8a3f410bb037a43b6bd02e9119cd395466278fe6a21ea55",
	"scenery.inspect.services":  "sha256:eed0de0f1862bd98948f299b756a92df5f13532b1f1d71df5dd908cf33aa0c28",
	"scenery.inspect.routes":    "sha256:000c440270d6bfe7bde83bbba963e0a4614aa830b284c4fca8424e82c1ca5bd0",
	"scenery.inspect.endpoints": "sha256:af1066b46918c1a19a1e24c22e7316c35a406a6d8cfdd04c49e1a1623a797d13",
}

type PayloadIdentity struct {
	Kind           string `json:"kind"`
	SchemaRevision string `json:"schema_revision"`
}

func NewPayloadIdentity(kind, _ string) PayloadIdentity {
	return PayloadIdentity{Kind: kind, SchemaRevision: payloadSchemaRevisions[kind]}
}

type AppRef struct {
	Name       string `json:"name"`
	ID         string `json:"id,omitempty"`
	Root       string `json:"root"`
	ConfigPath string `json:"config_path"`
	ModulePath string `json:"module_path,omitempty"`
}

type AppResponse struct {
	PayloadIdentity
	App         AppRef         `json:"app"`
	Config      appcfg.Config  `json:"config"`
	Counts      AppCounts      `json:"counts"`
	Services    []string       `json:"services"`
	AuthHandler *AuthBriefInfo `json:"auth_handler,omitempty"`
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
	PayloadIdentity
	App      AppRef           `json:"app"`
	Services []ServiceDetails `json:"services"`
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
	PayloadIdentity
	App    AppRef        `json:"app"`
	Routes []RouteRecord `json:"routes"`
}

type EndpointsResponse struct {
	PayloadIdentity
	App       AppRef           `json:"app"`
	Endpoints []EndpointRecord `json:"endpoints"`
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
	Generated  bool     `json:"generated,omitempty"`
	HasPayload bool     `json:"has_payload"`
}

type EndpointRecord struct {
	ID         string   `json:"id"`
	Service    string   `json:"service"`
	Endpoint   string   `json:"endpoint"`
	Access     string   `json:"access"`
	Raw        bool     `json:"raw"`
	Path       string   `json:"path"`
	Methods    []string `json:"methods"`
	Generated  bool     `json:"generated,omitempty"`
	HasPayload bool     `json:"has_payload"`
	File       string   `json:"-"`
	Receiver   string   `json:"-"`
	Tags       []string `json:"-"`
}
