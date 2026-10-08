package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestQueryFramingAssertionsCannotSatisfyExpectedFailure(t *testing.T) {
	t.Parallel()
	queryErr, assertionErr := errors.New("product failure"), errors.New("prefix assertion failed")
	for _, item := range []struct {
		name               string
		wantError          bool
		queryErr, checkErr error
		reject             bool
		preserved          error
	}{
		{"healthy", false, nil, nil, false, nil},
		{"expected product failure", true, queryErr, nil, false, nil},
		{"expected held cancellation", true, context.Canceled, nil, false, nil},
		{"missing product failure", true, nil, nil, true, nil},
		{"unexpected product failure", false, queryErr, nil, true, queryErr},
		{"assertion on healthy call", false, nil, assertionErr, true, assertionErr},
		{"callback assertion as expected failure", true, assertionErr, assertionErr, true, assertionErr},
		{"separate assertion and expected product failure", true, queryErr, assertionErr, true, assertionErr},
		{"private response in product error", true, errors.New("private-framing-token"), nil, true, nil},
	} {
		t.Run(item.name, func(t *testing.T) {
			err := validateFramingOutcome(item.wantError, item.queryErr, item.checkErr)
			if (err != nil) != item.reject || item.preserved != nil && !errors.Is(err, item.preserved) {
				t.Fatalf("err=%v reject=%t preserved=%v", err, item.reject, item.preserved)
			}
			if err != nil && strings.Contains(err.Error(), "private-framing-token") {
				t.Fatalf("private fixture bytes exposed: %v", err)
			}
		})
	}
}
