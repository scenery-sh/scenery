package page

type Collection[T any] struct {
	Route          string
	Title          string
	Columns        []string
	ColumnDisplays []ColumnDisplayRef
	Filters        []FilterRef
	Sorts          []SortRef
	Slots          []ComponentRef
}

type ComponentRef struct {
	Name string
}

type ColumnDisplayKind string

const (
	DisplayText     ColumnDisplayKind = "text"
	DisplayDateTime ColumnDisplayKind = "datetime"
	DisplayBadge    ColumnDisplayKind = "badge"
)

type ColumnDisplayRef struct {
	Field string
	Kind  ColumnDisplayKind
}

type FilterOp string

const (
	Equal     FilterOp = "eq"
	NotEqual  FilterOp = "neq"
	IsNull    FilterOp = "is_null"
	IsNotNull FilterOp = "is_not_null"
)

type FilterRef struct {
	Field string
	Op    FilterOp
	Value string
}

type SortDirection string

const (
	Asc  SortDirection = "asc"
	Desc SortDirection = "desc"
)

type SortRef struct {
	Field     string
	Direction SortDirection
}

func Component(name string) ComponentRef {
	return ComponentRef{Name: name}
}

func Column(field string, kind ColumnDisplayKind) ColumnDisplayRef {
	return ColumnDisplayRef{Field: field, Kind: kind}
}

func Filter(field string, op FilterOp, value ...string) FilterRef {
	ref := FilterRef{Field: field, Op: op}
	if len(value) > 0 {
		ref.Value = value[0]
	}
	return ref
}

func Sort(field string, direction SortDirection) SortRef {
	return SortRef{Field: field, Direction: direction}
}
