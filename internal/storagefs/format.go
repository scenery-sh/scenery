package storagefs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/machine"
)

const (
	ownerKind            = "scenery.storage.owner"
	generationKind       = "scenery.storage.generation"
	identityDescriptor   = `{"app_id":"string","app_root":"string","worktree_key":"string","user_id":"integer","managed":"boolean"}`
	ownerDescriptor      = `{"identity":"artifact","binding":` + identityDescriptor + `,"incarnation":"string","generation":"string","state":"string"}`
	generationDescriptor = `{"identity":"artifact","binding":` + identityDescriptor + `,"incarnation":"string","generation":"string"}`
	maxRecordBytes       = 64 << 10
)

// Binding is supplied by the retained-worktree owner, never inferred from a
// store name. Explicit external roots use Managed=false and no worktree key.
type Binding struct {
	AppID       string `json:"app_id"`
	AppRoot     string `json:"app_root"`
	WorktreeKey string `json:"worktree_key"`
	UserID      int    `json:"user_id"`
	Managed     bool   `json:"managed"`
}

func (b Binding) validate() error {
	if b.AppID == "" || !filepath.IsAbs(b.AppRoot) || filepath.Clean(b.AppRoot) != b.AppRoot || b.UserID != os.Getuid() || (b.Managed && !isHexID(b.WorktreeKey, 32)) || (!b.Managed && b.WorktreeKey != "") {
		return fmt.Errorf("%w: incomplete root authority", ErrOwnership)
	}
	return nil
}

type Owner struct {
	machine.ArtifactIdentity
	Binding     Binding `json:"binding"`
	Incarnation string  `json:"incarnation"`
	Generation  string  `json:"generation"`
	State       string  `json:"state"`
}

type generation struct {
	machine.ArtifactIdentity
	Binding     Binding `json:"binding"`
	Incarnation string  `json:"incarnation"`
	Generation  string  `json:"generation"`
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func isHexID(value string, bytes int) bool {
	if len(value) != bytes*2 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Tokens frame both the domain and values; concatenation and unscoped tenant
// identities cannot alias a different logical tuple.
func token(domain string, values ...string) string {
	h := sha256.New()
	for _, value := range append([]string{"scenery.storage." + domain}, values...) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func generationPath(id string) string { return filepath.Join("generations", id) }
