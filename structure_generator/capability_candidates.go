package structure_generator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/gearboxlogic/tappet/internal/capability"
	tappetclient "github.com/gearboxlogic/tappet/internal/client"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

const candidateHeader = "# Generated capability candidate. Review before installation.\n"

type candidateHierarchyNode struct {
	Overview  string                             `json:"overview,omitempty"`
	Tools     map[string]candidateToolDefinition `json:"tools,omitempty"`
	MCPServer json.RawMessage                    `json:"mcp_server,omitempty"`
}

type candidateToolDefinition struct {
	Title        string                 `json:"title,omitempty"`
	Description  string                 `json:"description,omitempty"`
	MapsTo       string                 `json:"maps_to,omitempty"`
	Server       string                 `json:"server"`
	InputSchema  map[string]interface{} `json:"inputSchema,omitempty"`
	OutputSchema map[string]interface{} `json:"outputSchema,omitempty"`
	Annotations  map[string]interface{} `json:"annotations,omitempty"`
}

type candidateManifest struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   candidateMetadata `yaml:"metadata"`
	Spec       candidateSpec     `yaml:"spec"`
}

type candidateMetadata struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Version     string   `yaml:"version"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags,omitempty"`
}

type candidateSpec struct {
	Parent     string               `yaml:"parent,omitempty"`
	Operations []candidateOperation `yaml:"operations"`
	Providers  []candidateProvider  `yaml:"providers"`
}

type candidateOperation struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description"`
	Provider    string `yaml:"provider"`
	Target      string `yaml:"target"`
}

type candidateProvider struct {
	ID        string `yaml:"id"`
	Type      string `yaml:"type"`
	ServerRef string `yaml:"serverRef"`
}

type candidateSource struct {
	capabilityID string
	sourcePath   string
	toolName     string
	tool         candidateToolDefinition
}

// GenerateCapabilityCandidates converts hierarchy leaves into reviewable
// V1-alpha package candidates. It never copies provider configuration or tool
// schemas into a package.
func GenerateCapabilityCandidates(hierarchyDir, outputDir string) error {
	cleanOutputDir := filepath.Clean(outputDir)
	if err := validateOutputLocation(cleanOutputDir); err != nil {
		return err
	}
	if err := validateExistingCandidateDirectory(cleanOutputDir); err != nil {
		return err
	}
	candidates, err := readHierarchyCandidates(hierarchyDir)
	if err != nil {
		return err
	}
	parentDir := filepath.Dir(cleanOutputDir)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return fmt.Errorf("create candidate output parent: %w", err)
	}
	stagingDir, err := os.MkdirTemp(parentDir, "."+filepath.Base(cleanOutputDir)+".tmp-")
	if err != nil {
		return fmt.Errorf("create candidate staging directory: %w", err)
	}
	defer os.RemoveAll(stagingDir)
	if err := os.Chmod(stagingDir, 0o755); err != nil {
		return fmt.Errorf("set candidate staging permissions: %w", err)
	}
	for _, candidate := range candidates {
		if err := writeCapabilityCandidate(stagingDir, candidate); err != nil {
			return err
		}
	}
	if err := replaceGeneratedDirectory(stagingDir, cleanOutputDir); err != nil {
		return fmt.Errorf("publish capability candidates: %w", err)
	}
	return nil
}

