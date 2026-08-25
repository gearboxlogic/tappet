package capability

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryProvidesExactLookupAndDeterministicHierarchy(t *testing.T) {
	root := t.TempDir()
	writeSimplePackage(t, root, "software.github.ci-debugging", "software.github", "inspect", "github")
	writeSimplePackage(t, root, "data.postgres.query-analysis", "data.postgres", "query", "postgres")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	records, err := loader.LoadAll()
	require.NoError(t, err)
	registry, err := NewRegistry(records...)
	require.NoError(t, err)
	t.Cleanup(registry.Close)

	assert.Equal(t, []string{"data.postgres.query-analysis", "software.github.ci-debugging"}, registry.CapabilityIDs())
	lease, err := registry.Lookup("software.github.ci-debugging")
	require.NoError(t, err)
	assert.Equal(t, "GitHub CI debugging", lease.Record().Metadata().Name)
	lease.Release()

	rootView, err := registry.Browse("")
	require.NoError(t, err)
	require.Len(t, rootView.Children, 2)
	assert.Equal(t, "data", rootView.Children[0].Path)
	assert.Equal(t, "software", rootView.Children[1].Path)
	githubView, err := registry.Browse("software.github")
	require.NoError(t, err)
	require.Len(t, githubView.Children, 1)
	assert.Equal(t, "software.github.ci-debugging", githubView.Children[0].CapabilityID)
	assert.Equal(t, 1, githubView.Children[0].Operations)
}

func TestRegistryRejectsDuplicateCapabilityDeterministically(t *testing.T) {
	root := t.TempDir()
	writeSimplePackage(t, root, "software.github.ci-debugging", "software.github", "inspect", "github")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	first, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	second, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	registry, err := NewRegistry(first)
	require.NoError(t, err)
	defer registry.Close()

	err = registry.Add(second)
	require.ErrorIs(t, err, ErrCapabilityDuplicate)
	second.release()
}

func TestInvocationLeaseKeepsOneGenerationAcrossReinstall(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeSimplePackage(t, root, "software.github.ci-debugging", "software.github", "inspect-v1", "github-v1")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	first, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	registry, err := NewRegistry(first)
	require.NoError(t, err)
	defer registry.Close()

	queued, err := registry.ResolveOperation("software.github.ci-debugging", "inspect")
	require.NoError(t, err)
	active, err := registry.ResolveOperation("software.github.ci-debugging", "inspect")
	require.NoError(t, err)
	oldGeneration := queued.Generation()
	oldStats := store.Stats()

	manifest := readFileString(t, manifestPath)
	manifest = stringsReplaceAll(manifest, map[string]string{"inspect-v1": "inspect-v2", "github-v1": "github-v2", "version: 0.1.0": "version: 0.2.0"})
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o644))
	replacement, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	require.NoError(t, registry.Reinstall(replacement))

	assert.Equal(t, "inspect-v1", queued.Operation().Target)
	assert.Equal(t, "github-v1", queued.ProviderBinding().ServerRef)
	assert.Equal(t, oldGeneration, active.Generation())
	current, err := registry.ResolveOperation("software.github.ci-debugging", "inspect")
	require.NoError(t, err)
	assert.Greater(t, current.Generation(), oldGeneration)
	assert.Equal(t, "inspect-v2", current.Operation().Target)
	assert.Equal(t, "github-v2", current.ProviderBinding().ServerRef)
	assert.GreaterOrEqual(t, store.Stats().SnapshotUsed, oldStats.SnapshotUsed)

	queued.Release()
	active.Release()
	current.Release()
}

