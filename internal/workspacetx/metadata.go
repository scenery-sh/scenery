package workspacetx

import "scenery.sh/internal/machine"

const (
	journalKind       = "scenery.change-transaction"
	lockKind          = "scenery.change-transaction-lock"
	journalDescriptor = `{"kind":"scenery.change-transaction","schema_revision":"digest","spec_revision":"digest","producer":"producer","owner":{"pid":"integer","started_at":"string","exe":"string","cmdline_hash":"string","agent_pid":"integer","created_by":"string","recorded_at":"datetime"},"receipt":"optional_path","directory":"path","entries":[{"path":"path","stage":"path","backup":"path","before_digest":"digest","after_digest":"optional_digest","before_exists":"boolean","after_exists":"boolean"}]}`
	lockDescriptor    = `{"kind":"scenery.change-transaction-lock","schema_revision":"digest","spec_revision":"digest","producer":"producer","owner":{"pid":"integer","started_at":"string","exe":"string","cmdline_hash":"string","agent_pid":"integer","created_by":"string","recorded_at":"datetime"},"transaction_dir":"path"}`
)

type Lock struct {
	machine.ArtifactIdentity
	Owner          Owner  `json:"owner"`
	TransactionDir string `json:"transaction_dir"`
}

type Entry struct {
	Path         string `json:"path"`
	Stage        string `json:"stage"`
	Backup       string `json:"backup"`
	BeforeDigest string `json:"before_digest"`
	AfterDigest  string `json:"after_digest,omitempty"`
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
}

type Journal struct {
	machine.ArtifactIdentity
	Owner     Owner   `json:"owner"`
	Receipt   string  `json:"receipt,omitempty"`
	Directory string  `json:"directory"`
	Entries   []Entry `json:"entries"`
}

func NewArtifacts(transactionDir, receipt string) (Lock, Journal) {
	owner := currentOwner()
	return Lock{
			ArtifactIdentity: machine.NewArtifactIdentity(lockKind, lockDescriptor),
			Owner:            owner, TransactionDir: transactionDir,
		}, Journal{
			ArtifactIdentity: machine.NewArtifactIdentity(journalKind, journalDescriptor),
			Owner:            owner, Receipt: receipt, Directory: transactionDir,
		}
}
