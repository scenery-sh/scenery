package nativecall

import "sync/atomic"

type RegistryOwner func() *Registry

var activeOwner atomic.Pointer[RegistryOwner]

func Bind(owner RegistryOwner) {
	if owner == nil {
		panic("scenery: nil internal call registry owner")
	}
	if !activeOwner.CompareAndSwap(nil, &owner) {
		panic("scenery: internal call registry owner already bound")
	}
}

func Current() *Registry {
	if owner := activeOwner.Load(); owner != nil {
		return (*owner)()
	}
	return nil
}