func readHierarchyCandidates(hierarchyDir string) ([]candidateSource, error) {
	root, err := filepath.Abs(hierarchyDir)
	if err != nil {
		return nil, fmt.Errorf("resolve hierarchy root: %w", err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect hierarchy root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, errors.New("hierarchy root must be a real directory")
	}

	var candidates []candidateSource
	seenIDs := make(map[string]string)
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("hierarchy contains symlink: %s", filePath)
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" || entry.Name() == "root.json" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return fmt.Errorf("hierarchy node is not a bounded regular file: %s", filePath)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		if err := tappetclient.RejectDuplicateJSONMembers(data); err != nil {
			return fmt.Errorf("invalid hierarchy node %s: %w", filePath, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var node candidateHierarchyNode
		if err := decoder.Decode(&node); err != nil {
			return fmt.Errorf("decode hierarchy node %s: %w", filePath, err)
		}
		if len(node.Tools) == 0 {
			return nil
		}
		nodePath, err := hierarchyNodePath(root, filePath)
		if err != nil {
			return err
		}
		toolNames := make([]string, 0, len(node.Tools))
		for name := range node.Tools {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		for _, toolName := range toolNames {
			tool := node.Tools[toolName]
			if tool.Server == "" {
				return fmt.Errorf("hierarchy tool %s.%s has no server mapping", nodePath, toolName)
			}
			sourceID := nodePath
			lastNodeSegment := sourceID
			if separator := strings.LastIndexByte(sourceID, '.'); separator >= 0 {
				lastNodeSegment = sourceID[separator+1:]
			}
			if len(node.Tools) != 1 || lastNodeSegment != toolName {
				sourceID += "." + toolName
			}
			capabilityID, err := normalizeCapabilityID(sourceID)
			if err != nil {
				return fmt.Errorf("normalize hierarchy tool %s: %w", sourceID, err)
			}
			if previous, exists := seenIDs[capabilityID]; exists {
				return fmt.Errorf("hierarchy paths %q and %q normalize to duplicate capability ID %q", previous, sourceID, capabilityID)
			}
			seenIDs[capabilityID] = sourceID
			relative, _ := filepath.Rel(root, filePath)
			candidates = append(candidates, candidateSource{
				capabilityID: capabilityID,
				sourcePath:   filepath.ToSlash(relative),
				toolName:     toolName,
				tool:         tool,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].capabilityID < candidates[j].capabilityID })
	return candidates, nil
}

func hierarchyNodePath(root, filePath string) (string, error) {
	relative, err := filepath.Rel(root, filePath)
	if err != nil {
		return "", err
	}
	directory := filepath.Dir(relative)
	fileName := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))
	if directory == "." {
		return fileName, nil
	}
	directoryPath := strings.ReplaceAll(filepath.ToSlash(directory), "/", ".")
	if fileName == filepath.Base(directory) {
		return directoryPath, nil
	}
	return directoryPath + "." + fileName, nil
}

func normalizeCapabilityID(value string) (string, error) {
	segments := strings.Split(value, ".")
	for index := range segments {
		segments[index] = normalizeCandidateIdentifier(segments[index])
		if segments[index] == "" {
			return "", errors.New("identifier segment has no letters or digits")
		}
	}
	return strings.Join(segments, "."), nil
}

func normalizeCandidateIdentifier(value string) string {
	value = norm.NFKC.String(value)
	var result strings.Builder
	lastHyphen := false
	runes := []rune(value)
	for index, character := range runes {
		if unicode.IsUpper(character) {
			previousIsLowerOrDigit := index > 0 && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1]))
			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if result.Len() > 0 && !lastHyphen && (previousIsLowerOrDigit || nextIsLower) {
				result.WriteByte('-')
			}
			character = unicode.ToLower(character)
		}
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			result.WriteRune(character)
			lastHyphen = false
			continue
		}
		if result.Len() > 0 && !lastHyphen {
			result.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(result.String(), "-")
}

func writeCapabilityCandidate(root string, candidate candidateSource) error {
	lastSeparator := strings.LastIndexByte(candidate.capabilityID, '.')
	parent := ""
	operationID := candidate.capabilityID
	if lastSeparator >= 0 {
		parent = candidate.capabilityID[:lastSeparator]
		operationID = candidate.capabilityID[lastSeparator+1:]
	}
	providerID := normalizeCandidateIdentifier(candidate.tool.Server)
	if providerID == "" {
		return fmt.Errorf("provider name %q cannot form a package identifier", candidate.tool.Server)
	}
	target := candidate.tool.MapsTo
	if target == "" {
		target = candidate.toolName
	}
	description := strings.TrimSpace(candidate.tool.Description)
	if description == "" {
		description = fmt.Sprintf("Invoke the %s MCP tool.", target)
	}
	name := strings.TrimSpace(candidate.tool.Title)
	if name == "" {
		name = candidate.toolName
	}
	manifest := candidateManifest{
		APIVersion: "tappet.gearboxlogic.dev/v1alpha1",
		Kind:       "Capability",
		Metadata: candidateMetadata{
			ID:          candidate.capabilityID,
			Name:        name,
			Version:     "0.1.0",
			Description: description,
			Tags:        []string{"mcp", providerID},
		},
		Spec: candidateSpec{
			Parent: parent,
			Operations: []candidateOperation{{
				ID:          operationID,
				Description: description,
				Provider:    providerID,
				Target:      target,
			}},
			Providers: []candidateProvider{{ID: providerID, Type: "mcp", ServerRef: candidate.tool.Server}},
		},
	}
	encoded, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode candidate %s: %w", candidate.capabilityID, err)
	}
	complete := append([]byte(candidateHeader+"# Source hierarchy node: "+candidate.sourcePath+"\n"), encoded...)
	if _, err := capability.ParseManifest(complete); err != nil {
		return fmt.Errorf("generated candidate %s failed validation: %w", candidate.capabilityID, err)
	}
	directory, err := generatedPath(root, candidate.capabilityID)
	if err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0o755); err != nil {
		return fmt.Errorf("create candidate directory %s: %w", candidate.capabilityID, err)
	}
	manifestPath, err := generatedPath(directory, "tappet.yaml")
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, complete, 0o644); err != nil {
		return fmt.Errorf("write candidate %s: %w", candidate.capabilityID, err)
	}
	return nil
}

func validateExistingCandidateDirectory(outputDir string) error {
	info, err := os.Lstat(outputDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing to replace non-directory or symlinked candidate output: %s", outputDir)
	}
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return fmt.Errorf("refusing to replace unrecognized candidate output: %s", outputDir)
		}
		manifestPath := filepath.Join(outputDir, entry.Name(), "tappet.yaml")
		data, err := os.ReadFile(manifestPath)
		if err != nil || !bytes.HasPrefix(data, []byte(candidateHeader)) {
			return fmt.Errorf("refusing to replace reviewed or unrecognized package: %s", entry.Name())
		}
	}
	return nil
}
