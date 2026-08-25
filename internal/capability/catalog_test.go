package capability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCatalogPaginatesChildrenDeterministically(t *testing.T) {
	records := make([]*Record, 205)
	for index := range records {
		records[index] = syntheticRecord(fmt.Sprintf("cap%03d", index), "")
	}
	registry, err := NewRegistry(records...)
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	catalog, err := newCatalog(registry, bytes.NewReader(bytes.Repeat([]byte{7}, 64)))
	require.NoError(t, err)

	request := CatalogSearchRequest{Query: "cap000", Limit: 1, ChildLimit: 100}
	first, err := catalog.Search(request)
	require.NoError(t, err)
	require.Len(t, first.Results, 1)
	assert.Equal(t, "cap000", first.Results[0].Card.ID)
	assert.Len(t, first.Children, 100)
	assert.Equal(t, 205, first.TotalChildren)
	assert.NotEmpty(t, first.NextChildCursor)

	retry, err := catalog.Search(request)
	require.NoError(t, err)
	assert.Equal(t, first, retry)

	all := append([]HierarchyEntry(nil), first.Children...)
	cursor := first.NextChildCursor
	for cursor != "" {
		page, pageErr := catalog.Search(CatalogSearchRequest{Query: "cap000", Limit: 1, ChildLimit: 100, ChildCursor: cursor})
		require.NoError(t, pageErr)
		all = append(all, page.Children...)
		cursor = page.NextChildCursor
	}
	require.Len(t, all, 205)
	for index, child := range all {
		assert.Equal(t, fmt.Sprintf("cap%03d", index), child.Path)
	}

	zero, err := catalog.Search(CatalogSearchRequest{Query: "cap000", Limit: 1, ChildLimit: 0})
	require.NoError(t, err)
	assert.Empty(t, zero.Children)
	assert.Empty(t, zero.NextChildCursor)
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), BrokerResponseMaxBytes)
}

func TestCatalogCursorStalesAfterEverySuccessfulMutation(t *testing.T) {
	tests := map[string]func(*Registry) error{
		"add":       func(registry *Registry) error { return registry.Add(syntheticRecord("third", "")) },
		"reinstall": func(registry *Registry) error { return registry.Reinstall(syntheticRecord("first", "")) },
		"uninstall": func(registry *Registry) error { return registry.Uninstall("second") },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			registry, err := NewRegistry(syntheticRecord("first", ""), syntheticRecord("second", ""))
			require.NoError(t, err)
			t.Cleanup(registry.Close)
			catalog, err := newCatalog(registry, bytes.NewReader(bytes.Repeat([]byte{3}, 64)))
			require.NoError(t, err)
			first, err := catalog.Search(CatalogSearchRequest{Query: "first", Limit: 1, ChildLimit: 1})
			require.NoError(t, err)
			require.NotEmpty(t, first.NextChildCursor)
			require.NoError(t, mutate(registry))

			_, err = catalog.Search(CatalogSearchRequest{Query: "first", Limit: 1, ChildLimit: 1, ChildCursor: first.NextChildCursor})
			assert.ErrorIs(t, err, ErrCatalogCursorStale)
		})
	}
}

func TestCatalogFailedMutationDoesNotStaleCursor(t *testing.T) {
	registry, err := NewRegistry(syntheticRecord("first", ""), syntheticRecord("second", ""))
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	catalog, err := newCatalog(registry, bytes.NewReader(bytes.Repeat([]byte{4}, 64)))
	require.NoError(t, err)
	first, err := catalog.Search(CatalogSearchRequest{Query: "first", Limit: 1, ChildLimit: 1})
	require.NoError(t, err)
	duplicate := syntheticRecord("first", "")
	err = registry.Add(duplicate)
	require.ErrorIs(t, err, ErrCapabilityDuplicate)
	duplicate.release()

	_, err = catalog.Search(CatalogSearchRequest{Query: "first", Limit: 1, ChildLimit: 1, ChildCursor: first.NextChildCursor})
	require.NoError(t, err)
}

func TestCatalogReportsStaleBeforeRemovedCursorPath(t *testing.T) {
	registry, err := NewRegistry(syntheticRecord("group.first", "group"), syntheticRecord("group.second", "group"))
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	catalog, err := newCatalog(registry, bytes.NewReader(bytes.Repeat([]byte{2}, 64)))
	require.NoError(t, err)
	first, err := catalog.Search(CatalogSearchRequest{Query: "first", Path: "group", Limit: 1, ChildLimit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, first.NextChildCursor)
	require.NoError(t, registry.Uninstall("group.first"))
	require.NoError(t, registry.Uninstall("group.second"))

	_, err = catalog.Search(CatalogSearchRequest{Query: "first", Path: "group", Limit: 1, ChildLimit: 1, ChildCursor: first.NextChildCursor})
	assert.ErrorIs(t, err, ErrCatalogCursorStale)
}

func TestCatalogRejectsInvalidAndOversizedCursorsBeforeDecode(t *testing.T) {
	registry, err := NewRegistry(syntheticRecord("first", ""), syntheticRecord("second", ""))
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	catalog, err := newCatalog(registry, bytes.NewReader(bytes.Repeat([]byte{5}, 64)))
	require.NoError(t, err)

	for _, cursor := range []string{"catalog:v1:tampered:value", strings.Repeat("x", OpaqueReferenceMaxBytes+1)} {
		_, err := catalog.Search(CatalogSearchRequest{Query: "first", Limit: 1, ChildLimit: 1, ChildCursor: cursor})
		assert.ErrorIs(t, err, ErrCatalogCursorInvalid)
	}
}

func TestCatalogRequiresCryptographicCursorKeyMaterial(t *testing.T) {
	registry, err := NewRegistry(syntheticRecord("first", ""))
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	_, err = newCatalog(registry, bytes.NewReader(nil))
	assert.Error(t, err)
}

func syntheticRecord(id, parent string) *Record {
	store := &SnapshotStore{objects: make(map[[32]byte]*snapshotObject)}
	return &Record{
		metadata: Metadata{ID: id, Name: id, Version: "1.0.0", Description: "Capability " + id},
		parent:   parent, manifestDigest: strings.Repeat("a", 64),
		operations: []Operation{{ID: "run", Description: "Run it."}},
		artifacts:  make(map[string]Artifact),
		snapshot:   &Snapshot{store: store, digests: make(map[string][32]byte)},
	}
}
