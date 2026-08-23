package structure_generator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// GenerateStructure creates a two-layer folder structure from MCP server tools
// Structure: structure/ (root) -> server_name/ (each server)
func GenerateStructure(servers []ServerTools, outputDir string) error {
	cleanOutputDir := filepath.Clean(outputDir)
	if err := validateOutputLocation(cleanOutputDir); err != nil {
		return err
	}
	if err := validateGeneratedNames(servers); err != nil {
		return err
	}
	parentDir := filepath.Dir(cleanOutputDir)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create output parent directory: %w", err)
	}

	if err := validateExistingHierarchyDirectory(cleanOutputDir); err != nil {
		return err
	}

	stagingDir, err := os.MkdirTemp(parentDir, "."+filepath.Base(cleanOutputDir)+".tmp-")
	if err != nil {
		return fmt.Errorf("failed to create staging directory: %w", err)
	}
	defer os.RemoveAll(stagingDir)
	if err := os.Chmod(stagingDir, 0755); err != nil {
		return fmt.Errorf("failed to set staging directory permissions: %w", err)
	}

	if err := generateStructure(servers, stagingDir); err != nil {
		return err
	}
	if err := replaceGeneratedDirectory(stagingDir, cleanOutputDir); err != nil {
		return fmt.Errorf("failed to publish generated structure: %w", err)
	}
	return nil
}

func validateOutputLocation(outputDir string) error {
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("failed to resolve output path: %w", err)
	}
	absWorkingDir, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("failed to resolve working directory: %w", err)
	}
	outputContainsWorkingDir := false
	if strings.EqualFold(filepath.VolumeName(absOutput), filepath.VolumeName(absWorkingDir)) {
		relativeWorkingDir, relErr := filepath.Rel(absOutput, absWorkingDir)
		if relErr != nil {
			return fmt.Errorf("failed to compare output path with working directory: %w", relErr)
		}
		outputContainsWorkingDir = relativeWorkingDir == "." ||
			(relativeWorkingDir != ".." && !strings.HasPrefix(relativeWorkingDir, ".."+string(filepath.Separator)))
	}
	absTempDir, err := filepath.Abs(os.TempDir())
	if err != nil {
		return fmt.Errorf("failed to resolve temporary directory: %w", err)
	}
	if outputContainsWorkingDir || absOutput == absTempDir {
		return fmt.Errorf("output path must be a dedicated hierarchy directory: %s", outputDir)
	}
	return nil
}

func validateExistingHierarchyDirectory(outputDir string) error {
	info, err := os.Lstat(outputDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect output path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlinked output directory: %s", outputDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to replace non-directory output path: %s", outputDir)
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("failed to inspect output directory: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}

	rootFound := false
	for _, entry := range entries {
		if entry.Name() == "root.json" {
			if entry.Type().IsRegular() {
				var root ToolNode
				data, readErr := os.ReadFile(filepath.Join(outputDir, entry.Name()))
				if readErr == nil && json.Unmarshal(data, &root) == nil && root.Overview != "" && len(root.Tools) == 0 {
					rootFound = true
					continue
				}
			}
			return fmt.Errorf("refusing to replace unrecognized output directory: invalid root.json in %s", outputDir)
		}
		if !entry.IsDir() {
			return fmt.Errorf("refusing to replace unrecognized output directory: unexpected file %s", filepath.Join(outputDir, entry.Name()))
		}
		providerIndex := filepath.Join(outputDir, entry.Name(), entry.Name()+".json")
		if info, statErr := os.Lstat(providerIndex); statErr != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace unrecognized output directory: missing provider index %s", providerIndex)
		}
	}
	if !rootFound {
		return fmt.Errorf("refusing to replace unrecognized output directory without root.json: %s", outputDir)
	}
	return nil
}

