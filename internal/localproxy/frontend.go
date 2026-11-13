package localproxy

type FrontendConfig struct {
	Name                string
	Host                string
	Root                string
	Upstream            string
	AllowSharedUpstream bool
	// Serve is the normalized dev serving mode: "" or "development" for the
	// managed dev server, "production" for a built static bundle.
	Serve string
}
