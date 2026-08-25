package capability

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildProjectionIncludesOnlySelectedPackageStructure(t *testing.T) {
	record := loadReviewedRecord(t, "everything.add")
	projection, err := BuildProjection(record, []string{DescribeReferences, DescribeSkills, DescribeOperations})
	require.NoError(t, err)
	encoded, err := json.Marshal(projection.serializable())
	require.NoError(t, err)

	assert.Equal(t, []string{DescribeSkills, DescribeOperations, DescribeReferences}, projection.Include)
	assert.Equal(t, CapabilityCounts{Skills: 1, Operations: 1, References: 2}, projection.Counts)
	assert.NotContains(t, string(encoded), "Use numeric addition")
	assert.NotContains(t, string(encoded), "serverRef")
	assert.NotContains(t, string(encoded), "everything-server")
	assert.Equal(t, "unavailable", projection.Operations[0].MetadataState)
	record.release()
}

func TestDescribePagesAreStableRetryableAndSurviveUninstall(t *testing.T) {
	record := loadReviewedRecord(t, "everything.add")
	registry, err := NewRegistry(record)
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	clock := newFakeClock(time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC))
	store, err := newProjectionStore(registry, DefaultProjectionLimits(), clock.Now, bytes.NewReader(bytes.Repeat([]byte{9}, 256)))
	require.NoError(t, err)
	t.Cleanup(store.Close)

	first, err := store.Describe(DescribeRequest{CapabilityID: "everything.add", Include: []string{DescribeSkills, DescribeOperations, DescribeReferences}, Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, first.NextCursor)
	assert.Len(t, first.Skills, 1)
	assert.Empty(t, first.Operations)
	require.NoError(t, registry.Uninstall("everything.add"))

	secondRequest := DescribeRequest{Cursor: first.NextCursor, Limit: 1}
	second, err := store.Describe(secondRequest)
	require.NoError(t, err)
	retry, err := store.Describe(secondRequest)
	require.NoError(t, err)
	assert.Equal(t, second, retry)
	assert.Len(t, second.Operations, 1)
	third, err := store.Describe(DescribeRequest{Cursor: second.NextCursor, Limit: 1})
	require.NoError(t, err)
	assert.Len(t, third.References, 1)
	fourth, err := store.Describe(DescribeRequest{Cursor: third.NextCursor, Limit: 1})
	require.NoError(t, err)
	assert.Len(t, fourth.References, 1)
	assert.Empty(t, fourth.NextCursor)
	assert.Equal(t, []string{"provider-behavior", "skills/numeric-addition/number-inputs"}, []string{third.References[0].ID, fourth.References[0].ID})
}

func TestDescribeOnePageRetainsNoProjectionState(t *testing.T) {
	registry, err := NewRegistry(syntheticRecord("single", ""))
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	store, err := newProjectionStore(registry, DefaultProjectionLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{2}, 128)))
	require.NoError(t, err)
	t.Cleanup(store.Close)

	response, err := store.Describe(DescribeRequest{CapabilityID: "single", Include: []string{DescribeOperations}, Limit: 1})
	require.NoError(t, err)
	assert.Empty(t, response.NextCursor)
	assert.Equal(t, ProjectionStats{}, store.Stats())
}

func TestDescribeConcurrentCursorRetryCannotSkipOrDuplicate(t *testing.T) {
	registry, err := NewRegistry(maximumStructureRecord())
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	store, err := newProjectionStore(registry, DefaultProjectionLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{6}, 1024)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	first, err := store.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeSkills, DescribeOperations, DescribeReferences}, Limit: 37})
	require.NoError(t, err)

	responses := make(chan DescribeResponse, 32)
	errorsSeen := make(chan error, 32)
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, callErr := store.Describe(DescribeRequest{Cursor: first.NextCursor, Limit: 37})
			responses <- response
			errorsSeen <- callErr
		}()
	}
	wait.Wait()
	close(responses)
	close(errorsSeen)
	for callErr := range errorsSeen {
		require.NoError(t, callErr)
	}
	var expected *DescribeResponse
	for response := range responses {
		if expected == nil {
			copy := response
			expected = &copy
			continue
		}
		assert.Equal(t, *expected, response)
	}
}

