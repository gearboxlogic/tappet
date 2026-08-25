package capability

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type yamlLimits struct {
	bytes int
	depth int
	nodes int
}

func parseBoundedYAML(data []byte, limits yamlLimits) (*yaml.Node, error) {
	if len(data) > limits.bytes {
		return nil, fmt.Errorf("encoded YAML exceeds %d bytes", limits.bytes)
	}
	if !utf8.Valid(data) {
		return nil, errors.New("YAML is not valid UTF-8")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("YAML contains a NUL byte")
	}
	if err := preflightYAML(data, limits); err != nil {
		return nil, err
	}

	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil && len(extra.Content) != 0 {
		return nil, errors.New("multiple YAML documents are not allowed")
	}
	if len(document.Content) != 1 {
		return nil, errors.New("YAML document is empty")
	}

	count := 0
	if err := validateYAMLNode(document.Content[0], 1, limits, &count); err != nil {
		return nil, err
	}
	return document.Content[0], nil
}

// preflightYAML applies conservative syntax and nesting budgets while scanning
// the bounded bytes. It runs before yaml.v3 constructs its node tree. The exact
// node walk below remains authoritative and catches syntax forms that this
// deliberately small scanner overestimates or cannot classify.
func preflightYAML(data []byte, limits yamlLimits) error {
	lines := bytes.Split(data, []byte{'\n'})
	indentStack := []int{-1}
	flowDepth := 0
	nodes := 0
	blockIndent := -1

	for _, rawLine := range lines {
		line := bytes.TrimSuffix(rawLine, []byte{'\r'})
		indent := leadingSpaces(line)
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}
		if blockIndent >= 0 {
			if indent > blockIndent {
				continue
			}
			blockIndent = -1
		}

		for len(indentStack) > 1 && indent <= indentStack[len(indentStack)-1] {
			indentStack = indentStack[:len(indentStack)-1]
		}
		if indent > indentStack[len(indentStack)-1] {
			indentStack = append(indentStack, indent)
		}
		if len(indentStack)-1+flowDepth > limits.depth {
			return fmt.Errorf("YAML nesting exceeds %d levels before parsing", limits.depth)
		}

		lineNodes, lineFlow, beginsBlock := scanYAMLLine(trimmed)
		nodes += lineNodes
		if nodes > limits.nodes {
			return fmt.Errorf("YAML syntax exceeds %d nodes before parsing", limits.nodes)
		}
		flowDepth += lineFlow
		if flowDepth < 0 {
			flowDepth = 0
		}
		if len(indentStack)-1+flowDepth > limits.depth {
			return fmt.Errorf("YAML nesting exceeds %d levels before parsing", limits.depth)
		}
		if beginsBlock {
			blockIndent = indent
		}
	}
	return nil
}

func leadingSpaces(line []byte) int {
	count := 0
	for count < len(line) && line[count] == ' ' {
		count++
	}
	return count
}

func scanYAMLLine(line []byte) (nodes, flowDelta int, beginsBlock bool) {
	// Every nonempty logical line contributes at least one scalar or collection
	// event. Separators add a conservative count without charging punctuation
	// inside quoted strings or comments.
	nodes = 1
	singleQuoted := false
	doubleQuoted := false
	escaped := false
	lastSignificant := byte(0)
	for index, character := range line {
		if doubleQuoted {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				doubleQuoted = false
			}
			continue
		}
		if singleQuoted {
			if character == '\'' {
				if index+1 < len(line) && line[index+1] == '\'' {
					continue
				}
				singleQuoted = false
			}
			continue
		}
		switch character {
		case '#':
			return nodes, flowDelta, lastSignificant == '|' || lastSignificant == '>'
		case '\'':
			singleQuoted = true
		case '"':
			doubleQuoted = true
		case '{', '[':
			nodes++
			flowDelta++
		case '}', ']':
			flowDelta--
		case ':', ',':
			nodes++
		case '-':
			if index == 0 && (len(line) == 1 || line[1] == ' ') {
				nodes++
			}
		}
		if character != ' ' && character != '\t' {
			lastSignificant = character
		}
	}
	return nodes, flowDelta, lastSignificant == '|' || lastSignificant == '>'
}

func validateYAMLNode(node *yaml.Node, depth int, limits yamlLimits, count *int) error {
	if depth > limits.depth {
		return fmt.Errorf("YAML nesting exceeds %d levels", limits.depth)
	}
	*count++
	if *count > limits.nodes {
		return fmt.Errorf("YAML syntax exceeds %d nodes", limits.nodes)
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return errors.New("YAML anchors and aliases are not allowed")
	}

	if node.Kind == yaml.MappingNode {
		if len(node.Content)%2 != 0 {
			return errors.New("invalid YAML mapping")
		}
		seen := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Value == "<<" {
				return errors.New("YAML merge keys are not allowed")
			}
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return errors.New("YAML mapping keys must be strings")
			}
			if _, ok := seen[key.Value]; ok {
				return fmt.Errorf("duplicate YAML mapping key %q", key.Value)
			}
			seen[key.Value] = struct{}{}
		}
	}

	for _, child := range node.Content {
		if err := validateYAMLNode(child, depth+1, limits, count); err != nil {
			return err
		}
	}
	return nil
}

func mapping(node *yaml.Node, path string, allowed ...string) (map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, packageError("package_manifest_type_invalid", path, errors.New("must be a mapping"))
	}
	known := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		known[name] = struct{}{}
	}
	result := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		name := node.Content[i].Value
		if _, ok := known[name]; !ok {
			return nil, packageError("package_manifest_unknown_field", path+"."+name, errors.New("field is not defined by v1alpha1"))
		}
		result[name] = node.Content[i+1]
	}
	return result, nil
}

func requiredField(fields map[string]*yaml.Node, name, path string) (*yaml.Node, error) {
	node, ok := fields[name]
	if !ok {
		return nil, packageError("package_manifest_required_field", path+"."+name, errors.New("field is required"))
	}
	return node, nil
}

func scalarString(node *yaml.Node, path string) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", packageError("package_manifest_type_invalid", path, errors.New("must be a string"))
	}
	return node.Value, nil
}

func optionalString(fields map[string]*yaml.Node, name, path string) (string, error) {
	node, ok := fields[name]
	if !ok {
		return "", nil
	}
	return scalarString(node, path+"."+name)
}

func sequence(node *yaml.Node, path string) ([]*yaml.Node, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, packageError("package_manifest_type_invalid", path, errors.New("must be a sequence"))
	}
	return node.Content, nil
}
