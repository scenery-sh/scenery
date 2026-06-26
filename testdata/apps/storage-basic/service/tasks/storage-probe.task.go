//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"scenery.sh/storage"
)

func main() {
	ctx := storage.WithTenantID(context.Background(), "storage-probe")
	if err := run(ctx); err != nil {
		panic(err)
	}
}

func run(ctx context.Context) error {
	store, err := storage.Default(ctx)
	if err != nil {
		return err
	}
	obj, err := store.Put(ctx, "task/probe.txt", strings.NewReader("storage task probe"), storage.PutOptions{ContentType: "text/plain"})
	if err != nil {
		return err
	}
	body, got, err := store.Get(ctx, obj.Key, storage.GetOptions{})
	if err != nil {
		return err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"key":        got.Key,
		"size_bytes": got.SizeBytes,
		"body":       string(data),
	})
}
