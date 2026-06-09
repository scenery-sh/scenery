package neonselfhost

func newTestBackendState(projectName, tenantID string, pgVersion int) BackendState {
	state := NewBackendState()
	project := NewBackendProject(projectName, pgVersion)
	project.TenantID = tenantID
	state.Projects[projectName] = project
	return state
}
