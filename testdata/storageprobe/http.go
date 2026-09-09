package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"scenery.sh/internal/storagefs"
	"scenery.sh/storage"
)

func verifyHTTP(ctx context.Context, store *storagefs.Store) error {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := storage.ServeObject(w, req, store, "a"); err != nil {
			storage.HTTPError(w, err)
		}
	}))
	defer server.Close()
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		req, err := http.NewRequestWithContext(ctx, method, server.URL, nil)
		if err != nil {
			return err
		}
		if method == http.MethodGet {
			req.Header.Set("Range", "bytes=1-3")
		}
		response, err := server.Client().Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(response.Body)
		if err := errors.Join(err, response.Body.Close()); err != nil {
			return err
		}
		if response.Header.Get("ETag") == "" {
			return errors.New("native HTTP omitted ETag")
		}
		if method == http.MethodHead {
			if response.StatusCode != 200 || len(data) != 0 || response.ContentLength != 8 {
				return fmt.Errorf("native HEAD mismatch: status=%d length=%d body=%q", response.StatusCode, response.ContentLength, data)
			}
		} else if response.StatusCode != 206 || string(data) != "est" || response.Header.Get("Content-Range") != "bytes 1-3/8" {
			return fmt.Errorf("native range mismatch: status=%d body=%q range=%s", response.StatusCode, data, response.Header.Get("Content-Range"))
		}
	}
	return nil
}
