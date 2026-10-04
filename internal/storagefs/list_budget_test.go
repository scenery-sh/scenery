package storagefs

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestListPageBudgetIncludesMixedEscapedEntriesAndCursor(t *testing.T) {
	var entries []listEntry
	for i := range 80 {
		key := fmt.Sprintf("%03d", i) + strings.Repeat("<&\"", 100)
		kind := "object"
		if i%3 == 0 {
			kind = "prefix"
			key += "/"
		}
		entries = append(entries, listEntry{key: key, kind: kind, object: Object{Key: key}})
	}
	for _, limit := range []int{1, 20, 100} {
		page, err := collectListPage(entries, limit, "binding")
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(page)
		count := len(page.Objects) + len(page.Prefixes)
		if err != nil || len(data) > MaxPageBytes || count == 0 || count > limit {
			t.Fatalf("invalid page: bytes=%d count=%d err=%v", len(data), count, err)
		}
		cursor, err := decodeCursor(page.NextCursor, "binding")
		if err != nil || compareEntry(cursor, entries[count-1]) != 0 {
			t.Fatalf("cursor does not name last entry: %+v %v", cursor, err)
		}
		if count < limit {
			if entries[count].kind == "prefix" {
				page.Prefixes = append(page.Prefixes, entries[count].key)
			} else {
				page.Objects = append(page.Objects, entries[count].object)
			}
			page.NextCursor = encodeCursor("binding", entries[count])
			next, _ := json.Marshal(page)
			if len(next) <= MaxPageBytes {
				t.Fatal("page stopped before byte budget")
			}
		}
	}
}
