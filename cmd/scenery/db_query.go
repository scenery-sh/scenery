package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
)

const dbQueryKind = "scenery.db.query.result"

// dbQueryLockTimeout bounds how long a statement may queue behind locks held
// by the running application, so a blocked DDL cannot stall the app's queries.
const dbQueryLockTimeout = 5 * time.Second

type dbQueryOptions struct {
	root, service, sql string
	params             []any
	limit              int
	timeout            time.Duration
	json               bool
}

type dbQueryResult struct {
	cliPayloadIdentity
	Service      string              `json:"service"`
	Schema       string              `json:"schema"`
	Columns      []string            `json:"columns"`
	Rows         [][]json.RawMessage `json:"rows"`
	RowCount     int                 `json:"row_count"`
	RowsAffected int64               `json:"rows_affected"`
	Truncated    bool                `json:"truncated"`
	DurationMS   int64               `json:"duration_ms"`
}

func parseDBQueryArgs(args []string) (dbQueryOptions, error) {
	var opts dbQueryOptions
	flags := newCLIFlagSet("db query")
	flags.StringVar(&opts.root, "app-root", "", "")
	flags.StringVar(&opts.service, "service", "", "")
	flags.StringVar(&opts.sql, "sql", "", "")
	flags.IntVar(&opts.limit, "limit", 200, "")
	flags.DurationVar(&opts.timeout, "timeout", 30*time.Second, "")
	flags.Func("param", "", func(value string) error {
		opts.params = append(opts.params, value)
		return nil
	})
	registerJSONOutput(flags, &opts.json)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return opts, err
	}
	if len(positionals) == 1 && !cliFlagSet(flags, "sql") {
		opts.sql = positionals[0]
	} else if len(positionals) > 0 {
		return opts, usageErrorf("provide one SQL statement using a positional argument or --sql")
	}
	if strings.TrimSpace(opts.service) == "" || strings.TrimSpace(opts.sql) == "" || opts.limit < 1 {
		return opts, usageErrorf("db query requires --service, one SQL statement, and a positive --limit")
	}
	if opts.timeout <= 0 || opts.timeout > time.Hour {
		return opts, usageErrorf("--timeout must be positive and at most 1h")
	}
	return opts, nil
}

func dbQueryBinding(compiled *compiler.Result, service string) (compiler.SQLBinding, error) {
	var bindings []compiler.SQLBinding
	found := false
	for _, resource := range compiler.RuntimeServices(compiled.Manifest.Resources) {
		if resource.Module != service && resource.Name != service && resource.Address != service {
			continue
		}
		found = true
		for _, requirement := range compiled.SQLRequirements {
			if requirement.Kind == compiler.SQLDataSource && slices.Contains(requirement.Consumers, resource.Address) {
				binding := compiler.SQLBinding{Name: requirement.Name, Schema: requirement.Schema}
				if !slices.Contains(bindings, binding) {
					bindings = append(bindings, binding)
				}
			}
		}
	}
	if len(bindings) > 1 {
		// A module can explicitly select its own canonical binding by name.
		for _, binding := range bindings {
			if binding.Name == service {
				return binding, nil
			}
		}
		return compiler.SQLBinding{}, usageErrorf("service %q has multiple Postgres bindings: %v", service, bindings)
	}
	if len(bindings) == 1 {
		return bindings[0], nil
	}
	if found {
		return compiler.SQLBinding{}, usageErrorf("service %q has no Postgres binding", service)
	}
	// The other db commands also accept declared SQL binding names.
	for _, requirement := range compiled.SQLRequirements {
		if requirement.Kind == compiler.SQLDataSource && requirement.Name == service {
			return compiler.SQLBinding{Name: requirement.Name, Schema: requirement.Schema}, nil
		}
	}
	return compiler.SQLBinding{}, usageErrorf("unknown database service %q", service)
}

func dbQueryCommand(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runDBQuery(ctx, os.Stdout, args)
}

func runDBQuery(ctx context.Context, out io.Writer, args []string) error {
	opts, err := parseDBQueryArgs(args)
	if err != nil {
		return err
	}
	root, cfg, err := discoverConfiguredApp(opts.root)
	if err != nil {
		return err
	}
	compiled, err := compileSQLContract(root)
	if err != nil {
		return err
	}
	binding, err := dbQueryBinding(compiled, opts.service)
	if err != nil {
		return err
	}
	database, err := resolveDBQueryDatabase(ctx, root, cfg, compiled.SQLRequirements)
	if err != nil {
		return err
	}
	for _, schema := range database.Schemas {
		if schema.Name == binding.Name {
			result, err := executeDBQuery(ctx, opts, schema.URL, binding.Schema)
			if err != nil {
				return err
			}
			if opts.json {
				return writeCLIJSON(out, result)
			}
			return printDBQuery(out, result)
		}
	}
	return preconditionErrorf("Postgres schema %q is unavailable", binding.Schema)
}

