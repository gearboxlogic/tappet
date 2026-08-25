package capability

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrRegistryClosed      = errors.New("capability registry is closed")
	ErrCapabilityNotFound  = errors.New("capability not found")
	ErrOperationNotFound   = errors.New("capability operation not found")
	ErrCapabilityDuplicate = errors.New("capability is already installed")
	ErrGenerationReleased  = errors.New("capability generation lease is released")
)

type Registry struct {
	mu        sync.RWMutex
	entries   map[string]*registryGeneration
	nodes     map[string]hierarchyNode
	next      uint64
	closed    bool
	onReclaim func(uint64)
}

type registryGeneration struct {
	id      uint64
	record  *Record
	refs    int
	retired bool
}

type hierarchyNode struct {
	capabilityID string
	children     []string
}

func NewRegistry(records ...*Record) (*Registry, error) {
	registry := &Registry{entries: make(map[string]*registryGeneration), nodes: make(map[string]hierarchyNode)}
	for _, record := range records {
		if err := registry.addLocked(record); err != nil {
			registry.closeLocked()
			return nil, err
		}
	}
	registry.rebuildHierarchyLocked()
	return registry, nil
}

// Add publishes a capability that is not already installed. The registry owns
// the record after a successful call.
func (r *Registry) Add(record *Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrRegistryClosed
	}
	if err := r.addLocked(record); err != nil {
		return err
	}
	r.rebuildHierarchyLocked()
	return nil
}

func (r *Registry) addLocked(record *Record) error {
	if record == nil || record.snapshot == nil {
		return errors.New("normalized capability record is required")
	}
	id := record.metadata.ID
	if _, exists := r.entries[id]; exists {
		return fmt.Errorf("%w: %s", ErrCapabilityDuplicate, id)
	}
	r.next++
	r.entries[id] = &registryGeneration{id: r.next, record: record, refs: 1}
	return nil
}

// Reinstall atomically replaces one installed capability. The registry owns the
// replacement after success. Existing leases keep the retired generation live.
func (r *Registry) Reinstall(record *Record) error {
	if record == nil || record.snapshot == nil {
		return errors.New("normalized capability record is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrRegistryClosed
	}
	id := record.metadata.ID
	current, exists := r.entries[id]
	if !exists {
		return fmt.Errorf("%w: %s", ErrCapabilityNotFound, id)
	}
	r.next++
	r.entries[id] = &registryGeneration{id: r.next, record: record, refs: 1}
	r.retireLocked(current)
	r.rebuildHierarchyLocked()
	return nil
}

func (r *Registry) Uninstall(capabilityID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrRegistryClosed
	}
	current, exists := r.entries[capabilityID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrCapabilityNotFound, capabilityID)
	}
	delete(r.entries, capabilityID)
	r.retireLocked(current)
	r.rebuildHierarchyLocked()
	return nil
}

func (r *Registry) Lookup(capabilityID string) (*CapabilityLease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrRegistryClosed
	}
	generation, exists := r.entries[capabilityID]
	if !exists || generation.retired {
		return nil, fmt.Errorf("%w: %s", ErrCapabilityNotFound, capabilityID)
	}
	generation.refs++
	return &CapabilityLease{registry: r, generation: generation}, nil
}

func (r *Registry) ResolveOperation(capabilityID, operationID string) (*InvocationLease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrRegistryClosed
	}
	generation, exists := r.entries[capabilityID]
	if !exists || generation.retired {
		return nil, fmt.Errorf("%w: %s", ErrCapabilityNotFound, capabilityID)
	}
	var operation Operation
	foundOperation := false
	for _, candidate := range generation.record.operations {
		if candidate.ID == operationID {
			operation = candidate
			foundOperation = true
			break
		}
	}
	if !foundOperation {
		return nil, fmt.Errorf("%w: %s/%s", ErrOperationNotFound, capabilityID, operationID)
	}
	var binding ProviderBinding
	foundBinding := false
	for _, candidate := range generation.record.providers {
		if candidate.ID == operation.Provider {
			binding = candidate
			foundBinding = true
			break
		}
	}
	if !foundBinding {
		return nil, packageError("operation_generation_invalid", capabilityID+"/"+operationID, fmt.Errorf("provider binding %q is absent", operation.Provider))
	}
	generation.refs++
	return &InvocationLease{
		registry:     r,
		generation:   generation,
		capabilityID: capabilityID,
		operation:    operation,
		binding:      binding,
	}, nil
}

