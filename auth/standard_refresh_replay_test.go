package auth

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"scenery.sh/errs"
)

func TestRefreshReplayRevokesSessionAcrossTransaction(t *testing.T) {
	failure := errors.New("transaction failed")
	for _, mode := range []string{"commit", "revoke fails", "commit fails"} {
		t.Run(mode, func(t *testing.T) {
			revoke := authSQLStep{name: "RevokeRefreshSession", check: func(_ string, args []driver.NamedValue) {
				if args[0].Value != "refresh_replay" {
					t.Fatalf("revocation reason=%v", args)
				}
			}}
			if mode == "revoke fails" {
				revoke.err = failure
			}
			svc, script := authSQLService(t, authSQLStep{name: "GetRefreshSessionByID", rows: [][]driver.Value{authSQLSessionRow("new-token-hash")}}, revoke)
			if mode == "commit fails" {
				script.commitErr = failure
			}
			_, err := svc.Refresh(t.Context(), &RefreshParams{RefreshToken: "11111111-1111-1111-1111-111111111111.replayed-secret"})
			if mode == "commit" {
				if errs.Code(err) != errs.Unauthenticated {
					t.Fatalf("replay error=%v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("failure=%v", err)
			}
			end := "commit"
			if mode == "revoke fails" {
				end = "rollback"
			}
			if !reflect.DeepEqual(script.events, []string{"begin", "GetRefreshSessionByID", "RevokeRefreshSession", end}) {
				t.Fatalf("transaction events=%v", script.events)
			}
		})
	}
}
