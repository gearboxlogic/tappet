package capability

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewedCapabilityPackagesLoadAndPreserveCurrentMappings(t *testing.T) {
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(filepath.Join("..", "..", "testdata", "capabilities"), store)
	require.NoError(t, err)
	records, err := loader.LoadAll()
	require.NoError(t, err)
	registry, err := NewRegistry(records...)
	require.NoError(t, err)
	defer registry.Close()

	require.Len(t, records, 10)
	assert.Equal(t, []string{
		"everything.add",
		"everything.annotated-message",
		"everything.echo",
		"everything.get-resource-links",
		"everything.get-resource-reference",
		"everything.get-tiny-image",
		"everything.long-running-operation",
		"everything.print-env",
		"everything.sample-llm",
		"everything.structured-content",
	}, registry.CapabilityIDs())

	expectedTargets := map[string]string{
		"everything.add":                    "add",
		"everything.annotated-message":      "annotatedMessage",
		"everything.echo":                   "echo",
		"everything.get-resource-links":     "getResourceLinks",
		"everything.get-resource-reference": "getResourceReference",
		"everything.get-tiny-image":         "getTinyImage",
		"everything.long-running-operation": "longRunningOperation",
		"everything.print-env":              "printEnv",
		"everything.sample-llm":             "sampleLLM",
		"everything.structured-content":     "structuredContent",
	}
	for capabilityID, target := range expectedTargets {
		lease, err := registry.Lookup(capabilityID)
		require.NoError(t, err)
		operations := lease.Record().Operations()
		require.Len(t, operations, 1)
		assert.Equal(t, target, operations[0].Target)
		lease.Release()
	}

	add, err := registry.Lookup("everything.add")
	require.NoError(t, err)
	defer add.Release()
	assert.Equal(t, "Numeric addition", add.Record().Metadata().Name)
	require.Len(t, add.Record().Skills(), 1)
	require.Len(t, add.Record().Skills()[0].Resources, 1)
	require.Len(t, add.Record().Context(), 1)

	echo, err := registry.Lookup("everything.echo")
	require.NoError(t, err)
	assert.Equal(t, "Text echo", echo.Record().Metadata().Name)
	echo.Release()
	structured, err := registry.Lookup("everything.structured-content")
	require.NoError(t, err)
	assert.Equal(t, "Structured content example", structured.Record().Metadata().Name)
	structured.Release()
}
