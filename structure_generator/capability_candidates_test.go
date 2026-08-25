package structure_generator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gearboxlogic/tappet/internal/capability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateCapabilityCandidatesMigratesCurrentHierarchyWithoutMappingLoss(t *testing.T) {
	output := filepath.Join(t.TempDir(), "candidates")
	require.NoError(t, GenerateCapabilityCandidates(filepath.Join("..", "testdata", "mcp_hierarchy"), output))
	store, err := capability.NewSnapshotStore(capability.DefaultStoreLimits())
	require.NoError(t, err)
	loader, err := capability.NewLoader(output, store)
	require.NoError(t, err)
	records, err := loader.LoadAll()
	require.NoError(t, err)
	defer func() {
		registry, registryErr := capability.NewRegistry(records...)
		if registryErr == nil {
			registry.Close()
		}
	}()

	require.Len(t, records, 10)
	mappings := make(map[string]string, len(records))
	for _, record := range records {
		operations := record.Operations()
		require.Len(t, operations, 1)
		mappings[record.Metadata().ID] = operations[0].Target
		providers := record.Providers()
		require.Len(t, providers, 1)
		assert.Equal(t, "everything", providers[0].ServerRef)
	}
	assert.Equal(t, map[string]string{
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
	}, mappings)
}

func TestGenerateCapabilityCandidatesIsDeterministicAndReviewable(t *testing.T) {
	hierarchy := writeCandidateHierarchy(t, "provider", "readFile", "readFile", "credential-not-copied")
	output := filepath.Join(t.TempDir(), "candidates")
	require.NoError(t, GenerateCapabilityCandidates(hierarchy, output))
	first := readGeneratedTree(t, output)
	require.NoError(t, GenerateCapabilityCandidates(hierarchy, output))
	second := readGeneratedTree(t, output)

	assert.Equal(t, first, second)
	manifest := second["provider.read-file/tappet.yaml"]
	assert.Contains(t, manifest, candidateHeader)
	assert.Contains(t, manifest, "# Source hierarchy node: provider/readFile.json")
	assert.NotContains(t, manifest, "inputSchema")
	assert.NotContains(t, manifest, "credential-not-copied")
}

func TestGenerateCapabilityCandidatesRejectsNormalizedIDCollisions(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "provider"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "root.json"), []byte(`{"overview":"root"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "provider", "tools.json"), []byte(`{
  "tools": {
    "getURL": {"description":"one","server":"provider","maps_to":"getURL"},
    "get-url": {"description":"two","server":"provider","maps_to":"get-url"}
  }
}`), 0o644))

	err := GenerateCapabilityCandidates(root, filepath.Join(t.TempDir(), "candidates"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate capability ID")
}

func TestGenerateCapabilityCandidatesWillNotOverwriteReviewedPackages(t *testing.T) {
	hierarchy := writeCandidateHierarchy(t, "provider", "readFile", "readFile", "secret")
	output := filepath.Join(t.TempDir(), "candidates")
	require.NoError(t, GenerateCapabilityCandidates(hierarchy, output))
	manifestPath := filepath.Join(output, "provider.read-file", "tappet.yaml")
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, data[len(candidateHeader):], 0o644))

	err = GenerateCapabilityCandidates(hierarchy, output)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reviewed or unrecognized")
}

func TestNormalizeCandidateIdentifierKeepsAcronymsTogether(t *testing.T) {
	assert.Equal(t, "get-url", normalizeCandidateIdentifier("getURL"))
	assert.Equal(t, "sample-llm", normalizeCandidateIdentifier("sampleLLM"))
	assert.Equal(t, "get-tiny-image", normalizeCandidateIdentifier("getTinyImage"))
}

func writeCandidateHierarchy(t *testing.T, provider, toolName, mapsTo, credential string) string {
	t.Helper()
	root := t.TempDir()
	providerDir := filepath.Join(root, provider)
	require.NoError(t, os.MkdirAll(providerDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "root.json"), []byte(`{"overview":"root"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(providerDir, provider+".json"), []byte(`{
  "overview": "provider",
  "mcp_server": {"headers":{"Authorization":"`+credential+`"}}
}`), 0o644))
	leaf := `{
  "tools": {
    "` + toolName + `": {
      "description": "Read a file.",
      "server": "` + provider + `",
      "maps_to": "` + mapsTo + `",
      "inputSchema": {"type":"object","default":"` + credential + `"}
    }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(providerDir, toolName+".json"), []byte(leaf), 0o644))
	return root
}

func readGeneratedTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = string(data)
		return nil
	}))
	return result
}
