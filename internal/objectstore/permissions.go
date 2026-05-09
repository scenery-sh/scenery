package objectstore

import "context"

type ObjectRef struct {
	TenantID string
	ObjectID string
	Name     string
}

type FieldRef struct {
	TenantID string
	ObjectID string
	FieldID  string
	Name     string
}

type Permissions interface {
	CanReadObject(context.Context, Actor, ObjectRef) error
	CanWriteObject(context.Context, Actor, ObjectRef) error
	CanReadField(context.Context, Actor, FieldRef) error
	CanWriteField(context.Context, Actor, FieldRef) error
	RowFilter(context.Context, Actor, ObjectRef) (*Filter, error)
}

type AllowAllPermissions struct{}

func (AllowAllPermissions) CanReadObject(context.Context, Actor, ObjectRef) error {
	return nil
}

func (AllowAllPermissions) CanWriteObject(context.Context, Actor, ObjectRef) error {
	return nil
}

func (AllowAllPermissions) CanReadField(context.Context, Actor, FieldRef) error {
	return nil
}

func (AllowAllPermissions) CanWriteField(context.Context, Actor, FieldRef) error {
	return nil
}

func (AllowAllPermissions) RowFilter(context.Context, Actor, ObjectRef) (*Filter, error) {
	return nil, nil
}

func objectRef(state *metadataState) ObjectRef {
	return ObjectRef{
		TenantID: state.Tenant.ID,
		ObjectID: state.Object.ID,
		Name:     state.Object.NameSingular,
	}
}

func fieldRef(state *metadataState, field *Field) FieldRef {
	return FieldRef{
		TenantID: state.Tenant.ID,
		ObjectID: state.Object.ID,
		FieldID:  field.ID,
		Name:     field.Name,
	}
}
