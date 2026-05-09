package main

import (
	"context"

	"github.com/pbrazdil/onlava/data"
)

func openStore(ctx context.Context, db data.DB) (*data.Store, error) {
	return data.Open(ctx, db, data.Options{})
}

func companySetup(ctx context.Context, store *data.Store, actor data.Actor) error {
	if _, err := store.CreateObject(ctx, actor, data.CreateObjectRequest{
		TenantKey:    "acme",
		NameSingular: "company",
		NamePlural:   "companies",
	}); err != nil {
		return err
	}
	if _, err := store.CreateField(ctx, actor, "company", data.CreateFieldRequest{
		TenantKey: "acme",
		Name:      "name",
		Type:      data.FieldText,
	}); err != nil {
		return err
	}
	if _, err := store.CreateField(ctx, actor, "company", data.CreateFieldRequest{
		TenantKey: "acme",
		Name:      "stage",
		Type:      data.FieldSelect,
		Options: []data.FieldOptionRequest{
			{Value: "lead"},
			{Value: "won"},
		},
	}); err != nil {
		return err
	}
	_, err := store.CreateView(ctx, actor, "company", data.CreateViewRequest{
		TenantKey:  "acme",
		Name:       "won_companies",
		Columns:    []string{"name", "stage"},
		Filter:     data.EQ("stage", "won"),
		Sort:       []data.Sort{data.Asc("name")},
		Visibility: data.ViewVisibilityShared,
	})
	return err
}

func queryCompanies(ctx context.Context, store *data.Store, actor data.Actor, cursor string) (*data.RecordPage, error) {
	return store.QueryRecords(ctx, actor, "company", data.QueryRecordsRequest{
		TenantKey: "acme",
		Query: data.Query{
			Select: []string{"name", "stage"},
			Filter: data.EQ("stage", "won"),
			Sort:   []data.Sort{data.Asc("name")},
			Limit:  50,
			Cursor: cursor,
		},
	})
}
