package objectstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	MetadataSchema = "onlava_data"
	RecordsSchema  = "onlava_data_records"
)

type DB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Begin(context.Context) (pgx.Tx, error)
}

type Queryer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Options struct {
	Permissions Permissions
	Now         func() time.Time
}

type Store struct {
	db     DB
	perms  Permissions
	now    func() time.Time
	router *LiveRouter
}

type Tenant struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Object struct {
	ID                    string    `json:"id"`
	TenantID              string    `json:"tenant_id"`
	NameSingular          string    `json:"name_singular"`
	NamePlural            string    `json:"name_plural"`
	TableName             string    `json:"table_name"`
	LabelSingular         string    `json:"label_singular"`
	LabelPlural           string    `json:"label_plural"`
	IsCustom              bool      `json:"is_custom"`
	IsSystem              bool      `json:"is_system"`
	SchemaVersion         int64     `json:"schema_version"`
	OutboxTriggersEnabled bool      `json:"outbox_triggers_enabled"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type FieldType string

const (
	FieldText        FieldType = "text"
	FieldRichText    FieldType = "rich_text"
	FieldNumber      FieldType = "number"
	FieldNumeric     FieldType = "numeric"
	FieldCurrency    FieldType = "currency"
	FieldBoolean     FieldType = "boolean"
	FieldDate        FieldType = "date"
	FieldDatetime    FieldType = "datetime"
	FieldUUID        FieldType = "uuid"
	FieldSelect      FieldType = "select"
	FieldMultiSelect FieldType = "multi_select"
	FieldRating      FieldType = "rating"
	FieldJSON        FieldType = "json"
	FieldRawJSON     FieldType = "raw_json"
	FieldFiles       FieldType = "files"
	FieldFullName    FieldType = "full_name"
	FieldAddress     FieldType = "address"
	FieldEmails      FieldType = "emails"
	FieldPhones      FieldType = "phones"
	FieldRelation    FieldType = "relation"
)

type PhysicalColumn struct {
	Name     string `json:"name"`
	Part     string `json:"part,omitempty"`
	SQLType  string `json:"sql_type"`
	Nullable bool   `json:"nullable"`
}

type Field struct {
	ID               string           `json:"id"`
	TenantID         string           `json:"tenant_id"`
	ObjectID         string           `json:"object_id"`
	Name             string           `json:"name"`
	Label            string           `json:"label"`
	Type             FieldType        `json:"type"`
	IsCustom         bool             `json:"is_custom"`
	IsSystem         bool             `json:"is_system"`
	IsNullable       bool             `json:"is_nullable"`
	IsUnique         bool             `json:"is_unique"`
	IsArray          bool             `json:"is_array"`
	IsSearchable     bool             `json:"is_searchable"`
	SearchWeight     string           `json:"search_weight,omitempty"`
	RelationObjectID string           `json:"relation_object_id,omitempty"`
	Settings         map[string]any   `json:"settings,omitempty"`
	Columns          []PhysicalColumn `json:"columns"`
	Options          []FieldOption    `json:"options,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

type FieldOption struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	FieldID    string `json:"field_id"`
	Value      string `json:"value"`
	Label      string `json:"label"`
	Color      string `json:"color,omitempty"`
	Position   int    `json:"position"`
	IsArchived bool   `json:"is_archived"`
}

type FieldOptionRequest struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	Color string `json:"color,omitempty"`
}

type IndexMethod string

const (
	IndexMethodBTree IndexMethod = "btree"
	IndexMethodGIN   IndexMethod = "gin"
)

