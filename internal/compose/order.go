package compose

// OrderedContainerIDs returns container IDs in dependency-first start order
// (same order as [BuildContainerSpecs]).
func OrderedContainerIDs(stack *Stack, project string) []string {
	names := TopologicalOrder(stack)
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = composeContainerID(project, n)
	}
	return out
}

// ReverseOrderedContainerIDs returns IDs in reverse start order (dependents first),
// suitable for stop / remove / down.
func ReverseOrderedContainerIDs(stack *Stack, project string) []string {
	ids := OrderedContainerIDs(stack, project)
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	return ids
}
