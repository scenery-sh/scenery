package compiler

import (
	"sort"
	"strconv"
	"strings"

	"scenery.sh/internal/machine"
)

type DeploymentProjection struct {
	machine.ArtifactIdentity
	Deployment       string                                  `json:"deployment"`
	Environment      string                                  `json:"environment"`
	ContractRevision string                                  `json:"contract_revision"`
	Resources        map[string]DeploymentResourceProjection `json:"resources"`
}

type DeploymentResourceProjection struct {
	Kind       string            `json:"kind"`
	Values     map[string]any    `json:"values"`
	Provenance map[string]string `json:"provenance"`
}

func ResolveDeployment(manifest *Manifest, name string) (DeploymentProjection, []Diagnostic) {
	projection := DeploymentProjection{ArtifactIdentity: machine.NewArtifactIdentity(deploymentProjectionKind, deploymentProjectionDescriptor), ContractRevision: "", Resources: map[string]DeploymentResourceProjection{}}
	if manifest == nil {
		return projection, []Diagnostic{{Code: "SCN2801", Severity: "error", Message: "deployment resolution requires a manifest"}}
	}
	projection.ContractRevision = manifest.ContractRevision
	byAddress := resourcesByAddress(manifest)
	deploymentAddress := name
	if !strings.Contains(name, "/") {
		deploymentAddress = resourceAddress("app", "deployment", name)
	}
	deployment, ok := byAddress[deploymentAddress]
	if !ok || deployment.Kind != "scenery.deployment" {
		return projection, []Diagnostic{{Code: "SCN2801", Severity: "error", Message: "deployment not found", Address: deploymentAddress}}
	}
	projection.Deployment, projection.Environment = deployment.Address, stringValue(deployment.Spec["environment"])
	writers := map[string]bool{}
	var diagnostics []Diagnostic
	for _, target := range manifest.Resources {
		var baseline DeploymentResourceProjection
		switch target.Kind {
		case "scenery.module":
			baseline, _ = resolveModuleDeployment(target, map[string]any{}, deployment, writers)
		case "scenery.provider", "scenery.data-source", "scenery.execution-engine", "scenery.event-bus", "scenery.secret-store":
			baseline, _ = resolveProviderDeployment(byAddress, target, map[string]any{}, deployment, writers)
		case "scenery.service":
			baseline = deploymentBaseline(target, []string{"replicas", "resources", "placement", "runtime_resources"})
		case "scenery.http-gateway":
			baseline = deploymentBaseline(target, []string{"listener"})
		case "scenery.secret":
			baseline = deploymentBaseline(target, []string{"store", "key", "version"})
		default:
			continue
		}
		projection.Resources[target.Address] = baseline
	}
	for _, fixture := range manifest.Resources {
		if fixture.Kind != "scenery.fixture" || !containsFixtureEnvironment(stringValues(fixture.Spec["environments"]), projection.Environment) {
			continue
		}
		projection.Resources[fixture.Address] = DeploymentResourceProjection{
			Kind:       fixture.Kind,
			Values:     map[string]any{"entity": fixture.Spec["entity"], "mode": fixture.Spec["mode"], "values": fixture.Spec["values"]},
			Provenance: map[string]string{"/entity": "application:" + fixture.Address, "/mode": "application:" + fixture.Address, "/values": "application:" + fixture.Address},
		}
	}
	for _, overlayKind := range []string{"module", "data_source", "service", "http_gateway", "provider", "secret"} {
		for _, overlay := range namedChildren(deployment.Spec, overlayKind) {
			targetAddress := resolveDeploymentTarget(byAddress, deployment, overlayKind, refString(overlay["target"]))
			target, targetOK := byAddress[targetAddress]
			if !targetOK || !deploymentTargetKindMatches(target.Kind, overlayKind) {
				diagnostics = append(diagnostics, deploymentDiagnostic("SCN2802", "deployment overlay target does not resolve to one typed "+overlayKind, deployment, "/spec/"+overlayKind))
				continue
			}
			var resolved DeploymentResourceProjection
			var overlayDiagnostics []Diagnostic
			switch overlayKind {
			case "module":
				resolved, overlayDiagnostics = resolveModuleDeployment(target, overlay, deployment, writers)
			case "data_source", "provider":
				resolved, overlayDiagnostics = resolveProviderDeployment(byAddress, target, overlay, deployment, writers)
			case "service":
				resolved, overlayDiagnostics = resolveServiceDeployment(target, overlay, deployment, writers)
			case "http_gateway":
				resolved, overlayDiagnostics = resolveGatewayDeployment(target, overlay, deployment, writers)
			case "secret":
				resolved, overlayDiagnostics = resolveSecretDeployment(target, overlay, deployment, writers)
			}
			diagnostics = append(diagnostics, overlayDiagnostics...)
			if existing, exists := projection.Resources[targetAddress]; exists {
				resolved = mergeDeploymentProjection(existing, resolved)
			}
			projection.Resources[targetAddress] = resolved
		}
	}
	return projection, diagnostics
}

