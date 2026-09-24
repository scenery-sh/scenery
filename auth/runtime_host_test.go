package auth

// Standard auth reaches the runtime through the appsdk host, which the runtime
// registers from its package initialization; link it into these tests.
import _ "scenery.sh/runtime"
