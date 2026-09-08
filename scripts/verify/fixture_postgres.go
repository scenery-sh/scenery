package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

var openPostgresDatabase = postgresdb.Open

func provisionHarnessDatabase(ctx context.Context, repoRoot, root string, cfg app.Config, expectedSchemas ...string) ([]string, postgresdb.Database, error) {
	for _, args := range [][]string{{"db", "server", "start"}, {"db", "setup"}} {
		if err := runProduct(ctx, repoRoot, io.Discard, append(args, "--app-root", root, "-o", "json")...); err != nil {
			return nil, postgresdb.Database{}, err
		}
	}
	home, err := localagent.DefaultPaths()
	if err != nil {
		return nil, postgresdb.Database{}, err
	}
	paths, err := localagent.PathsForWorktree(home.Home, root)
	if err != nil {
		return nil, postgresdb.Database{}, err
	}
	record, err := paths.LoadRecord(cfg.AppID())
	if err != nil {
		return nil, postgresdb.Database{}, err
	}
	if record.Postgres == nil {
		return nil, postgresdb.Database{}, fmt.Errorf("public database setup did not retain a fixture allocation")
	}
	name := postgresname.DatabaseNameFor(cfg.AppID(), paths.AppRoot)
	database := postgresdb.Database{Database: name, URL: worktreePostgresURL(record.Postgres, name), Source: postgresdb.SourceManaged, AppRoot: paths.AppRoot, ResourceID: record.Postgres.InstanceID}
	// These are expected fixture schemas, not a second production requirement
	// resolver. Real SQL assertions below verify that public setup created them.
	for _, schema := range expectedSchemas {
		connection, err := postgresdb.ServiceURL(database.URL, schema)
		if err != nil {
			return nil, postgresdb.Database{}, err
		}
		database.Schemas = append(database.Schemas, postgresdb.Service{Name: schema, Schema: schema, URL: connection})
	}
	return postgresdb.Env(database), database, nil
}

// Add real typed SQL dependencies to the basic source fixture. The constructor
// accepts its generated input, so the runtime exercises ordinary injection.
func addHarnessSQLDeclarations(repoRoot, root string, names ...string) error {
	appPath := filepath.Join(root, "app.scn")
	appSource, err := os.ReadFile(appPath)
	if err != nil {
		return err
	}
	packagePath := filepath.Join(root, "service", "package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		return err
	}
	var declarations, inputs, dependencies strings.Builder
	declarations.WriteString("\nprovider \"postgres\" { source = \"registry.scenery.dev/core/postgres\" }\n")
	for _, name := range names {
		fmt.Fprintf(&declarations, "data_source %q {\n provider = provider.postgres\n lifecycle = \"managed\"\n require_capabilities = [\"sql.query/v1\", \"sql.transaction/v1\"]\n config = { database = %q }\n}\n", name, name)
		fmt.Fprintf(&inputs, "    %s = data_source.%s\n", name, name)
		fmt.Fprintf(&dependencies, "  dependency %q { instance = var.%s }\n", name, name)
		packageSource = append(packageSource, []byte(fmt.Sprintf("\ninput %q { type = resource_ref(\"data_source\") }\n", name))...)
	}
	appSource = []byte(strings.Replace(string(appSource), "    gateway = http_gateway.public_api", "    gateway = http_gateway.public_api\n"+inputs.String(), 1) + declarations.String())
	packageSource = []byte(strings.Replace(string(packageSource), "service \"service\" {", "service \"service\" {\n"+dependencies.String(), 1))
	if err := os.WriteFile(appPath, appSource, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		return err
	}
	lock, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "apps", "worktree-postgres", "app.lock.scn"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "app.lock.scn"), lock, 0o644)
}

// Only callers holding a fixture-owned allocation record use this connection
// string. Credentials never enter argv or the public evidence summary.
func worktreePostgresURL(p *localagent.WorktreePostgres, database string) string {
	u := &url.URL{Scheme: "postgres", User: url.UserPassword(p.User, p.Password), Host: fmt.Sprintf("127.0.0.1:%d", p.Port), Path: "/" + database}
	return u.String()
}

func isMissingDockerObject(out string, err error) bool {
	message := strings.ToLower(out)
	if err != nil {
		message += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(message, "no such object") || strings.Contains(message, "no such container") || strings.Contains(message, "no such volume")
}

func (p *worktreeRuntimeProbe) verifyRetainedContainer(record localagent.WorktreeRecord) error {
	if record.Postgres == nil {
		return fmt.Errorf("fixture has no retained PostgreSQL allocation")
	}
	output, err := p.run(p.repo, p.binaryForRoot(record.AppRoot), "db", "server", "status", "--app-root", record.AppRoot, "-o", "json")
	if err != nil {
		return err
	}
	var status dbServerStatusResponse
	if err := decodeCLIJSON(output, &status); err != nil {
		return err
	}
	if status.Scope != "worktree" || status.ResourceID != record.Postgres.InstanceID || status.Container != record.Postgres.Container || status.Status == "container-missing" || status.Status == "absent" {
		return fmt.Errorf("public database status did not verify the retained fixture container")
	}
	current, err := p.record(record.AppRoot)
	if err != nil {
		return err
	}
	if !sameWorktreeCluster(record, current) {
		return fmt.Errorf("fixture allocation identity changed before container access")
	}
	return nil
}