// resolveDBQueryDatabase holds the worktree operation lock only while the
// retained database is verified. The statement runs after the lock is
// released, so a slow query never blocks `scenery up` or `db server stop`.
func resolveDBQueryDatabase(ctx context.Context, root string, cfg appcfg.Config, requirements compiler.SQLRequirements) (_ postgresdb.Database, returnErr error) {
	env := envpolicy.Environ()
	if lookupEnvValue(env, appDatabaseURLEnv) == "" {
		paths, err := commandWorktreePaths(root)
		if err != nil {
			return postgresdb.Database{}, err
		}
		op, err := paths.BeginOperation()
		if err != nil {
			return postgresdb.Database{}, err
		}
		defer func() { returnErr = errors.Join(returnErr, op.Close()) }()
		if err := checkStorageStartup(ctx, root, cfg); err != nil {
			return postgresdb.Database{}, err
		}
	}
	return resolvePostgresDatabaseFromEnv(ctx, root, cfg, requirements, env)
}

// dbQueryConnConfig bounds the session: the statement may run for at most
// timeout and may wait at most dbQueryLockTimeout for a lock.
func dbQueryConnConfig(databaseURL string, timeout time.Duration) (*pgx.ConnConfig, error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	config.ConnectTimeout = min(timeout, 10*time.Second)
	config.RuntimeParams["statement_timeout"] = strconv.FormatInt(timeout.Milliseconds(), 10)
	config.RuntimeParams["lock_timeout"] = strconv.FormatInt(min(timeout, dbQueryLockTimeout).Milliseconds(), 10)
	return config, nil
}

func executeDBQuery(ctx context.Context, opts dbQueryOptions, databaseURL, schema string) (dbQueryResult, error) {
	result := dbQueryResult{
		cliPayloadIdentity: newCLIPayloadIdentity(dbQueryKind),
		Service:            opts.service, Schema: schema, Columns: []string{}, Rows: [][]json.RawMessage{},
	}
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	config, err := dbQueryConnConfig(databaseURL, opts.timeout)
	if err != nil {
		return result, preconditionErrorf("cannot connect to the selected Postgres database")
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return result, preconditionErrorf("cannot connect to the selected Postgres database")
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var current *string
	if err := conn.QueryRow(ctx, "SELECT current_schema()").Scan(&current); err != nil || current == nil || *current != schema {
		return result, preconditionErrorf("Postgres schema %q is missing or inaccessible", schema)
	}
	started := time.Now()
	// Extended protocol binds string parameters as text and accepts one
	// statement. Closing the rows drains the complete command tag.
	params := append([]any{pgx.QueryExecModeExec}, opts.params...)
	rows, err := conn.Query(ctx, opts.sql, params...)
	if err != nil {
		return result, dbQueryError(err)
	}
	defer rows.Close()
	for _, field := range rows.FieldDescriptions() {
		result.Columns = append(result.Columns, field.Name)
	}
	for rows.Next() {
		if len(result.Rows) == opts.limit {
			result.Truncated = true
			break
		}
		values, err := rows.Values()
		if err != nil {
			return result, dbQueryError(err)
		}
		row := make([]json.RawMessage, len(values))
		for i, value := range values {
			encoded, err := json.Marshal(dbQueryValue(value))
			if err != nil {
				encoded, _ = json.Marshal(string(rows.RawValues()[i]))
			}
			row[i] = encoded
		}
		result.Rows = append(result.Rows, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, dbQueryError(err)
	}
	result.RowCount = len(result.Rows)
	tag := rows.CommandTag()
	if tag.Insert() || tag.Update() || tag.Delete() || tag.String() == "MERGE" || strings.HasPrefix(tag.String(), "MERGE ") {
		result.RowsAffected = tag.RowsAffected()
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

// dbQueryValue gives decoded values without a natural JSON form their
// canonical text: pgx decodes uuid columns as [16]byte, not as a string.
func dbQueryValue(value any) any {
	switch typed := value.(type) {
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", typed[0:4], typed[4:6], typed[6:8], typed[8:10], typed[10:16])
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = dbQueryValue(item)
		}
		return items
	default:
		return value
	}
}

func dbQueryError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return preconditionErrorf("Postgres SQLSTATE %s: %s", pg.Code, pg.Message)
	}
	return preconditionErrorf("Postgres query failed: %s", err)
}

func printDBQuery(out io.Writer, result dbQueryResult) error {
	if len(result.Columns) > 0 {
		_, _ = fmt.Fprintln(out, strings.Join(result.Columns, "\t"))
	}
	for _, row := range result.Rows {
		columns := make([]string, len(row))
		for i, value := range row {
			columns[i] = string(value)
		}
		_, _ = fmt.Fprintln(out, strings.Join(columns, "\t"))
	}
	_, err := fmt.Fprintf(out, "%d rows returned, %d affected (truncated: %t)\n", result.RowCount, result.RowsAffected, result.Truncated)
	return err
}
