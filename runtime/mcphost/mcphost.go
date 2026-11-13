// Package mcphost links the MCP implementation into a process: the private
// assistant gateway. It is imported for its effect by the entrypoints that
// serve it, the production executable
// and the development process host. A development service process answers tool
// calls the host dispatches to it and imports neither, which keeps the MCP SDK
// and its dependencies out of every service executable.
package mcphost

import (
	// The gateway installs its constructor in internal/mcpapi when linked.
	_ "scenery.sh/internal/mcpgateway"
)
