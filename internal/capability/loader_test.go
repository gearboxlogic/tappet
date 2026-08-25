package capability

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoaderStagesValidPackageIntoImmutableRecord(t *testing.T) {
	root := t.TempDir()
	packageDir := writePackageFixture(t, root, "software.github.ci-debugging")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)

	record, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	t.Cleanup(record.release)

	assert.Equal(t, "software.github.ci-debugging", record.Metadata().ID)
	assert.Equal(t, "software.github", record.Parent())
	assert.Len(t, record.ManifestDigest(), 64)
	require.Len(t, record.Operations(), 1)
	assert.Equal(t, "get_check_runs", record.Operations()[0].Target)
	require.Len(t, record.Skills(), 1)
	require.Len(t, record.Context(), 1)

	skill := record.Skills()[0]
	skillBytes, ok := record.ReadArtifact(skill.Artifact.Ref)
	require.True(t, ok)
	assert.Contains(t, string(skillBytes), "Inspect the failed job")
	skillBytes[0] = 'x'
	secondRead, ok := record.ReadArtifact(skill.Artifact.Ref)
	require.True(t, ok)
	assert.Equal(t, byte('-'), secondRead[0], "callers must not receive a writable alias")

	require.NoError(t, os.WriteFile(filepath.Join(packageDir, "skills", "github-actions-debugging", "SKILL.md"), []byte("changed source"), 0o644))
	thirdRead, ok := record.ReadArtifact(skill.Artifact.Ref)
	require.True(t, ok)
	assert.Equal(t, secondRead, thirdRead, "published bytes must not follow the mutable source")

	stats := store.Stats()
	assert.Zero(t, stats.StagingReserved)
	assert.Zero(t, stats.SnapshotReserved)
	assert.Positive(t, stats.SnapshotUsed)
	assert.Positive(t, stats.Objects)
}

func TestLoaderValidatesOnlyPrivateStagedBytes(t *testing.T) {
	root := t.TempDir()
	packageDir := writePackageFixture(t, root, "software.github.ci-debugging")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	skillPath := filepath.Join(packageDir, "skills", "github-actions-debugging", "SKILL.md")
	loader.afterStage = func() {
		require.NoError(t, os.WriteFile(skillPath, []byte("invalid replacement"), 0o644))
	}

	record, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	defer record.release()
	skill := record.Skills()[0]
	data, ok := record.ReadArtifact(skill.Artifact.Ref)
	require.True(t, ok)
	assert.Contains(t, string(data), "name: github-actions-debugging")
	assert.Equal(t, "invalid replacement", readFileString(t, skillPath))
}

func TestLoaderFailureDoesNotPublishOrRetainStagedBytes(t *testing.T) {
	root := t.TempDir()
	packageDir := writePackageFixture(t, root, "software.github.ci-debugging")
	require.NoError(t, os.WriteFile(
		filepath.Join(packageDir, "skills", "github-actions-debugging", "SKILL.md"),
		[]byte("not a skill"),
		0o644,
	))
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)

	_, err = loader.Load("software.github.ci-debugging")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "package_skill_invalid")
	assert.Equal(t, StoreStats{}, store.Stats())
}

func TestLoadAllUnknownFieldRejectsCompletePackageSet(t *testing.T) {
	root := t.TempDir()
	writePackageFixture(t, root, "software.github.a-valid")
	invalidDir := writePackageFixture(t, root, "software.github.z-invalid")
	manifestPath := filepath.Join(invalidDir, manifestFileName)
	manifest := strings.Replace(readFileString(t, manifestPath), "kind: Capability", "kind: Capability\ncredential: must-not-survive", 1)
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o644))
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)

	_, err = loader.LoadAll()
	assertPackageError(t, err, "package_manifest_unknown_field", "manifest.credential")
	assert.NotContains(t, fmt.Sprint(err), "must-not-survive")
	assert.Equal(t, StoreStats{}, store.Stats(), "no package generation may survive an atomic load failure")
}

func TestRecordGettersDoNotExposeMutableState(t *testing.T) {
	root := t.TempDir()
	writePackageFixture(t, root, "software.github.ci-debugging")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	record, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	defer record.release()

	metadata := record.Metadata()
	metadata.Tags[0] = "mutated"
	skills := record.Skills()
	skills[0].Metadata.Metadata["owner"] = "mutated"
	skills[0].Resources[0].ID = "mutated"
	operations := record.Operations()
	operations[0].Target = "mutated"
	context := record.Context()
	context[0].Path = "mutated"
	providers := record.Providers()
	providers[0].ServerRef = "mutated"

	assert.Equal(t, "github", record.Metadata().Tags[0])
	assert.Equal(t, "platform", record.Skills()[0].Metadata.Metadata["owner"])
	assert.Equal(t, "common-failures", record.Skills()[0].Resources[0].ID)
	assert.Equal(t, "get_check_runs", record.Operations()[0].Target)
	assert.Equal(t, "context/repository-conventions.md", record.Context()[0].Path)
	assert.Equal(t, "github", record.Providers()[0].ServerRef)
}

func TestLoaderReservesAggregateCapacityBeforeArtifactCopies(t *testing.T) {
	root := t.TempDir()
	packageDir := writePackageFixture(t, root, "software.github.ci-debugging")
	contextPath := filepath.Join(packageDir, "context", "repository-conventions.md")
	require.NoError(t, os.Truncate(contextPath, 1024))
	store, err := NewSnapshotStore(StoreLimits{StagingBytes: 512, SnapshotBytes: 512})
	require.NoError(t, err)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)

	_, err = loader.Load("software.github.ci-debugging")
	assertPackageError(t, err, "package_staging_capacity_exhausted", "")
	assert.Equal(t, StoreStats{}, store.Stats())
}

