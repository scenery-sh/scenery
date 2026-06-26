package storage

import "fmt"

type InvalidKeyError struct {
	Key    string
	Reason string
}

func (e *InvalidKeyError) Error() string {
	if e == nil {
		return "invalid storage key"
	}
	if e.Reason == "" {
		return fmt.Sprintf("invalid storage key %q", e.Key)
	}
	return fmt.Sprintf("invalid storage key %q: %s", e.Key, e.Reason)
}

type NotFoundError struct {
	Store string
	Key   string
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return "storage object not found"
	}
	if e.Store == "" {
		return fmt.Sprintf("storage object %q not found", e.Key)
	}
	return fmt.Sprintf("storage object %q/%q not found", e.Store, e.Key)
}

type AlreadyExistsError struct {
	Store string
	Key   string
}

func (e *AlreadyExistsError) Error() string {
	if e == nil {
		return "storage object already exists"
	}
	if e.Store == "" {
		return fmt.Sprintf("storage object %q already exists", e.Key)
	}
	return fmt.Sprintf("storage object %q/%q already exists", e.Store, e.Key)
}

type NotConfiguredError struct {
	Store string
}

func (e *NotConfiguredError) Error() string {
	if e == nil || e.Store == "" {
		return "scenery storage is not configured"
	}
	return fmt.Sprintf("scenery storage store %q is not configured", e.Store)
}

type TenantRequiredError struct {
	Store string
}

func (e *TenantRequiredError) Error() string {
	if e == nil || e.Store == "" {
		return "scenery storage tenant is required"
	}
	return fmt.Sprintf("scenery storage store %q requires a tenant", e.Store)
}
