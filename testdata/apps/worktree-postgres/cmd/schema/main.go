package main

import (
	"context"
	"log"
	"scenery.sh/db"
)

func main() {
	ctx := context.Background()
	database, err := db.Get(ctx, "library")
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	_, err = database.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS books (
 book_id text PRIMARY KEY,
 title text NOT NULL,
 borrower text NOT NULL DEFAULT ''
 )`)
	if err != nil {
		log.Fatal(err)
	}
}
