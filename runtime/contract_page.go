package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type ContractRendererRegistration struct {
	Address              string `json:"address"`
	Runtime              string `json:"runtime"`
	Module               string `json:"module"`
	ImplementationDigest string `json:"implementation_digest"`
	ConfigJSON           string `json:"config_json,omitempty"`
}

func validateContractPagesRegistered() error {
	global.mu.RLock()
	defer global.mu.RUnlock()
	for _, page := range global.contractPages {
		if global.contractBindings[page.LoadBinding].Invoke == nil {
			return fmt.Errorf("contract page %s load binding %s is not registered", page.Address, page.LoadBinding)
		}
		for name, binding := range page.Actions {
			if global.contractBindings[binding].Invoke == nil {
				return fmt.Errorf("contract page %s action %s binding %s is not registered", page.Address, name, binding)
			}
		}
	}
	return nil
}

type ContractPageRegistration struct {
	Address     string                         `json:"address"`
	Package     string                         `json:"package"`
	Path        string                         `json:"path"`
	LoadBinding string                         `json:"load_binding"`
	Actions     map[string]string              `json:"actions"`
	Renderers   []ContractRendererRegistration `json:"renderers"`
}

func RegisterContractPage(registration ContractPageRegistration) error {
	registration.Address = strings.TrimSpace(registration.Address)
	registration.Package = strings.TrimSpace(registration.Package)
	registration.Path = strings.TrimSpace(registration.Path)
	registration.LoadBinding = strings.TrimSpace(registration.LoadBinding)
	if registration.Address == "" || registration.Package == "" || registration.Path == "" || registration.LoadBinding == "" {
		return fmt.Errorf("contract page requires address, package, path, and load binding")
	}
	actions := make(map[string]string, len(registration.Actions))
	for name, binding := range registration.Actions {
		name, binding = strings.TrimSpace(name), strings.TrimSpace(binding)
		if name == "" || binding == "" {
			return fmt.Errorf("contract page %s has an invalid action", registration.Address)
		}
		actions[name] = binding
	}
	registration.Actions = actions
	sort.Slice(registration.Renderers, func(i, j int) bool { return registration.Renderers[i].Address < registration.Renderers[j].Address })
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.contractPages == nil {
		global.contractPages = map[string]ContractPageRegistration{}
	}
	if _, exists := global.contractPages[registration.Address]; exists {
		return fmt.Errorf("duplicate contract page %s", registration.Address)
	}
	for _, existing := range global.contractPages {
		if existing.Path == registration.Path {
			return fmt.Errorf("duplicate contract page path %s", registration.Path)
		}
	}
	global.contractPages[registration.Address] = registration
	return nil
}

func ContractPages() []ContractPageRegistration {
	global.mu.RLock()
	pages := make([]ContractPageRegistration, 0, len(global.contractPages))
	for _, page := range global.contractPages {
		page.Actions = cloneContractMap(page.Actions)
		page.Renderers = append([]ContractRendererRegistration(nil), page.Renderers...)
		pages = append(pages, page)
	}
	global.mu.RUnlock()
	sort.Slice(pages, func(i, j int) bool { return pages[i].Address < pages[j].Address })
	return pages
}

// InvokeContractPageJSON executes a page load when action is empty, otherwise
// the named page action. The caller must provide a runtime invocation context;
// page dispatch cannot mint or forge a principal.
func InvokeContractPageJSON(ctx context.Context, pageAddress, action string, input []byte) ([]byte, error) {
	global.mu.RLock()
	page := global.contractPages[pageAddress]
	global.mu.RUnlock()
	if page.Address == "" {
		return nil, fmt.Errorf("contract page %s is not registered", pageAddress)
	}
	binding := page.LoadBinding
	if action != "" {
		binding = page.Actions[action]
		if binding == "" {
			return nil, fmt.Errorf("contract page %s has no action %s", pageAddress, action)
		}
	}
	return InvokeContractBindingJSON(ctx, binding, page.Package, input)
}
