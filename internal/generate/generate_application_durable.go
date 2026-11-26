package generate

func executionForBinding(resources map[string]Resource, binding Resource) (Resource, bool) {
	address := resolveResourceRef(binding, refString(binding.Spec["execution"]), "execution")
	execution, ok := resources[address]
	return execution, ok && execution.Kind == "scenery.execution"
}
