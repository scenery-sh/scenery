package runtime

// The runtime reaches its MCP implementation through internal/mcpapi, which a
// process links by importing scenery.sh/runtime/mcphost. These tests exercise
// that implementation, so the test binary links it the same way.
import _ "scenery.sh/runtime/mcphost"
