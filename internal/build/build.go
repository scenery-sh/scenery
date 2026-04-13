package build

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"pulse.dev/internal/app"
	"pulse.dev/internal/codegen"
	"pulse.dev/internal/parse"
)

type Result struct {
	Dir    string
	Binary string
}

func App(appRoot, name string) (*Result, error) {
	model, err := parse.App(appRoot, name)
	if err != nil {
		return nil, err
	}
	gen, err := codegen.Generate(model)
	if err != nil {
		return nil, err
	}

	tempDir, err := os.MkdirTemp("", "pulse-build-*")
	if err != nil {
		return nil, err
	}
	if err := copyTree(appRoot, tempDir); err != nil {
		return nil, err
	}
	for rel, data := range gen.Rewritten {
		if filepath.Ext(rel) == ".go" {
			data, err = rewriteEncoreCompat(filepath.Join(appRoot, rel), data)
			if err != nil {
				return nil, err
			}
		}
		if err := writeFile(tempDir, rel, data); err != nil {
			return nil, err
		}
	}
	for rel, data := range gen.Generated {
		if err := writeFile(tempDir, rel, data); err != nil {
			return nil, err
		}
	}
	if err := patchGoMod(filepath.Join(tempDir, "go.mod"), app.RepoRoot()); err != nil {
		return nil, err
	}
	if err := runGo(tempDir, "mod", "tidy"); err != nil {
		return nil, err
	}

	binary := filepath.Join(tempDir, "pulse-app")
	if err := runGo(tempDir, "build", "-o", binary, "./pulse_internal_main"); err != nil {
		return nil, err
	}
	return &Result{Dir: tempDir, Binary: binary}, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && shouldSkipDir(rel) {
			return filepath.SkipDir
		}
		if !d.IsDir() && shouldSkipFile(rel) {
			return nil
		}
		if shouldSkipSymlink(path, d) {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func shouldSkipDir(rel string) bool {
	base := filepath.Base(rel)
	if strings.HasPrefix(base, ".") {
		return true
	}
	return base == "node_modules" || base == "pulse_internal_main"
}

func shouldSkipFile(rel string) bool {
	return filepath.Base(rel) == "encore.gen.go"
}

func shouldSkipSymlink(path string, d os.DirEntry) bool {
	if d.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return err == nil && info.IsDir()
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if filepath.Ext(src) == ".go" {
		data, err = rewriteEncoreCompat(src, data)
		if err != nil {
			return err
		}
	}
	return os.WriteFile(dst, data, 0o644)
}

func writeFile(root, rel string, data []byte) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func patchGoMod(path, repoRoot string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	file, err := modfile.Parse(path, data, nil)
	if err != nil {
		return err
	}
	if err := file.AddRequire("pulse.dev", "v0.0.0"); err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	_ = file.DropReplace("pulse.dev", "")
	if err := file.AddReplace("pulse.dev", "", repoRoot, ""); err != nil {
		return err
	}
	formatted, err := file.Format()
	if err != nil {
		return err
	}
	return os.WriteFile(path, formatted, 0o644)
}

func runGo(dir string, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go %s failed: %w\n%s", strings.Join(args, " "), err, output)
	}
	return nil
}

func rewriteEncoreCompat(path string, src []byte) ([]byte, error) {
	text := string(src)
	needsCronStrip := strings.Contains(text, "encore.dev/cron") && strings.Contains(text, "cron.NewJob")
	needsRlogRewrite := strings.Contains(text, "encore.dev/rlog")
	needsAuthRewrite := strings.Contains(text, "encore.dev/beta/auth")
	needsErrsRewrite := strings.Contains(text, "encore.dev/beta/errs")
	needsRootRewrite := strings.Contains(text, "\"encore.dev\"")
	if !needsCronStrip && !needsRlogRewrite && !needsAuthRewrite && !needsErrsRewrite && !needsRootRewrite {
		return src, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	changed := false
	if rewriteImportPath(file, "encore.dev/rlog", "pulse.dev/rlog", "") {
		changed = true
	}
	if rewriteImportPath(file, "encore.dev/beta/auth", "pulse.dev/auth", "") {
		changed = true
	}
	if rewriteImportPath(file, "encore.dev/beta/errs", "pulse.dev/errs", "") {
		changed = true
	}
	if rewriteImportPath(file, "encore.dev", "pulse.dev", "encore") {
		changed = true
	}

	if needsCronStrip {
		cronAliases := cronImportAliases(file)
		if len(cronAliases) > 0 {
			var decls []ast.Decl
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					decls = append(decls, decl)
					continue
				}
				var specs []ast.Spec
				for _, spec := range gen.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if ok && isUnsupportedCronJob(valueSpec, cronAliases) {
						changed = true
						continue
					}
					specs = append(specs, spec)
				}
				if len(specs) == 0 {
					changed = true
					continue
				}
				gen.Specs = specs
				decls = append(decls, gen)
			}
			file.Decls = decls
			pruneUnusedCronImports(file, cronAliases)
		}
	}
	if !changed {
		return src, nil
	}

	out, err := format.Source(renderAST(fset, file))
	if err != nil {
		return nil, err
	}
	return out, nil
}

func cronImportAliases(file *ast.File) map[string]bool {
	aliases := make(map[string]bool)
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, "\"") != "encore.dev/cron" {
			continue
		}
		if imp.Name != nil && imp.Name.Name != "." {
			aliases[imp.Name.Name] = true
			continue
		}
		aliases["cron"] = true
	}
	return aliases
}

func rewriteImportPath(file *ast.File, oldPath, newPath, alias string) bool {
	changed := false
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, "\"") != oldPath {
			continue
		}
		imp.Path.Value = fmt.Sprintf("%q", newPath)
		if alias != "" && imp.Name == nil {
			imp.Name = ast.NewIdent(alias)
		}
		changed = true
	}
	return changed
}

func isUnsupportedCronJob(spec *ast.ValueSpec, cronAliases map[string]bool) bool {
	if len(spec.Names) != 1 || spec.Names[0].Name != "_" || len(spec.Values) != 1 {
		return false
	}
	call, ok := spec.Values[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "NewJob" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && cronAliases[ident.Name]
}

func pruneUnusedCronImports(file *ast.File, cronAliases map[string]bool) {
	used := false
	ast.Inspect(file, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if ok && cronAliases[ident.Name] {
			used = true
			return false
		}
		return true
	})
	if used {
		return
	}

	var decls []ast.Decl
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			decls = append(decls, decl)
			continue
		}
		var specs []ast.Spec
		for _, spec := range gen.Specs {
			imp := spec.(*ast.ImportSpec)
			if strings.Trim(imp.Path.Value, "\"") == "encore.dev/cron" {
				continue
			}
			specs = append(specs, spec)
		}
		if len(specs) == 0 {
			continue
		}
		gen.Specs = specs
		decls = append(decls, gen)
	}
	file.Decls = decls
}

func renderAST(fset *token.FileSet, file *ast.File) []byte {
	var buf strings.Builder
	_ = format.Node(&buf, fset, file)
	return []byte(buf.String())
}
