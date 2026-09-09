package storagefs

import "context"

type NamespaceStatistics struct {
	Total  Stats
	Stores map[string]Stats
}

// Statistics computes exact totals in one complete locked metadata scan.
// Store output is bounded by the caller's declared store-name set.
func (n *Namespace) Statistics(ctx context.Context, names []string) (NamespaceStatistics, error) {
	lease, err := n.acquire(ctx, false)
	if err != nil {
		return NamespaceStatistics{}, err
	}
	defer func() { _ = lease.Close() }()
	mutation, err := lockFile(ctx, lease.root, "mutation.lock", false)
	if err != nil {
		return NamespaceStatistics{}, err
	}
	defer func() { _ = mutation.Close() }()
	result := NamespaceStatistics{Stores: make(map[string]Stats, len(names))}
	for _, name := range names {
		result.Stores[name] = Stats{}
	}
	err = scanAllReferences(ctx, lease, func(ref reference) error {
		if err := addTotals(&result.Total.Objects, &result.Total.Bytes, ref.Object.SizeBytes); err != nil {
			return err
		}
		if stats, ok := result.Stores[ref.Object.Store]; ok {
			if err := addTotals(&stats.Objects, &stats.Bytes, ref.Object.SizeBytes); err != nil {
				return err
			}
			result.Stores[ref.Object.Store] = stats
		}
		return nil
	})
	if err != nil {
		return NamespaceStatistics{}, err
	}
	return result, nil
}
