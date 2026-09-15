// Package text is ordinary Go code compiled into every service that imports
// it, so an edit here replaces both service processes in one generation.
package text

// Label prefixes a message with the answering service.
func Label(service, message string) string {
	return service + ":" + message
}
