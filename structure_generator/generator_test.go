package structure_generator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateStructureWritesLoadableHierarchy(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "hierarchy")
	servers := []ServerTools{
		{
			ServerName: "alpha",
			Tools: []Tool{
				{Name: "first", Description: "first tool", InputSchema: map[string]interface{}{"type": "object"}},
				{Name: "second", Description: "second tool", InputSchema: map[string]interface{}{"type": "object"}},
			},
		},
		{
			ServerName: "empty",
			Tools:      []Tool{},
		},
	}

	require.NoError(t, GenerateStructure(servers, outputDir))

	root := readToolNode(t, filepath.Join(outputDir, "root.json"))
	assert.Contains(t, root.Overview, "2 servers")
	assert.Contains(t, root.Overview, "2 tools")
	assert.Empty(t, root.Tools)

	alpha := readToolNode(t, filepath.Join(outputDir, "alpha", "alpha.json"))
	assert.Contains(t, alpha.Overview, "alpha: 2 tools")
	assert.Empty(t, alpha.Tools)

	first := readToolNode(t, filepath.Join(outputDir, "alpha", "first.json"))
	require.Contains(t, first.Tools, "first")
	assert.Equal(t, "alpha", first.Tools["first"].Server)
	assert.Equal(t, "first", first.Tools["first"].MapsTo)
	assert.Equal(t, map[string]interface{}{"type": "object"}, first.Tools["first"].InputSchema)

	empty := readToolNode(t, filepath.Join(outputDir, "empty", "empty.json"))
	assert.Equal(t, "empty MCP server with no tools", empty.Overview)
}

func TestRegeneratePreservesManualOverviewAndTracksMovedTools(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "hierarchy")
	require.NoError(t, GenerateStructure([]ServerTools{{
		ServerName: "alpha",
		Tools: []Tool{
			{Name: "first", Description: "first tool", InputSchema: map[string]interface{}{"type": "object"}},
			{Name: "second", Description: "second tool", InputSchema: map[string]interface{}{"type": "object"}},
		},
	}}, outputDir))

	alphaPath := filepath.Join(outputDir, "alpha", "alpha.json")
	require.NoError(t, os.WriteFile(alphaPath, []byte(`{"overview":"Hand-written provider description."}`), 0o644))
	groupDir := filepath.Join(outputDir, "alpha", "group")
	require.NoError(t, os.MkdirAll(groupDir, 0o755))
	require.NoError(t, os.Rename(filepath.Join(outputDir, "alpha", "first.json"), filepath.Join(groupDir, "first.json")))

	require.NoError(t, Regenerate(outputDir))
	alpha := readToolNode(t, alphaPath)
	assert.Equal(t, "Hand-written provider description.", alpha.Overview)
	group := readToolNode(t, filepath.Join(groupDir, "group.json"))
	assert.Contains(t, group.Overview, "group: 1 tools")
}

func TestGenerateStructureReplacesStaleProvidersAndTools(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "hierarchy")
	require.NoError(t, GenerateStructure([]ServerTools{
		{ServerName: "alpha", Tools: []Tool{{Name: "current"}, {Name: "removed"}}},
		{ServerName: "removed-provider", Tools: []Tool{{Name: "old"}}},
	}, outputDir))

	require.NoError(t, GenerateStructure([]ServerTools{
		{ServerName: "alpha", Tools: []Tool{{Name: "current"}}},
	}, outputDir))

	_, err := os.Stat(filepath.Join(outputDir, "alpha", "removed.json"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(outputDir, "removed-provider"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	root := readToolNode(t, filepath.Join(outputDir, "root.json"))
	assert.Contains(t, root.Overview, "1 servers, 1 tools")
}

func TestGenerateStructureKeepsPreviousTreeWhenGenerationFails(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "hierarchy")
	require.NoError(t, GenerateStructure([]ServerTools{
		{ServerName: "alpha", Tools: []Tool{{Name: "stable"}}},
	}, outputDir))

	err := GenerateStructure([]ServerTools{
		{ServerName: "alpha", Tools: []Tool{{Name: "missing/child"}}},
	}, outputDir)
	require.Error(t, err)

	stable := readToolNode(t, filepath.Join(outputDir, "alpha", "stable.json"))
	require.Contains(t, stable.Tools, "stable")
}

func TestGenerateStructureRejectsUnrecognizedExistingDirectory(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "not-a-hierarchy")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))
	unrelatedPath := filepath.Join(outputDir, "keep.txt")
	require.NoError(t, os.WriteFile(unrelatedPath, []byte("unrelated"), 0o644))

	err := GenerateStructure([]ServerTools{{ServerName: "alpha"}}, outputDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to replace unrecognized output directory")
	data, readErr := os.ReadFile(unrelatedPath)
	require.NoError(t, readErr)
	assert.Equal(t, "unrelated", string(data))
}

func TestGenerateStructureRejectsWorkingDirectoryAncestors(t *testing.T) {
	err := GenerateStructure(nil, "..")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output path must be a dedicated hierarchy directory")
}

func TestGenerateStructureRejectsInvalidOutputPath(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(filePath, []byte("not a directory"), 0o644))
	err := GenerateStructure(nil, filepath.Join(filePath, "hierarchy"))
	assert.Error(t, err)
}

func readToolNode(t *testing.T, path string) ToolNode {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var node ToolNode
	require.NoError(t, json.Unmarshal(data, &node))
	return node
}
