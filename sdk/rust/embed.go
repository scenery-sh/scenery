// Package rust contains the SDK source distributed with the compiler.
package rust

import "embed"

// SDK holds the Rust files materialized by the contract generator.
//
//go:embed scenery-sdk/Cargo.toml scenery-sdk/src/*.rs scenery-sdk/scenery-wire/Cargo.toml scenery-sdk/scenery-wire/src/*.rs
var SDK embed.FS

// Host holds the separately built runtime executable and its wire dependency.
//
//go:embed scenery-host/Cargo.toml scenery-host/Cargo.lock scenery-host/src/*.rs scenery-host/src/durable_schema.sql scenery-sdk/scenery-wire/Cargo.toml scenery-sdk/scenery-wire/src/*.rs
var Host embed.FS

// DurableSchema is the shared Postgres migration used by both hosts.
//
//go:embed scenery-host/src/durable_schema.sql
var DurableSchema string