func validateGeneratedNames(servers []ServerTools) error {
	serverNames := make(map[string]struct{}, len(servers))
	foldedServerNames := make(map[string]string, len(servers))
	rootNameKey := foldGeneratedName("root.json")
	for _, server := range servers {
		if err := validateGeneratedComponent("provider", server.ServerName); err != nil {
			return err
		}
		serverNameKey := foldGeneratedName(server.ServerName)
		if serverNameKey == rootNameKey {
			return errors.New("provider name is reserved for the hierarchy root: root.json")
		}
		if _, exists := serverNames[server.ServerName]; exists {
			return fmt.Errorf("duplicate provider name: %s", server.ServerName)
		}
		if existing, exists := foldedServerNames[serverNameKey]; exists {
			return fmt.Errorf("provider name %q has a case-folding collision with %q", server.ServerName, existing)
		}
		serverNames[server.ServerName] = struct{}{}
		foldedServerNames[serverNameKey] = server.ServerName

		toolNames := make(map[string]struct{}, len(server.Tools))
		foldedToolNames := make(map[string]string, len(server.Tools))
		for _, tool := range server.Tools {
			if err := validateGeneratedComponent("tool", tool.Name); err != nil {
				return fmt.Errorf("provider %s: %w", server.ServerName, err)
			}
			toolNameKey := foldGeneratedName(tool.Name)
			if tool.Name == server.ServerName {
				return fmt.Errorf("provider %s: tool name collides with provider index: %s", server.ServerName, tool.Name)
			}
			if toolNameKey == serverNameKey {
				return fmt.Errorf("provider %s: tool name %q has a case-folding collision with the provider index", server.ServerName, tool.Name)
			}
			if _, exists := toolNames[tool.Name]; exists {
				return fmt.Errorf("provider %s: duplicate tool name: %s", server.ServerName, tool.Name)
			}
			if existing, exists := foldedToolNames[toolNameKey]; exists {
				return fmt.Errorf("provider %s: tool name %q has a case-folding collision with %q", server.ServerName, tool.Name, existing)
			}
			toolNames[tool.Name] = struct{}{}
			foldedToolNames[toolNameKey] = tool.Name
		}
	}
	return nil
}

func foldGeneratedName(name string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(name)))
}

func validateGeneratedComponent(kind, name string) error {
	if name == "" || name == "." || name == ".." || filepath.IsAbs(name) ||
		filepath.VolumeName(name) != "" || strings.ContainsAny(name, `/\\`) || strings.ContainsRune(name, 0) {
		return fmt.Errorf("invalid %s name for generated path: %q", kind, name)
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("invalid %s name for generated path: trailing periods and spaces are not portable: %q", kind, name)
	}
	return nil
}

func generatedPath(root string, components ...string) (string, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve generation root: %w", err)
	}
	parts := append([]string{rootPath}, components...)
	candidate := filepath.Join(parts...)
	relative, err := filepath.Rel(rootPath, candidate)
	if err != nil {
		return "", fmt.Errorf("failed to verify generated path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("generated path escapes staging directory: %s", candidate)
	}
	return candidate, nil
}

func generateStructure(servers []ServerTools, outputDir string) error {
	// Process each server (skip root.json generation)
	for _, server := range servers {
		if err := generateServerStructure(server, outputDir); err != nil {
			return fmt.Errorf("failed to generate structure for server %s: %w", server.ServerName, err)
		}
	}

	// Generate root.json AFTER all server files are created
	if err := Regenerate(outputDir); err != nil {
		return fmt.Errorf("failed to generate root.json: %w", err)
	}

	return nil
}

func replaceGeneratedDirectory(stagingDir, outputDir string) error {
	info, err := os.Lstat(outputDir)
	if os.IsNotExist(err) {
		return os.Rename(stagingDir, outputDir)
	} else if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlinked output directory: %s", outputDir)
	}

	backupDir := stagingDir + ".previous"
	if err := os.Rename(outputDir, backupDir); err != nil {
		return err
	}
	if err := os.Rename(stagingDir, outputDir); err != nil {
		if restoreErr := os.Rename(backupDir, outputDir); restoreErr != nil {
			return fmt.Errorf("replace failed: %w; restoring previous output also failed: %v", err, restoreErr)
		}
		return err
	}
	if err := os.RemoveAll(backupDir); err != nil {
		return fmt.Errorf("generated output is complete but previous output cleanup failed: %w", err)
	}
	return nil
}

