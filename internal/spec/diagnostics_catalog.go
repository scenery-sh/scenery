package spec

import (
	"sort"
	"strconv"
	"strings"
)

type DiagnosticDefinition struct {
	Code             string   `json:"code"`
	Category         string   `json:"category"`
	Identity         string   `json:"identity"`
	Meaning          string   `json:"meaning"`
	DefaultSeverity  string   `json:"default_severity"`
	StructuredFields []string `json:"structured_fields"`
	Documentation    string   `json:"documentation"`
}

var diagnosticDefinitions = parseDiagnosticDefinitions(diagnosticCatalogRows)

func DiagnosticDefinitions() []DiagnosticDefinition {
	result := append([]DiagnosticDefinition(nil), diagnosticDefinitions...)
	for index := range result {
		result[index].StructuredFields = append([]string(nil), result[index].StructuredFields...)
	}
	return result
}

func DiagnosticDefinitionFor(code string) (DiagnosticDefinition, bool) {
	index := sort.Search(len(diagnosticDefinitions), func(index int) bool { return diagnosticDefinitions[index].Code >= code })
	if index == len(diagnosticDefinitions) || diagnosticDefinitions[index].Code != code {
		return DiagnosticDefinition{}, false
	}
	definition := diagnosticDefinitions[index]
	definition.StructuredFields = append([]string(nil), definition.StructuredFields...)
	return definition, true
}

// ParseDiagnosticDefinitions validates and parses the compact checked-in
// catalog representation. It is exported for catalog conformance tests.
func ParseDiagnosticDefinitions(rows string) []DiagnosticDefinition {
	return parseDiagnosticDefinitions(rows)
}

// DiagnosticCategory returns the stable category for a diagnostic code.
func DiagnosticCategory(code string) string {
	return diagnosticCategory(code)
}

func parseDiagnosticDefinitions(rows string) []DiagnosticDefinition {
	var definitions []DiagnosticDefinition
	identities := map[string]string{}
	for _, row := range strings.Split(strings.TrimSpace(rows), "\n") {
		parts := strings.SplitN(strings.TrimSpace(row), "|", 3)
		if len(parts) != 3 {
			panic("invalid diagnostic catalog row: " + row)
		}
		code, identity, meaning := parts[0], parts[1], parts[2]
		if previous := identities[code]; previous != "" {
			panic("diagnostic code " + code + " has duplicate identities " + previous + " and " + identity)
		}
		identities[code] = identity
		fields := []string{"address", "path", "range", "related", "suggestions", "details"}
		if strings.HasPrefix(code, "SCN90") {
			fields = append(fields, "report_token")
		}
		definitions = append(definitions, DiagnosticDefinition{
			Code: code, Category: diagnosticCategory(code), Identity: identity, Meaning: meaning,
			DefaultSeverity: diagnosticDefaultSeverity(code), StructuredFields: fields,
			Documentation: meaning + ". Agents branch on " + code + ", not its message text.",
		})
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Code < definitions[j].Code })
	return definitions
}

func diagnosticCategory(code string) string {
	number, _ := strconv.Atoi(strings.TrimPrefix(code, "SCN"))
	switch {
	case number >= 1000 && number <= 1099:
		return "syntax"
	case number >= 1100 && number <= 1199:
		return "identity"
	case number >= 1200 && number <= 1399:
		return "types_and_evaluation"
	case number >= 2000 && number <= 2199:
		return "operation_and_http_binding"
	case number >= 2200 && number <= 2399:
		return "execution_and_delivery"
	case number >= 2400 && number <= 2499:
		return "binding_and_cli"
	case number >= 2500 && number <= 2599:
		return "data"
	case number >= 2600 && number <= 2699:
		return "ui"
	case number >= 2700 && number <= 2799:
		return "events"
	case number >= 2800 && number <= 2899:
		return "deployment"
	case number >= 2900 && number <= 2999:
		return "patches"
	case number >= 3000 && number <= 3199:
		return "packages_modules_and_registry"
	case number >= 3200 && number <= 3399:
		return "providers_entities_and_extensions"
	case number >= 3400 && number <= 3499:
		return "go_configuration"
	case number >= 4000 && number <= 4199:
		return "security_and_secret_flow"
	case number >= 4200 && number <= 4299:
		return "runtime_policy"
	case number >= 6000 && number <= 6199:
		return "go_implementation_abi"
	case number >= 6200 && number <= 6299:
		return "go_generation_and_verification"
	case number >= 6300 && number <= 6399:
		return "typescript_generation"
	case number >= 6400 && number <= 6499:
		return "compatibility"
	case number >= 7000 && number <= 7099:
		return "profile_conformance"
	case number >= 8000 && number <= 8099:
		return "request_protocol"
	case number >= 9000 && number <= 9099:
		return "internal"
	default:
		return "unassigned"
	}
}

