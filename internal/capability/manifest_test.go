package capability

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validManifest = `apiVersion: tappet.gearboxlogic.dev/v1alpha1
kind: Capability
metadata:
  id: software.github.ci-debugging
  name: GitHub CI debugging
  version: 0.1.0
  description: Inspect failing GitHub Actions checks.
  tags:
    - github
    - ci
spec:
  parent: software.github
  skills:
    - path: skills/github-actions-debugging
      resources:
        - id: common-failures
          kind: reference
          path: references/common-failures.md
  operations:
    - id: inspect-failed-checks
      description: Inspect failed checks for a pull request.
      provider: github
      target: get_check_runs
  context:
    - id: repository-conventions
      path: context/repository-conventions.md
  providers:
    - id: github
      type: mcp
      serverRef: github
`

func TestParseManifestAcceptsV1AlphaPackage(t *testing.T) {
	manifest, err := ParseManifest([]byte(validManifest))

	require.NoError(t, err)
	assert.Equal(t, manifestAPIVersion, manifest.APIVersion)
	assert.Equal(t, "software.github.ci-debugging", manifest.Metadata.ID)
	assert.Equal(t, "software.github", manifest.Spec.Parent)
	require.Len(t, manifest.Spec.Skills, 1)
	assert.Equal(t, "references/common-failures.md", manifest.Spec.Skills[0].Resources[0].Path)
	require.Len(t, manifest.Spec.Operations, 1)
	assert.Equal(t, "get_check_runs", manifest.Spec.Operations[0].Target)
	require.Len(t, manifest.Spec.Providers, 1)
	assert.Equal(t, "github", manifest.Spec.Providers[0].ServerRef)
}

func TestParseManifestRejectsUnknownFieldsAtEveryMappingLevel(t *testing.T) {
	testCases := []struct {
		name        string
		old         string
		replacement string
		path        string
	}{
		{"root", "kind: Capability", "kind: Capability\ncredential: secret", "manifest.credential"},
		{"metadata", "  name: GitHub CI debugging", "  name: GitHub CI debugging\n  credential: secret", "manifest.metadata.credential"},
		{"spec", "  parent: software.github", "  parent: software.github\n  credential: secret", "manifest.spec.credential"},
		{"skill", "    - path: skills/github-actions-debugging", "    - path: skills/github-actions-debugging\n      credential: secret", "manifest.spec.skills[0].credential"},
		{"skill resource", "          path: references/common-failures.md", "          path: references/common-failures.md\n          credential: secret", "manifest.spec.skills[0].resources[0].credential"},
		{"operation", "      target: get_check_runs", "      target: get_check_runs\n      credential: secret", "manifest.spec.operations[0].credential"},
		{"context", "      path: context/repository-conventions.md", "      path: context/repository-conventions.md\n      credential: secret", "manifest.spec.context[0].credential"},
		{"provider", "      serverRef: github", "      serverRef: github\n      credential: secret", "manifest.spec.providers[0].credential"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(strings.Replace(validManifest, testCase.old, testCase.replacement, 1)))
			assertPackageError(t, err, "package_manifest_unknown_field", testCase.path)
		})
	}
}

func TestParseManifestExcludesCredentialBearingProviderFields(t *testing.T) {
	for _, field := range []string{"token", "env", "headers", "command", "args", "url"} {
		t.Run(field, func(t *testing.T) {
			manifest := strings.Replace(validManifest, "      serverRef: github", "      serverRef: github\n      "+field+": credential-do-not-store", 1)
			_, err := ParseManifest([]byte(manifest))
			assertPackageError(t, err, "package_manifest_unknown_field", "manifest.spec.providers[0]."+field)
			assert.NotContains(t, fmt.Sprint(err), "credential-do-not-store")
		})
	}
}

func TestParseManifestRejectsDuplicateAndInvalidReferences(t *testing.T) {
	testCases := []struct {
		name string
		edit func(string) string
		code string
	}{
		{
			name: "duplicate provider",
			edit: func(value string) string {
				return value + "    - id: github\n      type: mcp\n      serverRef: second\n"
			},
			code: "package_duplicate_reference",
		},
		{
			name: "unknown operation provider",
			edit: func(value string) string { return strings.Replace(value, "provider: github", "provider: missing", 1) },
			code: "package_reference_invalid",
		},
		{
			name: "sideways parent",
			edit: func(value string) string {
				return strings.Replace(value, "parent: software.github", "parent: software.gitlab", 1)
			},
			code: "package_parent_invalid",
		},
		{
			name: "escaping context path",
			edit: func(value string) string {
				return strings.Replace(value, "context/repository-conventions.md", "../credential.txt", 1)
			},
			code: "package_path_invalid",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(testCase.edit(validManifest)))
			assertPackageError(t, err, testCase.code, "")
		})
	}
}

func TestParseManifestRejectsAmbiguousYAML(t *testing.T) {
	testCases := []struct {
		name string
		data string
		part string
	}{
		{"duplicate key", strings.Replace(validManifest, "kind: Capability", "kind: Capability\nkind: Capability", 1), "duplicate YAML mapping key"},
		{"anchor", strings.Replace(validManifest, "name: GitHub CI debugging", "name: &name GitHub CI debugging", 1), "anchors and aliases"},
		{"alias", strings.Replace(strings.Replace(validManifest, "name: GitHub CI debugging", "name: &name GitHub CI debugging", 1), "description: Inspect failing GitHub Actions checks.", "description: *name", 1), "anchors and aliases"},
		{"merge key", strings.Replace(validManifest, "  parent: software.github", "  <<: {parent: software.github}", 1), "merge keys"},
		{"multiple documents", validManifest + "---\nextra: document\n", "multiple YAML documents"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(testCase.data))
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.part)
		})
	}
}

func TestParseManifestEnforcesInputAndSyntaxLimits(t *testing.T) {
	_, err := ParseManifest(make([]byte, ManifestMaxBytes+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")

	deep := "apiVersion: tappet.gearboxlogic.dev/v1alpha1\nkind: Capability\nmetadata:\n  id: a\n  name: a\n  version: 0.1.0\n  description: a\nspec:\n"
	indent := "  "
	for range ManifestMaxDepth + 1 {
		deep += indent + "nested:\n"
		indent += "  "
	}
	deep += indent + "value\n"
	_, err = ParseManifest([]byte(deep))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nesting exceeds")

	var many strings.Builder
	many.WriteString("items:\n")
	for index := 0; index < ManifestMaxNodes; index++ {
		fmt.Fprintf(&many, "  - %d\n", index)
	}
	_, err = parseBoundedYAML([]byte(many.String()), yamlLimits{bytes: ManifestMaxBytes, depth: ManifestMaxDepth, nodes: ManifestMaxNodes})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "syntax exceeds")
}

func assertPackageError(t *testing.T, err error, code, path string) {
	t.Helper()
	require.Error(t, err)
	var packageErr *Error
	require.True(t, errors.As(err, &packageErr), "expected capability.Error, got %T: %v", err, err)
	assert.Equal(t, code, packageErr.Code)
	if path != "" {
		assert.Equal(t, path, packageErr.Path)
	}
}
