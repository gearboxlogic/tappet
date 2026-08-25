package capability

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSkillMatchesAgentSkillsReferenceRules(t *testing.T) {
	data := []byte(`---
name: ci-debugging
description: Diagnose CI failures and explain when to inspect logs.
license: MIT
compatibility: Requires an MCP provider.
allowed-tools: Read Bash(git:*)
metadata:
  author: Tappet
---
# CI debugging

Inspect the failed job before changing code.
`)
	document, err := ParseSkill(data, "skills/ci-debugging")

	require.NoError(t, err)
	assert.Equal(t, "ci-debugging", document.Metadata.Name)
	assert.Equal(t, "Tappet", document.Metadata.Metadata["author"])
	assert.Contains(t, document.Body, "Inspect the failed job")
}

func TestParseSkillAcceptsNormalizedUnicodeName(t *testing.T) {
	data := []byte("---\nname: cafe\u0301\ndescription: A Unicode skill.\n---\nBody\n")
	document, err := ParseSkill(data, "skills/café")

	require.NoError(t, err)
	assert.Equal(t, "café", document.Metadata.Name)
}

func TestParseSkillRejectsReferenceValidatorFailures(t *testing.T) {
	testCases := []struct {
		name      string
		skillPath string
		data      string
		part      string
	}{
		{"missing frontmatter", "skills/example", "# Example", "frontmatter delimiter"},
		{"unclosed frontmatter", "skills/example", "---\nname: example\n", "not closed"},
		{"missing name", "skills/example", "---\ndescription: Example.\n---\n", "field is required"},
		{"empty description", "skills/example", "---\nname: example\ndescription: ''\n---\n", "description must be"},
		{"uppercase", "skills/Example", "---\nname: Example\ndescription: Example.\n---\n", "lowercase"},
		{"leading hyphen", "skills/-example", "---\nname: -example\ndescription: Example.\n---\n", "cannot start"},
		{"consecutive hyphens", "skills/ex--ample", "---\nname: ex--ample\ndescription: Example.\n---\n", "consecutive"},
		{"invalid character", "skills/ex_ample", "---\nname: ex_ample\ndescription: Example.\n---\n", "letters, digits"},
		{"directory mismatch", "skills/wrong", "---\nname: right\ndescription: Example.\n---\n", "must match"},
		{"unknown field", "skills/example", "---\nname: example\ndescription: Example.\nsecret: value\n---\n", "unknown_field"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseSkill([]byte(testCase.data), testCase.skillPath)
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.part)
		})
	}
}

func TestParseSkillEnforcesWholeFileFrontmatterAndMetadataLimits(t *testing.T) {
	_, err := ParseSkill(make([]byte, SkillMaxBytes+1), "skills/example")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit_exceeded")

	frontmatter := "---\nname: example\ndescription: Example.\nmetadata:\n  value: |\n    " + strings.Repeat("x", SkillFrontmatterMax) + "\n---\n"
	_, err = ParseSkill([]byte(frontmatter), "skills/example")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "frontmatter_limit_exceeded")

	longDescription := strings.Repeat("x", maxAgentSkillDescriptionChars+1)
	_, err = ParseSkill([]byte("---\nname: example\ndescription: "+longDescription+"\n---\n"), "skills/example")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "description exceeds")
}

func TestParseSkillRejectsAmbiguousFrontmatterBeforeSemanticDecode(t *testing.T) {
	for name, data := range map[string]string{
		"duplicate": "---\nname: example\nname: example\ndescription: Example.\n---\n",
		"anchor":    "---\nname: &name example\ndescription: Example.\n---\n",
		"alias":     "---\nname: example\ndescription: *name\n---\n",
		"merge":     "---\nname: example\ndescription: Example.\n<<: {license: MIT}\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSkill([]byte(data), "skills/example")
			require.Error(t, err)
		})
	}
}