// Regenerate regenerates all JSON files in the hierarchy by reading directory structure
// Preserves manual edits - if an overview has been manually modified, it won't be overwritten
func Regenerate(outputDir string) error {
	// First, recursively regenerate all subdirectories
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("failed to read output directory: %w", err)
	}

	// Regenerate each server directory recursively
	for _, entry := range entries {
		if !entry.IsDir() {
			continue // Skip files like root.json
		}

		serverDir := filepath.Join(outputDir, entry.Name())
		if err := RegenerateDirectory(serverDir, entry.Name()); err != nil {
			return fmt.Errorf("failed to regenerate directory %s: %w", entry.Name(), err)
		}
	}

	// Now generate root.json from the regenerated server files
	var childSummaries []string
	totalTools := 0

	// Scan each server directory
	for _, entry := range entries {
		if !entry.IsDir() {
			continue // Skip files like root.json
		}

		serverName := entry.Name()
		serverJSONPath := filepath.Join(outputDir, serverName, serverName+".json")

		// Read the server's JSON file
		data, err := os.ReadFile(serverJSONPath)
		if err != nil {
			continue // Skip if file doesn't exist
		}

		var node ToolNode
		if err := json.Unmarshal(data, &node); err != nil {
			continue // Skip if invalid JSON
		}

		// Count tools from subdirectories
		toolCount := countTotalTools(filepath.Join(outputDir, serverName))
		totalTools += toolCount

		// Extract brief description from node's overview (first sentence or up to semicolon)
		brief := extractBriefDescription(node.Overview)
		childSummaries = append(childSummaries, fmt.Sprintf("%s -> %s", serverName, brief))
	}

	// Create overview text in the format: "Root: N servers, M tools; server1 -> desc1, server2 -> desc2"
	var overview string
	if len(childSummaries) == 0 {
		overview = "CapScope hierarchical tool index. Use get_tools_in_category to explore categories and execute_tool to run tools."
	} else {
		overview = fmt.Sprintf("Root: %d servers, %d tools; %s",
			len(childSummaries), totalTools, joinWithCommas(childSummaries))
	}

	// Create root node - branch node with overview only
	rootNode := ToolNode{
		Path:     "root",
		Overview: overview,
		Tools:    nil, // Root doesn't have direct tools
	}

	// Write root.json
	rootPath := filepath.Join(outputDir, "root.json")
	return writeNodeToJSON(rootNode, rootPath)
}

