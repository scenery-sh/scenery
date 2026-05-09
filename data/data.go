// Package data exposes onlava's native dynamic data platform for app code.
package data

import (
	"context"
	"net/http"

	onlavaauth "github.com/pbrazdil/onlava/auth"
	"github.com/pbrazdil/onlava/internal/objectstore"
)

type (
	DB                     = objectstore.DB
	Options                = objectstore.Options
	Tenant                 = objectstore.Tenant
	Object                 = objectstore.Object
	Field                  = objectstore.Field
	FieldType              = objectstore.FieldType
	PhysicalColumn         = objectstore.PhysicalColumn
	FieldOption            = objectstore.FieldOption
	FieldOptionRequest     = objectstore.FieldOptionRequest
	IndexMethod            = objectstore.IndexMethod
	Index                  = objectstore.Index
	IndexField             = objectstore.IndexField
	CreateIndexRequest     = objectstore.CreateIndexRequest
	ListIndexesRequest     = objectstore.ListIndexesRequest
	Actor                  = objectstore.Actor
	CreateObjectRequest    = objectstore.CreateObjectRequest
	CreateFieldRequest     = objectstore.CreateFieldRequest
	RelationKind           = objectstore.RelationKind
	RelationDeleteBehavior = objectstore.RelationDeleteBehavior
	RelationSettings       = objectstore.RelationSettings
	CreateRecordRequest    = objectstore.CreateRecordRequest
	UpdateRecordRequest    = objectstore.UpdateRecordRequest
	DeleteRecordRequest    = objectstore.DeleteRecordRequest
	QueryRecordsRequest    = objectstore.QueryRecordsRequest
	ViewType               = objectstore.ViewType
	ViewVisibility         = objectstore.ViewVisibility
	View                   = objectstore.View
	CreateViewRequest      = objectstore.CreateViewRequest
	UpdateViewRequest      = objectstore.UpdateViewRequest
	ListViewsRequest       = objectstore.ListViewsRequest
	DeleteViewRequest      = objectstore.DeleteViewRequest
	QueryViewRequest       = objectstore.QueryViewRequest
	Record                 = objectstore.Record
	RecordResponse         = objectstore.RecordResponse
	DeleteRecordResponse   = objectstore.DeleteRecordResponse
	RecordPage             = objectstore.RecordPage
	Query                  = objectstore.Query
	Filter                 = objectstore.Filter
	Sort                   = objectstore.Sort
	SubscriptionRequest    = objectstore.SubscriptionRequest
	Event                  = objectstore.Event
	Permissions            = objectstore.Permissions
	AllowAllPermissions    = objectstore.AllowAllPermissions
	ObjectRef              = objectstore.ObjectRef
	FieldRef               = objectstore.FieldRef
)

type Store struct {
	inner *objectstore.Store
}

const (
	FieldText              = objectstore.FieldText
	FieldRichText          = objectstore.FieldRichText
	FieldNumber            = objectstore.FieldNumber
	FieldNumeric           = objectstore.FieldNumeric
	FieldCurrency          = objectstore.FieldCurrency
	FieldBoolean           = objectstore.FieldBoolean
	FieldDate              = objectstore.FieldDate
	FieldDatetime          = objectstore.FieldDatetime
	FieldUUID              = objectstore.FieldUUID
	FieldSelect            = objectstore.FieldSelect
	FieldMultiSelect       = objectstore.FieldMultiSelect
	FieldRating            = objectstore.FieldRating
	FieldJSON              = objectstore.FieldJSON
	FieldRawJSON           = objectstore.FieldRawJSON
	FieldFiles             = objectstore.FieldFiles
	FieldFullName          = objectstore.FieldFullName
	FieldAddress           = objectstore.FieldAddress
	FieldEmails            = objectstore.FieldEmails
	FieldPhones            = objectstore.FieldPhones
	FieldRelation          = objectstore.FieldRelation
	IndexMethodBTree       = objectstore.IndexMethodBTree
	IndexMethodGIN         = objectstore.IndexMethodGIN
	RelationManyToOne      = objectstore.RelationManyToOne
	RelationManyToMany     = objectstore.RelationManyToMany
	RelationDeleteRestrict = objectstore.RelationDeleteRestrict
	RelationDeleteSetNull  = objectstore.RelationDeleteSetNull
	RelationDeleteCascade  = objectstore.RelationDeleteCascade
	ViewTypeTable          = objectstore.ViewTypeTable
	ViewTypeKanban         = objectstore.ViewTypeKanban
	ViewTypeCalendar       = objectstore.ViewTypeCalendar
	ViewVisibilityPrivate  = objectstore.ViewVisibilityPrivate
	ViewVisibilityShared   = objectstore.ViewVisibilityShared
)

func Open(ctx context.Context, db DB, opts Options) (*Store, error) {
	inner, err := objectstore.Open(ctx, db, opts)
	if err != nil {
		return nil, wrapError("Open", err)
	}
	return &Store{inner: inner}, nil
}

func (s *Store) CreateObject(ctx context.Context, actor Actor, req CreateObjectRequest) (*Object, error) {
	out, err := s.inner.CreateObject(ctx, actor, req)
	return out, wrapError("CreateObject", err)
}

func (s *Store) CreateField(ctx context.Context, actor Actor, object string, req CreateFieldRequest) (*Field, error) {
	out, err := s.inner.CreateField(ctx, actor, object, req)
	return out, wrapError("CreateField", err)
}

func (s *Store) EnableOutboxTriggers(ctx context.Context, actor Actor, tenantKey string, object string) (*Object, error) {
	out, err := s.inner.EnableOutboxTriggers(ctx, actor, tenantKey, object)
	return out, wrapError("EnableOutboxTriggers", err)
}

