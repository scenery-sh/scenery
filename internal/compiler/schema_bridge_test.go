package compiler

import "testing"

func TestCompilerResourceSchemaBridgeReusesDetachedCatalog(t *testing.T) {
	first, ok := authoredResourceSourceSchema("binding")
	if !ok {
		t.Fatal("binding source schema is unavailable")
	}
	second, _ := authoredResourceSourceSchema("binding")
	if first != second {
		t.Fatal("compiler bridge cloned the resource schema per lookup")
	}
}
