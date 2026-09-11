package generate

import (
	"path/filepath"
	"reflect"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/gotarget"
)

func TestPreparedGoTargetsPreserveEveryContextField(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	selected := compiler.GoBuildTarget{Role: "development", Address: "app/go_target/dev", Context: gotarget.Context{ModuleRoot: root, Patterns: []string{"./..."}}}
	contract := selected
	contract.Role = "contract"
	base := []compiler.GoBuildTarget{contract}
	prepared, err := preparedGoVerificationTargets(root, workspace, base, selected)
	if err != nil || len(prepared) != 1 || prepared[0].Role != "development" || prepared[0].Context.ModuleRoot != workspace {
		t.Fatalf("selected target was not fully checked in the workspace: %#v %v", prepared, err)
	}
	if base[0].Role != "contract" || base[0].Context.ModuleRoot != root {
		t.Fatal("verification mutated compiler target ownership")
	}
	for index := range reflect.TypeFor[gotarget.Context]().NumField() {
		changed := selected
		value := reflect.ValueOf(&changed.Context).Elem()
		field := value.Field(index)
		if value.Type().Field(index).Name == "ModuleRoot" {
			field.SetString(filepath.Join(root, "nested"))
		} else {
			switch field.Kind() {
			case reflect.String:
				field.SetString("different")
			case reflect.Bool:
				field.SetBool(true)
			case reflect.Slice:
				field.Set(reflect.MakeSlice(field.Type(), 2, 2))
			case reflect.Map:
				field.Set(reflect.MakeMap(field.Type()))
			default:
				t.Fatalf("add mutation for target field %s", value.Type().Field(index).Name)
			}
		}
		prepared, err := preparedGoVerificationTargets(root, workspace, base, changed)
		if err != nil || len(prepared) != 2 {
			t.Fatalf("verification merged distinct %s: %#v %v", value.Type().Field(index).Name, prepared, err)
		}
	}
	selected.Context.ModuleRoot = filepath.Dir(root)
	if _, err := preparedGoVerificationTargets(root, workspace, nil, selected); err == nil {
		t.Fatal("verification escaped the owned workspace")
	}
}