// ResolveToolPath preserves the current dotted broker path while capability
// packages replace hierarchy JSON. A one-operation capability whose operation
// ID matches its final ID segment resolves at the capability ID itself. Other
// operations resolve as <capability-id>.<operation-id>.
func (r *Registry) ResolveToolPath(toolPath string) (*InvocationLease, error) {
	r.mu.RLock()
	if generation := r.entries[toolPath]; generation != nil && !generation.retired && len(generation.record.operations) == 1 {
		operation := generation.record.operations[0]
		lastSegment := toolPath
		if separator := strings.LastIndexByte(toolPath, '.'); separator >= 0 {
			lastSegment = toolPath[separator+1:]
		}
		if operation.ID == lastSegment {
			r.mu.RUnlock()
			return r.ResolveOperation(toolPath, operation.ID)
		}
	}
	r.mu.RUnlock()

	separator := strings.LastIndexByte(toolPath, '.')
	if separator <= 0 || separator == len(toolPath)-1 {
		return nil, fmt.Errorf("%w: %s", ErrOperationNotFound, toolPath)
	}
	return r.ResolveOperation(toolPath[:separator], toolPath[separator+1:])
}

func (r *Registry) CapabilityIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil
	}
	result := make([]string, 0, len(r.entries))
	for id := range r.entries {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

type HierarchyEntry struct {
	Path         string
	CapabilityID string
	Name         string
	Description  string
	Operations   int
}

type HierarchyView struct {
	Path         string
	CapabilityID string
	Children     []HierarchyEntry
}

func (r *Registry) Browse(hierarchyPath string) (HierarchyView, error) {
	hierarchyPath = strings.Trim(hierarchyPath, ".")
	if hierarchyPath == "/" {
		hierarchyPath = ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return HierarchyView{}, ErrRegistryClosed
	}
	node, exists := r.nodes[hierarchyPath]
	if !exists {
		return HierarchyView{}, fmt.Errorf("hierarchy path not found: %s", hierarchyPath)
	}
	view := HierarchyView{Path: hierarchyPath, CapabilityID: node.capabilityID}
	for _, childPath := range node.children {
		childNode := r.nodes[childPath]
		entry := HierarchyEntry{Path: childPath, CapabilityID: childNode.capabilityID}
		if childNode.capabilityID != "" {
			generation := r.entries[childNode.capabilityID]
			if generation != nil {
				entry.Name = generation.record.metadata.Name
				entry.Description = generation.record.metadata.Description
				entry.Operations = len(generation.record.operations)
			}
		}
		view.Children = append(view.Children, entry)
	}
	return view, nil
}

func (r *Registry) rebuildHierarchyLocked() {
	nodes := map[string]hierarchyNode{"": {}}
	edges := make(map[string]map[string]struct{})
	ensurePath := func(hierarchyPath string) {
		if hierarchyPath == "" {
			return
		}
		parts := strings.Split(hierarchyPath, ".")
		parent := ""
		for index := range parts {
			current := strings.Join(parts[:index+1], ".")
			if _, ok := nodes[current]; !ok {
				nodes[current] = hierarchyNode{}
			}
			if edges[parent] == nil {
				edges[parent] = make(map[string]struct{})
			}
			edges[parent][current] = struct{}{}
			parent = current
		}
	}
	for id, generation := range r.entries {
		ensurePath(generation.record.parent)
		ensurePath(id)
		node := nodes[id]
		node.capabilityID = id
		nodes[id] = node
	}
	for parent, childSet := range edges {
		node := nodes[parent]
		for child := range childSet {
			node.children = append(node.children, child)
		}
		sort.Strings(node.children)
		nodes[parent] = node
	}
	r.nodes = nodes
}

func (r *Registry) retireLocked(generation *registryGeneration) {
	generation.retired = true
	generation.refs--
	if generation.refs == 0 {
		r.reclaimLocked(generation)
	}
}

func (r *Registry) release(generation *registryGeneration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if generation.refs <= 0 {
		return
	}
	generation.refs--
	if generation.refs == 0 && generation.retired {
		r.reclaimLocked(generation)
	}
}

func (r *Registry) reclaimLocked(generation *registryGeneration) {
	generation.record.release()
	if r.onReclaim != nil {
		r.onReclaim(generation.id)
	}
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeLocked()
}

func (r *Registry) closeLocked() {
	if r.closed {
		return
	}
	r.closed = true
	for _, generation := range r.entries {
		r.retireLocked(generation)
	}
	clear(r.entries)
	r.nodes = map[string]hierarchyNode{}
}

type CapabilityLease struct {
	registry   *Registry
	generation *registryGeneration
	once       sync.Once
}

func (l *CapabilityLease) Generation() uint64 { return l.generation.id }
func (l *CapabilityLease) Record() *Record    { return l.generation.record }
func (l *CapabilityLease) Release() {
	l.once.Do(func() { l.registry.release(l.generation) })
}

type InvocationLease struct {
	registry     *Registry
	generation   *registryGeneration
	capabilityID string
	operation    Operation
	binding      ProviderBinding
	once         sync.Once
}

func (l *InvocationLease) Generation() uint64               { return l.generation.id }
func (l *InvocationLease) CapabilityID() string             { return l.capabilityID }
func (l *InvocationLease) Operation() Operation             { return l.operation }
func (l *InvocationLease) ProviderBinding() ProviderBinding { return l.binding }
func (l *InvocationLease) Release() {
	l.once.Do(func() { l.registry.release(l.generation) })
}
