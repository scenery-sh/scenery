package dbstudio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"onlava.com/internal/envfile"
)

const DefaultPort = 4002

type Config struct {
	DatabaseURL string
	Source      string
	Dialect     string
}

type Options struct {
	AppRoot string
	AppID   string
	Config  Config
	Port    int
	Verbose bool
	Stdout  io.Writer
	Stderr  io.Writer
}

type Instance struct {
	cmd       *exec.Cmd
	done      chan error
	workspace string
	url       string
}

func (i *Instance) Interrupt() error {
	if i == nil || i.cmd == nil || i.cmd.Process == nil {
		return nil
	}
	return interruptProcessTree(i.cmd)
}

func (i *Instance) Kill() error {
	if i == nil || i.cmd == nil || i.cmd.Process == nil {
		return nil
	}
	return killProcessTree(i.cmd)
}

func (i *Instance) WaitOrKill(grace time.Duration) error {
	if i == nil {
		return nil
	}
	select {
	case waitErr := <-i.done:
		if waitErr == nil || isExpectedExit(waitErr) {
			return nil
		}
		return waitErr
	case <-time.After(grace):
		_ = i.Kill()
		select {
		case waitErr := <-i.done:
			if waitErr == nil || isExpectedExit(waitErr) {
				return nil
			}
			return waitErr
		case <-time.After(time.Second):
			return fmt.Errorf("db studio did not exit after SIGKILL")
		}
	}
}

func Discover(appRoot string) (Config, bool, error) {
	for _, key := range []string{"DATABASE_URL", "DatabaseURL"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			cfg, err := configForURL(value, key)
			return cfg, err == nil, err
		}
	}
	env, err := envfile.ParseFile(filepath.Join(appRoot, ".env"))
	if err != nil {
		return Config{}, false, err
	}
	for _, key := range []string{"DATABASE_URL", "DatabaseURL"} {
		if value := strings.TrimSpace(env[key]); value != "" {
			cfg, err := configForURL(value, ".env:"+key)
			return cfg, err == nil, err
		}
	}
	return Config{}, false, nil
}

func Start(ctx context.Context, opts Options) (*Instance, error) {
	if opts.Port == 0 {
		opts.Port = DefaultPort
	}
	if strings.TrimSpace(opts.AppRoot) == "" {
		return nil, errors.New("db studio: app root must not be empty")
	}
	if opts.Config.DatabaseURL == "" {
		discovered, ok, err := Discover(opts.AppRoot)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		opts.Config = discovered
	}
	if opts.Config.Dialect == "" {
		dialect, err := inferDialect(opts.Config.DatabaseURL)
		if err != nil {
			return nil, err
		}
		opts.Config.Dialect = dialect
	}
	if err := ensurePortAvailable(opts.Port); err != nil {
		return nil, err
	}
	bunPath, err := exec.LookPath("bunx")
	if err != nil {
		return nil, fmt.Errorf("db studio requires bunx: %w", err)
	}
	workspace, err := workspaceDir(opts.AppRoot, opts.AppID)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(workspace)
	}
	if err := os.RemoveAll(workspace); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, err
	}
	if err := ensureWorkspaceDeps(ctx, workspace, opts); err != nil {
		cleanup()
		return nil, err
	}
	configPath, err := writeConfigFile(workspace, opts.Config)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := runPull(ctx, bunPath, workspace, configPath, opts); err != nil {
		cleanup()
		return nil, err
	}

	cmd := commandTreeContext(ctx, bunPath, studioArgs(configPath, opts)...)
	cmd.Dir = workspace
	if opts.Verbose {
		cmd.Stdout = opts.Stdout
		cmd.Stderr = opts.Stderr
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, err
	}

	inst := &Instance{
		cmd:       cmd,
		done:      make(chan error, 1),
		workspace: workspace,
		url:       DefaultURL(opts.Port),
	}
	go func() {
		inst.done <- cmd.Wait()
		close(inst.done)
	}()
	if err := waitForReady(ctx, opts.Port, inst.done, opts.Verbose); err != nil {
		_ = inst.Close()
		return nil, err
	}
	return inst, nil
}

func (i *Instance) URL() string {
	if i == nil {
		return ""
	}
	return i.url
}

func (i *Instance) Close() error {
	if i == nil {
		return nil
	}
	var err error
	if i.cmd != nil && i.cmd.Process != nil {
		if interruptErr := i.Interrupt(); interruptErr != nil {
			err = interruptErr
		} else if waitErr := i.WaitOrKill(5 * time.Second); waitErr != nil {
			err = waitErr
		}
	}
	if removeErr := os.RemoveAll(i.workspace); removeErr != nil && err == nil {
		err = removeErr
	}
	return err
}

func DefaultURL(port int) string {
	if port == 0 {
		port = DefaultPort
	}
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

func runPull(ctx context.Context, bunPath, workspace, configPath string, opts Options) error {
	cmd := commandTreeContext(ctx, bunPath, pullArgs(configPath, opts)...)
	cmd.Dir = workspace
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return fmt.Errorf("db studio schema pull failed: %w", err)
		}
		return fmt.Errorf("db studio schema pull failed: %w\n%s", err, msg)
	}
	return nil
}