func TestDescribeReconstructsMaximumStructureAcrossBoundedPages(t *testing.T) {
	registry, err := NewRegistry(maximumStructureRecord())
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	store, err := newProjectionStore(registry, DefaultProjectionLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{8}, 2048)))
	require.NoError(t, err)
	t.Cleanup(store.Close)

	request := DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeReferences, DescribeOperations, DescribeSkills}, Limit: 37}
	response, err := store.Describe(request)
	require.NoError(t, err)
	var skills, operations, references int
	for {
		encoded, marshalErr := json.Marshal(response)
		require.NoError(t, marshalErr)
		assert.LessOrEqual(t, len(encoded), BrokerResponseMaxBytes)
		skills += len(response.Skills)
		operations += len(response.Operations)
		references += len(response.References)
		if response.NextCursor == "" {
			break
		}
		response, err = store.Describe(DescribeRequest{Cursor: response.NextCursor, Limit: 37})
		require.NoError(t, err)
	}
	assert.Equal(t, MaxSkills, skills)
	assert.Equal(t, MaxOperations, operations)
	assert.Equal(t, MaxContextReferences+MaxSkillResources, references)
}

func TestDescribeBytePaginatesValidLargeSkillMetadataWithUniquePathIDs(t *testing.T) {
	registry := loadLargeSkillProjectionRegistry(t)
	store, err := newProjectionStore(registry, DefaultProjectionLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{7}, 4096)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	lease, err := registry.Lookup("docs.large-skills")
	require.NoError(t, err)
	projection, err := BuildProjection(lease.Record(), []string{DescribeSkills})
	lease.Release()
	require.NoError(t, err)
	assert.Greater(t, projection.bytes, int64(MaxStructureBytes))

	response, err := store.Describe(DescribeRequest{CapabilityID: "docs.large-skills", Include: []string{DescribeSkills}, Limit: 100})
	require.NoError(t, err)
	assert.NotEmpty(t, response.NextCursor)
	assert.Less(t, len(response.Skills), MaxSkills)
	var ids []string
	for {
		encoded, marshalErr := json.Marshal(response)
		require.NoError(t, marshalErr)
		assert.LessOrEqual(t, len(encoded), BrokerResponseMaxBytes)
		for _, skill := range response.Skills {
			ids = append(ids, skill.ID)
			assert.Equal(t, "guide", skill.Name)
		}
		if response.NextCursor == "" {
			break
		}
		response, err = store.Describe(DescribeRequest{Cursor: response.NextCursor, Limit: 100})
		require.NoError(t, err)
	}
	require.Len(t, ids, MaxSkills)
	assert.Equal(t, "skills/team-00/guide", ids[0])
	assert.Equal(t, "skills/team-31/guide", ids[len(ids)-1])
	for index := 1; index < len(ids); index++ {
		assert.Less(t, ids[index-1], ids[index])
	}
}

func TestDescribeExpiryQuotaRNGAndShutdown(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC))
	registry, err := NewRegistry(maximumStructureRecord(), syntheticRecord("other", ""))
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	limits := DefaultProjectionLimits()
	limits.MaxCount = 1
	store, err := newProjectionStore(registry, limits, clock.Now, bytes.NewReader(bytes.Repeat([]byte{1}, 512)))
	require.NoError(t, err)
	first, err := store.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeOperations}, Limit: 1})
	require.NoError(t, err)
	_, err = store.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeReferences}, Limit: 1})
	assert.ErrorIs(t, err, ErrServiceQuota)
	clock.Advance(5 * time.Minute)
	_, err = store.Describe(DescribeRequest{Cursor: first.NextCursor, Limit: 1})
	assert.ErrorIs(t, err, ErrDescribeCursorStale)
	_, err = store.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeReferences}, Limit: 1})
	require.NoError(t, err)
	store.Close()
	assert.Equal(t, ProjectionStats{}, store.Stats())
	_, err = store.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeReferences}, Limit: 1})
	assert.ErrorIs(t, err, ErrServiceClosed)

	failing, err := newProjectionStore(registry, DefaultProjectionLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{2}, 32)))
	require.NoError(t, err)
	_, err = failing.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeOperations}, Limit: 1})
	assert.ErrorIs(t, err, ErrHandleGeneration)
}

