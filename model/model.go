package model

type EntityDefinition[T any] struct{}

type Action string

const (
	ActionList   Action = "list"
	ActionGet    Action = "get"
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

type EntityOption interface{ entityOption() }

type FieldOption interface{ fieldOption() }

func Entity[T any](opts ...EntityOption) EntityDefinition[T] {
	return EntityDefinition[T]{}
}

func Table(name string) EntityOption {
	return entityOptionFunc(func() {})
}

func Field(name string, opts ...FieldOption) EntityOption {
	return entityOptionFunc(func() {})
}

func Generate(actions ...Action) EntityOption {
	return entityOptionFunc(func() {})
}

func Disable(actions ...Action) EntityOption {
	return entityOptionFunc(func() {})
}

func Override(action Action, endpoint any) EntityOption {
	return entityOptionFunc(func() {})
}

func EnumValues(values ...string) FieldOption {
	return fieldOptionFunc(func() {})
}

func Filterable() FieldOption {
	return fieldOptionFunc(func() {})
}

func Computed() FieldOption {
	return fieldOptionFunc(func() {})
}

func Relationship() FieldOption {
	return fieldOptionFunc(func() {})
}

func RenamedFrom(name string) FieldOption {
	return fieldOptionFunc(func() {})
}

type entityOptionFunc func()

func (entityOptionFunc) entityOption() {}

type fieldOptionFunc func()

func (fieldOptionFunc) fieldOption() {}
