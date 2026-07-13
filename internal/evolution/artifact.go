package evolution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"scenery.sh/internal/machine"
)

const (
	changePlanKind                    = "scenery.change-plan"
	changeReceiptKind                 = "scenery.change-receipt"
	changePlanSchemaDescriptor        = machine.ExactSchemaRevision("sha256:0bda81817a4bd0abbf0e0b12bf9f495f9f1883ce95f37aaeb25e19aceb22b523")
	changeReceiptSchemaDescriptor     = machine.ExactSchemaRevision("sha256:2182ab37641ce10c18c89b2a22e9a71894c7f524ff40f5f28777e038b3f36497")
	changeTransactionJournalKind      = "scenery.change-transaction"
	changeTransactionLockKind         = "scenery.change-transaction-lock"
	changeTransactionSchemaDescriptor = `{"kind":"scenery.change-transaction","schema_revision":"digest","spec_revision":"digest","producer":"producer","owner":{"pid":"integer","started_at":"string","exe":"string","cmdline_hash":"string","agent_pid":"integer","created_by":"string","recorded_at":"datetime"},"receipt":"optional_path","directory":"path","entries":[{"path":"path","stage":"path","backup":"path","before_digest":"digest","after_digest":"optional_digest","before_exists":"boolean","after_exists":"boolean"}]}`
	changeTransactionLockDescriptor   = `{"kind":"scenery.change-transaction-lock","schema_revision":"digest","spec_revision":"digest","producer":"producer","owner":{"pid":"integer","started_at":"string","exe":"string","cmdline_hash":"string","agent_pid":"integer","created_by":"string","recorded_at":"datetime"},"transaction_dir":"path"}`
	semanticDiffKind                  = "scenery.semantic-diff"
	semanticDiffSchemaDescriptor      = `{"kind":"scenery.semantic-diff","schema_revision":"digest","spec_revision":"digest","producer":"producer","catalog_digest":"digest","base_revision":"optional_revision","target_revision":"optional_revision","view":"string","scope":"optional_string","dimensions":["string"],"changes":[{"change_id":"string","operation":"string","address":"string","expected_kind":"optional_string","base_schema_revision":"optional_revision","target_schema_revision":"optional_revision","path":"optional_path","base":"optional_any","target":"optional_any","classifications":"map","affected_artifacts":["string"],"evidence":["any"]}],"summary":{"compatible":"integer","breaking":"integer","migration_required":"integer","unknown":"integer"},"required_migrations":["any"],"generated_consequences":["string"],"risk_records":["any"],"comparison_digest":"digest"}`
	approvalTrustKind                 = "scenery.approval-trust"
	approvalTrustSchemaDescriptor     = machine.ExactSchemaRevision("sha256:c4416d44a07d767c87d6f7af40de85f78c62b468e020b54c2e31779e401d508c")
)

func decodeArtifactExact(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}