// RegenerateDirectory recursively regenerates a directory's JSON file from its subdirectories
// This enables drag-and-drop reorganization: move tool folders around, then regenerate
func RegenerateDirectory(dirPath string, nodeName string) error {
	// Read all entries in this directory
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	// First, recursively regenerate all subdirectories
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		subDirPath := filepath.Join(dirPath, entry.Name())
		// Recursively regenerate subdirectory
		if err := RegenerateDirectory(subDirPath, entry.Name()); err != nil {
			return fmt.Errorf("failed to regenerate subdirectory %s: %w", entry.Name(), err)
		}
	}

	// Check if this is a leaf node by reading existing JSON file
	nodeJSONPath := filepath.Join(dirPath, nodeName+".json")
	existingData, err := os.ReadFile(nodeJSONPath)
	isLeafNode := false

	if err == nil {
		var existingNode ToolNode
		if json.Unmarshal(existingData, &existingNode) == nil {
			// If this node has tools, it's a leaf node - don't regenerate it
			if len(existingNode.Tools) > 0 {
				isLeafNode = true
			}
		}
	}

	// Don't regenerate leaf nodes (tool files) - they should be left as-is
	if isLeafNode {
		return nil
	}

	// This is a branch node - collect info from children and generate overview
	var childSummaries []string
	totalTools := 0

	for _, entry := range entries {
		var childNode ToolNode
		var childName string
		var data []byte
		var err error

		if entry.IsDir() {
			// Nested structure: child is in subdirectory
			childName = entry.Name()
			childJSONPath := filepath.Join(dirPath, childName, childName+".json")

			// Read the child's JSON file
			data, err = os.ReadFile(childJSONPath)
			if err != nil {
				continue // Skip if file doesn't exist
			}
		} else {
			// Flat structure: child JSON file is directly in this directory
			// Only process .json files that aren't the current node's file
			if !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == nodeName+".json" {
				continue
			}

			childName = strings.TrimSuffix(entry.Name(), ".json")
			childJSONPath := filepath.Join(dirPath, entry.Name())

			// Read the flat child's JSON file
			data, err = os.ReadFile(childJSONPath)
			if err != nil {
				continue
			}
		}

		if err := json.Unmarshal(data, &childNode); err != nil {
			continue // Skip if invalid JSON
		}

		// Determine if child is a leaf or branch
		if len(childNode.Tools) > 0 {
			// Leaf node - count tools
			toolCount := len(childNode.Tools)
			totalTools += toolCount
			// Get the first tool's description as brief
			var brief string
			for _, tool := range childNode.Tools {
				brief = extractBriefDescription(tool.Description)
				break
			}
			childSummaries = append(childSummaries, fmt.Sprintf("%s -> %s", childName, brief))
		} else if entry.IsDir() {
			// Branch node - use its overview (only for directories)
			brief := extractBriefDescription(childNode.Overview)
			childSummaries = append(childSummaries, fmt.Sprintf("%s -> %s", childName, brief))
			// Count tools recursively
			totalTools += countTotalTools(filepath.Join(dirPath, childName))
		}
	}

	// Generate new overview in format: "Name: N tools; child1 -> desc1, child2 -> desc2"
	var generatedOverview string
	if len(childSummaries) == 0 {
		generatedOverview = fmt.Sprintf("%s with no items", nodeName)
	} else if totalTools > 0 {
		generatedOverview = fmt.Sprintf("%s: %d tools; %s", nodeName, totalTools, joinWithCommas(childSummaries))
	} else {
		generatedOverview = fmt.Sprintf("%s: %s", nodeName, joinWithCommas(childSummaries))
	}

	// Check if user has manually edited the overview
	// If existing overview doesn't match what we would have generated previously, preserve it
	var finalOverview string
	if existingData != nil {
		var existingNode ToolNode
		if json.Unmarshal(existingData, &existingNode) == nil {
			// Compare existing with what would be generated
			// If they're different and existing is not empty, user has edited it - preserve it
			if existingNode.Overview != "" && existingNode.Overview != generatedOverview {
				// Check if it looks like a previous auto-generated format
				// Auto-generated always has ":" and either "tools;" or "with"
				isAutoGenerated := strings.Contains(existingNode.Overview, ":") &&
					(strings.Contains(existingNode.Overview, "tools;") || strings.Contains(existingNode.Overview, "with"))

				if !isAutoGenerated {
					// User has manually customized it, preserve it
					finalOverview = existingNode.Overview
				} else {
					// It's an old auto-generated format, update it
					finalOverview = generatedOverview
				}
			} else {
				// Same as generated or empty, use new generated
				finalOverview = generatedOverview
			}
		} else {
			finalOverview = generatedOverview
		}
	} else {
		finalOverview = generatedOverview
	}

	// Create the branch node
	node := ToolNode{
		Path:     nodeName,
		Overview: finalOverview,
		Tools:    nil, // Branch nodes don't have tools
	}

	// Write the updated JSON file
	return writeNodeToJSON(node, nodeJSONPath)
}

// countTotalTools recursively counts all tools in a directory tree
// Supports both nested (tool/tool.json) and flat (tool.json) structures
func countTotalTools(dirPath string) int {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return 0
	}

	// Get the directory name to skip its own JSON file in flat structures
	dirName := filepath.Base(dirPath)

	total := 0
	for _, entry := range entries {
		var data []byte
		var err error
		var jsonPath string

		if entry.IsDir() {
			// Nested structure: check subdirectory for child/child.json
			childPath := filepath.Join(dirPath, entry.Name())
			jsonPath = filepath.Join(childPath, entry.Name()+".json")

			data, err = os.ReadFile(jsonPath)
			if err != nil {
				continue
			}

			var node ToolNode
			if err := json.Unmarshal(data, &node); err != nil {
				continue
			}

			// If this node has tools, count them (leaf node)
			if len(node.Tools) > 0 {
				total += len(node.Tools)
			} else {
				// Otherwise, it's a branch node - recursively count tools in subdirectories
				total += countTotalTools(childPath)
			}
		} else {
			// Flat structure: check for .json files directly in this directory
			// Skip the parent directory's own JSON file
			if !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == dirName+".json" {
				continue
			}

			jsonPath = filepath.Join(dirPath, entry.Name())
			data, err = os.ReadFile(jsonPath)
			if err != nil {
				continue
			}

			var node ToolNode
			if err := json.Unmarshal(data, &node); err != nil {
				continue
			}

			// Flat structure files should be leaf nodes with tools
			if len(node.Tools) > 0 {
				total += len(node.Tools)
			}
		}
	}

	return total
}

