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

	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/machine"
)

const (
	buildInputKind             = "scenery.go-build-input-manifest"
	buildInputSchemaDescriptor = machine.ExactSchemaRevision("sha256:0b3dbb89ce6779d9102139831f455f792adee4a3c0e332099816a2761c4d9ec2")
)

type BuildInput struct {
	Identity string `json:"identity"`
	Digest   string `json:"digest"`
}

type BuildInputManifest struct {
	machine.ArtifactIdentity
	Target  string       `json:"target"`
	Entries []BuildInput `json:"entries"`
	Digest  string       `json:"digest"`
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
	Dir      string
	Version  string
	Sum      string
	GoMod    string
	GoModSum string
	Replace  *goListModule
}

// Ask Go for the complete consumed-file/module projection, without computing
// unrelated package presentation fields such as transitive import summaries.
const goBuildInputFields = "Dir,ImportPath,Standard,GoFiles,CgoFiles,CFiles,CXXFiles,MFiles,HFiles,FFiles,SFiles,SwigFiles,SwigCXXFiles,SysoFiles,EmbedFiles,Module"

func buildInputManifest(ctx context.Context, result *Result) (*BuildInputManifest, error) {
	if result == nil || result.Target == nil {
		return nil, fmt.Errorf("build target is unavailable")
	}
	target := result.Target
	args := []string{"list", "-deps", "-json=" + goBuildInputFields}
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
	command.Env = gotarget.Environment(target.Context)
	var output []byte
	err := observeBuildAction(ctx, "go.input_discovery", func() error {
		var err error
		output, err = command.CombinedOutput()
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("go %s failed while producing build inputs: %w\n%s", strings.Join(args, " "), err, output)
	}
	var manifest *BuildInputManifest
	err = observeBuildAction(ctx, "go.input_fingerprint", func() error {
		var err error
		manifest, err = buildInputManifestFromGoList(result, output)
		return err
	})
	return manifest, err
}

func buildInputManifestFromGoList(result *Result, output []byte) (*BuildInputManifest, error) {
	if result == nil || result.Target == nil {
		return nil, fmt.Errorf("build target is unavailable")
	}
	target := result.Target
	entries := map[string]string{}
	frameworkRoot := ""
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
			if pkg.Module.Path == "scenery.sh" {
				// Published GoMod files live in the download metadata cache;
				// only Dir identifies the module's actual package source tree.
				if !filepath.IsAbs(module.Dir) || module.GoMod == "" {
					return nil, fmt.Errorf("go build graph omits the selected Scenery module directory or go.mod")
				}
				root := filepath.Clean(module.Dir)
				if frameworkRoot != "" && frameworkRoot != root {
					return nil, fmt.Errorf("go build graph contains multiple Scenery framework roots")
				}
				frameworkRoot = root
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
	if frameworkRoot != "" {
		source, err := FrameworkSourceManifest(frameworkRoot)
		if err != nil {
			return nil, err
		}
		if linkedFrameworkDigest != "" && source.Digest != linkedFrameworkDigest {
			return nil, fmt.Errorf("actual Go build framework does not match the selected Scenery producer; prepare a coherent framework selection")
		}
		producer, err := executableDigest()
		if err != nil {
			return nil, err
		}
		entries["framework/scenery.sh/source"] = source.Digest
		entries["producer/scenery-cli/executable"] = producer
		result.FrameworkSourceRoot, result.FrameworkSourceDigest = source.Root, source.Digest
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
	return newBuildInputManifest(target.Name, entries), nil
}

func newBuildInputManifest(target string, entries map[string]string) *BuildInputManifest {
	identities := make([]string, 0, len(entries))
	for identity := range entries {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	manifest := &BuildInputManifest{ArtifactIdentity: machine.NewArtifactIdentity(buildInputKind, buildInputSchemaDescriptor), Target: target}
	for _, identity := range identities {
		manifest.Entries = append(manifest.Entries, BuildInput{Identity: identity, Digest: entries[identity]})
	}
	projection, _ := json.Marshal(struct {
		machine.ArtifactIdentity
		Target  string       `json:"target"`
		Entries []BuildInput `json:"entries"`
	}{manifest.ArtifactIdentity, manifest.Target, manifest.Entries})
	digest := sha256.Sum256(append([]byte("scenery.go-build-input-manifest\x00"), projection...))
	manifest.Digest = "sha256:" + hex.EncodeToString(digest[:])
	return manifest
}

func addBuildInput(entries map[string]string, identity, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("go build input is not a regular non-symlink file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if previous := entries[identity]; previous != "" && previous != digest {
		return fmt.Errorf("go build input identity collision: %s", identity)
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
