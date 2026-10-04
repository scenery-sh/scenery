package spec

import (
	"maps"
	"strings"
)

type authoredAttributeSchema struct {
	Type              map[string]any
	Phase             string
	InputPhaseSource  string
	RevisionDomain    string
	RevisionSource    string
	Sensitive         bool
	SensitivitySource string
	Patchable         bool
	Ordered           bool
	Default           any
	Constraints       map[string]any
	MetadataStatus    string
	DefaultSource     string
	UnsupportedDraft  string
}

type authoredFieldKey struct {
	Revision string
	Name     string
}

type authoredFieldOverride struct {
	Phase             string
	RevisionDomain    string
	Sensitive         bool
	SensitivitySource string
	Ordered           bool
	Default           any
	DefaultSource     string
	Constraints       map[string]any
	UnsupportedDraft  string
}

type AuthoredFieldKey = authoredFieldKey
type AuthoredFieldOverride = authoredFieldOverride

var statusBadgeVariants = []string{
	"blue", "cyan", "error", "green", "info", "neutral", "orange",
	"pink", "purple", "red", "success", "teal", "warning", "yellow",
}

var authoredFieldOverrides = map[authoredFieldKey]authoredFieldOverride{
	{Name: "config"}: {SensitivitySource: "provider_schema"},

	{Revision: "scenery.authorization.rule", Name: "allow"}:                           {Phase: "runtime"},
	{Revision: "scenery.authorization.rule", Name: "deny"}:                            {Phase: "runtime"},
	{Revision: "scenery.record.validation", Name: "when"}:                             {Phase: "runtime"},
	{Revision: "scenery.operation.idempotency", Name: "key"}:                          {Phase: "runtime", Ordered: true, Constraints: map[string]any{"min_items": 1, "reference_root": "input", "reference_shape": "direct_input_field"}},
	{Revision: "scenery.execution.concurrency", Name: "key"}:                          {Phase: "runtime"},
	{Revision: "scenery.binding.http.context", Name: "from"}:                          {Phase: "runtime"},
	{Revision: "scenery.binding.http.response", Name: "when"}:                         {Phase: "runtime"},
	{Revision: "scenery.binding.http.response-body", Name: "from"}:                    {Phase: "runtime"},
	{Revision: "scenery.binding.http.response-header", Name: "from"}:                  {Phase: "runtime"},
	{Revision: "scenery.binding.http.response-cookie", Name: "from"}:                  {Phase: "runtime"},
	{Revision: "scenery.binding.cli.context", Name: "from"}:                           {Phase: "runtime"},
	{Revision: "scenery.binding.cli.output", Name: "from"}:                            {Phase: "runtime"},
	{Revision: "scenery.binding.cli.outcome", Name: "when"}:                           {Phase: "runtime"},
	{Revision: "scenery.binding.event.map", Name: "from"}:                             {Phase: "runtime"},
	{Revision: "scenery.binding.event", Name: "ordering_key"}:                         {Phase: "runtime"},
	{Revision: "scenery.binding.event", Name: "deduplication_key"}:                    {Phase: "runtime"},
	{Revision: "scenery.source.event_emission", Name: "ordering_key"}:                 {Phase: "runtime"},
	{Revision: "scenery.source.event_emission", Name: "deduplication_key"}:            {Phase: "runtime"},
	{Revision: "scenery.event-emission.from", Name: "when"}:                           {Phase: "runtime"},
	{Revision: "scenery.event-emission.from", Name: "payload"}:                        {Phase: "runtime"},
	{Revision: "scenery.source.authorization", Name: "strategy"}:                      {Default: "deny_unless_allowed", DefaultSource: "spec", Constraints: enumConstraint("all_must_allow", "allow_if_all", "allow_if_any", "any_allow", "deny_unless_allowed", "first_applicable")},
	{Revision: "scenery.source.record", Name: "unknown_fields"}:                       {Default: "reject", DefaultSource: "spec", Constraints: enumConstraint("preserve", "reject")},
	{Revision: "scenery.source.enum", Name: "open"}:                                   {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.source.union", Name: "open"}:                                  {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.binding.http.response-cookie", Name: "path"}:                  {Default: "/", DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie", Name: "max_age"}:               {Default: 0, DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie", Name: "secure"}:                {Default: true, DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie", Name: "http_only"}:             {Default: true, DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie", Name: "same_site"}:             {Default: "lax", DefaultSource: "http_profile", Constraints: enumConstraint("lax", "none", "strict")},
	{Revision: "scenery.binding.http", Name: "guarantee"}:                             {Default: "framework_enforced", DefaultSource: "http_profile"},
	{Revision: "scenery.binding.cli", Name: "command"}:                                {Ordered: true},
	{Revision: "scenery.deployment.http-listener", Name: "http_versions"}:             {Ordered: true},
	{Revision: "scenery.source.typescript_client", Name: "module"}:                    {Constraints: enumConstraint("esm")},
	{Revision: "scenery.source.typescript_client", Name: "runtime"}:                   {Constraints: enumConstraint("fetch")},
	{Revision: "scenery.source.typescript_client", Name: "materialization"}:           {Default: "source", DefaultSource: "spec", Constraints: enumConstraint("cache", "source")},
	{Revision: "scenery.typescript-client.retry", Name: "policy"}:                     {Constraints: enumConstraint("scenery.retry.idempotent")},
	{Revision: "scenery.typescript-client.retry", Name: "maximum_attempts"}:           {Constraints: map[string]any{"minimum": 2, "maximum": 10}},
	{Revision: "scenery.typescript-client.retry", Name: "maximum_delay_milliseconds"}: {Constraints: map[string]any{"maximum": 86_400_000}},
	{Revision: "scenery.typescript-client.retry", Name: "statuses"}:                   {Constraints: map[string]any{"item_minimum": 400, "item_maximum": 599, "unique_items": true}},
	{Revision: "scenery.operation.idempotency", Name: "mode"}:                         {Constraints: enumConstraint("keyed", "none")},
	{Revision: "scenery.source.execution", Name: "mode"}:                              {Constraints: enumConstraint("direct", "durable", "workflow")},
	{Revision: "scenery.execution.retry", Name: "strategy"}:                           {Constraints: enumConstraint("exponential", "none")},
	{Revision: "scenery.execution.deduplication", Name: "conflict"}:                   {Constraints: enumConstraint("return_existing")},
	{Revision: "scenery.source.binding", Name: "delivery"}:                            {Constraints: enumConstraint("call", "enqueue", "stream", "wait")},
	{Revision: "scenery.source.binding", Name: "exposure"}:                            {Constraints: enumConstraint("application", "internet", "local", "package", "private_network")},
	{Revision: "scenery.source.binding", Name: "protocol"}:                            {Constraints: enumConstraint("cli", "event", "http", "internal", "mcp")},
	{Revision: "scenery.binding.mcp", Name: "name"}:                                   {Constraints: map[string]any{"name_pattern": "^[a-z][a-z0-9_]{0,127}$"}},
	{Revision: "scenery.binding.mcp", Name: "allow_sensitive_output"}:                 {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.source.mcp_connection", Name: "transport"}:                    {Constraints: enumConstraint("streamable_http")},
	{Revision: "scenery.source.mcp_connection", Name: "tools"}:                        {RevisionDomain: "contract"},
	{Revision: "scenery.mcp-connection.auth", Name: "scheme"}:                         {Constraints: enumConstraint("none", "bearer", "header")},
	{Revision: "scenery.mcp-connection.auth", Name: "header"}:                         {Constraints: map[string]any{"pattern": httpHeaderLabelPattern}},
	{Revision: "scenery.mcp-server.capability", Name: "approval"}:                     {Constraints: enumConstraint("always", "never")},
	{Revision: "scenery.mcp-server.capability", Name: "name"}:                         {Constraints: map[string]any{"name_pattern": "^[a-z][a-z0-9_]{0,127}$"}},
	{Revision: "scenery.mcp-server.connection", Name: "required"}:                     {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.mcp-server.connection", Name: "namespace"}:                    {Constraints: map[string]any{"name_pattern": semanticLabelPattern}},
	{Revision: "scenery.source.assistant", Name: "implementation"}:                    {RevisionDomain: "implementation"},
	{Revision: "scenery.assistant.surface", Name: "session_access"}:                   {Constraints: enumConstraint("initiator")},
	{Revision: "scenery.binding.internal", Name: "visibility"}:                        {Constraints: enumConstraint("application", "package")},
	{Revision: "scenery.binding.internal", Name: "principal"}:                         {Constraints: enumConstraint("inherit")},
	{Revision: "scenery.binding.event", Name: "direction"}:                            {Constraints: enumConstraint("consume")},
	{Revision: "scenery.binding.event", Name: "guarantee"}:                            {Constraints: enumConstraint("at_least_once", "at_most_once", "exactly_once")},
	{Revision: "scenery.source.event_emission", Name: "guarantee"}:                    {Constraints: enumConstraint("at_least_once", "at_most_once", "exactly_once")},
	{Revision: "scenery.event.broker-retry", Name: "backoff"}:                         {Constraints: enumConstraint("exponential", "fixed", "none")},
	{Revision: "scenery.source.schedule", Name: "overlap"}:                            {Constraints: enumConstraint("allow", "queue", "replace", "skip")},
	{Revision: "scenery.entity.field-default", Name: "strategy"}:                      {Constraints: enumConstraint("current_datetime", "provider", "uuid_v7")},
	{Revision: "scenery.crud.execution", Name: "mode"}:                                {Constraints: enumConstraint("direct", "durable")},
	{Revision: "scenery.crud.list", Name: "max_page_size"}:                            {Default: 100, DefaultSource: "spec", Constraints: map[string]any{"minimum": 1, "maximum": 1000}},
	{Revision: "scenery.source.table_page", Name: "page_size"}:                        {Default: 50, DefaultSource: "spec", Constraints: map[string]any{"minimum": 1}},
	{Revision: "scenery.source.table_page", Name: "metadata"}:                         {Constraints: map[string]any{"min_items": 1, "unique_items": true}},
	{Revision: "scenery.source.table_page", Name: "nav_order"}:                        {Constraints: map[string]any{"minimum": 0}},
	{Revision: "scenery.source.table_page", Name: "scroll"}:                           {Default: "table", DefaultSource: "spec", Constraints: enumConstraint("page", "table")},
	{Revision: "scenery.source.split_page", Name: "nav_order"}:                        {Constraints: map[string]any{"minimum": 0}},
	{Revision: "scenery.source.content_page", Name: "nav_order"}:                      {Constraints: map[string]any{"minimum": 0}},
	{Revision: "scenery.source.workspace_page", Name: "nav_order"}:                    {Constraints: map[string]any{"minimum": 0}},
	{Revision: "scenery.source.workspace_page", Name: "presentation"}:                 {Default: "tabs", DefaultSource: "spec", Constraints: enumConstraint("tabs", "sidebar")},
	{Revision: "scenery.source.detail_page", Name: "nav_order"}:                       {Constraints: map[string]any{"minimum": 0}},
	{Revision: "scenery.source.detail_page", Name: "presentation"}:                    {Default: "page", DefaultSource: "spec", Constraints: enumConstraint("both", "dialog", "page")},
	{Revision: "scenery.detail-page.field", Name: "appearance"}:                       {Default: "auto", DefaultSource: "spec", Constraints: enumConstraint("auto", "badge", "datetime", "number", "text")},
	{Revision: "scenery.detail-page.field", Name: "hide_empty"}:                       {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.table-page.column", Name: "appearance"}:                       {Default: "auto", DefaultSource: "spec", Constraints: enumConstraint("auto", "badge", "datetime", "number", "text")},
	{Revision: "scenery.table-page.column", Name: "hidden"}:                           {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.table-page.column", Name: "export"}:                           {Default: true, DefaultSource: "spec"},
	{Revision: "scenery.table-page.column", Name: "export_format"}:                    {Default: "display", DefaultSource: "spec", Constraints: enumConstraint("display", "raw", "date")},
	{Revision: "scenery.table-page.column", Name: "export_zero_empty"}:                {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.table-page.sort", Name: "default"}:                            {Constraints: enumConstraint("asc", "desc")},
	{Revision: "scenery.table-page.group", Name: "default"}:                           {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.table-page.filter", Name: "hidden"}:                           {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.table-page.filter.preset", Name: "range"}:                     {Constraints: enumConstraint("today", "last_7_days", "month_to_date")},
	{Revision: "scenery.table-page.stats.tile", Name: "appearance"}:                   {Default: "plain", DefaultSource: "spec", Constraints: enumConstraint("plain", "money", "count", "percent")},
	{Revision: "scenery.table-page.stats.tile", Name: "sub_appearance"}:               {Default: "plain", DefaultSource: "spec", Constraints: enumConstraint("plain", "money", "count", "percent")},
	{Revision: "scenery.table-page.stats.tile", Name: "icon"}:                         {Constraints: semanticIconConstraint()},
	{Revision: "scenery.table-page.stats.tile", Name: "clear"}:                        {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.workspace-page.stats.tile", Name: "appearance"}:               {Default: "plain", DefaultSource: "spec", Constraints: enumConstraint("plain", "money", "count", "percent")},
	{Revision: "scenery.workspace-page.stats.tile", Name: "sub_appearance"}:           {Default: "plain", DefaultSource: "spec", Constraints: enumConstraint("plain", "money", "count", "percent")},
	{Revision: "scenery.workspace-page.stats.tile", Name: "icon"}:                     {Constraints: semanticIconConstraint()},
	{Revision: "scenery.table-page.toolbar", Name: "placement"}:                       {Default: "header", DefaultSource: "spec", Constraints: enumConstraint("header", "content")},
	{Revision: "scenery.table-page.row-detail", Name: "presentation"}:                 {Default: "inline", DefaultSource: "spec", Constraints: enumConstraint("inline", "panel")},
	{Revision: "scenery.table-page.row-detail", Name: "panel_width"}:                  {Constraints: map[string]any{"minimum": 280, "maximum": 560}},
	{Revision: "scenery.status-map.status", Name: "variant"}:                          {Constraints: enumConstraint(statusBadgeVariants...)},
	{Revision: "scenery.form-dialog.field", Name: "control"}:                          {Default: "auto", DefaultSource: "spec", Constraints: enumConstraint("auto", "select", "textarea", "text")},
	{Revision: "scenery.table-page.action", Name: "primary"}:                          {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.table-page.action", Name: "icon"}:                             {Constraints: semanticIconConstraint()},
	{Revision: "scenery.table-page.export", Name: "label"}:                            {Default: "Export", DefaultSource: "spec"},
	{Revision: "scenery.table-page.export", Name: "icon"}:                             {Constraints: semanticIconConstraint()},
	{Revision: "scenery.source.fixture", Name: "mode"}:                                {Constraints: enumConstraint("insert", "replace", "upsert")},
	{Revision: "scenery.deployment.secret", Name: "value"}:                            {Sensitive: true},
	{Revision: "scenery.deployment.http-listener", Name: "certificate"}:               {UnsupportedDraft: "platform_listener_and_certificate_schemas"},
	{Revision: "scenery.deployment.http-listener", Name: "platform_identity"}:         {UnsupportedDraft: "platform_listener_and_certificate_schemas"},

	{Revision: "scenery.source.service", Name: "implementation"}:         {RevisionDomain: "implementation"},
	{Revision: "scenery.source.service", Name: "lifecycle"}:              {RevisionDomain: "implementation"},
	{Revision: "scenery.source.service", Name: "config"}:                 {RevisionDomain: "implementation"},
	{Revision: "scenery.source.service", Name: "config_schema"}:          {RevisionDomain: "implementation"},
	{Revision: "scenery.source.operation", Name: "handler"}:              {RevisionDomain: "implementation"},
	{Revision: "scenery.source.provider", Name: "source"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.provider", Name: "config"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.data_source", Name: "config"}:             {RevisionDomain: "implementation"},
	{Revision: "scenery.source.execution_engine", Name: "config"}:        {RevisionDomain: "implementation"},
	{Revision: "scenery.source.event_bus", Name: "config"}:               {RevisionDomain: "implementation"},
	{Revision: "scenery.source.secret_store", Name: "config"}:            {RevisionDomain: "implementation"},
	{Revision: "scenery.source.view", Name: "implementation"}:            {RevisionDomain: "implementation"},
	{Revision: "scenery.source.view", Name: "implementation_digest"}:     {RevisionDomain: "implementation"},
	{Revision: "scenery.source.crud", Name: "implementation"}:            {RevisionDomain: "implementation"},
	{Revision: "scenery.source.renderer", Name: "module"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.renderer", Name: "config"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.renderer", Name: "implementation_digest"}: {RevisionDomain: "implementation"},
}

func authoredAttributeDefinition(revision, name string) authoredAttributeSchema {
	typeDefinition, status := authoredAttributeType(revision, name)
	definition := authoredAttributeSchema{
		Type: typeDefinition, Phase: "compile", RevisionDomain: authoredRevisionDomain(revision, name), Patchable: true,
		DefaultSource: "none", Constraints: authoredPrimitiveConstraints(typeDefinition), MetadataStatus: status,
	}
	applyAuthoredFieldOverride(&definition, authoredFieldOverrides[authoredFieldKey{Name: name}])
	applyAuthoredFieldOverride(&definition, authoredFieldOverrides[authoredFieldKey{Revision: revision, Name: name}])
	return definition
}

func AuthoredAttributeDefinition(revision, name string) SourceAttributeSchema {
	return cloneAuthoredAttributeSchema(authoredAttributeDefinition(revision, name))
}

func AuthoredFieldOverrides() map[AuthoredFieldKey]AuthoredFieldOverride {
	result := make(map[AuthoredFieldKey]AuthoredFieldOverride, len(authoredFieldOverrides))
	for key, override := range authoredFieldOverrides {
		override.Default = cloneSemanticValue(override.Default)
		override.Constraints = cloneMapValue(override.Constraints)
		result[key] = override
	}
	return result
}

func AuthoredRevisionDomain(revision, name string) string {
	return authoredRevisionDomain(revision, name)
}

func StatusBadgeVariants() []string {
	return append([]string(nil), statusBadgeVariants...)
}

func enumConstraint(values ...string) map[string]any {
	return map[string]any{"enum": values}
}

// semanticIconConstraint mirrors Astryx's IconName vocabulary. Generated
// clients pass these names directly to the catalog Icon component.
func semanticIconConstraint() map[string]any {
	return enumConstraint(
		"arrowDown", "arrowUp", "arrowsUpDown", "calendar", "check",
		"checkDouble", "chevronDown", "chevronLeft", "chevronRight", "clock",
		"close", "copy", "error", "externalLink", "eyeSlash", "funnel",
		"info", "menu", "microphone", "moreHorizontal", "search", "stop",
		"success", "viewColumns", "warning", "wrench",
	)
}

func authoredPrimitiveConstraints(typeDefinition map[string]any) map[string]any {
	constraints := map[string]any{}
	switch typeDefinition["primitive"] {
	case "non_negative_int":
		constraints["minimum"] = 0
	case "positive_int":
		constraints["minimum"] = 1
	case "tcp_port":
		constraints["minimum"], constraints["maximum"] = 1, 65535
	case "http_status":
		constraints["minimum"], constraints["maximum"] = 100, 599
	case "relative_path":
		constraints["format"] = "normalized_relative_path"
	case "route_path":
		constraints["format"] = "absolute_normalized_route"
	case "url":
		constraints["format"] = "absolute_url"
	case "json_pointer":
		constraints["format"] = "json_pointer"
	}
	return constraints
}

func applyAuthoredFieldOverride(definition *authoredAttributeSchema, override authoredFieldOverride) {
	if override.Phase != "" {
		definition.Phase = override.Phase
	}
	if override.RevisionDomain != "" {
		definition.RevisionDomain = override.RevisionDomain
	}
	if override.DefaultSource != "" {
		definition.Default = override.Default
		definition.DefaultSource = override.DefaultSource
	}
	if override.Sensitive {
		definition.Sensitive = true
	}
	if override.SensitivitySource != "" {
		definition.SensitivitySource = override.SensitivitySource
	}
	if override.Ordered {
		definition.Ordered = true
	}
	if override.UnsupportedDraft != "" {
		definition.UnsupportedDraft = override.UnsupportedDraft
	}
	maps.Copy(definition.Constraints, override.Constraints)
}

func UnsupportedDraftCapability(revision, name string) string {
	return authoredFieldOverrides[authoredFieldKey{Revision: revision, Name: name}].UnsupportedDraft
}

func authoredRevisionDomain(revision, name string) string {
	domain := authoredDefaultRevisionDomain(revision)
	if override := authoredFieldOverrides[authoredFieldKey{Revision: revision, Name: name}]; override.RevisionDomain != "" {
		domain = override.RevisionDomain
	}
	return domain
}

func authoredDefaultRevisionDomain(revision string) string {
	if revision == "scenery.mcp-connection.tools" {
		return "contract"
	}
	if strings.HasPrefix(revision, "scenery.deployment.") || revision == "scenery.source.deployment" || strings.HasPrefix(revision, "scenery.mcp-connection.") || revision == "scenery.source.mcp_connection" {
		return "deployment"
	}
	if revision == "scenery.source.secret" {
		return "deployment"
	}
	if revision == "scenery.source.module" || revision == "scenery.source.patch" {
		return "workspace_only"
	}
	if revision == "scenery.patch.operation" {
		return "workspace_only"
	}
	if strings.HasPrefix(revision, "scenery.source.go-") || strings.HasPrefix(revision, "scenery.go-target.") || revision == "scenery.source.typescript_client" || revision == "scenery.assistant.implementation" {
		return "implementation"
	}
	if revision == "scenery.typescript-client.retry" || revision == "scenery.typescript-client.react" {
		return "implementation"
	}
	switch revision {
	case "scenery.service.implementation", "scenery.service.lifecycle", "scenery.service.config", "scenery.operation.handler", "scenery.view.implementation":
		return "implementation"
	}
	return "contract"
}
