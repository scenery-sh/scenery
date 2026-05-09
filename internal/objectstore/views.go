package objectstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateView(ctx context.Context, actor Actor, objectName string, req CreateViewRequest) (*View, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanWriteObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	view, err := s.buildView(ctx, state, req)
	if err != nil {
		return nil, err
	}
	now := s.now()
	view.ID, err = newUUID()
	if err != nil {
		return nil, err
	}
	view.TenantID = state.Tenant.ID
	view.ObjectID = state.Object.ID
	view.CreatedAt = now
	view.UpdatedAt = now
	filterData, sortData, layoutData, err := viewJSON(view)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		insert into `+qualifiedIdent(MetadataSchema, "views")+` (
			id, tenant_id, object_id, name, type, filter, sort, limit_count,
			visibility, owner_id, layout, created_at, updated_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
	`, view.ID, view.TenantID, view.ObjectID, view.Name, string(view.Type), filterData, sortData, view.Limit, string(view.Visibility), view.OwnerID, layoutData, now); err != nil {
		return nil, fmt.Errorf("create view %s on object %s: %w", view.Name, state.Object.NameSingular, err)
	}
	if err := insertViewFields(ctx, tx, view, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return view, nil
}

func (s *Store) UpdateView(ctx context.Context, actor Actor, objectName string, viewName string, req UpdateViewRequest) (*View, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanWriteObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	existing, err := s.loadView(ctx, state.Tenant.ID, state.Object.ID, viewName)
	if err != nil {
		return nil, err
	}
	createReq := CreateViewRequest{
		TenantKey:  req.TenantKey,
		Name:       firstNonEmpty(req.Name, existing.Name),
		Type:       req.Type,
		Columns:    req.Columns,
		Filter:     req.Filter,
		Sort:       req.Sort,
		Limit:      req.Limit,
		Visibility: req.Visibility,
		OwnerID:    req.OwnerID,
		Layout:     req.Layout,
	}
	if createReq.Type == "" {
		createReq.Type = existing.Type
	}
	if createReq.Columns == nil {
		createReq.Columns = existing.Columns
	}
	if createReq.Filter == nil {
		createReq.Filter = existing.Filter
	}
	if createReq.Sort == nil {
		createReq.Sort = existing.Sort
	}
	if createReq.Limit == 0 {
		createReq.Limit = existing.Limit
	}
	if createReq.Visibility == "" {
		createReq.Visibility = existing.Visibility
	}
	if createReq.OwnerID == "" {
		createReq.OwnerID = existing.OwnerID
	}
	if createReq.Layout == nil {
		createReq.Layout = existing.Layout
	}
	view, err := s.buildView(ctx, state, createReq)
	if err != nil {
		return nil, err
	}
	view.ID = existing.ID
	view.TenantID = existing.TenantID
	view.ObjectID = existing.ObjectID
	view.CreatedAt = existing.CreatedAt
	view.UpdatedAt = s.now()
	filterData, sortData, layoutData, err := viewJSON(view)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		update `+qualifiedIdent(MetadataSchema, "views")+`
		set name = $1, type = $2, filter = $3, sort = $4, limit_count = $5,
		    visibility = $6, owner_id = $7, layout = $8, updated_at = $9
		where tenant_id = $10 and object_id = $11 and id = $12
	`, view.Name, string(view.Type), filterData, sortData, view.Limit, string(view.Visibility), view.OwnerID, layoutData, view.UpdatedAt, view.TenantID, view.ObjectID, view.ID); err != nil {
		return nil, fmt.Errorf("update view %s on object %s: %w", viewName, state.Object.NameSingular, err)
	}
	if _, err := tx.Exec(ctx, `delete from `+qualifiedIdent(MetadataSchema, "view_fields")+` where tenant_id = $1 and view_id = $2`, view.TenantID, view.ID); err != nil {
		return nil, err
	}
	if err := insertViewFields(ctx, tx, view, view.UpdatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return view, nil
}

func (s *Store) ListViews(ctx context.Context, actor Actor, objectName string, req ListViewsRequest) ([]View, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanReadObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	return s.loadViews(ctx, state.Tenant.ID, state.Object.ID)
}

func (s *Store) DeleteView(ctx context.Context, actor Actor, objectName string, viewName string, req DeleteViewRequest) error {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return err
	}
	if err := s.perms.CanWriteObject(ctx, actor, objectRef(state)); err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `
		delete from `+qualifiedIdent(MetadataSchema, "views")+`
		where tenant_id = $1 and object_id = $2 and name = $3
	`, state.Tenant.ID, state.Object.ID, viewName)
	if err != nil {
		return fmt.Errorf("delete view %s on object %s: %w", viewName, state.Object.NameSingular, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("view %s does not exist on object %s", viewName, state.Object.NameSingular)
	}
	return nil
}

func (s *Store) QueryView(ctx context.Context, actor Actor, objectName string, viewName string, req QueryViewRequest) (*RecordPage, error) {
	state, err := s.loadState(ctx, req.TenantKey, objectName)
	if err != nil {
		return nil, err
	}
	view, err := s.loadView(ctx, state.Tenant.ID, state.Object.ID, viewName)
	if err != nil {
		return nil, err
	}
	query := Query{
		Object: objectName,
		Select: view.Columns,
		Filter: view.Filter,
		Sort:   view.Sort,
		Limit:  view.Limit,
		Cursor: req.Cursor,
	}
	if req.Limit > 0 {
		query.Limit = req.Limit
	}
	return s.QueryRecords(ctx, actor, objectName, QueryRecordsRequest{TenantKey: req.TenantKey, Query: query})
}

