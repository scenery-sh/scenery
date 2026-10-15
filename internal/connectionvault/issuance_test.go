package connectionvault

import (
	"errors"
	"testing"
	"time"

	"scenery.sh/errs"
)

func TestVaultIssuesUnknownExpiryGrantOnce(t *testing.T) {
	for _, refreshable := range []bool{false, true} {
		v, s, p := vaultFixture(t, "independent")
		p.grant.Issuer, p.grant.Subject, p.grant.ExpiresAt = s.row.Issuer, s.row.Subject, time.Time{}
		s.row.Status = Disconnected
		if !refreshable {
			p.grant.RefreshToken = ""
		}
		if err := connectGrant(t, v, "owner", "independent", []string{"Read"}, p.grant); err != nil {
			t.Fatal(err)
		}
		if !s.row.AccessExpiresAt.IsZero() {
			t.Fatal("invented expiry")
		}
		token, err := v.Token(t.Context(), "owner", "independent", []string{"Read"})
		if err != nil || token != p.grant.AccessToken || p.calls != 0 || len(s.row.AccessCiphertext) != 0 {
			t.Fatalf("initial issuance: %v", err)
		}
		for range 2 {
			token, err = v.Token(t.Context(), "owner", "independent", []string{"Read"})
			if refreshable {
				if err != nil || token != p.grant.AccessToken || len(s.row.AccessCiphertext) != 0 {
					t.Fatalf("refresh issuance: %v", err)
				}
			} else {
				status, statusErr := v.Status(t.Context(), "owner", "independent")
				if errs.Code(err) != errs.ConnectionReauthRequired || token != "" || statusErr != nil || status.Status != ReauthRequired || status.ReauthReason != "refresh_credential_missing" {
					t.Fatalf("exhausted unknown-expiry grant: %v, status=%#v, statusErr=%v", err, status, statusErr)
				}
			}
		}
		if refreshable && p.calls != 2 {
			t.Fatal("refreshed unknown-expiry token was issued twice")
		}
	}
}

func TestVaultUnknownExpiryIssuanceRequiresCommit(t *testing.T) {
	for _, commit := range []bool{false, true} {
		v, s, _ := vaultFixture(t, "independent")
		s.row.AccessExpiresAt = time.Time{}
		if commit {
			s.commitErr = errors.New("commit failed")
		} else {
			s.saveErr = errors.New("save failed")
		}
		if token, err := v.Token(t.Context(), "owner", "independent", nil); token != "" || errs.Code(err) != errs.ConnectionUnavailable {
			t.Fatalf("uncommitted issuance: %v", err)
		}
		if len(s.row.AccessCiphertext) == 0 {
			t.Fatal("failed transaction changed the grant")
		}
	}
}

func TestVaultUsesNonrefreshableGrantUntilActualExpiry(t *testing.T) {
	v, s, p := vaultFixture(t, "independent")
	p.grant.Issuer, p.grant.Subject = s.row.Issuer, s.row.Subject
	p.grant.ExpiresAt, p.grant.RefreshToken = testNow.Add(30*time.Second), ""
	s.row.Status = Disconnected
	if err := connectGrant(t, v, "owner", "independent", []string{"Read"}, p.grant); err != nil {
		t.Fatal(err)
	}
	for _, elapsed := range []time.Duration{0, 29 * time.Second, 30 * time.Second, 31 * time.Second} {
		v.now = func() time.Time { return testNow.Add(elapsed) }
		token, err := v.Token(t.Context(), "owner", "independent", []string{"Read"})
		if elapsed < 30*time.Second {
			if err != nil || token != p.grant.AccessToken {
				t.Fatalf("valid short grant at %s: %v", elapsed, err)
			}
		} else {
			status, statusErr := v.Status(t.Context(), "owner", "independent")
			if token != "" || errs.Code(err) != errs.ConnectionReauthRequired || statusErr != nil || status.Status != ReauthRequired || status.ReauthReason != "refresh_credential_missing" {
				t.Fatalf("expired grant at %s: %v, status=%#v, statusErr=%v", elapsed, err, status, statusErr)
			}
		}
	}
	if p.calls != 0 || !s.row.AccessExpiresAt.Equal(p.grant.ExpiresAt) {
		t.Fatal("nonrefreshable expiry was changed or provider was refreshed")
	}
}

func TestVaultRetainsEarlyRefreshForRefreshableGrants(t *testing.T) {
	v, s, p := vaultFixture(t, "independent")
	s.row.AccessExpiresAt = testNow.Add(30 * time.Second)
	token, err := v.Token(t.Context(), "owner", "independent", []string{"Read"})
	if err != nil || token != p.grant.AccessToken || p.calls != 1 {
		t.Fatalf("early refresh: %v, calls=%d", err, p.calls)
	}
}

func TestVaultDisconnectAlwaysExpiresPendingStates(t *testing.T) {
	for _, state := range []string{Active, Disconnected, "absent"} {
		for _, fail := range []bool{false, true} {
			v, s, p := vaultFixture(t, "independent")
			s.row.Status, s.present, s.pendingStates = state, state != "absent", 2
			if fail {
				s.expireErr = errors.New("expiry failed")
			}
			status, err := v.Disconnect(t.Context(), "owner", "independent")
			if fail {
				if errs.Code(err) != errs.ConnectionUnavailable || s.pendingStates != 2 || s.checkpoints != 0 || p.revocations != 0 {
					t.Fatalf("expiry failure: %v", err)
				}
			} else if err != nil || status.Status != Disconnected || s.pendingStates != 0 || s.expiredStates != 2 {
				t.Fatalf("disconnect did not expire pending states: %v", err)
			}
		}
	}
}

func TestVaultExhaustedGrantRequiresCommittedReauthStatus(t *testing.T) {
	for _, commit := range []bool{false, true} {
		v, s, p := vaultFixture(t, "independent")
		s.row.AccessExpiresAt, s.row.RefreshCiphertext = testNow, nil
		if commit {
			s.commitErr = errors.New("commit failed")
		} else {
			s.saveErr = errors.New("save failed")
		}
		if token, err := v.Token(t.Context(), "owner", "independent", nil); token != "" || errs.Code(err) != errs.ConnectionUnavailable || s.row.Status != Active || p.calls != 0 {
			t.Fatalf("uncommitted reauth transition: token=%q err=%v status=%s", token, err, s.row.Status)
		}
	}
}