type Index struct {
	ID           string       `json:"id"`
	TenantID     string       `json:"tenant_id"`
	ObjectID     string       `json:"object_id"`
	Name         string       `json:"name"`
	PhysicalName string       `json:"physical_name"`
	Method       IndexMethod  `json:"method"`
	IsUnique     bool         `json:"is_unique"`
	IsSystem     bool         `json:"is_system"`
	Fields       []IndexField `json:"fields"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

type IndexField struct {
	Field     string `json:"field"`
	FieldID   string `json:"field_id,omitempty"`
	Position  int    `json:"position,omitempty"`
	Desc      bool   `json:"desc,omitempty"`
	Direction string `json:"direction,omitempty"`
	OpClass   string `json:"opclass,omitempty"`
}

type CreateIndexRequest struct {
	TenantKey string       `json:"tenant_key"`
	Name      string       `json:"name"`
	Method    IndexMethod  `json:"method,omitempty"`
	Unique    bool         `json:"unique,omitempty"`
	Fields    []IndexField `json:"fields"`
}

type ListIndexesRequest struct {
	TenantKey string `json:"tenant_key"`
}

type Actor struct {
	ID   string `json:"id,omitempty"`
	Data any    `json:"data,omitempty"`
}

type CreateObjectRequest struct {
	TenantKey     string `json:"tenant_key"`
	TenantName    string `json:"tenant_name,omitempty"`
	NameSingular  string `json:"name_singular"`
	NamePlural    string `json:"name_plural,omitempty"`
	LabelSingular string `json:"label_singular,omitempty"`
	LabelPlural   string `json:"label_plural,omitempty"`
}

type CreateFieldRequest struct {
	TenantKey      string               `json:"tenant_key"`
	Name           string               `json:"name"`
	Label          string               `json:"label,omitempty"`
	Type           FieldType            `json:"type"`
	Nullable       *bool                `json:"nullable,omitempty"`
	Unique         bool                 `json:"unique,omitempty"`
	Array          bool                 `json:"array,omitempty"`
	Searchable     bool                 `json:"searchable,omitempty"`
	SearchWeight   string               `json:"search_weight,omitempty"`
	RelationObject string               `json:"relation_object,omitempty"`
	Relation       RelationSettings     `json:"relation,omitempty"`
	Settings       map[string]any       `json:"settings,omitempty"`
	Options        []FieldOptionRequest `json:"options,omitempty"`
}

type RelationKind string

const (
	RelationManyToOne  RelationKind = "many_to_one"
	RelationManyToMany RelationKind = "many_to_many"
)

type RelationDeleteBehavior string

const (
	RelationDeleteRestrict RelationDeleteBehavior = "restrict"
	RelationDeleteSetNull  RelationDeleteBehavior = "set_null"
	RelationDeleteCascade  RelationDeleteBehavior = "cascade"
)

type RelationSettings struct {
	Kind         RelationKind           `json:"kind,omitempty"`
	InverseField string                 `json:"inverse_field,omitempty"`
	OnDelete     RelationDeleteBehavior `json:"on_delete,omitempty"`
}

type CreateRecordRequest struct {
	TenantKey string `json:"tenant_key"`
	Values    Record `json:"values"`
}

type UpdateRecordRequest struct {
	TenantKey string `json:"tenant_key"`
	Values    Record `json:"values"`
}

type DeleteRecordRequest struct {
	TenantKey string `json:"tenant_key"`
}

type QueryRecordsRequest struct {
	TenantKey string `json:"tenant_key"`
	Query     Query  `json:"query"`
}

type ViewType string

const (
	ViewTypeTable    ViewType = "table"
	ViewTypeKanban   ViewType = "kanban"
	ViewTypeCalendar ViewType = "calendar"
)

type ViewVisibility string

const (
	ViewVisibilityPrivate ViewVisibility = "private"
	ViewVisibilityShared  ViewVisibility = "shared"
)

type View struct {
	ID         string         `json:"id"`
	TenantID   string         `json:"tenant_id"`
	ObjectID   string         `json:"object_id"`
	Name       string         `json:"name"`
	Type       ViewType       `json:"type"`
	Columns    []string       `json:"columns"`
	Filter     *Filter        `json:"filter,omitempty"`
	Sort       []Sort         `json:"sort,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	Visibility ViewVisibility `json:"visibility"`
	OwnerID    string         `json:"owner_id,omitempty"`
	Layout     map[string]any `json:"layout,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type CreateViewRequest struct {
	TenantKey  string         `json:"tenant_key"`
	Name       string         `json:"name"`
	Type       ViewType       `json:"type,omitempty"`
	Columns    []string       `json:"columns,omitempty"`
	Filter     *Filter        `json:"filter,omitempty"`
	Sort       []Sort         `json:"sort,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	Visibility ViewVisibility `json:"visibility,omitempty"`
	OwnerID    string         `json:"owner_id,omitempty"`
	Layout     map[string]any `json:"layout,omitempty"`
}

