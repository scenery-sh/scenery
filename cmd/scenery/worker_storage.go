package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/storageconfig"
	workerruntime "scenery.sh/runtime"
	"scenery.sh/storage"
)

func configureWorkerDevelopmentStorage(ctx context.Context, root string, cfg app.Config) error {
	environment, err := storageCapabilityEnv(ctx, root, cfg, nil, nil, "")
	if err != nil {
		return err
	}
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		if err := envpolicy.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}

func prepareWorkerStorage(plans []workerruntime.WorkerServicePlan) (workerruntime.WorkerStorageHandler, error) {
	configured := false
	for _, plan := range plans {
		configured = configured || len(plan.Stores) != 0
	}
	if !configured {
		return nil, nil
	}
	runtimeConfig, available, err := storageconfig.LoadRuntimeConfigValue(envpolicy.Get(storageconfig.RuntimeConfigEnv))
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, fmt.Errorf("worker storage dependencies require configured storage")
	}
	for _, plan := range plans {
		for _, name := range plan.Stores {
			if _, exists := runtimeConfig.Stores[name]; !exists {
				return nil, fmt.Errorf("storage dependency %s is unavailable for %s", name, plan.Address)
			}
		}
	}
	return func(name string, w http.ResponseWriter, request *http.Request) {
		method := map[string]string{
			"/host/v1/storage/read": http.MethodGet, "/host/v1/storage/write": http.MethodPut,
			"/host/v1/storage/delete": http.MethodDelete, "/host/v1/storage/list": http.MethodGet,
		}[request.URL.Path]
		if request.Method != method && (request.URL.Path != "/host/v1/storage/read" || request.Method != http.MethodHead) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ctx := request.Context()
		if runtimeConfig.Stores[name].TenantScoped {
			invocation, _ := runtimeapi.InvocationFromContext(ctx)
			ctx = storage.WithTenantID(ctx, invocation.TenantID())
		}
		store, err := storage.Named(ctx, name)
		if err != nil {
			writeStorageProxyError(w, err)
			return
		}
		request = request.WithContext(ctx)
		if request.URL.Path == "/host/v1/storage/list" {
			handleStorageProxyList(w, request, store)
			return
		}
		query := request.URL.Query()
		keys := query["key"]
		if request.URL.Path == "/host/v1/storage/delete" && storageProxyBool(query.Get("recursive")) {
			if len(keys) > 1 || request.Header.Get("If-Match") != "" || request.Header.Get("If-None-Match") != "" {
				http.Error(w, "invalid recursive delete parameters", http.StatusBadRequest)
				return
			}
			prefix := ""
			if len(keys) == 1 {
				prefix = keys[0]
			}
			handleStorageProxyDeletePrefix(w, request, store, prefix)
			return
		}
		if len(keys) != 1 || query.Has("recursive") {
			http.Error(w, "invalid storage key or operation", http.StatusBadRequest)
			return
		}
		handleStorageProxyObject(w, request, store, keys[0])
	}, nil
}
