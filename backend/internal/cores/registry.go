// SPDX-License-Identifier: AGPL-3.0-or-later

package cores

import (
	"fmt"
	"sort"
	"sync"
)

// Registry maps a provider name (e.g. "sing-box") to the
// CoreProvider instance. The registry is process-global and
// concurrency-safe; providers self-register from a package
// `init()` so the panel binary picks them up by importing the
// subpackage.
//
// The registry is intentionally simple: no priority, no
// fallback, no per-node routing. Those concerns belong to
// the panel's render layer, not the registry.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]CoreProvider
}

// ErrDuplicateName is returned by Register when a provider
// with the same Name() has already been registered. Two
// providers claiming the same name is a programming error,
// not a runtime one, and we surface it loudly.
var ErrDuplicateName = fmt.Errorf("cores: provider name already registered")

// NewRegistry returns an empty registry. The package also
// exposes a package-global registry via the Default /
// Register / Get / List helpers below; tests can use
// NewRegistry to get an isolated instance.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]CoreProvider)}
}

// Default is the process-wide registry. Provider init()
// functions call Default.Register at import time; the rest
// of the panel reads via Default.Get / Default.List.
var Default = NewRegistry()

// Register adds p to the registry under p.Name(). Returns
// ErrDuplicateName if a provider with the same name is
// already registered.
//
// Register is safe to call concurrently.
func (r *Registry) Register(p CoreProvider) error {
	if p == nil {
		return fmt.Errorf("cores: nil provider")
	}
	name := p.Name()
	if name == "" {
		return fmt.Errorf("cores: provider name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateName, name)
	}
	r.providers[name] = p
	return nil
}

// Unregister removes p from the registry. Mostly useful in
// tests that want a clean slate between cases. Returns
// nil if the name was not registered.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.providers, name)
	return nil
}

// Get returns the provider registered under name, or an
// error if no such provider is registered.
func (r *Registry) Get(name string) (CoreProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("cores: provider %q not registered", name)
	}
	return p, nil
}

// List returns every registered provider sorted by name for
// deterministic output (the GET /api/v1/cores endpoint relies
// on this for stable JSON).
func (r *Registry) List() []CoreProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CoreProvider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

// Names returns the registered provider names in the same
// sorted order as List. Useful for log lines and error
// messages that need to enumerate the registry.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for n := range r.providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Default-register / Get / List are package-level shortcuts
// for the common case of the process-global registry.

// Register adds p to the process-global registry. See
// Registry.Register for the full contract.
func Register(p CoreProvider) error { return Default.Register(p) }

// Get looks up a provider by name in the process-global
// registry. See Registry.Get for the full contract.
func Get(name string) (CoreProvider, error) { return Default.Get(name) }

// List returns every provider in the process-global
// registry, sorted by name. See Registry.List.
func List() []CoreProvider { return Default.List() }

// Names returns the sorted list of provider names in the
// process-global registry. See Registry.Names.
func Names() []string { return Default.Names() }

// Unregister removes a provider by name from the
// process-global registry. See Registry.Unregister.
func Unregister(name string) error { return Default.Unregister(name) }
