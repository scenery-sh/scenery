package runtimescope

import "testing"

func TestScopeRestoresNestedValuesWithoutChildInheritance(t *testing.T) {
	var scope Scope[*int]
	a, b := 1, 2
	leave := scope.Enter(&a)
	nested := scope.Enter(&b)
	if value, ok := scope.Current(); !ok || value != &b {
		t.Fatal("nested pointer not retained")
	}
	child := make(chan bool, 1)
	go func() { _, ok := scope.Current(); child <- ok }()
	if <-child {
		t.Fatal("child goroutine inherited request authority")
	}
	nested()
	if value, ok := scope.Current(); !ok || value != &a {
		t.Fatal("parent scope not restored")
	}
	leave()
	if _, ok := scope.Current(); ok {
		t.Fatal("scope leaked")
	}
}