func deploymentBaseline(target Resource, fields []string) DeploymentResourceProjection {
	projection := DeploymentResourceProjection{Kind: target.Kind, Values: map[string]any{}, Provenance: map[string]string{}}
	for _, field := range fields {
		if value := target.Spec[field]; value != nil {
			path := "/" + escapeJSONPointer(field)
			projection.Values[field], projection.Provenance[path] = value, "application:"+target.Address
		}
	}
	return projection
}

func resolveModuleDeployment(module Resource, overlay map[string]any, deployment Resource, writers map[string]bool) (DeploymentResourceProjection, []Diagnostic) {
	projection := DeploymentResourceProjection{Kind: module.Kind, Values: map[string]any{"inputs": map[string]any{}}, Provenance: map[string]string{}}
	values := projection.Values["inputs"].(map[string]any)
	declarations, _ := module.Spec["interface_inputs"].(map[string]any)
	baseline, _ := module.Spec["inputs"].(map[string]any)
	for name, raw := range declarations {
		declaration, _ := raw.(map[string]any)
		if declaration["default"] != nil {
			values[name] = declaration["default"]
			projection.Provenance["/inputs/"+escapeJSONPointer(name)] = "package-default:" + module.Address
		}
	}
	for name, value := range baseline {
		values[name] = value
		projection.Provenance["/inputs/"+escapeJSONPointer(name)] = "application:" + module.Address
	}
	requested, _ := overlay["inputs"].(map[string]any)
	var diagnostics []Diagnostic
	for name, value := range requested {
		declaration, ok := declarations[name].(map[string]any)
		path := "/inputs/" + escapeJSONPointer(name)
		if !ok || stringValue(declaration["phase"]) != "deployment" {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2802", "module overlay may set only declared deployment-phase inputs", deployment, path))
			continue
		}
		if !deploymentValueMatchesType(value, stringValue(declaration["type"])) {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2803", "module deployment input has the wrong type", deployment, path))
			continue
		}
		if duplicateDeploymentWriter(writers, module.Address, path) {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2802", "multiple deployment overlays write the same field", deployment, path))
			continue
		}
		values[name] = value
		projection.Provenance[path] = "deployment:" + deployment.Address
	}
	for key := range overlay {
		if key != "target" && key != "inputs" {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2804", "module overlay cannot change "+key, deployment, "/spec/module/"+key))
		}
	}
	return projection, diagnostics
}