func diagnosticDefaultSeverity(string) string {
	return "error"
}

const diagnosticCatalogRows = `
SCN1000|hcl_syntax|HCL syntax parsing failed
SCN1001|source_access|A declared source file is unavailable or unsafe
SCN1002|top_level_block|A top-level source block is unknown
SCN1003|reserved_source_selector|Source selectors are not part of the current language
SCN1004|application_labels|The application declaration has invalid labels
SCN1005|package_block|A package source block is unknown
SCN1006|resource_labels|A resource declaration has invalid labels
SCN1007|canonical_resource_field|A canonical resource contains an unknown field
SCN1008|resource_schema|A canonical resource schema is unknown
SCN1009|required_resource_field|A canonical resource is missing a required field
SCN1010|dynamic_expression|A compile-phase expression is not statically evaluable
SCN1011|source_encoding|Source encoding is not canonical valid UTF-8
SCN1012|comment_syntax|Source comments do not use canonical hash syntax
SCN1013|source_identifier|A source identifier or label is not lower snake case ASCII
SCN1014|duplicate_authored_block|An authored singleton or labeled child block is duplicated
SCN1015|required_authored_field|An authored block is missing a required field
SCN1016|authored_label_count|An authored block has the wrong label count
SCN1017|authored_attribute_shape|An authored attribute is unknown or must be a child block
SCN1018|authored_child_block|An authored child block is not allowed by its parent schema
SCN1019|source_formatting|Source formatting failed
SCN1020|authored_attribute_constraint|An authored attribute violates a declared schema constraint
SCN1021|legacy_contract_filename|An authored contract file uses a retired pre-cutover filename and must be renamed
SCN1101|root_singleton_identity|A required root singleton is missing or repeated
SCN1102|module_identity|A module declaration is duplicated
SCN1103|module_resource_identity|A resource identity is duplicated within a module
SCN1104|resource_address_identity|A canonical resource address is duplicated
SCN1201|type_expression|A type expression is missing or invalid
SCN1202|string_type_reference|A type reference was authored as a string
SCN1203|unknown_type_reference|A type reference cannot be resolved
SCN1204|record_wire_name|Record fields have duplicate wire names
SCN1205|named_type_child_identity|A named enum or union child is duplicated
SCN1206|named_type_wire_value|Enum or union children have duplicate wire values
SCN1207|resource_reference|A typed resource or module input reference cannot be resolved
SCN1208|union_discriminator|A tagged union discriminator is missing
SCN1209|open_union_preservation|An open union has no unknown variant preservation declaration
SCN1210|union_payload|A union variant payload is not a record
SCN1211|union_discriminator_collision|A union discriminator collides with a payload field
SCN1212|contextual_scalar|A contextual exact scalar is invalid
SCN1220|unknown_record_fields_policy|A record unknown-fields policy is invalid
SCN1221|record_field_identity|Record field names are empty or duplicated
SCN1222|record_field_attribute|A record field constraint attribute is unknown
SCN1223|numeric_constraint_type|A numeric constraint has an incompatible field or value
SCN1224|numeric_constraint_order|A numeric minimum exceeds its maximum
SCN1225|collection_constraint_value|A length or item constraint is inapplicable or negative
SCN1226|collection_constraint_order|A minimum length or item count exceeds its maximum
SCN1227|string_pattern|A string pattern constraint is inapplicable or invalid RE2
SCN1228|string_format|A string format constraint is unsupported
SCN1229|unique_items_type|A unique-items constraint targets an incompatible type
SCN1230|record_validation_identity|Record validation names are empty or duplicated
SCN1231|record_validation_shape|A record validation is incomplete or malformed
SCN1232|record_validation_path|A record validation path does not target its record
SCN1233|record_validation_expression|A record validation expression is invalid
SCN2001|http_binding_shape|An HTTP binding is missing its gateway method or path
SCN2002|http_route_identity|Two HTTP bindings own the same route
SCN2003|operation_idempotency|An operation idempotency declaration has an invalid mode or key shape
SCN2004|operation_owner|An operation does not have exactly one service or library owner
SCN2005|library_contract|A library declaration or operation contract is invalid
SCN2101|http_method|An HTTP method is not canonical uppercase
SCN2102|http_path|An HTTP path is not absolute normalized and wildcard-free
SCN2103|http_raw_codec|The raw HTTP codec is outside the claimed profile
SCN2104|internet_http_policy|An internet binding lacks explicit security policy
SCN2105|http_codec_profile|An HTTP codec profile is unsupported
SCN2106|http_exposure|An HTTP binding widens gateway exposure
SCN2107|trusted_proxy_policy|Forwarded headers lack a trusted-proxy policy
SCN2108|http_authorization|HTTP authorization is not explicit
SCN2109|anonymous_http_authorization|Anonymous HTTP access lacks the public authorization policy
SCN2110|http_path_mapping|HTTP path parameters are not mapped exactly once
SCN2111|http_outcome_mapping_cardinality|Reachable outcomes do not have exactly one HTTP response mapping
SCN2112|http_response_status|An HTTP response status or no-body status mapping is invalid
SCN2113|http_value_mapping|An HTTP input or output value mapping is incomplete overlapping or invalid
SCN2114|http_wire_codec|An HTTP mapped value has an incompatible wire codec
SCN2115|http_wire_name_and_cookie|An HTTP wire name header cookie or cookie policy is invalid
SCN2116|http_form_body|A form body does not target one complete record
SCN2117|http_multipart_coverage|Multipart parts are duplicated or leave required fields unmapped
SCN2118|http_multipart_part_type|A multipart part kind is incompatible with its target type
SCN2119|http_multipart_metadata|Multipart media limits or retained file metadata are invalid
SCN2120|http_effective_limits|HTTP effective limits are absent or invalid
SCN2121|http_limit_widening|An HTTP binding widens a gateway limit or compression policy
SCN2122|http_timeout|An HTTP timeout value is invalid
SCN2123|http_timeout_widening|An HTTP binding widens a gateway timeout
SCN2200|execution_shape|An execution is missing its operation or mode
SCN2201|durable_engine|A durable execution has no engine
SCN2202|durable_revision|A durable execution revision is not positive
SCN2203|durable_policy_shape|A durable execution lacks required timeout retry or retention policy
SCN2204|workflow_unavailable|Workflow execution is unavailable in the current Scenery build
SCN2205|durable_idempotency|Durable keyed idempotency lacks valid deduplication policy
SCN2206|durable_engine_reference|A durable execution engine reference is invalid
SCN2207|durable_timing|Durable timeout lease or attempts are invalid
SCN2208|durable_retry_retention|Durable retry or retention policy is invalid
SCN2209|durable_concurrency|Durable concurrency key or limit is invalid
SCN2210|durable_external_name|A durable external name is duplicated within its engine
SCN2301|schedule_trigger|A schedule does not select exactly one trigger
SCN2302|schedule_invoke_shape|A schedule invocation is incomplete
SCN2303|schedule_invoke_contract|A schedule invocation has inconsistent typed references or input
SCN2304|schedule_policy|Schedule trigger overlap or catchup policy is invalid
SCN2401|binding_shape|A binding is missing required operation execution delivery or policy fields
SCN2402|internal_binding_principal|An internal binding does not inherit its principal
SCN2403|binding_execution_operation|A binding and its execution select different operations
SCN2404|binding_execution_delivery|A binding delivery is unsupported by its execution
SCN2405|internal_binding_visibility|Internal binding visibility is invalid
SCN2406|internal_binding_exposure|Internal binding exposure does not match visibility
SCN2410|cli_command_identity|A CLI command name is invalid or reserved
SCN2411|cli_command_collision|A CLI command is duplicated
SCN2412|cli_input_target|A CLI input mapping target is invalid or duplicated
SCN2413|cli_argument_position|CLI argument positions are invalid duplicated or non-contiguous
SCN2414|cli_flag_name|A CLI flag long or short name is invalid or duplicated
SCN2415|cli_input_coverage|CLI mappings do not populate operation input exactly once
SCN2416|cli_outcome_exit|A CLI outcome condition or exit status is invalid or duplicated
SCN2417|cli_output_codec|CLI output does not use a supported typed codec
SCN2418|cli_outcome_coverage|A reachable operation outcome has no CLI mapping
SCN2501|entity_record|An entity has no record type
SCN2502|entity_data_source|An entity has no data source
SCN2503|view_shape|A view is missing a typed data source input result or implementation
SCN2504|crud_contract|A CRUD resource has invalid actions implementation execution or projection
SCN2505|data_source_contract|A data source has invalid provider lifecycle or capability requirements
SCN2506|entity_contract|Entity mapping fields keys or defaults are invalid
SCN2507|view_implementation|A view implementation locator kind or file is invalid
SCN2508|fixture_contract|Fixture entity mode environment or typed rows are invalid
SCN2509|view_result_contract|A view implementation result is incompatible with its declared type
SCN2510|crud_expansion_identity|A CRUD-derived resource address collides
SCN2511|fixture_shape|A fixture is missing required entity environment mode or values
SCN2512|crud_list_field|A CRUD list capability references an unavailable field or action
SCN2513|crud_list_filter|A CRUD list filter is not supported by the standard query contract
SCN2514|crud_list_sort|A CRUD list sort or default ordering is invalid
SCN2515|crud_list_search|A CRUD list search field is not supported by the standard query contract
SCN2601|page_contract|A page lacks a path or typed load binding
SCN2602|renderer_contract|A renderer lacks a page runtime or module
SCN2603|page_semantics|Page path load actions or renderer references are invalid
SCN2604|renderer_semantics|Renderer module exports or configuration are invalid
SCN2605|page_route_identity|A page route is duplicated
SCN2606|renderer_runtime_identity|A renderer runtime identity is duplicated
SCN2607|react_component|A declared React component module or export is invalid
SCN2608|table_page_contract|A table page source or required declaration is invalid
SCN2609|table_page_column|A table page column field appearance export formatting or identity is invalid
SCN2610|table_page_query|A table page filter sort or default ordering is invalid
SCN2611|table_page_slot|A table page slot does not resolve to a declared React component
SCN2612|table_page_row_link|A table page row link references an unavailable field
SCN2613|table_page_page_size|A table page page size is invalid for its source
SCN2614|table_page_expansion_identity|A table-page-derived resource address collides
SCN2615|split_page_contract|A split page source or slot contract is invalid
SCN2616|split_page_expansion_identity|A split-page-derived resource address collides
SCN2617|content_page_contract|A content page source or slot contract is invalid
SCN2618|content_page_expansion_identity|A content-page-derived resource address collides
SCN2619|page_route_contract|A generated page search or navigation contract is invalid
SCN2620|status_map_contract|A status map declaration is invalid
SCN2621|form_dialog_contract|A form dialog declaration or mutation binding is invalid
SCN2622|table_page_workbench|A table page workbench declaration is invalid
SCN2623|table_page_grouping_and_detail|A table page grouping row detail presentation or row-intent hook is invalid
SCN2624|table_page_pagination|A table page pagination mapping is invalid
SCN2625|table_page_input_mapping|A table page predicate or query mapping is invalid
SCN2626|workspace_page_contract|A workspace page declaration tab or presentation is invalid
SCN2627|workspace_page_stats|A workspace page stats count or availability field is invalid
SCN2628|workspace_page_expansion_identity|A workspace-page-derived resource address collides
SCN2629|detail_page_contract|A detail page source path parameter or presentation contract is invalid
SCN2630|detail_page_section|A detail page section or result field declaration is invalid
SCN2631|detail_page_action|A detail page action dialog or seed contract is invalid
SCN2632|detail_page_table|A detail page related table or parameter mapping is invalid
SCN2633|detail_page_expansion_identity|A detail-page-derived resource address collides
SCN2634|table_page_stats_tile|A table page statistics tile format sub-field or filter action is invalid
SCN2635|table_page_filter_preset|A table page date or datetime filter preset is invalid
SCN2701|event_contract|An event lacks a payload or positive version
SCN2702|event_emission_shape|An event emission is incomplete
SCN2703|event_binding_shape|An event binding is incomplete
SCN2704|event_binding_direction|An event consumer binding has the wrong direction
SCN2705|event_binding_contract|An event consumer channel delivery mapping or retry policy is invalid
SCN2706|event_emission_contract|An event emission selection payload or retry policy is invalid
SCN2801|deployment_resolution|A requested deployment or manifest cannot be resolved
SCN2802|deployment_overlay_target|A deployment overlay target or write ownership is invalid
SCN2803|deployment_overlay_value|A deployment overlay value has an invalid field type or value
SCN2804|deployment_overlay_boundary|A deployment overlay changes a forbidden contract field
SCN2805|deployment_shape|A deployment is missing its environment
SCN2901|patch_target|A patch target cannot be resolved
SCN2902|patch_shape|A patch lacks the target's exact schema revision or preconditions
SCN2903|patch_precondition|A patch precondition failed
SCN2904|patch_write_path|A patch path is not writable
SCN2906|patch_boundary|A patch crosses a private or unpatchable boundary
SCN2907|patch_collision|Two patches write the same path
SCN3001|module_labels|A module declaration has invalid labels
SCN3002|module_source|A module source path is invalid or unsafe
SCN3004|module_package_access|A module package source cannot be read
SCN3005|module_package_manifest|A module lacks package.scn
SCN3006|module_package_singleton|A module has the wrong package block count
SCN3007|module_input_required|A required module input is missing or unresolved
SCN3008|module_input_value|A supplied module input is unknown or invalid
SCN3009|module_dependency_cycle|A module dependency cycle or unavailable export blocks compilation
SCN3010|module_export|A root or nested module export cannot be resolved
SCN3100|lockfile_syntax|The lockfile source or declaration shape is invalid
SCN3101|locked_content|Required locked content is absent corrupt or unsafe
SCN3103|locked_descriptor_identity|A locked module or provider descriptor identity mismatches cached content
SCN3104|locked_integrity|Locked package integrity is invalid
SCN3105|locked_source|A locked package source is invalid or unavailable
SCN3106|provider_lock|A provider lock entry is missing or invalid
SCN3107|lock_ordering|Lock entries are duplicated or non-canonical
SCN3401|go_config_reference|A Go service config field does not reference a package input
SCN3402|go_config_input|A Go service config package input is unavailable
SCN3403|go_config_value|A Go service config field has no resolved value
SCN3404|go_config_type|A Go service config field has no stable package input type
SCN3405|go_config_key|A Go service config key is not lower snake case
SCN3406|go_config_phase|A Go service config package input phase is invalid
SCN3407|go_config_value_type|A Go service config value does not match its package input type
SCN4001|secret_config_sink|Secret configuration lacks a sensitive typed secret reference
SCN4002|nonsecret_config_flow|A resource or secret reference flows into non-secret configuration
SCN4003|sensitive_config_reference|Sensitive Go configuration lacks a secret resource reference
SCN4004|secret_plaintext|Secret plaintext entered a forbidden graph artifact or value
SCN4012|generated_go_name_collision|Generated Go identifiers collide or use reserved names
SCN4101|authentication_policy|Authentication policy is invalid or incomplete
SCN4102|authorization_rule|An authorization rule shape type or syntax is invalid
SCN4103|internet_authentication|An internet binding has invalid authentication
SCN4104|internet_authorization|An internet binding has invalid authorization
SCN4105|workload_identity|A workload identity contract is invalid
SCN4106|authentication_provider|An authentication provider reference or kind is invalid
SCN4107|security_reference|A security policy reference cannot be resolved
SCN4201|middleware_contract|A middleware declaration has invalid protocols phases or effects
SCN4202|pipeline_contract|A pipeline has invalid steps or middleware ordering
SCN4203|runtime_policy_reference|A runtime policy or middleware reference cannot be resolved
SCN6101|go_contract_generation|A Go contract cannot be generated from the canonical graph
SCN6103|go_type_lowering|A canonical type cannot be lowered to Go
SCN6104|go_handler_contract|A Go handler contract is invalid
SCN6105|go_service_contract|A Go service contract is invalid
SCN6110|go_package_identity|A Go package contract identity is invalid
SCN6111|go_package_abi|A Go package contract ABI revision is invalid
SCN6112|go_runtime_abi|A Go runtime ABI requirement is invalid
SCN6113|go_capability_abi|A Go capability ABI requirement is invalid
SCN6114|go_contract_descriptor|A generated Go contract descriptor is invalid
SCN6115|go_composition_contract|A Go composition registration or ownership contract is invalid
SCN6120|go_contract_declaration|A source unit declares the wrong Go contract boundary
SCN6121|go_contract_import_ownership|A Go contract import path is owned by multiple source units
SCN6122|implementation_revision_input|A Go implementation revision input or generated adapter digest is invalid
SCN6130|go_module_root|A Go module root is unavailable unsafe or non-portable
SCN6131|go_module_import_path|A Go module import path is not portable
SCN6132|go_toolchain_version|A Go toolchain version is not exact semantic version
SCN6133|go_toolchain_experiments|Go toolchain experiments are not canonical
SCN6134|go_target_inheritance|Go target inheritance is conflicting or cyclic
SCN6135|go_target_role|A Go target role is unsupported
SCN6136|go_target_dependencies|A Go target module or toolchain reference is unresolved
SCN6137|go_target_platform|A fixed Go target lacks platform identity
SCN6138|go_target_cgo|A Go target CGO mode is invalid
SCN6139|go_target_packages|A Go target has no package patterns
SCN6140|go_target_flags|A Go target flag is ambient or non-portable
SCN6141|native_toolchain_identity|A Go target needs an unresolved native toolchain identity
SCN6142|go_architecture_features|Go target architecture features are invalid or non-canonical
SCN6143|go_module_ownership|A Go module root or import path has multiple owners
SCN6150|implementation_target_resolution|Implementation revision target inheritance cannot be resolved
SCN6151|implementation_target_module|An implementation revision target has no resolved Go module
SCN6160|go_library_package|A Go library implementation package or handler is invalid
SCN6201|generated_go_contract|Generated Go contract artifacts are invalid
SCN6202|go_verification|Staged Go target or implementation verification failed
SCN6203|stale_generated_go|Committed generated Go artifacts are stale or invalid
SCN6204|stale_generated_typescript|Committed generated TypeScript artifacts are stale or invalid
SCN6205|planned_generated_go|A change plan produces invalid generated Go artifacts
SCN6206|planned_generated_typescript|A change plan produces invalid generated TypeScript artifacts
SCN6207|generated_artifact_transaction|Generated artifact planning overlay or transaction failed
SCN6208|package_abi_invariance|Package Go ABI shape varies by module input
SCN6209|revision_scheme_changed|A pending artifact uses a superseded revision scheme
SCN6301|typescript_target|A TypeScript client target is invalid or unavailable
SCN6302|typescript_gateway|A TypeScript client gateway selection is invalid
SCN6303|typescript_operation_selection|A TypeScript client operation selection is invalid
SCN6304|typescript_type_lowering|A canonical type cannot be lowered to TypeScript
SCN6305|typescript_codec|A TypeScript client codec mapping is invalid
SCN6306|typescript_materialization|TypeScript client materialization is invalid
SCN6307|typescript_runtime|Generated TypeScript runtime configuration is invalid
SCN6308|typescript_response|A TypeScript response mapping is invalid
SCN6309|typescript_target_contract|A TypeScript target package module runtime or output root is invalid
SCN6310|typescript_type_name|Generated TypeScript type names collide
SCN6311|typescript_field_name|Generated TypeScript field names collide
SCN6312|typescript_method_name|Generated TypeScript client method names collide
SCN6313|typescript_metadata|Generated TypeScript metadata is invalid
SCN6314|typescript_selection_artifact|A TypeScript client selection artifact is invalid
SCN6315|typescript_transport_outcome|A TypeScript transport outcome mapping is invalid
SCN6316|typescript_field_constraint|A TypeScript field constraint cannot be represented safely
SCN6320|typescript_react_override|A declared React override is incompatible with its generated slot contract
SCN6321|typescript_react_application|A reachable application module has an unrelated TypeScript error
SCN6322|typescript_react_readiness|The native React generation checker or application dependencies are unavailable
SCN7001|feature_unavailable|A recognized source feature is unavailable
SCN7008|streaming_unavailable|Streaming or server-sent events are unavailable
SCN7009|unsupported_draft_surface|A declaration uses an unresolved draft capability
SCN8001|invalid_request|The CLI or agent request is invalid
SCN8002|revision_conflict|A request revision no longer matches current state
SCN8003|failed_precondition|A request precondition is not satisfied
SCN8004|capability_unavailable|A required provider extension or capability is unavailable
SCN8005|permission_denied|Permission or required approval was denied
SCN9000|internal_tooling_failure|An unexpected internal tooling failure occurred
SCN9001|internal_parser_invariant|The parser returned an impossible body implementation
SCN9002|internal_revision_invariant|Canonical contract revision construction failed internally
SCN9003|internal_compilation_result_invariant|An internal caller omitted its compilation result
`
