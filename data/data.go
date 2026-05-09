// Package data exposes onlava's native dynamic data platform for app code.
package data

import (
	"context"
	"net/http"

	onlavaauth "github.com/pbrazdil/onlava/auth"
	"github.com/pbrazdil/onlava/internal/objectstore"
)

type (
	DB                   = objectstore.DB
	Options              = objectstore.Options
	Tenant               = objectstore.Tenant
	Object               = objectstore.Object
	Field                = objectstore.Field
	FieldType            = objectstore.FieldType
	PhysicalColumn       = objectstore.PhysicalColumn
	FieldOption          = objectstore.FieldOption
	FieldOptionRequest   = objectstore.FieldOptionRequest
	IndexMethod          = objectstore.IndexMethod
	Index                = objectstore.Index
	IndexField           = objectstore.IndexField
	CreateIndexRequest   = objectstore.CreateIndexRequest
	ListIndexesRequest   = objectstore.ListIndexesRequest
	Actor                = objectstore.Actor
	CreateObjectRequest  = objectstore.CreateObjectRequest
	CreateFieldRequest   = objectstore.CreateFieldRequest
	CreateRecordRequest  = objectstore.CreateRecordRequest
	UpdateRecordRequest  = objectstore.UpdateRecordRequest
	DeleteRecordRequest  = objectstore.DeleteRecordRequest
	QueryRecordsRequest  = objectstore.QueryRecordsRequest
	Record               = objectstore.Record
	RecordResponse       = objectstore.RecordResponse
	DeleteRecordResponse = objectstore.DeleteRecordResponse
	RecordPage           = objectstore.RecordPage
	Query                = objectstore.Query
	Filter               = objectstore.Filter
	Sort                 = objectstore.Sort
	SubscriptionRequest  = objectstore.SubscriptionRequest
	Event                = objectstore.Event
	Permissions          = objectstore.Permissions
	AllowAllPermissions  = objectstore.AllowAllPermissions
	ObjectRef            = objectstore.ObjectRef
	FieldRef             = objectstore.FieldRef
)

type Store struct {
	inner *objectstore.Store
}

const (
	FieldText        = objectstore.FieldText
	FieldRichText    = objectstore.FieldRichText
	FieldNumber      = objectstore.FieldNumber
	FieldNumeric     = objectstore.FieldNumeric
	FieldCurrency    = objectstore.FieldCurrency
	FieldBoolean     = objectstore.FieldBoolean
	FieldDate        = objectstore.FieldDate
	FieldDatetime    = objectstore.FieldDatetime
	FieldUUID        = objectstore.FieldUUID
	FieldSelect      = objectstore.FieldSelect
	FieldMultiSelect = objectstore.FieldMultiSelect
	FieldRating      = objectstore.FieldRating
	FieldJSON        = objectstore.FieldJSON
	FieldRawJSON     = objectstore.FieldRawJSON
	FieldFiles       = objectstore.FieldFiles
	FieldFullName    = objectstore.FieldFullName
	FieldAddress     = objectstore.FieldAddress
	FieldEmails      = objectstore.FieldEmails
	FieldPhones      = objectstore.FieldPhones
	FieldRelation    = objectstore.FieldRelation
	IndexMethodBTree = objectstore.IndexMethodBTree
	IndexMethodGIN   = objectstore.IndexMethodGIN
)

func Open(ctx context.Context, db DB, opts Options) (*Store, error) {
	inner, err := objectstore.Open(ctx, db, opts)
	if err != nil {
		return nil, err
	}
	return &Store{inner: inner}, nil
}

func (s *Store) CreateObject(ctx context.Context, actor Actor, req CreateObjectRequest) (*Object, error) {
	return s.inner.CreateObject(ctx, actor, req)
}

func (s *Store) CreateField(ctx context.Context, actor Actor, object string, req CreateFieldRequest) (*Field, error) {
	return s.inner.CreateField(ctx, actor, object, req)
}

func (s *Store) EnableOutboxTriggers(ctx context.Context, actor Actor, tenantKey string, object string) (*Object, error) {
	return s.inner.EnableOutboxTriggers(ctx, actor, tenantKey, object)
}

func (s *Store) CreateIndex(ctx context.Context, actor Actor, object string, req CreateIndexRequest) (*Index, error) {
	return s.inner.CreateIndex(ctx, actor, object, req)
}

func (s *Store) ListIndexes(ctx context.Context, actor Actor, object string, req ListIndexesRequest) ([]Index, error) {
	return s.inner.ListIndexes(ctx, actor, object, req)
}

func (s *Store) CreateRecord(ctx context.Context, actor Actor, object string, req CreateRecordRequest) (*RecordResponse, error) {
	return s.inner.CreateRecord(ctx, actor, object, req)
}

func (s *Store) UpdateRecord(ctx context.Context, actor Actor, object string, id string, req UpdateRecordRequest) (*RecordResponse, error) {
	return s.inner.UpdateRecord(ctx, actor, object, id, req)
}

func (s *Store) DeleteRecord(ctx context.Context, actor Actor, object string, id string, req DeleteRecordRequest) (*DeleteRecordResponse, error) {
	return s.inner.DeleteRecord(ctx, actor, object, id, req)
}

func (s *Store) QueryRecords(ctx context.Context, actor Actor, object string, req QueryRecordsRequest) (*RecordPage, error) {
	return s.inner.QueryRecords(ctx, actor, object, req)
}

func (s *Store) ServeEvents(ctx context.Context, actor Actor, w http.ResponseWriter, req *http.Request) error {
	return s.inner.ServeEvents(ctx, actor, w, req)
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
