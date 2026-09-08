package repoinfo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"scenery.sh/internal/spec"
)

const (
	IndexKind           = "scenery.docs.index"
	docsIndexDescriptor = `{"kind":"scenery.docs.index","identity":"source","generated_at":"datetime","owner_default":"string","freshness_policy":"policy","documents":"documents","plans":"paths","tech_debt":"path"}`
	InspectKind         = "scenery.inspect.docs"
)

var IndexSchemaRevision = string(spec.SchemaRevision(docsIndexDescriptor))

var execPlanPathPattern = regexp.MustCompile(`^docs/plans/[0-9]{4}-.+\.md$`)

func IsExecPlanPath(path string) bool { return execPlanPathPattern.MatchString(path) }

func ReadKnowledge(repoRoot string) (KnowledgeIndex, error) {
	path := filepath.Join(repoRoot, "docs", "knowledge.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return KnowledgeIndex{}, err
	}
	var index KnowledgeIndex
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&index); err != nil {
		return KnowledgeIndex{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return KnowledgeIndex{}, fmt.Errorf("docs/knowledge.json contains trailing JSON")
	}
	if index.Kind != IndexKind || index.SchemaRevision != IndexSchemaRevision {
		return KnowledgeIndex{}, fmt.Errorf("docs/knowledge.json identity = %q at %q, want %q at %q", index.Kind, index.SchemaRevision, IndexKind, IndexSchemaRevision)
	}
	return index, nil
}

func docsReviewDue(value string, now time.Time) bool {
	if value == "" {
		return false
	}
	reviewAfter, err := time.Parse("2006-01-02", value)
	if err != nil {
		return false
	}
	return !reviewAfter.After(now)
}

func DocumentReviewDue(doc KnowledgeDocument, now time.Time) bool {
	if IsCompletedPlan(doc) {
		return false
	}
	return docsReviewDue(doc.ReviewAfter, now)
}

func IsCompletedPlan(doc KnowledgeDocument) bool {
	return doc.Status == "completed" && IsExecPlanPath(doc.Path)
}
