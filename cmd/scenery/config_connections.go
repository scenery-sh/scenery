package main

import "io"

// configMigrateConnectionsCommand uses the selected environment's authority,
// including its existing SSH receiver, without exporting old secret values.
func configMigrateConnectionsCommand(stdout io.Writer, args []string) error {
	opts, positionals, err := parseConfigOptions("migrate-connections", args, true)
	if err != nil {
		return err
	}
	if len(positionals) != 0 {
		return usageErrorf("config migrate-connections takes no positional arguments")
	}
	session, err := openConfigSession(opts)
	if err != nil {
		return err
	}
	configured := false
	for _, provider := range session.cfg.Auth.Connections {
		if provider.ID == "google" && provider.Preset == "google" {
			configured = true
		}
	}
	if !configured {
		return preconditionErrorf("declare the google connection preset in auth.connections before migrating its environment credentials")
	}
	return session.applyMutation(stdout, opts, configRemoteRequest{Operation: "migrate_connections", Key: "auth.connections.google", ExpectRevision: opts.ExpectRevision})
}
