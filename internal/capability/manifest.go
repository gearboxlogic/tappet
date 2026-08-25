package capability

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

const (
	manifestAPIVersion = "tappet.gearboxlogic.dev/v1alpha1"
	manifestKind       = "Capability"
)

var (
	capabilityIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$`)
	localIDPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	semverPattern       = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
)

// ParseManifest parses and validates a closed V1-alpha capability manifest.
// Cross-file checks run later against private staged artifact bytes.
func ParseManifest(data []byte) (Manifest, error) {
	root, err := parseBoundedYAML(data, yamlLimits{
		bytes: ManifestMaxBytes,
		depth: ManifestMaxDepth,
		nodes: ManifestMaxNodes,
	})
	if err != nil {
		return Manifest{}, packageError("package_manifest_invalid", "tappet.yaml", err)
	}
	fields, err := mapping(root, "manifest", "apiVersion", "kind", "metadata", "spec")
	if err != nil {
		return Manifest{}, err
	}

	apiNode, err := requiredField(fields, "apiVersion", "manifest")
	if err != nil {
		return Manifest{}, err
	}
	apiVersion, err := scalarString(apiNode, "manifest.apiVersion")
	if err != nil {
		return Manifest{}, err
	}
	if apiVersion != manifestAPIVersion {
		return Manifest{}, packageError("package_manifest_version_unsupported", "manifest.apiVersion", fmt.Errorf("must equal %q", manifestAPIVersion))
	}

	kindNode, err := requiredField(fields, "kind", "manifest")
	if err != nil {
		return Manifest{}, err
	}
	kind, err := scalarString(kindNode, "manifest.kind")
	if err != nil {
		return Manifest{}, err
	}
	if kind != manifestKind {
		return Manifest{}, packageError("package_manifest_kind_unsupported", "manifest.kind", fmt.Errorf("must equal %q", manifestKind))
	}

	metadataNode, err := requiredField(fields, "metadata", "manifest")
	if err != nil {
		return Manifest{}, err
	}
	metadata, err := parseMetadata(metadataNode)
	if err != nil {
		return Manifest{}, err
	}
	specNode, err := requiredField(fields, "spec", "manifest")
	if err != nil {
		return Manifest{}, err
	}
	spec, err := parseSpec(specNode, metadata.ID)
	if err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{APIVersion: apiVersion, Kind: kind, Metadata: metadata, Spec: spec}
	if err := validateNormalizedSize(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func parseMetadata(node *yaml.Node) (Metadata, error) {
	fields, err := mapping(node, "manifest.metadata", "id", "name", "version", "description", "tags")
	if err != nil {
		return Metadata{}, err
	}
	id, err := requiredString(fields, "id", "manifest.metadata")
	if err != nil {
		return Metadata{}, err
	}
	name, err := requiredString(fields, "name", "manifest.metadata")
	if err != nil {
		return Metadata{}, err
	}
	version, err := requiredString(fields, "version", "manifest.metadata")
	if err != nil {
		return Metadata{}, err
	}
	description, err := requiredString(fields, "description", "manifest.metadata")
	if err != nil {
		return Metadata{}, err
	}

	id = norm.NFC.String(id)
	name = norm.NFC.String(name)
	version = norm.NFC.String(version)
	description = norm.NFC.String(description)
	if err := validateRequiredText("manifest.metadata.id", id, MaxCapabilityIDBytes); err != nil {
		return Metadata{}, err
	}
	if !capabilityIDPattern.MatchString(id) {
		return Metadata{}, packageError("package_capability_id_invalid", "manifest.metadata.id", errors.New("must be a lowercase dot-delimited identifier"))
	}
	if err := validateRequiredText("manifest.metadata.name", name, MaxNameBytes); err != nil {
		return Metadata{}, err
	}
	if err := validateRequiredText("manifest.metadata.version", version, MaxVersionBytes); err != nil {
		return Metadata{}, err
	}
	if !semverPattern.MatchString(version) {
		return Metadata{}, packageError("package_version_invalid", "manifest.metadata.version", errors.New("must be semantic version syntax"))
	}
	if err := validateRequiredText("manifest.metadata.description", description, MaxDescriptionBytes); err != nil {
		return Metadata{}, err
	}

	tags, err := parseStringSequence(fields["tags"], "manifest.metadata.tags", MaxTags, MaxTagBytes)
	if err != nil {
		return Metadata{}, err
	}
	tagBytes := 0
	seenTags := make(map[string]struct{}, len(tags))
	for i := range tags {
		tags[i] = norm.NFC.String(tags[i])
		tagBytes += len(tags[i])
		if tags[i] == "" {
			return Metadata{}, packageError("package_field_invalid", fmt.Sprintf("manifest.metadata.tags[%d]", i), errors.New("must not be empty"))
		}
		if _, ok := seenTags[tags[i]]; ok {
			return Metadata{}, packageError("package_duplicate_reference", fmt.Sprintf("manifest.metadata.tags[%d]", i), fmt.Errorf("duplicate tag %q", tags[i]))
		}
		seenTags[tags[i]] = struct{}{}
	}
	if tagBytes > MaxAllTagsBytes {
		return Metadata{}, packageError("package_field_limit_exceeded", "manifest.metadata.tags", fmt.Errorf("normalized tags exceed %d bytes", MaxAllTagsBytes))
	}
	return Metadata{ID: id, Name: name, Version: version, Description: description, Tags: tags}, nil
}

func parseSpec(node *yaml.Node, capabilityID string) (Spec, error) {
	fields, err := mapping(node, "manifest.spec", "parent", "skills", "operations", "context", "providers")
	if err != nil {
		return Spec{}, err
	}
	parent, err := optionalString(fields, "parent", "manifest.spec")
	if err != nil {
		return Spec{}, err
	}
	parent = norm.NFC.String(parent)
	if parent != "" {
		if len(parent) > MaxHierarchyPathBytes || !capabilityIDPattern.MatchString(parent) {
			return Spec{}, packageError("package_parent_invalid", "manifest.spec.parent", errors.New("must be a bounded lowercase dot-delimited path"))
		}
		if !strings.HasPrefix(capabilityID, parent+".") {
			return Spec{}, packageError("package_parent_invalid", "manifest.spec.parent", errors.New("must be a proper dot-delimited prefix of the capability ID"))
		}
	}

	providers, err := parseProviders(fields["providers"])
	if err != nil {
		return Spec{}, err
	}
	providerIDs := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		providerIDs[provider.ID] = struct{}{}
	}
	operations, err := parseOperations(fields["operations"], providerIDs)
	if err != nil {
		return Spec{}, err
	}
	skills, err := parseSkills(fields["skills"])
	if err != nil {
		return Spec{}, err
	}
	contextRefs, err := parseContext(fields["context"])
	if err != nil {
		return Spec{}, err
	}
	return Spec{Parent: parent, Skills: skills, Operations: operations, Context: contextRefs, Providers: providers}, nil
}

func parseProviders(node *yaml.Node) ([]ProviderBinding, error) {
	items, err := sequence(node, "manifest.spec.providers")
	if err != nil {
		return nil, err
	}
	if len(items) > MaxProviders {
		return nil, countLimitError("manifest.spec.providers", len(items), MaxProviders)
	}
	result := make([]ProviderBinding, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		itemPath := fmt.Sprintf("manifest.spec.providers[%d]", i)
		fields, err := mapping(item, itemPath, "id", "type", "serverRef")
		if err != nil {
			return nil, err
		}
		id, err := requiredNormalizedID(fields, "id", itemPath, MaxProviderIDBytes)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[id]; ok {
			return nil, duplicateError(itemPath+".id", id)
		}
		seen[id] = struct{}{}
		providerType, err := requiredString(fields, "type", itemPath)
		if err != nil {
			return nil, err
		}
		if err := validateRequiredText(itemPath+".type", providerType, MaxProviderTypeBytes); err != nil {
			return nil, err
		}
		if providerType != "mcp" {
			return nil, packageError("package_provider_type_unsupported", itemPath+".type", errors.New("only mcp is supported"))
		}
		serverRef, err := requiredString(fields, "serverRef", itemPath)
		if err != nil {
			return nil, err
		}
		serverRef = norm.NFC.String(serverRef)
		if err := validateRequiredText(itemPath+".serverRef", serverRef, MaxServerRefBytes); err != nil {
			return nil, err
		}
		result = append(result, ProviderBinding{ID: id, Type: providerType, ServerRef: serverRef})
	}
	return result, nil
}

func parseOperations(node *yaml.Node, providers map[string]struct{}) ([]Operation, error) {
	items, err := sequence(node, "manifest.spec.operations")
	if err != nil {
		return nil, err
	}
	if len(items) > MaxOperations {
		return nil, countLimitError("manifest.spec.operations", len(items), MaxOperations)
	}
	result := make([]Operation, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		itemPath := fmt.Sprintf("manifest.spec.operations[%d]", i)
		fields, err := mapping(item, itemPath, "id", "description", "provider", "target")
		if err != nil {
			return nil, err
		}
		id, err := requiredNormalizedID(fields, "id", itemPath, MaxOperationIDBytes)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[id]; ok {
			return nil, duplicateError(itemPath+".id", id)
		}
		seen[id] = struct{}{}
		description, err := requiredString(fields, "description", itemPath)
		if err != nil {
			return nil, err
		}
		description = norm.NFC.String(description)
		if err := validateRequiredText(itemPath+".description", description, MaxOperationDescription); err != nil {
			return nil, err
		}
		provider, err := requiredNormalizedID(fields, "provider", itemPath, MaxProviderAliasBytes)
		if err != nil {
			return nil, err
		}
		if _, ok := providers[provider]; !ok {
			return nil, packageError("package_reference_invalid", itemPath+".provider", fmt.Errorf("unknown provider %q", provider))
		}
		target, err := requiredString(fields, "target", itemPath)
		if err != nil {
			return nil, err
		}
		target = norm.NFC.String(target)
		if err := validateRequiredText(itemPath+".target", target, MaxOperationTargetBytes); err != nil {
			return nil, err
		}
		result = append(result, Operation{ID: id, Description: description, Provider: provider, Target: target})
	}
	return result, nil
}

func parseSkills(node *yaml.Node) ([]SkillDeclaration, error) {
	items, err := sequence(node, "manifest.spec.skills")
	if err != nil {
		return nil, err
	}
	if len(items) > MaxSkills {
		return nil, countLimitError("manifest.spec.skills", len(items), MaxSkills)
	}
	result := make([]SkillDeclaration, 0, len(items))
	seenPaths := make(map[string]struct{}, len(items))
	totalResources := 0
	for i, item := range items {
		itemPath := fmt.Sprintf("manifest.spec.skills[%d]", i)
		fields, err := mapping(item, itemPath, "path", "resources")
		if err != nil {
			return nil, err
		}
		skillPath, err := requiredString(fields, "path", itemPath)
		if err != nil {
			return nil, err
		}
		skillPath, err = normalizePackagePath(skillPath, MaxSkillPathBytes, itemPath+".path")
		if err != nil {
			return nil, err
		}
		if _, ok := seenPaths[skillPath]; ok {
			return nil, duplicateError(itemPath+".path", skillPath)
		}
		seenPaths[skillPath] = struct{}{}

		resourceNodes, err := sequence(fields["resources"], itemPath+".resources")
		if err != nil {
			return nil, err
		}
		if len(resourceNodes) > MaxResourcesPerSkill {
			return nil, countLimitError(itemPath+".resources", len(resourceNodes), MaxResourcesPerSkill)
		}
		totalResources += len(resourceNodes)
		if totalResources > MaxSkillResources {
			return nil, countLimitError("manifest.spec.skills.resources", totalResources, MaxSkillResources)
		}
		resources := make([]SkillResource, 0, len(resourceNodes))
		seenIDs := make(map[string]struct{}, len(resourceNodes))
		seenResourcePaths := make(map[string]struct{}, len(resourceNodes))
		for resourceIndex, resourceNode := range resourceNodes {
			resourcePath := fmt.Sprintf("%s.resources[%d]", itemPath, resourceIndex)
			resourceFields, err := mapping(resourceNode, resourcePath, "id", "kind", "path")
			if err != nil {
				return nil, err
			}
			id, err := requiredNormalizedID(resourceFields, "id", resourcePath, MaxResourceIDBytes)
			if err != nil {
				return nil, err
			}
			if _, ok := seenIDs[id]; ok {
				return nil, duplicateError(resourcePath+".id", id)
			}
			seenIDs[id] = struct{}{}
			kind, err := requiredString(resourceFields, "kind", resourcePath)
			if err != nil {
				return nil, err
			}
			if len(kind) > MaxResourceKindBytes || kind != "reference" {
				return nil, packageError("package_resource_kind_unsupported", resourcePath+".kind", errors.New("only reference is supported"))
			}
			localPath, err := requiredString(resourceFields, "path", resourcePath)
			if err != nil {
				return nil, err
			}
			localPath, err = normalizePackagePath(localPath, MaxResourcePathBytes, resourcePath+".path")
			if err != nil {
				return nil, err
			}
			if _, ok := seenResourcePaths[localPath]; ok {
				return nil, duplicateError(resourcePath+".path", localPath)
			}
			seenResourcePaths[localPath] = struct{}{}
			resources = append(resources, SkillResource{ID: id, Kind: kind, Path: localPath})
		}
		result = append(result, SkillDeclaration{Path: skillPath, Resources: resources})
	}
	return result, nil
}

func parseContext(node *yaml.Node) ([]ContextReference, error) {
	items, err := sequence(node, "manifest.spec.context")
	if err != nil {
		return nil, err
	}
	if len(items) > MaxContextReferences {
		return nil, countLimitError("manifest.spec.context", len(items), MaxContextReferences)
	}
	result := make([]ContextReference, 0, len(items))
	seenIDs := make(map[string]struct{}, len(items))
	seenPaths := make(map[string]struct{}, len(items))
	for i, item := range items {
		itemPath := fmt.Sprintf("manifest.spec.context[%d]", i)
		fields, err := mapping(item, itemPath, "id", "path")
		if err != nil {
			return nil, err
		}
		id, err := requiredNormalizedID(fields, "id", itemPath, MaxContextIDBytes)
		if err != nil {
			return nil, err
		}
		if _, ok := seenIDs[id]; ok {
			return nil, duplicateError(itemPath+".id", id)
		}
		seenIDs[id] = struct{}{}
		localPath, err := requiredString(fields, "path", itemPath)
		if err != nil {
			return nil, err
		}
		localPath, err = normalizePackagePath(localPath, MaxContextPathBytes, itemPath+".path")
		if err != nil {
			return nil, err
		}
		if _, ok := seenPaths[localPath]; ok {
			return nil, duplicateError(itemPath+".path", localPath)
		}
		seenPaths[localPath] = struct{}{}
		result = append(result, ContextReference{ID: id, Path: localPath})
	}
	return result, nil
}

func requiredString(fields map[string]*yaml.Node, name, path string) (string, error) {
	node, err := requiredField(fields, name, path)
	if err != nil {
		return "", err
	}
	return scalarString(node, path+"."+name)
}

func requiredNormalizedID(fields map[string]*yaml.Node, name, itemPath string, maxBytes int) (string, error) {
	value, err := requiredString(fields, name, itemPath)
	if err != nil {
		return "", err
	}
	value = norm.NFC.String(value)
	if err := validateRequiredText(itemPath+"."+name, value, maxBytes); err != nil {
		return "", err
	}
	if !localIDPattern.MatchString(value) {
		return "", packageError("package_identifier_invalid", itemPath+"."+name, errors.New("must be a lowercase identifier containing letters, digits, and internal hyphens"))
	}
	return value, nil
}

func parseStringSequence(node *yaml.Node, fieldPath string, maxItems, maxItemBytes int) ([]string, error) {
	items, err := sequence(node, fieldPath)
	if err != nil {
		return nil, err
	}
	if len(items) > maxItems {
		return nil, countLimitError(fieldPath, len(items), maxItems)
	}
	result := make([]string, 0, len(items))
	for i, item := range items {
		value, err := scalarString(item, fmt.Sprintf("%s[%d]", fieldPath, i))
		if err != nil {
			return nil, err
		}
		value = norm.NFC.String(value)
		if len(value) > maxItemBytes {
			return nil, packageError("package_field_limit_exceeded", fmt.Sprintf("%s[%d]", fieldPath, i), fmt.Errorf("exceeds %d normalized UTF-8 bytes", maxItemBytes))
		}
		result = append(result, value)
	}
	return result, nil
}

func validateRequiredText(fieldPath, value string, maxBytes int) error {
	if value == "" {
		return packageError("package_field_invalid", fieldPath, errors.New("must not be empty"))
	}
	if !utf8.ValidString(value) {
		return packageError("package_field_invalid", fieldPath, errors.New("must be valid UTF-8"))
	}
	if len(value) > maxBytes {
		return packageError("package_field_limit_exceeded", fieldPath, fmt.Errorf("exceeds %d normalized UTF-8 bytes", maxBytes))
	}
	return nil
}

func normalizePackagePath(value string, maxBytes int, fieldPath string) (string, error) {
	value = norm.NFC.String(value)
	if err := validateRequiredText(fieldPath, value, maxBytes); err != nil {
		return "", err
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || strings.ContainsRune(value, 0) {
		return "", packageError("package_path_invalid", fieldPath, errors.New("must be a portable package-relative path"))
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != value {
		return "", packageError("package_path_invalid", fieldPath, errors.New("must be normalized and remain inside the package"))
	}
	return cleaned, nil
}

func validateNormalizedSize(manifest Manifest) error {
	baseCard := struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Version     string   `json:"version"`
		Description string   `json:"description"`
		Path        string   `json:"path,omitempty"`
		Tags        []string `json:"tags,omitempty"`
	}{manifest.Metadata.ID, manifest.Metadata.Name, manifest.Metadata.Version, manifest.Metadata.Description, manifest.Spec.Parent, manifest.Metadata.Tags}
	encodedCard, err := json.Marshal(baseCard)
	if err != nil {
		return packageError("package_normalization_failed", "manifest.metadata", err)
	}
	if len(encodedCard) > MaxBaseCardBytes {
		return packageError("package_structure_limit_exceeded", "manifest.metadata", fmt.Errorf("normalized base card exceeds %d encoded JSON bytes", MaxBaseCardBytes))
	}

	total := 0
	items := make([]any, 0, len(manifest.Spec.Skills)+len(manifest.Spec.Operations)+len(manifest.Spec.Context)+len(manifest.Spec.Providers))
	for _, item := range manifest.Spec.Skills {
		items = append(items, item)
		for _, resource := range item.Resources {
			items = append(items, resource)
		}
	}
	for _, item := range manifest.Spec.Operations {
		items = append(items, item)
	}
	for _, item := range manifest.Spec.Context {
		items = append(items, item)
	}
	for _, item := range manifest.Spec.Providers {
		items = append(items, item)
	}
	for index, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			return packageError("package_normalization_failed", fmt.Sprintf("manifest.structure[%d]", index), err)
		}
		if len(encoded) > MaxStructureItemBytes {
			return packageError("package_structure_limit_exceeded", fmt.Sprintf("manifest.structure[%d]", index), fmt.Errorf("normalized item exceeds %d encoded JSON bytes", MaxStructureItemBytes))
		}
		total += len(encoded)
	}
	if total > MaxStructureBytes {
		return packageError("package_structure_limit_exceeded", "manifest.spec", fmt.Errorf("normalized structure exceeds %d encoded JSON bytes", MaxStructureBytes))
	}
	return nil
}

func countLimitError(fieldPath string, count, limit int) error {
	return packageError("package_count_limit_exceeded", fieldPath, fmt.Errorf("contains %d items, limit is %d", count, limit))
}

func duplicateError(fieldPath, value string) error {
	return packageError("package_duplicate_reference", fieldPath, fmt.Errorf("duplicate value %q", value))
}
