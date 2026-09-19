// Package mcphost links the MCP implementation into a process: the private
// assistant gateway and the federation of remote MCP servers. It is imported
// for its effect by the entrypoints that serve them, the production executable
// and the development process host. A development service process answers tool
// calls the host dispatches to it and imports neither, which keeps the MCP SDK
// and its dependencies out of every service executable.
package mcphost

import (
	// Each package installs its constructor in internal/mcpapi when linked.
	_ "scenery.sh/internal/mcpfederation"
	_ "scenery.sh/internal/mcpgateway"
)