func (s *Store) buildView(ctx context.Context, state *metadataState, req CreateViewRequest) (*View, error) {
	if err := validateName("view", req.Name); err != nil {
		return nil, err
	}
	viewType := req.Type
	if viewType == "" {
		viewType = ViewTypeTable
	}
	switch viewType {
	case ViewTypeTable, ViewTypeKanban, ViewTypeCalendar:
	default:
		return nil, fmt.Errorf("view type %q is not supported", viewType)
	}
	visibility := req.Visibility
	if visibility == "" {
		visibility = ViewVisibilityPrivate
	}
	switch visibility {
	case ViewVisibilityPrivate, ViewVisibilityShared:
	default:
		return nil, fmt.Errorf("view visibility %q is not supported", visibility)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	columns := req.Columns
	if len(columns) == 0 {
		columns, _ = selectedFields(state, nil)
	}
	if _, err := compileQuery(state, Query{Select: columns, Filter: req.Filter, Sort: req.Sort, Limit: limit}); err != nil {
		return nil, fmt.Errorf("validate view %s query: %w", req.Name, err)
	}
	layout := copySettings(req.Layout)
	return &View{
		Name:       req.Name,
		Type:       viewType,
		Columns:    columns,
		Filter:     req.Filter,
		Sort:       req.Sort,
		Limit:      limit,
		Visibility: visibility,
		OwnerID:    strings.TrimSpace(req.OwnerID),
		Layout:     layout,
	}, nil
}

func (s *Store) loadView(ctx context.Context, tenantID, objectID, name string) (*View, error) {
	var view View
	var viewType, visibility string
	var filterData, sortData, layoutData []byte
	err := s.db.QueryRow(ctx, `
		select id::text, tenant_id::text, object_id::text, name, type, filter, sort,
		       limit_count, visibility, owner_id, layout, created_at, updated_at
		from `+qualifiedIdent(MetadataSchema, "views")+`
		where tenant_id = $1 and object_id = $2 and name = $3
	`, tenantID, objectID, name).Scan(
		&view.ID, &view.TenantID, &view.ObjectID, &view.Name, &viewType, &filterData, &sortData,
		&view.Limit, &visibility, &view.OwnerID, &layoutData, &view.CreatedAt, &view.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("view %s does not exist", name)
	}
	if err != nil {
		return nil, err
	}
	view.Type = ViewType(viewType)
	view.Visibility = ViewVisibility(visibility)
	if len(filterData) > 0 && string(filterData) != "null" {
		var filter Filter
		if err := json.Unmarshal(filterData, &filter); err != nil {
			return nil, fmt.Errorf("decode view %s filter: %w", name, err)
		}
		view.Filter = &filter
	}
	if len(sortData) > 0 {
		_ = json.Unmarshal(sortData, &view.Sort)
	}
	if len(layoutData) > 0 {
		_ = json.Unmarshal(layoutData, &view.Layout)
	}
	fields, err := s.loadViewFields(ctx, tenantID, view.ID)
	if err != nil {
		return nil, err
	}
	view.Columns = fields
	return &view, nil
}

func (s *Store) loadViews(ctx context.Context, tenantID, objectID string) ([]View, error) {
	rows, err := s.db.Query(ctx, `
		select name from `+qualifiedIdent(MetadataSchema, "views")+`
		where tenant_id = $1 and object_id = $2
		order by name
	`, tenantID, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	views := make([]View, 0, len(names))
	for _, name := range names {
		view, err := s.loadView(ctx, tenantID, objectID, name)
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

func (s *Store) loadViewFields(ctx context.Context, tenantID, viewID string) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		select field_name
		from `+qualifiedIdent(MetadataSchema, "view_fields")+`
		where tenant_id = $1 and view_id = $2
		order by position
	`, tenantID, viewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fields []string
	for rows.Next() {
		var field string
		if err := rows.Scan(&field); err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, rows.Err()
}

func insertViewFields(ctx context.Context, tx pgxTx, view *View, now any) error {
	for pos, field := range view.Columns {
		id, err := newUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			insert into `+qualifiedIdent(MetadataSchema, "view_fields")+` (
				id, tenant_id, view_id, field_name, position, created_at, updated_at
			) values ($1, $2, $3, $4, $5, $6, $6)
		`, id, view.TenantID, view.ID, field, pos, now); err != nil {
			return fmt.Errorf("insert view field %s[%d]: %w", view.Name, pos, err)
		}
	}
	return nil
}

func viewJSON(view *View) (filterData, sortData, layoutData string, err error) {
	filter, err := json.Marshal(view.Filter)
	if err != nil {
		return "", "", "", err
	}
	sortDataBytes, err := json.Marshal(view.Sort)
	if err != nil {
		return "", "", "", err
	}
	layoutDataBytes, err := json.Marshal(view.Layout)
	if err != nil {
		return "", "", "", err
	}
	return string(filter), string(sortDataBytes), string(layoutDataBytes), nil
}