func resolveProviderDeployment(resources map[string]Resource, target Resource, overlay map[string]any, deployment Resource, writers map[string]bool) (DeploymentResourceProjection, []Diagnostic) {
	projection := DeploymentResourceProjection{Kind: target.Kind, Values: map[string]any{"config": map[string]any{}}, Provenance: map[string]string{}}
	values := projection.Values["config"].(map[string]any)
	baseline, _ := target.Spec["config"].(map[string]any)
	for name, value := range baseline {
		values[name] = value
		projection.Provenance["/config/"+escapeJSONPointer(name)] = "application:" + target.Address
	}
	provider := target
	if target.Kind != "scenery.provider" {
		provider = resources[resolveResourceRef(target, refString(target.Spec["provider"]), "provider")]
	}
	schema, _ := provider.Spec["config_schema"].(map[string]any)
	requested, _ := overlay["config"].(map[string]any)
	var diagnostics []Diagnostic
	for name, value := range requested {
		field, ok := schema[name].(map[string]any)
		path := "/config/" + escapeJSONPointer(name)
		if !ok || field["deployment_bindable"] != true {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2803", "provider field is unknown or not deployment_bindable", deployment, path))
			continue
		}
		if typeName := stringValue(field["type"]); typeName != "" && !deploymentValueMatchesType(value, typeName) {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2803", "provider deployment field has the wrong type", deployment, path))
			continue
		}
		if duplicateDeploymentWriter(writers, target.Address, path) {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2802", "multiple deployment overlays write the same field", deployment, path))
			continue
		}
		values[name] = value
		projection.Provenance[path] = "deployment:" + deployment.Address
	}
	for key := range overlay {
		if key != "target" && key != "config" {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2804", "provider overlay cannot change "+key, deployment, "/spec/"+key))
		}
	}
	return projection, diagnostics
}

func resolveServiceDeployment(target Resource, overlay map[string]any, deployment Resource, writers map[string]bool) (DeploymentResourceProjection, []Diagnostic) {
	projection := DeploymentResourceProjection{Kind: target.Kind, Values: map[string]any{}, Provenance: map[string]string{}}
	allowed := map[string]bool{"target": true, "replicas": true, "resources": true, "placement": true, "runtime": true}
	var diagnostics []Diagnostic
	for key, value := range overlay {
		if !allowed[key] {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2804", "service overlay cannot change "+key, deployment, "/spec/service/"+key))
			continue
		}
		if key == "target" {
			continue
		}
		path := "/" + escapeJSONPointer(key)
		if key == "replicas" {
			replicas, ok := integerValue(value)
			if !ok || replicas <= 0 {
				diagnostics = append(diagnostics, deploymentDiagnostic("SCN2803", "service replicas must be positive", deployment, path))
				continue
			}
		}
		if duplicateDeploymentWriter(writers, target.Address, path) {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2802", "multiple deployment overlays write the same field", deployment, path))
			continue
		}
		projection.Values[key], projection.Provenance[path] = value, "deployment:"+deployment.Address
	}
	return projection, diagnostics
}

func resolveGatewayDeployment(target Resource, overlay map[string]any, deployment Resource, writers map[string]bool) (DeploymentResourceProjection, []Diagnostic) {
	projection := DeploymentResourceProjection{Kind: target.Kind, Values: map[string]any{}, Provenance: map[string]string{}}
	var diagnostics []Diagnostic
	for key := range overlay {
		if key != "target" && key != "listener" {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2804", "HTTP gateway overlay may change only listeners", deployment, "/spec/http_gateway/"+key))
		}
	}
	listeners := namedChildren(overlay, "listener")
	if len(listeners) == 0 {
		return projection, diagnostics
	}
	canonical := make([]any, 0, len(listeners))
	seen := map[string]bool{}
	for _, listener := range listeners {
		host, address, tls := stringValue(listener["host"]), stringValue(listener["address"]), stringValue(listener["tls"])
		port, portOK := integerValue(listener["port"])
		identity := host + "|" + address + "|" + strconv.Itoa(port)
		if !portOK || port <= 0 || port > 65535 || (tls != "disabled" && tls != "optional" && tls != "required") || seen[identity] {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2803", "gateway listener requires unique host/address/port and valid TLS mode", deployment, "/listener"))
			continue
		}
		seen[identity] = true
		canonical = append(canonical, listener)
	}
	path := "/listener"
	if duplicateDeploymentWriter(writers, target.Address, path) {
		diagnostics = append(diagnostics, deploymentDiagnostic("SCN2802", "multiple deployment overlays write the same field", deployment, path))
	} else {
		projection.Values["listener"], projection.Provenance[path] = canonical, "deployment:"+deployment.Address
	}
	return projection, diagnostics
}

