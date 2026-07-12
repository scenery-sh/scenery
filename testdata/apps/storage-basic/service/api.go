package service

import (
	"context"
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
