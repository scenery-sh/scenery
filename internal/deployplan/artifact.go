package deployplan

import "scenery.sh/internal/machine"

const (
	deploymentPlanKind                = "scenery.deployment-plan"
	deploymentReceiptKind             = "scenery.deployment-receipt"
	providerDeploymentPlanKind        = "scenery.provider-deployment-plan"
	deploymentApplyJournalKind        = "scenery.deployment-apply-journal"
	deploymentApplyLockKind           = "scenery.deployment-apply-lock"
	deploymentStateKind               = "scenery.deployment-state"
	deploymentPlanSchemaDescriptor    = machine.ExactSchemaRevision("sha256:1f9c66d856bc96f3b208deb1c75b66e9d2689b524a033a5c19e2d991e6e8f732")
	deploymentReceiptSchemaDescriptor = machine.ExactSchemaRevision("sha256:6ebf2b8ab5d0a74a9c2a05481739f5216bc1224a2869622e45631b807de5e82d")
	providerPlanSchemaDescriptor      = `{"kind":"scenery.provider-deployment-plan","schema_revision":"digest","spec_revision":"digest","producer":"producer","provider_address":"address","provider_source":"source","provider_abi":"abi","instances":["address"],"actions":[{"kind":"string","address":"address","before":"optional_object","after":"optional_object","destructive":"optional_boolean"}],"opaque":"optional_json","requires_apply":"boolean","digest":"digest"}`
	deploymentJournalSchemaDescriptor = `{"kind":"scenery.deployment-apply-journal","schema_revision":"digest","spec_revision":"digest","producer":"producer","plan":"scenery.deployment-plan@sha256:1f9c66d856bc96f3b208deb1c75b66e9d2689b524a033a5c19e2d991e6e8f732","applied_provider_indexes":["integer"],"restore_state":"boolean","previous_state":"optional_bytes","previous_state_exists":"boolean","committed":"boolean"}`
	deploymentLockSchemaDescriptor    = `{"kind":"scenery.deployment-apply-lock","schema_revision":"digest","spec_revision":"digest","producer":"producer","owner":{"pid":"integer","started_at":"string","exe":"string","cmdline_hash":"string","agent_pid":"integer","created_by":"string","recorded_at":"datetime"}}`
	deploymentStateSchemaDescriptor   = `{"kind":"scenery.deployment-state","schema_revision":"digest","spec_revision":"digest","producer":"producer","plan":"scenery.deployment-plan@sha256:1f9c66d856bc96f3b208deb1c75b66e9d2689b524a033a5c19e2d991e6e8f732","receipt":"scenery.deployment-receipt@sha256:6ebf2b8ab5d0a74a9c2a05481739f5216bc1224a2869622e45631b807de5e82d"}`
)
