// Package lifecycle validates and executes dependency plans. Domain owners keep
// their data, caching, validation and publication; this package only schedules.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
)

type ID string
type Kind string
type Capability string
type CachePolicy string
type Ownership string

const (
	ReadSource       Capability = "source.read"
	WriteCache       Capability = "cache.write"
	PublishArtifacts Capability = "artifacts.publish"
	UseScratch       Capability = "scratch.use"
	MutateDatabase   Capability = "database.mutate"
	ControlRuntime   Capability = "runtime.control"

	Reusable          CachePolicy = "reusable"
	CurrentValidation CachePolicy = "current_validation"
	ExternalEffect    CachePolicy = "external_effect"

	Private  Ownership = "private"
	Managed  Ownership = "managed"
	External Ownership = "external"
)

// Reference binds an authored digest or an exact producer output. Empty Path
// denotes an in-memory value; filesystem paths are canonical workspace-relative.
type Reference struct {
	Name      string    `json:"name"`
	Kind      Kind      `json:"kind"`
	Path      string    `json:"path,omitempty"`
	Digest    string    `json:"digest,omitempty"`
	Producer  ID        `json:"producer,omitempty"`
	Ownership Ownership `json:"ownership,omitempty"`
	// Owner names a shared publication node; otherwise the producer owns its path.
	Owner ID `json:"owner,omitempty"`
}

type Node struct {
	ID           ID                          `json:"id"`
	Kind         Kind                        `json:"kind"`
	Dependencies []ID                        `json:"dependencies"`
	Inputs       []Reference                 `json:"inputs"`
	Outputs      []Reference                 `json:"outputs"`
	Identity     map[string]string           `json:"identity,omitempty"`
	Cache        CachePolicy                 `json:"cache_policy"`
	Effects      []Capability                `json:"effects"`
	Action       func(context.Context) error `json:"-"`
}

// Plan is validated and cloned before it can execute. No action or reusable
// cache entry can grant a capability that was absent from the selected closure.
type Plan struct {
	nodes map[ID]Node
	order []ID
}