func TestDescribeAbsoluteExpiryByteQuotaAndHandleCollision(t *testing.T) {
	record := maximumStructureRecord()
	projection, err := BuildProjection(record, []string{DescribeOperations})
	require.NoError(t, err)
	record.release()

	t.Run("absolute expiry", func(t *testing.T) {
		clock := newFakeClock(time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC))
		registry, err := NewRegistry(maximumStructureRecord())
		require.NoError(t, err)
		t.Cleanup(registry.Close)
		limits := DefaultProjectionLimits()
		limits.Absolute = 10 * time.Minute
		store, err := newProjectionStore(registry, limits, clock.Now, bytes.NewReader(bytes.Repeat([]byte{4}, 1024)))
		require.NoError(t, err)
		first, err := store.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeOperations}, Limit: 1})
		require.NoError(t, err)
		clock.Advance(4 * time.Minute)
		second, err := store.Describe(DescribeRequest{Cursor: first.NextCursor, Limit: 1})
		require.NoError(t, err)
		clock.Advance(4 * time.Minute)
		third, err := store.Describe(DescribeRequest{Cursor: second.NextCursor, Limit: 1})
		require.NoError(t, err)
		clock.Advance(2 * time.Minute)
		_, err = store.Describe(DescribeRequest{Cursor: third.NextCursor, Limit: 1})
		assert.ErrorIs(t, err, ErrDescribeCursorStale)
	})

	t.Run("byte quota", func(t *testing.T) {
		registry, err := NewRegistry(maximumStructureRecord())
		require.NoError(t, err)
		t.Cleanup(registry.Close)
		limits := DefaultProjectionLimits()
		limits.MaxBytes = projection.bytes - 1
		store, err := newProjectionStore(registry, limits, time.Now, bytes.NewReader(bytes.Repeat([]byte{5}, 1024)))
		require.NoError(t, err)
		_, err = store.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeOperations}, Limit: 1})
		assert.ErrorIs(t, err, ErrServiceQuota)
	})

	t.Run("handle collision", func(t *testing.T) {
		registry, err := NewRegistry(maximumStructureRecord())
		require.NoError(t, err)
		t.Cleanup(registry.Close)
		store, err := newProjectionStore(registry, DefaultProjectionLimits(), time.Now, repeatingReader{'c'})
		require.NoError(t, err)
		_, err = store.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeOperations}, Limit: 1})
		require.NoError(t, err)
		_, err = store.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeReferences}, Limit: 1})
		assert.ErrorIs(t, err, ErrHandleGeneration)
	})
}

func TestDescribeRejectsTamperedOversizedAndLimitChangingCursors(t *testing.T) {
	registry, err := NewRegistry(maximumStructureRecord())
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	store, err := newProjectionStore(registry, DefaultProjectionLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{3}, 512)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	first, err := store.Describe(DescribeRequest{CapabilityID: "maximum", Include: []string{DescribeOperations}, Limit: 1})
	require.NoError(t, err)

	for _, cursor := range []string{first.NextCursor + "x", string(bytes.Repeat([]byte{'x'}, OpaqueReferenceMaxBytes+1))} {
		_, err = store.Describe(DescribeRequest{Cursor: cursor, Limit: 1})
		assert.ErrorIs(t, err, ErrDescribeCursorInvalid)
	}
	_, err = store.Describe(DescribeRequest{Cursor: first.NextCursor, Limit: 2})
	assert.ErrorIs(t, err, ErrDescribeCursorInvalid)
}

func TestProjectionStoreRequiresCursorKeyMaterial(t *testing.T) {
	registry, err := NewRegistry(syntheticRecord("first", ""))
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	_, err = newProjectionStore(registry, DefaultProjectionLimits(), time.Now, bytes.NewReader(nil))
	assert.Error(t, err)
}

func TestDescribeRejectsUnboundedIdentifiersWithoutEchoingThem(t *testing.T) {
	registry, err := NewRegistry(syntheticRecord("first", ""))
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	store, err := newProjectionStore(registry, DefaultProjectionLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{1}, 256)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	oversizedID := strings.Repeat("a", MaxCapabilityIDBytes+1)
	oversizedInclude := strings.Repeat("z", DescribeIncludeMaxBytes+1)
	invalidUTF8 := string([]byte{0xff})
	for _, request := range []DescribeRequest{
		{CapabilityID: oversizedID, Include: []string{DescribeSkills}, Limit: 1},
		{CapabilityID: invalidUTF8, Include: []string{DescribeSkills}, Limit: 1},
		{CapabilityID: "first", Include: []string{oversizedInclude}, Limit: 1},
		{CapabilityID: "missing", Include: []string{oversizedInclude}, Limit: 1},
		{CapabilityID: "first", Include: []string{invalidUTF8}, Limit: 1},
		{CapabilityID: "missing", Include: []string{invalidUTF8}, Limit: 1},
	} {
		_, err = store.Describe(request)
		require.ErrorIs(t, err, ErrDescribeRequestInvalid)
		assert.Less(t, len(err.Error()), 256)
		assert.NotContains(t, err.Error(), oversizedID)
		assert.NotContains(t, err.Error(), oversizedInclude)
	}
}

