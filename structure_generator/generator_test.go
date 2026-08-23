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

func TestGenerateStructureRejectsProviderDerivedPathComponents(t *testing.T) {
	testCases := []struct {
		name    string
		servers []ServerTools
	}{
		{
			name:    "provider traversal",
			servers: []ServerTools{{ServerName: "../escape"}},
		},
		{
			name: "tool traversal",
			servers: []ServerTools{{
				ServerName: "alpha",
				Tools:      []Tool{{Name: "../../config"}},
			}},
		},
		{
			name: "portable separator",
			servers: []ServerTools{{
				ServerName: "alpha",
				Tools:      []Tool{{Name: `..\config`}},
			}},
		},
		{
			name: "hierarchy delimiter in tool name",
			servers: []ServerTools{{
				ServerName: "alpha",
				Tools:      []Tool{{Name: "read.file"}},
			}},
		},
		{
			name:    "Windows reserved provider name",
			servers: []ServerTools{{ServerName: "CON"}},
		},
		{
			name: "Windows reserved tool name",
			servers: []ServerTools{{
				ServerName: "alpha",
				Tools:      []Tool{{Name: "NUL"}},
			}},
		},
		{
			name: "Windows reserved serial device name",
			servers: []ServerTools{{
				ServerName: "alpha",
				Tools:      []Tool{{Name: "com1"}},
			}},
		},
		{
			name: "Windows reserved printer device name",
			servers: []ServerTools{{
				ServerName: "alpha",
				Tools:      []Tool{{Name: "LPT9"}},
			}},
		},
		{
			name: "Win32 trailing period provider alias",
			servers: []ServerTools{
				{ServerName: "foo"},
				{ServerName: "foo."},
			},
		},
		{
			name: "Win32 trailing space provider alias",
			servers: []ServerTools{
				{ServerName: "foo"},
				{ServerName: "foo "},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			sentinelPath := filepath.Join(root, "config.json")
			require.NoError(t, os.WriteFile(sentinelPath, []byte("sentinel"), 0o600))

			err := GenerateStructure(testCase.servers, filepath.Join(root, "hierarchy"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid")
			data, readErr := os.ReadFile(sentinelPath)
			require.NoError(t, readErr)
			assert.Equal(t, "sentinel", string(data))
		})
	}
}

func TestGenerateStructureRejectsCaseFoldingNameCollisions(t *testing.T) {
	testCases := []struct {
		name        string
		servers     []ServerTools
		errorSubstr string
	}{
		{
			name: "provider names",
			servers: []ServerTools{
				{ServerName: "Alpha"},
				{ServerName: "alpha"},
			},
			errorSubstr: "case-folding collision",
		},
		{
			name:        "reserved root name",
			servers:     []ServerTools{{ServerName: "ROOT"}},
			errorSubstr: "reserved for the hierarchy root",
		},
		{
			name: "tool names",
			servers: []ServerTools{{
				ServerName: "alpha",
				Tools:      []Tool{{Name: "Read"}, {Name: "read"}},
			}},
			errorSubstr: "case-folding collision",
		},
		{
			name: "tool and provider index",
			servers: []ServerTools{{
				ServerName: "alpha",
				Tools:      []Tool{{Name: "ALPHA"}},
			}},
			errorSubstr: "case-folding collision with the provider index",
		},
		{
			name: "unicode simple fold",
			servers: []ServerTools{{
				ServerName: "alpha",
				Tools:      []Tool{{Name: "K"}, {Name: "\u212a"}},
			}},
			errorSubstr: "case-folding collision",
		},
		{
			name: "unicode canonical normalization",
			servers: []ServerTools{{
				ServerName: "alpha",
				Tools:      []Tool{{Name: "é"}, {Name: "e\u0301"}},
			}},
			errorSubstr: "case-folding collision",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			outputDir := filepath.Join(t.TempDir(), "hierarchy")
			err := GenerateStructure(testCase.servers, outputDir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.errorSubstr)
			_, statErr := os.Stat(outputDir)
			assert.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

func TestGeneratedPathRejectsStagingEscape(t *testing.T) {
	_, err := generatedPath(t.TempDir(), "..", "outside.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes staging directory")
}

func TestGenerateStructureRejectsSymlinkedOutputDirectory(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	require.NoError(t, GenerateStructure([]ServerTools{{
		ServerName: "alpha",
		Tools:      []Tool{{Name: "stable"}},
	}}, targetDir))

	outputDir := filepath.Join(root, "hierarchy")
	if err := os.Symlink(targetDir, outputDir); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	err := GenerateStructure([]ServerTools{{ServerName: "replacement"}}, outputDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlinked output directory")
	info, lstatErr := os.Lstat(outputDir)
	require.NoError(t, lstatErr)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
	stable := readToolNode(t, filepath.Join(targetDir, "alpha", "stable.json"))
	require.Contains(t, stable.Tools, "stable")
}

func readToolNode(t *testing.T, path string) ToolNode {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var node ToolNode
	require.NoError(t, json.Unmarshal(data, &node))
	return node
}