type UpdateViewRequest struct {
	TenantKey  string         `json:"tenant_key"`
	Name       string         `json:"name,omitempty"`
	Type       ViewType       `json:"type,omitempty"`
	Columns    []string       `json:"columns,omitempty"`
	Filter     *Filter        `json:"filter,omitempty"`
	Sort       []Sort         `json:"sort,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	Visibility ViewVisibility `json:"visibility,omitempty"`
	OwnerID    string         `json:"owner_id,omitempty"`
	Layout     map[string]any `json:"layout,omitempty"`
}

type ListViewsRequest struct {
	TenantKey string `json:"tenant_key"`
}

type DeleteViewRequest struct {
	TenantKey string `json:"tenant_key"`
}

type QueryViewRequest struct {
	TenantKey string `json:"tenant_key"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type Record map[string]any

type RecordResponse struct {
	Record Record `json:"record"`
	Event  *Event `json:"event,omitempty"`
}

type DeleteRecordResponse struct {
	ID    string `json:"id"`
	Event *Event `json:"event,omitempty"`
}

type RecordPage struct {
	Records    []Record `json:"records"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

type Query struct {
	Object string   `json:"object,omitempty"`
	Select []string `json:"select,omitempty"`
	Filter *Filter  `json:"filter,omitempty"`
	Sort   []Sort   `json:"sort,omitempty"`
	Limit  int      `json:"limit,omitempty"`
	Cursor string   `json:"cursor,omitempty"`
}

type Filter struct {
	Op      string   `json:"op"`
	Field   string   `json:"field,omitempty"`
	Value   any      `json:"value,omitempty"`
	Values  []any    `json:"values,omitempty"`
	Filters []Filter `json:"filters,omitempty"`
}

type Sort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

type SubscriptionRequest struct {
	QueryID        string   `json:"query_id"`
	TenantKey      string   `json:"tenant_key"`
	Object         string   `json:"object"`
	Filter         *Filter  `json:"filter,omitempty"`
	SelectedFields []string `json:"selected_fields,omitempty"`
	AfterSeq       int64    `json:"after_seq,omitempty"`
}

type Event struct {
	Seq           int64     `json:"seq"`
	EventID       string    `json:"event_id"`
	TenantID      string    `json:"tenant_id"`
	ObjectID      string    `json:"object_id,omitempty"`
	Object        string    `json:"object"`
	RecordID      string    `json:"record_id,omitempty"`
	Action        string    `json:"action"`
	ActorID       string    `json:"actor_id,omitempty"`
	SchemaVersion int64     `json:"schema_version"`
	ChangedFields []string  `json:"changed_fields,omitempty"`
	Before        Record    `json:"before,omitempty"`
	After         Record    `json:"after,omitempty"`
	Diff          Record    `json:"diff,omitempty"`
	QueryIDs      []string  `json:"query_ids,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type metadataState struct {
	Tenant    *Tenant
	Object    *Object
	Fields    map[string]*Field
	Relations map[string]*relationTarget
}

type relationTarget struct {
	Object *Object
	Fields map[string]*Field
}

func cloneRecord(in Record) Record {
	if in == nil {
		return nil
	}
	out := make(Record, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func jsonRaw(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}
