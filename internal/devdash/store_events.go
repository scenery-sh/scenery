package devdash

import "sort"

func SortTraceSummariesByDuration(items []*TraceSummary) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].DurationNanos == items[j].DurationNanos {
			return items[i].StartedAt.After(items[j].StartedAt)
		}
		return items[i].DurationNanos > items[j].DurationNanos
	})
}