func TestLoaderRejectsArtifactAtDeclaredHardLimitBeforeRead(t *testing.T) {
	testCases := []struct {
		name     string
		relative string
		limit    int64
	}{
		{"skill", "skills/github-actions-debugging/SKILL.md", SkillMaxBytes},
		{"skill resource", "skills/github-actions-debugging/references/common-failures.md", ArtifactMaxBytes},
		{"context", "context/repository-conventions.md", ArtifactMaxBytes},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			packageDir := writePackageFixture(t, root, "software.github.ci-debugging")
			require.NoError(t, os.Truncate(filepath.Join(packageDir, filepath.FromSlash(testCase.relative)), testCase.limit+1))
			store := newTestSnapshotStore(t)
			loader, err := NewLoader(root, store)
			require.NoError(t, err)

			_, err = loader.Load("software.github.ci-debugging")
			assertPackageError(t, err, "package_artifact_limit_exceeded", "")
			assert.Equal(t, StoreStats{}, store.Stats())
		})
	}
}

func TestLoaderAcceptsArtifactsAtExactHardLimits(t *testing.T) {
	root := t.TempDir()
	packageDir := writePackageFixture(t, root, "software.github.ci-debugging")
	skillPath := filepath.Join(packageDir, "skills", "github-actions-debugging", "SKILL.md")
	skillPrefix := []byte("---\nname: github-actions-debugging\ndescription: Diagnose CI failures.\n---\n")
	require.NoError(t, os.WriteFile(skillPath, append(skillPrefix, []byte(strings.Repeat("x", SkillMaxBytes-len(skillPrefix)))...), 0o644))
	resourcePath := filepath.Join(packageDir, "skills", "github-actions-debugging", "references", "common-failures.md")
	require.NoError(t, os.WriteFile(resourcePath, []byte(strings.Repeat("r", ArtifactMaxBytes)), 0o644))
	contextPath := filepath.Join(packageDir, "context", "repository-conventions.md")
	require.NoError(t, os.WriteFile(contextPath, []byte(strings.Repeat("c", ArtifactMaxBytes)), 0o644))
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)

	record, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	defer record.release()
	assert.Equal(t, int64(SkillMaxBytes), record.Skills()[0].Artifact.Bytes)
	assert.Equal(t, int64(ArtifactMaxBytes), record.Skills()[0].Resources[0].Bytes)
	assert.Equal(t, int64(ArtifactMaxBytes), record.Context()[0].Bytes)
}

func TestLoaderRejectsAggregatePackageBytesBeforeArtifactCopies(t *testing.T) {
	root := t.TempDir()
	packageDir := writePackageFixture(t, root, "software.github.ci-debugging")
	manifestPath := filepath.Join(packageDir, manifestFileName)
	manifest := readFileString(t, manifestPath)
	var extraManifest strings.Builder
	for index := 0; index < 16; index++ {
		fmt.Fprintf(&extraManifest, "    - id: extra-%d\n      path: context/extra-%d.md\n", index, index)
		extraPath := filepath.Join(packageDir, "context", fmt.Sprintf("extra-%d.md", index))
		file, err := os.Create(extraPath)
		require.NoError(t, err)
		require.NoError(t, file.Truncate(ArtifactMaxBytes))
		require.NoError(t, file.Close())
	}
	manifest = strings.Replace(manifest, "  providers:\n", extraManifest.String()+"  providers:\n", 1)
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o644))
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)

	_, err = loader.Load("software.github.ci-debugging")
	assertPackageError(t, err, "package_staging_capacity_exhausted", "")
	assert.Equal(t, StoreStats{}, store.Stats())
}

func TestLoaderRejectsNonTextReferences(t *testing.T) {
	root := t.TempDir()
	packageDir := writePackageFixture(t, root, "software.github.ci-debugging")
	contextPath := filepath.Join(packageDir, "context", "repository-conventions.md")
	require.NoError(t, os.WriteFile(contextPath, []byte{0xff, 0xfe}, 0o644))
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)

	_, err = loader.Load("software.github.ci-debugging")
	assertPackageError(t, err, "package_artifact_invalid", "context/repository-conventions.md")
	assert.Equal(t, StoreStats{}, store.Stats())
}

func TestLoaderReleasesSnapshotObjects(t *testing.T) {
	root := t.TempDir()
	writePackageFixture(t, root, "software.github.ci-debugging")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	record, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	assert.Positive(t, store.Stats().SnapshotUsed)

	record.release()
	assert.Equal(t, StoreStats{}, store.Stats())
}

func newTestSnapshotStore(t *testing.T) *SnapshotStore {
	t.Helper()
	store, err := NewSnapshotStore(DefaultStoreLimits())
	require.NoError(t, err)
	return store
}

func writePackageFixture(t *testing.T, root, id string) string {
	t.Helper()
	packageDir := filepath.Join(root, id)
	require.NoError(t, os.MkdirAll(filepath.Join(packageDir, "skills", "github-actions-debugging", "references"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(packageDir, "context"), 0o755))
	manifest := strings.Replace(validManifest, "software.github.ci-debugging", id, 1)
	require.NoError(t, os.WriteFile(filepath.Join(packageDir, manifestFileName), []byte(manifest), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(packageDir, "skills", "github-actions-debugging", "SKILL.md"), []byte(`---
name: github-actions-debugging
description: Diagnose GitHub Actions failures from job logs.
metadata:
  owner: platform
---
# GitHub Actions debugging

Inspect the failed job before changing code.
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(packageDir, "skills", "github-actions-debugging", "references", "common-failures.md"), []byte("# Common failures\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(packageDir, "context", "repository-conventions.md"), []byte("# Repository conventions\n"), 0o644))
	return packageDir
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
