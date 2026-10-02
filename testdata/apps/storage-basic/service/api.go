package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	servicecontract "example.com/storagebasic/service/scenerycontract"
	"scenery.sh/storage"
)

type Service struct{}

func NewService(context.Context, servicecontract.ServiceConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (*Service) PublicStorageProbe(ctx context.Context, _ servicecontract.PublicStorageProbeInput) (servicecontract.PublicStorageProbeOutcome, error) {
	if err := verifyEmptyPrefixTenantIsolation(ctx); err != nil {
		return nil, err
	}
	ctx = storage.WithTenantID(ctx, "storage-probe")
	summary, err := runStorageProbe(ctx, "probe/public.txt", "hello public")
	if err != nil {
		return nil, err
	}
	return servicecontract.PublicStorageProbeOk{Value: summary}, nil
}

func (*Service) ReadPublicStorageProbe(ctx context.Context, _ servicecontract.ReadPublicStorageProbeInput) (servicecontract.ReadPublicStorageProbeOutcome, error) {
	ctx = storage.WithTenantID(ctx, "storage-probe")
	summary, err := readStorageProbe(ctx, "probe/public.txt")
	if err != nil {
		return nil, err
	}
	return servicecontract.ReadPublicStorageProbeOk{Value: summary}, nil
}

func runStorageProbe(ctx context.Context, key, value string) (servicecontract.ObjectSummary, error) {
	store, err := storage.Default(ctx)
	if err != nil {
		return servicecontract.ObjectSummary{}, err
	}
	obj, err := store.Put(ctx, key, strings.NewReader(value), storage.PutOptions{ContentType: "text/plain"})
	if err != nil {
		return servicecontract.ObjectSummary{}, err
	}
	return readObjectSummary(ctx, store, obj.Key, obj.SizeBytes)
}

func readStorageProbe(ctx context.Context, key string) (servicecontract.ObjectSummary, error) {
	store, err := storage.Default(ctx)
	if err != nil {
		return servicecontract.ObjectSummary{}, err
	}
	obj, err := store.Head(ctx, key)
	if err != nil {
		return servicecontract.ObjectSummary{}, err
	}
	return readObjectSummary(ctx, store, key, obj.SizeBytes)
}

func readObjectSummary(ctx context.Context, store storage.Store, key string, size int64) (servicecontract.ObjectSummary, error) {
	body, _, err := store.Get(ctx, key, storage.GetOptions{})
	if err != nil {
		return servicecontract.ObjectSummary{}, err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return servicecontract.ObjectSummary{}, err
	}
	return servicecontract.ObjectSummary{Key: key, SizeBytes: size, Body: string(data)}, nil
}

// The storage native probe owns Unix proxy transport and durable filesystem proof.
func verifyEmptyPrefixTenantIsolation(ctx context.Context) error {
	store, err := storage.Default(ctx)
	if err != nil {
		return err
	}
	tenantA := storage.WithTenantID(ctx, "storage-probe-empty-a")
	tenantB := storage.WithTenantID(ctx, "storage-probe-empty-b")
	for _, tenant := range []context.Context{tenantA, tenantB} {
		if _, err := store.Put(tenant, "a", strings.NewReader("tenant object"), storage.PutOptions{}); err != nil {
			return err
		}
	}
	if err := store.DeletePrefix(tenantA, ""); err != nil {
		return err
	}
	var missing *storage.NotFoundError
	if _, err := store.Head(tenantA, "a"); !errors.As(err, &missing) {
		return fmt.Errorf("empty-prefix deletion left tenant A object: %v", err)
	}
	if object, err := store.Head(tenantB, "a"); err != nil || object.Key != "a" {
		return fmt.Errorf("empty-prefix deletion affected tenant B: %v", err)
	}
	return store.DeletePrefix(tenantB, "")
}
