package main

import (
	"context"
	"log"

	"scenery.sh/db"
)

const schema = `CREATE TABLE IF NOT EXISTS processed_events (
  event_id text PRIMARY KEY,
  payload text NOT NULL,
  sha256 text NOT NULL
)`

func main() {
	ctx := context.Background()
	database, err := db.Get(ctx, "inbox")
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, schema); err != nil {
		log.Fatal(err)
	}
}
