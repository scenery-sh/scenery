package evolution

import (
	"sort"
	"strings"

	"scenery.sh/internal/graph"
)

func plannedRenameReceipts(base, target *Manifest, operations []SemanticOperation) []RenameReceipt {
	baseResources := resourcesByAddress(base)
	targetResources := resourcesByAddress(target)
	type renameState struct {
		from     string
		current  string
		resource Resource
	}
	statesByCurrent := map[string]*renameState{}
	for _, operation := range operations {
		if operation.Op != "resource.rename" {
			continue
		}
		state := statesByCurrent[operation.Address]
		if state == nil {
			resource, ok := baseResources[operation.Address]
			if !ok {
				continue
			}
			state = &renameState{from: operation.Address, current: operation.Address, resource: resource}
		}
		newName, ok := operation.Value.(string)
		if !ok {
			continue
		}
		delete(statesByCurrent, state.current)
		state.current = graph.ResourceAddress(state.resource.Module, blockTypeForKind(state.resource.Kind), newName)
		statesByCurrent[state.current] = state
	}
	receipts := []RenameReceipt{}
	seenReceipts := map[string]bool{}
	addReceipt := func(from, to string) {
		key := from + "\x00" + to
		if seenReceipts[key] {
			return
		}
		seenReceipts[key] = true
		receipt := RenameReceipt{From: from, To: to, BaseContractRevision: base.ContractRevision, TargetContractRevision: target.ContractRevision}
		receipt.Digest = renameReceiptDigest(receipt)
		receipts = append(receipts, receipt)
	}
	for _, state := range statesByCurrent {
		renamed, exists := targetResources[state.current]
		if !exists || renamed.Kind != state.resource.Kind {
			continue
		}
		addReceipt(state.from, state.current)
		if state.resource.Kind != "scenery.module" {
			continue
		}
		oldInstance, newInstance := moduleInstancePath(state.resource), moduleInstancePath(renamed)
		for _, descendant := range base.Resources {
			if !strings.HasPrefix(descendant.Address, oldInstance+"/") {
				continue
			}
			to := newInstance + strings.TrimPrefix(descendant.Address, oldInstance)
			targetDescendant, ok := targetResources[to]
			if !ok || !sameRenameLineage(descendant, targetDescendant) {
				continue
			}
			addReceipt(descendant.Address, to)
		}
	}
	sort.Slice(receipts, func(i, j int) bool {
		if receipts[i].From != receipts[j].From {
			return receipts[i].From < receipts[j].From
		}
		return receipts[i].To < receipts[j].To
	})
	return receipts
}

func sameRenameLineage(base, target Resource) bool {
	return base.Kind == target.Kind && base.Name == target.Name && base.Origin.Kind == target.Origin.Kind && base.Origin.SourceID != "" && base.Origin.SourceID == target.Origin.SourceID
}

func blockTypeForKind(kind string) string {
	return strings.ReplaceAll(strings.TrimPrefix(kind, "scenery."), "-", "_")
}

func moduleInstancePath(resource Resource) string {
	if resource.Module == "app" || resource.Module == "" {
		return resource.Name
	}
	return resource.Module + "/" + resource.Name
}