func resolveSecretDeployment(target Resource, overlay map[string]any, deployment Resource, writers map[string]bool) (DeploymentResourceProjection, []Diagnostic) {
	projection := DeploymentResourceProjection{Kind: target.Kind, Values: map[string]any{}, Provenance: map[string]string{}}
	var diagnostics []Diagnostic
	for key, value := range overlay {
		if key == "target" {
			continue
		}
		path := "/" + escapeJSONPointer(key)
		if refString(value) == "" {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN4004", "secret deployment values must be typed secret references", deployment, path))
			continue
		}
		if duplicateDeploymentWriter(writers, target.Address, path) {
			diagnostics = append(diagnostics, deploymentDiagnostic("SCN2802", "multiple deployment overlays write the same field", deployment, path))
			continue
		}
		projection.Values[key], projection.Provenance[path] = value, "deployment:"+deployment.Address
	}
	return projection, diagnostics
}

func resolveDeploymentTarget(resources map[string]Resource, deployment Resource, overlayKind, reference string) string {
	if strings.Contains(reference, "/") {
		return reference
	}
	parts := strings.Split(reference, ".")
	if len(parts) == 2 {
		module := "app"
		if overlayKind == "service" && parts[0] == "service" {
			module = deployment.Module
		}
		return resourceAddress(module, parts[0], parts[1])
	}
	if len(parts) == 3 && parts[0] == "module" {
		module := resources[resourceAddress("app", "module", parts[1])]
		exports, _ := module.Spec["exports"].(map[string]any)
		if exported := refString(exports[parts[2]]); exported != "" {
			return resolveResourceRef(Resource{Module: parts[1]}, exported, overlayKind)
		}
	}
	return reference
}

func deploymentTargetKindMatches(kind, overlayKind string) bool {
	want := "scenery." + strings.ReplaceAll(overlayKind, "_", "-")
	return kind == want
}

func deploymentValueMatchesType(value any, typeName string) bool {
	typeName = strings.TrimSpace(typeName)
	if inner, ok := deploymentWrappedType(typeName, "optional"); ok {
		return value != nil && deploymentValueMatchesType(value, inner)
	}
	if inner, ok := deploymentWrappedType(typeName, "nullable"); ok {
		return value == nil || deploymentValueMatchesType(value, inner)
	}
	switch typeName {
	case "string", "relative_path", "url", "uuid", "date", "datetime", "duration":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "int", "int32", "int64":
		_, err := strconv.ParseInt(stringValue(value), 10, 64)
		return err == nil
	case "uint32", "uint64", "size":
		_, err := strconv.ParseUint(stringValue(value), 10, 64)
		return err == nil
	case "decimal", "float32", "float64":
		_, err := strconv.ParseFloat(stringValue(value), 64)
		return err == nil
	default:
		return value != nil
	}
}

func deploymentWrappedType(typeName, wrapper string) (string, bool) {
	prefix := wrapper + "("
	if !strings.HasPrefix(typeName, prefix) || !strings.HasSuffix(typeName, ")") {
		return "", false
	}
	inner := strings.TrimSpace(typeName[len(prefix) : len(typeName)-1])
	return inner, inner != ""
}

func duplicateDeploymentWriter(writers map[string]bool, address, path string) bool {
	key := address + "\x00" + path
	if writers[key] {
		return true
	}
	writers[key] = true
	return false
}

func mergeDeploymentProjection(base, overlay DeploymentResourceProjection) DeploymentResourceProjection {
	if base.Values == nil {
		base.Values = map[string]any{}
	}
	if base.Provenance == nil {
		base.Provenance = map[string]string{}
	}
	for key, value := range overlay.Values {
		base.Values[key] = value
	}
	for key, value := range overlay.Provenance {
		base.Provenance[key] = value
	}
	return base
}

func validateDeploymentSemantics(manifest *Manifest) []Diagnostic {
	if manifest == nil {
		return nil
	}
	var diagnostics []Diagnostic
	for _, resource := range manifest.Resources {
		if resource.Kind != "scenery.deployment" {
			continue
		}
		_, current := ResolveDeployment(manifest, resource.Address)
		diagnostics = append(diagnostics, current...)
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Address != diagnostics[j].Address {
			return diagnostics[i].Address < diagnostics[j].Address
		}
		return diagnostics[i].Path < diagnostics[j].Path
	})
	return diagnostics
}

func deploymentDiagnostic(code, message string, deployment Resource, path string) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: message, Address: deployment.Address, Path: path}
}