func TestInvocationLeaseSurvivesUninstallUntilTerminalRelease(t *testing.T) {
	root := t.TempDir()
	writeSimplePackage(t, root, "software.github.ci-debugging", "software.github", "inspect", "github")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	record, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	registry, err := NewRegistry(record)
	require.NoError(t, err)
	defer registry.Close()
	reclaimed := make(chan uint64, 1)
	registry.onReclaim = func(generation uint64) { reclaimed <- generation }
	lease, err := registry.ResolveOperation("software.github.ci-debugging", "inspect")
	require.NoError(t, err)

	require.NoError(t, registry.Uninstall("software.github.ci-debugging"))
	_, err = registry.ResolveOperation("software.github.ci-debugging", "inspect")
	require.ErrorIs(t, err, ErrCapabilityNotFound)
	assert.Equal(t, "github", lease.ProviderBinding().ServerRef)
	select {
	case <-reclaimed:
		t.Fatal("uninstall reclaimed an active invocation generation")
	default:
	}

	generation := lease.Generation()
	lease.Release()
	assert.Equal(t, generation, <-reclaimed)
	assert.Equal(t, StoreStats{}, store.Stats())
}

func TestRegistryConcurrentResolutionNeverMixesGenerations(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeSimplePackage(t, root, "software.github.ci-debugging", "software.github", "target-v1", "provider-v1")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	record, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	registry, err := NewRegistry(record)
	require.NoError(t, err)
	defer registry.Close()

	start := make(chan struct{})
	errorsSeen := make(chan error, 64)
	var wait sync.WaitGroup
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			lease, resolveErr := registry.ResolveOperation("software.github.ci-debugging", "inspect")
			if resolveErr != nil {
				errorsSeen <- resolveErr
				return
			}
			defer lease.Release()
			target := lease.Operation().Target
			serverRef := lease.ProviderBinding().ServerRef
			if (target == "target-v1") != (serverRef == "provider-v1") || (target == "target-v2") != (serverRef == "provider-v2") {
				errorsSeen <- fmt.Errorf("mixed generation: target=%s provider=%s", target, serverRef)
			}
		}()
	}
	close(start)
	manifest := stringsReplaceAll(readFileString(t, manifestPath), map[string]string{"target-v1": "target-v2", "provider-v1": "provider-v2", "version: 0.1.0": "version: 0.2.0"})
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o644))
	replacement, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	require.NoError(t, registry.Reinstall(replacement))
	wait.Wait()
	close(errorsSeen)
	for concurrentErr := range errorsSeen {
		require.NoError(t, concurrentErr)
	}
}

func TestResolveToolPathPreservesSingleLeafAndCapabilityOperationForms(t *testing.T) {
	root := t.TempDir()
	writeSimplePackageWithOperation(t, root, "everything.add", "everything", "add", "add", "everything")
	writeSimplePackageWithOperation(t, root, "software.github.ci-debugging", "software.github", "inspect", "get-checks", "github")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	records, err := loader.LoadAll()
	require.NoError(t, err)
	registry, err := NewRegistry(records...)
	require.NoError(t, err)
	defer registry.Close()

	leaf, err := registry.ResolveToolPath("everything.add")
	require.NoError(t, err)
	assert.Equal(t, "add", leaf.Operation().Target)
	leaf.Release()
	operation, err := registry.ResolveToolPath("software.github.ci-debugging.inspect")
	require.NoError(t, err)
	assert.Equal(t, "get-checks", operation.Operation().Target)
	operation.Release()
}

func writeSimplePackage(t *testing.T, root, id, parent, target, serverRef string) string {
	t.Helper()
	return writeSimplePackageWithOperation(t, root, id, parent, "inspect", target, serverRef)
}

func writeSimplePackageWithOperation(t *testing.T, root, id, parent, operationID, target, serverRef string) string {
	t.Helper()
	packageDir := filepath.Join(root, id)
	require.NoError(t, os.MkdirAll(packageDir, 0o755))
	manifest := fmt.Sprintf(`apiVersion: tappet.gearboxlogic.dev/v1alpha1
kind: Capability
metadata:
  id: %s
  name: GitHub CI debugging
  version: 0.1.0
  description: Inspect failing checks.
spec:
  parent: %s
  operations:
    - id: %s
      description: Inspect failed checks.
      provider: provider
      target: %s
  providers:
    - id: provider
      type: mcp
      serverRef: %s
`, id, parent, operationID, target, serverRef)
	manifestPath := filepath.Join(packageDir, manifestFileName)
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o644))
	return manifestPath
}

func stringsReplaceAll(value string, replacements map[string]string) string {
	for old, replacement := range replacements {
		value = strings.ReplaceAll(value, old, replacement)
	}
	return value
}