// joinWithCommas joins strings with commas
func joinWithCommas(items []string) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 {
		return items[0]
	}

	result := ""
	for i, item := range items {
		if i == 0 {
			result = item
		} else {
			result += ", " + item
		}
	}
	return result
}

// extractBriefDescription extracts a brief description (first sentence or up to semicolon)
func extractBriefDescription(text string) string {
	if text == "" {
		return ""
	}

	// Split on semicolon first
	if idx := strings.Index(text, ";"); idx != -1 {
		return strings.TrimSpace(text[:idx])
	}

	// Split on first period followed by space
	if idx := strings.Index(text, ". "); idx != -1 {
		return strings.TrimSpace(text[:idx+1])
	}

	// Return full text if no delimiter found, but truncate if too long
	if len(text) > 100 {
		return text[:97] + "..."
	}
	return text
}

// generateServerStructure creates the folder and JSON file for a single server
// New structure: server_name/server_name.json (parent) + server_name/tool_name/tool_name.json (children)
func generateServerStructure(server ServerTools, outputDir string) error {
	// Create server directory: structure/server_name/
	serverDir, err := generatedPath(outputDir, server.ServerName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		return fmt.Errorf("failed to create server directory: %w", err)
	}

	// Create individual tool files
	var childSummaries []string
	for _, tool := range server.Tools {
		// Generate tool file (leaf node) in flat structure
		if err := generateToolFile(tool, outputDir, server.ServerName); err != nil {
			return fmt.Errorf("failed to generate tool file for %s: %w", tool.Name, err)
		}

		// Collect brief description for parent overview
		brief := extractBriefDescription(tool.Description)
		if brief == "" {
			brief = fmt.Sprintf("Tool: %s", tool.Name)
		}
		childSummaries = append(childSummaries, fmt.Sprintf("%s -> %s", tool.Name, brief))
	}

	// Generate overview for server: "ServerName: N tools; tool1 -> desc1, tool2 -> desc2"
	toolCount := len(server.Tools)
	var overview string
	if toolCount == 0 {
		overview = fmt.Sprintf("%s MCP server with no tools", server.ServerName)
	} else if toolCount == 1 {
		overview = fmt.Sprintf("%s: 1 tool; %s", server.ServerName, childSummaries[0])
	} else {
		overview = fmt.Sprintf("%s: %d tools; %s", server.ServerName, toolCount, joinWithCommas(childSummaries))
	}

	// Create server-level ToolNode (branch node)
	serverNode := ToolNode{
		Path:     server.ServerName,
		Overview: overview,
		Tools:    nil, // Branch node - no direct tools
	}

	// Write server JSON file: structure/server_name/server_name.json
	jsonPath, err := generatedPath(outputDir, server.ServerName, server.ServerName+".json")
	if err != nil {
		return err
	}
	return writeNodeToJSON(serverNode, jsonPath)
}

// generateToolFile creates a JSON file for a single tool in flat structure
// Structure: parent_dir/tool_name.json
// This creates a leaf node (has tools, no overview)
func generateToolFile(tool Tool, outputDir string, serverName string) error {
	// Flat structure: place tool.json directly in parent directory
	jsonPath, err := generatedPath(outputDir, serverName, tool.Name+".json")
	if err != nil {
		return err
	}

	// Create ToolNode for this tool (leaf node - no overview, only tools)
	toolNode := ToolNode{
		Path:     filepath.Join(serverName, tool.Name),
		Overview: "", // Leaf nodes don't have overview
		Tools: map[string]ToolDefinition{
			tool.Name: {
				Title:        tool.Title,
				Description:  tool.Description,
				MapsTo:       tool.Name,  // Maps to the actual MCP tool name
				Server:       serverName, // The MCP server that provides this tool
				InputSchema:  tool.InputSchema,
				OutputSchema: tool.OutputSchema,
				Annotations:  tool.Annotations,
			},
		},
	}

	// Write tool JSON file
	return writeNodeToJSON(toolNode, jsonPath)
}

// writeNodeToJSON writes a ToolNode to a JSON file with pretty formatting
func writeNodeToJSON(node ToolNode, path string) error {
	// Create file
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Use encoder to avoid HTML escaping (like > becoming \u003e)
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")

	// Encode with pointer to invoke MarshalJSON method
	if err := encoder.Encode(&node); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	return nil
}
