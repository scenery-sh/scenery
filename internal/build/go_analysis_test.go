package build

import (
	"reflect"
	"testing"

	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/model"
)

func TestMatchingGoAnalysisRequiresEveryTargetField(t *testing.T) {
	app := &model.App{Root: "/app", Name: "example"}
	analyses := []verifiedGoAnalysis{{target: gotarget.Context{}, app: app}}
	if got := matchingGoAnalysis(analyses, app.Root, app.Name, gotarget.Context{}); got != app {
		t.Fatal("exact analysis was not reused")
	}
	if matchingGoAnalysis(analyses, "/other", app.Name, gotarget.Context{}) != nil {
		t.Fatal("analysis crossed app identity")
	}
	// Enumerate the actual struct so a new target input cannot accidentally be
	// omitted from this regression or a hand-maintained comparison key.
	for i := range reflect.TypeFor[gotarget.Context]().NumField() {
		changed := gotarget.Context{}
		value := reflect.ValueOf(&changed).Elem()
		field := value.Field(i)
		switch field.Kind() {
		case reflect.String:
			field.SetString("changed")
		case reflect.Bool:
			field.SetBool(true)
		case reflect.Slice:
			field.Set(reflect.MakeSlice(field.Type(), 1, 1))
		case reflect.Map:
			field.Set(reflect.MakeMap(field.Type()))
		default:
			t.Fatalf("add target field mutation for %s", value.Type().Field(i).Name)
		}
		if got := matchingGoAnalysis(analyses, app.Root, app.Name, changed); got != nil {
			t.Errorf("analysis reused despite changed %s", value.Type().Field(i).Name)
		}
	}
}

func TestMatchingGoAnalysisAdaptsRuntimeNameWithoutMutatingVerification(t *testing.T) {
	checked := &model.App{
		Root: "/app", Name: "clean_tech", ModulePath: "example.test/app",
		Packages: []*model.Package{{ImportPath: "example.test/app/feature"}},
	}
	analyses := []verifiedGoAnalysis{{target: gotarget.Context{}, app: checked}}
	built := matchingGoAnalysis(analyses, checked.Root, "clean-tech", gotarget.Context{})
	if built == nil || built == checked || built.Name != "clean-tech" {
		t.Fatalf("runtime analysis = %#v; want independently named metadata", built)
	}
	if checked.Name != "clean_tech" || built.Root != checked.Root || built.ModulePath != checked.ModulePath || built.Packages[0] != checked.Packages[0] {
		t.Fatal("name adaptation changed checked metadata or package analysis")
	}
}
