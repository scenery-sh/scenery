package page

type Collection[T any] struct {
	Route   string
	Title   string
	Columns []string
	Slots   []ComponentRef
}

type ComponentRef struct {
	Name string
}

func Component(name string) ComponentRef {
	return ComponentRef{Name: name}
}
