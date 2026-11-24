package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
)

const dbQueryKind = "scenery.db.query.result"

type dbQueryOptions struct {
	root, service, sql string
	params             []any
	limit              int
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
	// The other db commands also accept declared SQL binding names.
	for _, requirement := range compiled.SQLRequirements {
		if found && requirement.Kind == compiler.SQLDataSource && requirement.Name == service {
			return compiler.SQLBinding{Name: requirement.Name, Schema: requirement.Schema}, nil
		}
	}
	if found {
		return compiler.SQLBinding{}, usageErrorf("service %q has no Postgres binding", service)
	}
	return compiler.SQLBinding{}, usageErrorf("unknown database service %q", service)
}

func dbQueryCommand(args []string) error {
	return runDBQuery(context.Background(), os.Stdout, args)
}

func runDBQuery(ctx context.Context, out io.Writer, args []string) (returnErr error) {
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
	env := envpolicy.Environ()
	if lookupEnvValue(env, appDatabaseURLEnv) == "" {
		paths, err := commandWorktreePaths(root)
		if err != nil {
			return err
		}
		op, err := paths.BeginOperation()
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, op.Close()) }()
		if err := checkStorageStartup(ctx, root, cfg); err != nil {
			return err
		}
	}
	database, err := resolvePostgresDatabaseFromEnv(ctx, root, cfg, compiled.SQLRequirements, env)
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

func executeDBQuery(ctx context.Context, opts dbQueryOptions, databaseURL, schema string) (dbQueryResult, error) {
	result := dbQueryResult{
		cliPayloadIdentity: newCLIPayloadIdentity(dbQueryKind),
		Service:            opts.service, Schema: schema, Columns: []string{}, Rows: [][]json.RawMessage{},
	}
	conn, err := pgx.Connect(ctx, databaseURL)
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
			encoded, err := json.Marshal(value)
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