func pullArgs(configPath string, opts Options) []string {
	args := []string{}
	if !opts.Verbose {
		args = append(args, "--silent")
	}
	args = append(args,
		"drizzle-kit",
		"pull",
		"--config="+configPath,
	)
	return args
}

func studioArgs(configPath string, opts Options) []string {
	args := []string{}
	if !opts.Verbose {
		args = append(args, "--silent")
	}
	args = append(args,
		"drizzle-kit",
		"studio",
		"--config="+configPath,
		"--host=127.0.0.1",
		"--port="+strconv.Itoa(opts.Port),
	)
	if opts.Verbose {
		args = append(args, "--verbose")
	}
	return args
}

func ensureWorkspaceDeps(ctx context.Context, workspace string, opts Options) error {
	if err := writeWorkspacePackageJSON(workspace, opts.Config.Dialect); err != nil {
		return err
	}
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		return fmt.Errorf("db studio requires bun: %w", err)
	}
	cmd := commandTreeContext(ctx, bunPath, "install", "--cwd", workspace)
	cmd.Dir = workspace
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return fmt.Errorf("db studio dependency install failed: %w", err)
		}
		return fmt.Errorf("db studio dependency install failed: %w\n%s", err, msg)
	}
	return nil
}

func writeWorkspacePackageJSON(workspace, dialect string) error {
	path := filepath.Join(workspace, "package.json")
	deps := map[string]string{
		"drizzle-kit": "latest",
		"drizzle-orm": "latest",
	}
	for _, dep := range driverPackagesForDialect(dialect) {
		deps[dep] = "latest"
	}
	data, err := json.MarshalIndent(map[string]any{
		"name":         "onlava-dbstudio",
		"private":      true,
		"dependencies": deps,
	}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func driverPackagesForDialect(dialect string) []string {
	switch dialect {
	case "postgresql":
		return []string{"pg"}
	case "mysql", "singlestore":
		return []string{"mysql2"}
	case "sqlite", "turso":
		return []string{"@libsql/client"}
	case "mssql":
		return []string{"mssql"}
	case "cockroachdb":
		return []string{"pg"}
	default:
		return nil
	}
}

func writeConfigFile(workspace string, cfg Config) (string, error) {
	path := filepath.Join(workspace, "drizzle.config.ts")
	data := "import { defineConfig } from \"drizzle-kit\";\n\n" +
		"export default defineConfig({\n" +
		"  dialect: " + strconv.Quote(cfg.Dialect) + ",\n" +
		"  schema: \"./drizzle/*.ts\",\n" +
		"  out: \"./drizzle\",\n" +
		"  dbCredentials: {\n" +
		"    url: " + strconv.Quote(cfg.DatabaseURL) + ",\n" +
		"  },\n" +
		"});\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func configForURL(rawURL, source string) (Config, error) {
	dialect, err := inferDialect(rawURL)
	if err != nil {
		return Config{}, err
	}
	return Config{
		DatabaseURL: rawURL,
		Source:      source,
		Dialect:     dialect,
	}, nil
}

func inferDialect(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("db studio: invalid DatabaseURL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "postgres", "postgresql":
		return "postgresql", nil
	case "mysql":
		return "mysql", nil
	case "sqlite", "file":
		return "sqlite", nil
	case "libsql", "turso":
		return "turso", nil
	default:
		return "", fmt.Errorf("db studio: unsupported DatabaseURL scheme %q", u.Scheme)
	}
}

func ensurePortAvailable(port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("db studio failed to listen on 127.0.0.1:%d: %w", port, err)
	}
	return ln.Close()
}

func waitForReady(ctx context.Context, port int, done <-chan error, verbose bool) error {
	deadline := time.Now().Add(20 * time.Second)
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for {
		conn, err := net.DialTimeout("tcp", target, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			if err == nil || isExpectedExit(err) {
				if verbose {
					return fmt.Errorf("db studio stopped before listening on %s", target)
				}
				return fmt.Errorf("db studio stopped before listening on %s; rerun with -v for details", target)
			}
			if verbose {
				return err
			}
			return fmt.Errorf("db studio stopped before listening on %s; rerun with -v for details", target)
		case <-time.After(200 * time.Millisecond):
			if time.Now().After(deadline) {
				return fmt.Errorf("db studio timed out waiting for %s", target)
			}
		}
	}
}

func workspaceDir(appRoot, appID string) (string, error) {
	cacheRoot, err := onlavaCacheRoot()
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(appRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absRoot))
	name := sanitizeWorkspaceLabel(appID)
	if name == "" {
		name = "app"
	}
	return filepath.Join(cacheRoot, "dbstudio", name+"-"+hex.EncodeToString(sum[:8])), nil
}

func onlavaCacheRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("ONLAVA_DEV_CACHE_DIR")); root != "" {
		return root, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "onlava"), nil
}

func sanitizeWorkspaceLabel(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
