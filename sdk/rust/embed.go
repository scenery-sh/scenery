// Package rust contains the SDK source distributed with the compiler.
package rust

import "embed"

// SDK holds the Rust files materialized by the contract generator.
//
//go:embed scenery-sdk/Cargo.toml scenery-sdk/src/*.rs
var SDK embed.FS