func New(nodes []Node) (*Plan, error) {
	p := &Plan{nodes: make(map[ID]Node, len(nodes))}
	for _, node := range nodes {
		if node.ID == "" || node.Kind == "" {
			return nil, fmt.Errorf("invalid_request: lifecycle node needs an id and kind")
		}
		if _, exists := p.nodes[node.ID]; exists {
			return nil, fmt.Errorf("invalid_request: duplicate lifecycle node %s", node.ID)
		}
		if node.Cache != Reusable && node.Cache != CurrentValidation && node.Cache != ExternalEffect {
			return nil, fmt.Errorf("invalid_request: invalid cache policy on %s", node.ID)
		}
		node.Dependencies = sortedUnique(node.Dependencies)
		node.Effects = sortedUnique(node.Effects)
		node.Inputs = slices.Clone(node.Inputs)
		node.Outputs = slices.Clone(node.Outputs)
		if node.Dependencies == nil {
			node.Dependencies = []ID{}
		}
		if node.Effects == nil {
			node.Effects = []Capability{}
		}
		if node.Inputs == nil {
			node.Inputs = []Reference{}
		}
		if node.Outputs == nil {
			node.Outputs = []Reference{}
		}
		if node.Identity != nil {
			identity := make(map[string]string, len(node.Identity))
			for k, v := range node.Identity {
				identity[k] = v
			}
			node.Identity = identity
		}
		for _, effect := range node.Effects {
			if effect != ReadSource && effect != WriteCache && effect != PublishArtifacts && effect != UseScratch && effect != MutateDatabase && effect != ControlRuntime {
				return nil, fmt.Errorf("invalid_request: unknown lifecycle capability %s", effect)
			}
			if node.Cache != ExternalEffect && (effect == MutateDatabase || effect == ControlRuntime || effect == UseScratch || effect == PublishArtifacts) {
				return nil, fmt.Errorf("invalid_request: external lifecycle effect %s cannot be reusable", node.ID)
			}
		}
		seen := map[string]bool{}
		for _, input := range node.Inputs {
			if input.Name == "" || input.Kind == "" || seen[input.Name] {
				return nil, fmt.Errorf("invalid_request: duplicate or invalid input on %s", node.ID)
			}
			seen[input.Name] = true
		}
		clear(seen)
		for _, output := range node.Outputs {
			if output.Name == "" || output.Kind == "" || seen[output.Name] {
				return nil, fmt.Errorf("invalid_request: duplicate or invalid output on %s", node.ID)
			}
			seen[output.Name] = true
			if output.Ownership != Private && output.Ownership != Managed && output.Ownership != External {
				return nil, fmt.Errorf("invalid_request: missing output ownership on %s", node.ID)
			}
		}
		for _, ref := range append(slices.Clone(node.Inputs), node.Outputs...) {
			if ref.Path != "" && (path.Clean(ref.Path) != ref.Path || path.IsAbs(ref.Path) || ref.Path == ".." || strings.HasPrefix(ref.Path, "../") || strings.Contains(ref.Path, "\\")) {
				return nil, fmt.Errorf("invalid_request: unsafe lifecycle path %s", ref.Path)
			}
		}
		p.nodes[node.ID] = node
	}
	// Validate every node, including unselected branches, before running any work.
	owners := map[string]ID{}
	for _, node := range p.nodes {
		for _, dependency := range node.Dependencies {
			if _, ok := p.nodes[dependency]; !ok {
				return nil, fmt.Errorf("invalid_request: missing lifecycle dependency %s", dependency)
			}
		}
		for _, input := range node.Inputs {
			if input.Producer == "" {
				continue
			}
			producer, ok := p.nodes[input.Producer]
			if !ok || !slices.Contains(node.Dependencies, input.Producer) {
				return nil, fmt.Errorf("invalid_request: missing lifecycle producer %s for %s", input.Producer, node.ID)
			}
			if !slices.ContainsFunc(producer.Outputs, func(output Reference) bool {
				return output.Name == input.Name && output.Kind == input.Kind && output.Path == input.Path
			}) {
				return nil, fmt.Errorf("invalid_request: missing lifecycle output %s from %s", input.Name, input.Producer)
			}
		}
		for _, output := range node.Outputs {
			if output.Path == "" {
				continue
			}
			owner := node.ID
			if output.Owner != "" && output.Owner != node.ID {
				publication, ok := p.nodes[output.Owner]
				if !ok || !slices.Contains(publication.Effects, PublishArtifacts) || !p.dependsOn(output.Owner, node.ID) {
					return nil, fmt.Errorf("invalid_request: output owner %s must publish the dependency %s", output.Owner, node.ID)
				}
				owner = output.Owner
			}
			for owned, existing := range owners {
				if existing != owner && (output.Path == owned || strings.HasPrefix(output.Path, owned+"/") || strings.HasPrefix(owned, output.Path+"/")) {
					return nil, fmt.Errorf("invalid_request: overlapping lifecycle output owners %s and %s", existing, owner)
				}
			}
			owners[output.Path] = owner
		}
	}
	ids := make([]ID, 0, len(p.nodes))
	for id := range p.nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	visiting, visited := map[ID]bool{}, map[ID]bool{}
	var visit func(ID) error
	visit = func(id ID) error {
		if visiting[id] {
			return fmt.Errorf("invalid_request: lifecycle cycle at %s", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range p.nodes[id].Dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		p.order = append(p.order, id)
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *Plan) dependsOn(owner, producer ID) bool {
	pending := []ID{owner}
	seen := map[ID]bool{}
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if id == producer {
			return true
		}
		if !seen[id] {
			seen[id] = true
			pending = append(pending, p.nodes[id].Dependencies...)
		}
	}
	return false
}

// Nodes returns dependency order for inspection; callers cannot mutate the plan.
func (p *Plan) Nodes() []Node {
	nodes := make([]Node, 0, len(p.order))
	for _, id := range p.order {
		n := p.nodes[id]
		n.Dependencies = slices.Clone(n.Dependencies)
		n.Effects = slices.Clone(n.Effects)
		n.Inputs = slices.Clone(n.Inputs)
		n.Outputs = slices.Clone(n.Outputs)
		n.Identity = nil
		for k, v := range p.nodes[id].Identity {
			if n.Identity == nil {
				n.Identity = map[string]string{}
			}
			n.Identity[k] = v
		}
		nodes = append(nodes, n)
	}
	return nodes
}

func (p *Plan) Run(ctx context.Context, targets []ID, allowed []Capability) error {
	selected := map[ID]bool{}
	var selectNode func(ID) error
	selectNode = func(id ID) error {
		node, ok := p.nodes[id]
		if !ok {
			return fmt.Errorf("invalid_request: unknown lifecycle target %s", id)
		}
		if selected[id] {
			return nil
		}
		selected[id] = true
		for _, dependency := range node.Dependencies {
			if err := selectNode(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range targets {
		if err := selectNode(id); err != nil {
			return err
		}
	}
	for _, id := range p.order {
		if !selected[id] {
			continue
		}
		for _, effect := range p.nodes[id].Effects {
			if !slices.Contains(allowed, effect) {
				return fmt.Errorf("permission_denied: lifecycle node %s requires %s", id, effect)
			}
		}
		if p.nodes[id].Action == nil {
			return fmt.Errorf("failed_precondition: lifecycle node %s has no action", id)
		}
	}
	for _, id := range p.order {
		if !selected[id] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.nodes[id].Action(ctx); err != nil {
			return err
		}
	}
	// The last successful action defines completion. A cancellation observed
	// afterward must not relabel an already committed external effect as failure.
	return nil
}

// Key is a domain-separated canonical identity. Dependency output digests are
// supplied by their owner; the executor never treats a key as validation proof.
func Key(node Node, dependencyDigests map[string]string) (string, error) {
	node.Action = nil
	node.Dependencies = sortedUnique(node.Dependencies)
	node.Effects = sortedUnique(node.Effects)
	node.Inputs = slices.Clone(node.Inputs)
	node.Outputs = slices.Clone(node.Outputs)
	less := func(a, b Reference) bool { return a.Name < b.Name }
	sort.Slice(node.Inputs, func(i, j int) bool { return less(node.Inputs[i], node.Inputs[j]) })
	sort.Slice(node.Outputs, func(i, j int) bool { return less(node.Outputs[i], node.Outputs[j]) })
	data, err := json.Marshal([]any{"scenery.lifecycle.node", 1, node, dependencyDigests})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func sortedUnique[T ~string](values []T) []T {
	values = slices.Clone(values)
	slices.Sort(values)
	return slices.Compact(values)
}
