package doctor

import (
	"os"
	"path/filepath"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/appwalk"
)

// AppFeatures records which optional tool and service surfaces the
// discovered app actually uses, so dependency checks stay relevant.
type AppFeatures struct {
	SQLCConfigured       bool
	AtlasRelevant        bool
	FrontendConfigured   bool
	TypeScriptTasks      bool
	DockerRelevant       bool
	DatabaseApplyCommand bool
	StorageConfigured    bool
	PostgresServices     bool
}

// Features derives AppFeatures from the discovered app configuration.
// A nil app yields the zero AppFeatures.
func Features(cfg appcfg.Config, app *AppInfo) AppFeatures {
	if app == nil {
		return AppFeatures{}
	}
	features := AppFeatures{}
	features.FrontendConfigured = len(cfg.Frontends) > 0
	features.SQLCConfigured = sqlcGeneratorConfigured(cfg.Generators.SQLC)
	features.AtlasRelevant = sqlcUsesAtlas(cfg.Generators.SQLC)
	features.DatabaseApplyCommand = strings.TrimSpace(cfg.Database.Apply.Command) != ""
	features.StorageConfigured = len(cfg.Storage.Stores) > 0
	features.PostgresServices = len(cfg.PostgresServices()) > 0
	features.DockerRelevant = appUsesDocker(cfg)
	features.TypeScriptTasks = appHasTypeScriptTasks(app.Root)
	return features
}

func sqlcGeneratorConfigured(cfg appcfg.SQLCGeneratorConfig) bool {
	return strings.TrimSpace(cfg.Provider) != "" ||
		strings.TrimSpace(cfg.Config) != "" ||
		strings.TrimSpace(cfg.DevURL) != "" ||
		len(cfg.Schemas) > 0
}

func sqlcUsesAtlas(cfg appcfg.SQLCGeneratorConfig) bool {
	for _, schema := range cfg.Schemas {
		if strings.TrimSpace(schema.AtlasSource) != "" || strings.TrimSpace(schema.AtlasSchema) != "" || strings.TrimSpace(schema.AtlasDevURL) != "" {
			return true
		}
	}
	return false
}

func appUsesDocker(cfg appcfg.Config) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.Generators.SQLC.DevURL)), "docker://") {
		return true
	}
	if len(cfg.PostgresServices()) > 0 {
		return true
	}
	for _, schema := range cfg.Generators.SQLC.Schemas {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(schema.AtlasDevURL)), "docker://") {
			return true
		}
	}
	return false
}

func appHasTypeScriptTasks(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if appwalk.SkipDir(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".task.ts") || name == "index.ts" && strings.Contains(filepath.ToSlash(path), "/tasks/") {
			found = true
		}
		return nil
	})
	return found
}
