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
	"pulse.dev/internal/model"
	"pulse.dev/internal/parse"
)

type Result struct {
	Dir    string
	Binary string
}

func App(appRoot string, cfg app.Config) (*Result, error) {
	model, err := parse.App(appRoot, cfg.Name)
	if err != nil {
		return nil, err
	}
	result, err := Prepare(appRoot, model, cfg)
	if err != nil {
		return nil, err
	}
	if err := Compile(result); err != nil {
		_ = os.RemoveAll(result.Dir)
		return nil, err
	}
	return result, nil
}

func Prepare(appRoot string, model *model.App, cfg app.Config) (*Result, error) {
	gen, err := codegen.GenerateWithConfig(model, cfg)
	if err != nil {
		return nil, err
	}

	tempDir, err := os.MkdirTemp("", "pulse-build-*")
	if err != nil {
		return nil, err
	}
	keepTempDir := false
	defer func() {
		if !keepTempDir {
			_ = os.RemoveAll(tempDir)
		}
	}()
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
	binary := filepath.Join(tempDir, "pulse-app")
	keepTempDir = true
	return &Result{Dir: tempDir, Binary: binary}, nil
}

func Compile(result *Result) error {
	if result == nil {
		return fmt.Errorf("nil build result")
	}
	if err := runGo(result.Dir, "mod", "tidy"); err != nil {
		return err
	}
	if err := runGo(result.Dir, "build", "-o", result.Binary, "./pulse_internal_main"); err != nil {
		return err
	}
	return nil
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
	needsCronRewrite := strings.Contains(text, "encore.dev/cron")
	needsRlogRewrite := strings.Contains(text, "encore.dev/rlog")
	needsAuthRewrite := strings.Contains(text, "encore.dev/beta/auth")
	needsErrsRewrite := strings.Contains(text, "encore.dev/beta/errs")
	needsMiddlewareRewrite := strings.Contains(text, "encore.dev/middleware")
	needsPGXPoolRewrite := strings.Contains(text, "github.com/jackc/pgx/v5/pgxpool")
	needsRootRewrite := strings.Contains(text, "\"encore.dev\"")
	if !needsCronRewrite && !needsRlogRewrite && !needsAuthRewrite && !needsErrsRewrite && !needsMiddlewareRewrite && !needsPGXPoolRewrite && !needsRootRewrite {
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
	if rewriteImportPath(file, "encore.dev/cron", "pulse.dev/cron", "") {
		changed = true
	}
	if rewriteImportPath(file, "encore.dev/beta/auth", "pulse.dev/auth", "") {
		changed = true
	}
	if rewriteImportPath(file, "encore.dev/beta/errs", "pulse.dev/errs", "") {
		changed = true
	}
	if rewriteImportPath(file, "encore.dev/middleware", "pulse.dev/middleware", "") {
		changed = true
	}
	if rewriteImportPath(file, "github.com/jackc/pgx/v5/pgxpool", "pulse.dev/pgxpool", "") {
		changed = true
	}
	if rewriteImportPath(file, "encore.dev", "pulse.dev", "encore") {
		changed = true
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

func renderAST(fset *token.FileSet, file *ast.File) []byte {
	var buf strings.Builder
	_ = format.Node(&buf, fset, file)
	return []byte(buf.String())
}
