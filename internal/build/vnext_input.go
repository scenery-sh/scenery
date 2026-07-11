package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"scenery.sh/internal/parse"
)

type VNextBuildInput struct {
	Identity string `json:"identity"`
	Digest   string `json:"digest"`
}

type VNextBuildInputManifest struct {
	APIVersion string            `json:"api_version"`
	Target     string            `json:"target"`
	Entries    []VNextBuildInput `json:"entries"`
	Digest     string            `json:"digest"`
}

type goListPackage struct {
	Dir          string
	ImportPath   string
	Standard     bool
	GoFiles      []string
	CgoFiles     []string
	CFiles       []string
	CXXFiles     []string
	MFiles       []string
	HFiles       []string
	FFiles       []string
	SFiles       []string
	SwigFiles    []string
	SwigCXXFiles []string
	SysoFiles    []string
	EmbedFiles   []string
	Module       *goListModule
}

type goListModule struct {
	Path     string
	Version  string
	Sum      string
	GoMod    string
	GoModSum string
	Replace  *goListModule
}

func buildVNextInputManifest(ctx context.Context, result *Result) (*VNextBuildInputManifest, error) {
	if result == nil || result.VNextTarget == nil {
		return nil, fmt.Errorf("vNext build target is unavailable")
	}
	target := result.VNextTarget
	args := []string{"list", "-deps", "-json"}
	args = append(args, target.Context.BuildFlags...)
	if len(target.Context.BuildTags) > 0 {
		args = append(args, "-tags="+strings.Join(target.Context.BuildTags, ","))
	}
	patterns := append([]string(nil), target.Context.Patterns...)
	patterns = append(patterns, "./scenery_internal_main")
	slices.Sort(patterns)
	patterns = slices.Compact(patterns)
	args = append(args, patterns...)
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = result.Dir
	command.Env = parse.GoTargetEnvironment(target.Context)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go %s failed while producing build inputs: %w\n%s", strings.Join(args, " "), err, output)
	}
	entries := map[string]string{}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode Go build input graph: %w", err)
		}
		if pkg.Standard {
			continue
		}
		files := append([]string{}, pkg.GoFiles...)
		files = append(files, pkg.CgoFiles...)
		files = append(files, pkg.CFiles...)
		files = append(files, pkg.CXXFiles...)
		files = append(files, pkg.MFiles...)
		files = append(files, pkg.HFiles...)
		files = append(files, pkg.FFiles...)
		files = append(files, pkg.SFiles...)
		files = append(files, pkg.SwigFiles...)
		files = append(files, pkg.SwigCXXFiles...)
		files = append(files, pkg.SysoFiles...)
		files = append(files, pkg.EmbedFiles...)
		for _, name := range files {
			path := filepath.Join(pkg.Dir, filepath.FromSlash(name))
			identity := "package/" + pkg.ImportPath + "/" + filepath.ToSlash(name)
			if err := addBuildInput(entries, identity, path); err != nil {
				return nil, err
			}
		}
		if pkg.Module != nil {
			module := pkg.Module
			if module.Replace != nil {
				module = module.Replace
			}
			if module.GoMod != "" {
				if err := addBuildInput(entries, "module/"+pkg.Module.Path+"/go.mod", module.GoMod); err != nil {
					return nil, err
				}
			}
			identity := pkg.Module.Path + "@" + pkg.Module.Version + "\x00" + pkg.Module.Sum + "\x00" + pkg.Module.GoModSum
			sum := sha256.Sum256([]byte(identity))
			entries["module/"+pkg.Module.Path] = "sha256:" + hex.EncodeToString(sum[:])
		}
	}
	for _, relative := range append(stringValuesForBuild(target.Effective["native_inputs"]), stringValuesForBuild(target.Effective["native_input"])...) {
		path := filepath.Join(result.AppRoot, filepath.FromSlash(relative))
		if err := filepath.WalkDir(path, func(filePath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(path, filePath)
			if err != nil {
				return err
			}
			return addBuildInput(entries, "native/"+filepath.ToSlash(relative)+"/"+filepath.ToSlash(rel), filePath)
		}); err != nil {
			return nil, err
		}
	}
	identities := make([]string, 0, len(entries))
	for identity := range entries {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	manifest := &VNextBuildInputManifest{APIVersion: "scenery.go-build-input-manifest/v1", Target: target.Name}
	for _, identity := range identities {
		manifest.Entries = append(manifest.Entries, VNextBuildInput{Identity: identity, Digest: entries[identity]})
	}
	projection, _ := json.Marshal(struct {
		APIVersion string            `json:"api_version"`
		Target     string            `json:"target"`
		Entries    []VNextBuildInput `json:"entries"`
	}{manifest.APIVersion, manifest.Target, manifest.Entries})
	digest := sha256.Sum256(append([]byte("scenery.go-build-input-manifest.v1\x00"), projection...))
	manifest.Digest = "sha256:" + hex.EncodeToString(digest[:])
	return manifest, nil
}

func addBuildInput(entries map[string]string, identity, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("Go build input is not a regular non-symlink file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if previous := entries[identity]; previous != "" && previous != digest {
		return fmt.Errorf("Go build input identity collision: %s", identity)
	}
	entries[identity] = digest
	return nil
}

func stringValuesForBuild(value any) []string {
	items, _ := value.([]any)
	values := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			values = append(values, text)
		}
	}
	return values
}