func TestDescribeCloseLinearizesOnePageAndConcurrentCalls(t *testing.T) {
	registry, err := NewRegistry(syntheticRecord("single", ""))
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	store, err := newProjectionStore(registry, DefaultProjectionLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{2}, 512)))
	require.NoError(t, err)
	store.Close()
	_, err = store.Describe(DescribeRequest{CapabilityID: "single", Include: []string{DescribeOperations}, Limit: 1})
	assert.ErrorIs(t, err, ErrServiceClosed)

	for range 64 {
		candidate, createErr := newProjectionStore(registry, DefaultProjectionLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{3}, 512)))
		require.NoError(t, createErr)
		start := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			<-start
			_, callErr := candidate.Describe(DescribeRequest{CapabilityID: "single", Include: []string{DescribeOperations}, Limit: 1})
			result <- callErr
		}()
		close(start)
		candidate.Close()
		callErr := <-result
		assert.True(t, callErr == nil || errors.Is(callErr, ErrServiceClosed))
		_, callErr = candidate.Describe(DescribeRequest{CapabilityID: "single", Include: []string{DescribeOperations}, Limit: 1})
		assert.ErrorIs(t, callErr, ErrServiceClosed)
	}
}

func loadReviewedRecord(t *testing.T, id string) *Record {
	t.Helper()
	store := newTestSnapshotStore(t)
	loader, err := NewLoader("../../testdata/capabilities", store)
	require.NoError(t, err)
	record, err := loader.Load(id)
	require.NoError(t, err)
	return record
}

func maximumStructureRecord() *Record {
	record := syntheticRecord("maximum", "")
	record.operations = make([]Operation, MaxOperations)
	for index := range record.operations {
		record.operations[index] = Operation{ID: fmt.Sprintf("op-%03d", index), Description: "Operation."}
	}
	record.skills = make([]Skill, MaxSkills)
	for index := range record.skills {
		id := fmt.Sprintf("skill-%03d", index)
		record.skills[index] = Skill{Metadata: SkillMetadata{Name: id, Description: "Skill."}, Artifact: Artifact{Ref: "artifact:v1:" + id, MediaType: "text/markdown", Bytes: 1, SHA256: fmt.Sprintf("%064d", index)}}
		resourcesPerSkill := MaxSkillResources / MaxSkills
		for resourceIndex := range resourcesPerSkill {
			resourceID := fmt.Sprintf("resource-%03d", index*resourcesPerSkill+resourceIndex)
			record.skills[index].Resources = append(record.skills[index].Resources, Artifact{
				ID: resourceID, Kind: ArtifactSkillResource, Ref: "artifact:v1:" + resourceID,
				MediaType: "text/plain", Bytes: 1, SHA256: fmt.Sprintf("%064d", index*resourcesPerSkill+resourceIndex),
			})
		}
	}
	record.context = make([]Artifact, MaxContextReferences)
	for index := range record.context {
		id := fmt.Sprintf("ref-%03d", index)
		record.context[index] = Artifact{ID: id, Kind: ArtifactContext, Ref: "artifact:v1:" + id, MediaType: "text/plain", Bytes: 1, SHA256: fmt.Sprintf("%064d", index)}
	}
	return record
}

func loadLargeSkillProjectionRegistry(t *testing.T) *Registry {
	t.Helper()
	root := t.TempDir()
	packageDir := filepath.Join(root, "docs.large-skills")
	var manifest strings.Builder
	manifest.WriteString("apiVersion: tappet.gearboxlogic.dev/v1alpha1\nkind: Capability\nmetadata:\n  id: docs.large-skills\n  name: Large skills\n  version: 1.0.0\n  description: Large valid skill metadata.\nspec:\n  parent: docs\n  skills:\n")
	license := strings.Repeat("l", 40<<10)
	for index := range MaxSkills {
		skillPath := fmt.Sprintf("skills/team-%02d/guide", index)
		manifest.WriteString("    - path: " + skillPath + "\n")
		directory := filepath.Join(packageDir, filepath.FromSlash(skillPath))
		require.NoError(t, os.MkdirAll(directory, 0o755))
		document := "---\nname: guide\ndescription: Shared-name guide.\nlicense: " + license + "\n---\nInstructions.\n"
		require.NoError(t, os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(document), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(packageDir, "tappet.yaml"), []byte(manifest.String()), 0o644))
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	record, err := loader.Load("docs.large-skills")
	require.NoError(t, err)
	registry, err := NewRegistry(record)
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	return registry
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(now time.Time) *fakeClock { return &fakeClock{now: now} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
