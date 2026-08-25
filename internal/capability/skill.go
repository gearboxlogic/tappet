package capability

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

const (
	maxAgentSkillNameCharacters     = 64
	maxAgentSkillDescriptionChars   = 1_024
	maxAgentSkillCompatibilityChars = 500
)

// ParseSkill validates one staged SKILL.md against the Agent Skills reference
// rules and Tappet's stricter bounded-input rules.
func ParseSkill(data []byte, skillPath string) (SkillDocument, error) {
	if len(data) > SkillMaxBytes {
		return SkillDocument{}, packageError("package_skill_limit_exceeded", skillPath+"/SKILL.md", fmt.Errorf("exceeds %d bytes", SkillMaxBytes))
	}
	if !utf8.Valid(data) {
		return SkillDocument{}, packageError("package_skill_invalid", skillPath+"/SKILL.md", errors.New("must be valid UTF-8"))
	}
	frontmatter, body, err := splitSkillFrontmatter(data)
	if err != nil {
		return SkillDocument{}, packageError("package_skill_invalid", skillPath+"/SKILL.md", err)
	}
	if len(frontmatter) > SkillFrontmatterMax {
		return SkillDocument{}, packageError("package_skill_frontmatter_limit_exceeded", skillPath+"/SKILL.md", fmt.Errorf("frontmatter exceeds %d bytes", SkillFrontmatterMax))
	}

	root, err := parseBoundedYAML(frontmatter, yamlLimits{
		bytes: SkillFrontmatterMax,
		depth: ManifestMaxDepth,
		nodes: ManifestMaxNodes,
	})
	if err != nil {
		return SkillDocument{}, packageError("package_skill_frontmatter_invalid", skillPath+"/SKILL.md", err)
	}
	fields, err := mapping(root, "skill.frontmatter", "name", "description", "license", "allowed-tools", "metadata", "compatibility")
	if err != nil {
		return SkillDocument{}, err
	}

	name, err := requiredString(fields, "name", "skill.frontmatter")
	if err != nil {
		return SkillDocument{}, err
	}
	description, err := requiredString(fields, "description", "skill.frontmatter")
	if err != nil {
		return SkillDocument{}, err
	}
	license, err := optionalString(fields, "license", "skill.frontmatter")
	if err != nil {
		return SkillDocument{}, err
	}
	allowedTools, err := optionalString(fields, "allowed-tools", "skill.frontmatter")
	if err != nil {
		return SkillDocument{}, err
	}
	compatibility, err := optionalString(fields, "compatibility", "skill.frontmatter")
	if err != nil {
		return SkillDocument{}, err
	}

	name = norm.NFKC.String(strings.TrimSpace(name))
	description = norm.NFC.String(strings.TrimSpace(description))
	license = norm.NFC.String(license)
	allowedTools = norm.NFC.String(allowedTools)
	compatibility = norm.NFC.String(compatibility)
	if err := validateAgentSkillName(name, path.Base(skillPath)); err != nil {
		return SkillDocument{}, packageError("package_skill_metadata_invalid", skillPath+"/SKILL.md", err)
	}
	if description == "" {
		return SkillDocument{}, packageError("package_skill_metadata_invalid", skillPath+"/SKILL.md", errors.New("description must be a non-empty string"))
	}
	if utf8.RuneCountInString(description) > maxAgentSkillDescriptionChars || len(description) > MaxSkillDescription {
		return SkillDocument{}, packageError("package_skill_metadata_invalid", skillPath+"/SKILL.md", fmt.Errorf("description exceeds %d characters or %d normalized UTF-8 bytes", maxAgentSkillDescriptionChars, MaxSkillDescription))
	}
	if utf8.RuneCountInString(compatibility) > maxAgentSkillCompatibilityChars {
		return SkillDocument{}, packageError("package_skill_metadata_invalid", skillPath+"/SKILL.md", fmt.Errorf("compatibility exceeds %d characters", maxAgentSkillCompatibilityChars))
	}

	metadata, err := parseSkillMetadataMap(fields["metadata"])
	if err != nil {
		return SkillDocument{}, err
	}
	return SkillDocument{
		Metadata: SkillMetadata{
			Name:          name,
			Description:   description,
			License:       license,
			AllowedTools:  allowedTools,
			Compatibility: compatibility,
			Metadata:      metadata,
		},
		Body: strings.TrimSpace(string(body)),
	}, nil
}

func splitSkillFrontmatter(data []byte) ([]byte, []byte, error) {
	if !bytes.HasPrefix(data, []byte("---\n")) && !bytes.HasPrefix(data, []byte("---\r\n")) {
		return nil, nil, errors.New("must start with YAML frontmatter delimiter")
	}
	lineStart := bytes.IndexByte(data, '\n') + 1
	for lineStart > 0 && lineStart <= len(data) {
		lineEndOffset := bytes.IndexByte(data[lineStart:], '\n')
		lineEnd := len(data)
		if lineEndOffset >= 0 {
			lineEnd = lineStart + lineEndOffset
		}
		line := bytes.TrimSuffix(data[lineStart:lineEnd], []byte("\r"))
		if bytes.Equal(line, []byte("---")) {
			bodyStart := lineEnd
			if bodyStart < len(data) {
				bodyStart++
			}
			return data[bytes.IndexByte(data, '\n')+1 : lineStart], data[bodyStart:], nil
		}
		if lineEndOffset < 0 {
			break
		}
		lineStart = lineEnd + 1
	}
	return nil, nil, errors.New("frontmatter is not closed with ---")
}

func validateAgentSkillName(name, directoryName string) error {
	if name == "" {
		return errors.New("name must be a non-empty string")
	}
	if utf8.RuneCountInString(name) > maxAgentSkillNameCharacters || len(name) > MaxSkillNameBytes {
		return fmt.Errorf("name exceeds %d characters or %d normalized UTF-8 bytes", maxAgentSkillNameCharacters, MaxSkillNameBytes)
	}
	if name != strings.ToLower(name) {
		return errors.New("name must be lowercase")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return errors.New("name cannot start or end with a hyphen")
	}
	if strings.Contains(name, "--") {
		return errors.New("name cannot contain consecutive hyphens")
	}
	for _, character := range name {
		if character != '-' && !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return errors.New("name may contain only Unicode letters, digits, and hyphens")
		}
	}
	if norm.NFKC.String(directoryName) != name {
		return fmt.Errorf("directory name %q must match skill name %q", directoryName, name)
	}
	return nil
}

func parseSkillMetadataMap(node *yaml.Node) (map[string]string, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, packageError("package_skill_metadata_invalid", "skill.frontmatter.metadata", errors.New("must be a mapping"))
	}
	metadata := make(map[string]string, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if key.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode {
			return nil, packageError("package_skill_metadata_invalid", "skill.frontmatter.metadata", errors.New("keys and values must be scalar"))
		}
		metadata[norm.NFC.String(key.Value)] = norm.NFC.String(value.Value)
	}
	return metadata, nil
}
