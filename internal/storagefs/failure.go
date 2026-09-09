package storagefs

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"strings"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/spec"
)

// Failure is a transport-neutral classification. Interfaces branch on typed
// errors and stable diagnostic identities, never message substrings.
type Failure struct {
	Code        string `json:"code"`
	Diagnostic  string `json:"diagnostic"`
	Message     string `json:"message"`
	ReportToken string `json:"report_token,omitempty"`
	Details     any    `json:"details,omitempty"`
	HTTPStatus  int    `json:"-"`
	ExitCode    int    `json:"-"`
}

func DescribeError(err error) (Failure, bool) {
	var partialDelete *PartialDeleteError
	var partialReclaim *PartialReclaimError
	var purge *PurgeRecoveryError
	var uncertain *atomicfile.PublicationError
	var recovery *RecoveryError
	switch {
	case errors.As(err, &recovery):
		return Failure{Code: "storage_recovery_required", Diagnostic: "SCN8008", Message: recovery.Error(), Details: recovery.Info, HTTPStatus: 409, ExitCode: 3}, true
	case errors.As(err, &partialDelete):
		return Failure{Code: "storage_partial_completion", Diagnostic: "SCN8010", Message: "Storage deletion did not fully complete; obtain a fresh preview before retrying", Details: partialDelete.Result, HTTPStatus: 409, ExitCode: 3}, true
	case errors.As(err, &partialReclaim):
		return Failure{Code: "storage_partial_completion", Diagnostic: "SCN8010", Message: "Storage reclamation did not fully complete; obtain a fresh preview before retrying", Details: partialReclaim.Result, HTTPStatus: 409, ExitCode: 3}, true
	case errors.As(err, &purge):
		return Failure{Code: "storage_recovery_required", Diagnostic: "SCN8008", Message: "Resume storage cleanup --purge with the same approved selection revision", Details: purge.Result, HTTPStatus: 409, ExitCode: 3}, true
	case errors.As(err, &uncertain):
		return Failure{Code: "storage_outcome_uncertain", Diagnostic: "SCN8007", Message: "The storage mutation may have completed; inspect current state before retrying", HTTPStatus: 409, ExitCode: 3}, true
	case errors.Is(err, ErrMigration):
		return Failure{Code: "storage_migration_required", Diagnostic: "SCN8006", Message: ErrMigration.Error() + "; see docs/runbooks/worktree-storage-migration.md", HTTPStatus: 409, ExitCode: 3}, true
	case errors.Is(err, ErrRecovery):
		return Failure{Code: "storage_recovery_required", Diagnostic: "SCN8008", Message: ErrRecovery.Error(), HTTPStatus: 409, ExitCode: 3}, true
	case errors.Is(err, ErrCorrupt):
		return Failure{Code: "storage_corrupt", Diagnostic: "SCN8009", Message: "Storage ownership or object state is incomplete or corrupt; no empty replacement was allocated", HTTPStatus: 409, ExitCode: 3}, true
	case errors.Is(err, ErrOwnership), errors.Is(err, ErrRetired):
		return Failure{Code: "storage_ownership_conflict", Diagnostic: "SCN8003", Message: "The selected storage namespace owner or incarnation does not match", HTTPStatus: 409, ExitCode: 3}, true
	case errors.Is(err, ErrPrecondition):
		return Failure{Code: "failed_precondition", Diagnostic: "SCN8003", Message: err.Error(), HTTPStatus: http.StatusPreconditionFailed, ExitCode: 3}, true
	case errors.Is(err, ErrInvalid):
		return Failure{Code: "invalid_argument", Diagnostic: "SCN8001", Message: err.Error(), HTTPStatus: 400, ExitCode: 2}, true
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrUninitialized):
		return Failure{Code: "not_found", Diagnostic: "SCN8003", Message: "Storage object or namespace is not initialized", HTTPStatus: 404, ExitCode: 3}, true
	case errors.Is(err, ErrTenantRequired):
		return Failure{Code: "tenant_required", Diagnostic: "SCN8005", Message: err.Error(), HTTPStatus: 403, ExitCode: 3}, true
	case errors.Is(err, ErrNotConfigured):
		return Failure{Code: "capability_unavailable", Diagnostic: "SCN8004", Message: err.Error(), HTTPStatus: 404, ExitCode: 3}, true
	case errors.Is(err, os.ErrPermission):
		return Failure{Code: "permission_denied", Diagnostic: "SCN8005", Message: "Permission to access storage was denied", HTTPStatus: 403, ExitCode: 5}, true
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return Failure{Code: "canceled", Diagnostic: "SCN8003", Message: err.Error(), HTTPStatus: 409, ExitCode: 3}, true
	}
	return Failure{}, false
}

func InternalFailure() Failure {
	definition, _ := spec.DiagnosticDefinitionFor("SCN9000")
	return Failure{Code: "internal", Diagnostic: "SCN9000", Message: definition.Meaning, ReportToken: "rpt_" + strings.ToLower(rand.Text()), HTTPStatus: 500, ExitCode: 10}
}