func (s *Store) CreateIndex(ctx context.Context, actor Actor, object string, req CreateIndexRequest) (*Index, error) {
	out, err := s.inner.CreateIndex(ctx, actor, object, req)
	return out, wrapError("CreateIndex", err)
}

func (s *Store) ListIndexes(ctx context.Context, actor Actor, object string, req ListIndexesRequest) ([]Index, error) {
	out, err := s.inner.ListIndexes(ctx, actor, object, req)
	return out, wrapError("ListIndexes", err)
}

func (s *Store) CreateRecord(ctx context.Context, actor Actor, object string, req CreateRecordRequest) (*RecordResponse, error) {
	out, err := s.inner.CreateRecord(ctx, actor, object, req)
	return out, wrapError("CreateRecord", err)
}

func (s *Store) UpdateRecord(ctx context.Context, actor Actor, object string, id string, req UpdateRecordRequest) (*RecordResponse, error) {
	out, err := s.inner.UpdateRecord(ctx, actor, object, id, req)
	return out, wrapError("UpdateRecord", err)
}

func (s *Store) DeleteRecord(ctx context.Context, actor Actor, object string, id string, req DeleteRecordRequest) (*DeleteRecordResponse, error) {
	out, err := s.inner.DeleteRecord(ctx, actor, object, id, req)
	return out, wrapError("DeleteRecord", err)
}

func (s *Store) QueryRecords(ctx context.Context, actor Actor, object string, req QueryRecordsRequest) (*RecordPage, error) {
	out, err := s.inner.QueryRecords(ctx, actor, object, req)
	return out, wrapError("QueryRecords", err)
}

func (s *Store) CreateView(ctx context.Context, actor Actor, object string, req CreateViewRequest) (*View, error) {
	out, err := s.inner.CreateView(ctx, actor, object, req)
	return out, wrapError("CreateView", err)
}

func (s *Store) UpdateView(ctx context.Context, actor Actor, object string, view string, req UpdateViewRequest) (*View, error) {
	out, err := s.inner.UpdateView(ctx, actor, object, view, req)
	return out, wrapError("UpdateView", err)
}

func (s *Store) ListViews(ctx context.Context, actor Actor, object string, req ListViewsRequest) ([]View, error) {
	out, err := s.inner.ListViews(ctx, actor, object, req)
	return out, wrapError("ListViews", err)
}

func (s *Store) DeleteView(ctx context.Context, actor Actor, object string, view string, req DeleteViewRequest) error {
	return wrapError("DeleteView", s.inner.DeleteView(ctx, actor, object, view, req))
}

func (s *Store) QueryView(ctx context.Context, actor Actor, object string, view string, req QueryViewRequest) (*RecordPage, error) {
	out, err := s.inner.QueryView(ctx, actor, object, view, req)
	return out, wrapError("QueryView", err)
}

func (s *Store) ServeEvents(ctx context.Context, actor Actor, w http.ResponseWriter, req *http.Request) error {
	return wrapError("ServeEvents", s.inner.ServeEvents(ctx, actor, w, req))
}

func ActorFromContext(context.Context) Actor {
	var actor Actor
	if uid, ok := onlavaauth.UserID(); ok {
		actor.ID = string(uid)
	}
	if data := onlavaauth.Data(); data != nil {
		actor.Data = data
	}
	return actor
}

func ServeEvents(ctx context.Context, store *Store, actor Actor, w http.ResponseWriter, req *http.Request) error {
	return store.ServeEvents(ctx, actor, w, req)
}

func EQ(field string, value any) *Filter {
	return &Filter{Op: "eq", Field: field, Value: value}
}

func NEQ(field string, value any) *Filter {
	return &Filter{Op: "neq", Field: field, Value: value}
}

func GT(field string, value any) *Filter {
	return &Filter{Op: "gt", Field: field, Value: value}
}

func GTE(field string, value any) *Filter {
	return &Filter{Op: "gte", Field: field, Value: value}
}

func LT(field string, value any) *Filter {
	return &Filter{Op: "lt", Field: field, Value: value}
}

func LTE(field string, value any) *Filter {
	return &Filter{Op: "lte", Field: field, Value: value}
}

func Contains(field string, value any) *Filter {
	return &Filter{Op: "contains", Field: field, Value: value}
}

func Search(value string) *Filter {
	return &Filter{Op: "search", Value: value}
}

func In(field string, values ...any) *Filter {
	return &Filter{Op: "in", Field: field, Values: values}
}

func IsNull(field string) *Filter {
	return &Filter{Op: "is_null", Field: field, Value: true}
}

func NotNull(field string) *Filter {
	return &Filter{Op: "is_null", Field: field, Value: false}
}

func And(filters ...*Filter) *Filter {
	return logicalFilter("and", filters...)
}

func Or(filters ...*Filter) *Filter {
	return logicalFilter("or", filters...)
}

func Not(filter *Filter) *Filter {
	if filter == nil {
		return nil
	}
	return &Filter{Op: "not", Filters: []Filter{*filter}}
}

func Asc(field string) Sort {
	return Sort{Field: field}
}

func Desc(field string) Sort {
	return Sort{Field: field, Desc: true}
}

func logicalFilter(op string, filters ...*Filter) *Filter {
	items := make([]Filter, 0, len(filters))
	for _, filter := range filters {
		if filter != nil {
			items = append(items, *filter)
		}
	}
	switch len(items) {
	case 0:
		return nil
	case 1:
		return &items[0]
	default:
		return &Filter{Op: op, Filters: items}
	}
}
