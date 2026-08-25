package capability

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchNormalizationMatchesFrozenFixtures(t *testing.T) {
	fixtures := decodeSearchCorpusJSON[normalizationFixtures](t, "normalization.json")
	for _, fixture := range fixtures.Valid {
		t.Run(fixture.Input, func(t *testing.T) {
			normalized := normalizeSearchText(fixture.Input)
			assert.Equal(t, fixture.Exact, normalized.exact)
			assert.Equal(t, fixture.Tokens, normalized.tokens)
		})
	}
	normalized := normalizeSearchText("GitLabPipeline")
	assert.Equal(t, "gitlabpipeline", normalized.exact)
	assert.Equal(t, []string{"gitlab", "pipeline"}, normalized.tokens)
}

func TestSearchRejectsInvalidAndOversizedQueries(t *testing.T) {
	fixtures := decodeSearchCorpusJSON[normalizationFixtures](t, "normalization.json")
	hexBytes, err := os.ReadFile(filepath.Join(searchCorpusPath, fixtures.InvalidUTF8HexFixture))
	require.NoError(t, err)
	invalid, err := hex.DecodeString(strings.TrimSpace(string(hexBytes)))
	require.NoError(t, err)
	require.False(t, utf8.Valid(invalid))

	registry := newSearchTestRegistry(t, "relevance", searchInstallOrderNormal)
	_, err = registry.Search(SearchRequest{Query: string(invalid), Limit: 5})
	assert.ErrorIs(t, err, ErrSearchQueryInvalid)
	_, err = registry.Search(SearchRequest{Query: strings.Repeat("a", SearchQueryMaxBytes+1), Limit: 5})
	assert.ErrorIs(t, err, ErrSearchQueryLimit)
	_, err = registry.Search(SearchRequest{Query: "github", Limit: 0})
	assert.ErrorIs(t, err, ErrSearchLimitInvalid)
	_, err = registry.Search(SearchRequest{Query: "github", Limit: SearchLimitMax + 1})
	assert.ErrorIs(t, err, ErrSearchLimitInvalid)
}

func TestSearchRejectsInvalidAndOversizedPathsBeforeLookup(t *testing.T) {
	registry := newSearchTestRegistry(t, "relevance", searchInstallOrderNormal)
	for name, path := range map[string]string{
		"invalid UTF-8": string([]byte{0xff}),
		"leading dot":   ".software",
		"trailing dot":  "software.",
		"uppercase":     "Software.github",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := registry.Search(SearchRequest{Query: "github", Path: path, Limit: 5})
			assert.ErrorIs(t, err, ErrSearchPathInvalid)
		})
	}
	_, err := registry.Search(SearchRequest{Query: "github", Path: strings.Repeat("a", MaxHierarchyPathBytes+1), Limit: 5})
	assert.ErrorIs(t, err, ErrSearchPathLimit)
}

func TestSearchDoesNotIndexExcludedRecordFields(t *testing.T) {
	record := &Record{
		metadata: Metadata{
			ID:          "fixture.allowed",
			Name:        "Allowed name",
			Version:     "zversionq",
			Description: "Allowed capability description",
			Tags:        []string{"allowed-tag"},
		},
		parent: "zparentq",
		skills: []Skill{{
			Path: "zskillpathq",
			Metadata: SkillMetadata{
				Name:          "Allowed skill",
				Description:   "Allowed skill description",
				License:       "zlicenseq",
				AllowedTools:  "ztoolsq",
				Compatibility: "zcompatibilityq",
				Metadata:      map[string]string{"key": "zmetadataq"},
			},
			Artifact:  Artifact{ID: "zartifactidq", Path: "zartifactpathq", SHA256: "zdigestq"},
			Resources: []Artifact{{ID: "zresourceidq", Path: "zresourcepathq"}},
		}},
		operations: []Operation{{
			ID:          "allowed-operation",
			Description: "Allowed operation description",
			Provider:    "zprovideridq",
			Target:      "zprovidertargetq",
		}},
		context:   []Artifact{{ID: "zcontextidq", Path: "zcontextpathq"}},
		providers: []ProviderBinding{{ID: "zprovideridq", Type: "zprovidertypeq", ServerRef: "zserverrefq"}},
	}

	for _, query := range []string{
		"zversionq",
		"zparentq",
		"zskillpathq",
		"zlicenseq",
		"ztoolsq",
		"zcompatibilityq",
		"zmetadataq",
		"zartifactidq",
		"zartifactpathq",
		"zdigestq",
		"zresourceidq",
		"zresourcepathq",
		"zcontextidq",
		"zcontextpathq",
		"zprovideridq",
		"zprovidertargetq",
		"zprovidertypeq",
		"zserverrefq",
	} {
		t.Run(query, func(t *testing.T) {
			_, matched := matchCapability(normalizeSearchText(query), record)
			assert.False(t, matched)
		})
	}
}

func TestSearchReturnsOnlyBoundedCardAndMatchMetadata(t *testing.T) {
	registry := newSearchTestRegistry(t, "contract", searchInstallOrderNormal)
	response, err := registry.Search(SearchRequest{Query: "fixture-tag-evidence", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, response.Results)
	assert.Equal(t, "fixture.hierarchy-only-canary-topaz.excluded-fields", response.Results[0].Card.ID)
	encoded, err := json.Marshal(response.Results[0])
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), MaxBaseCardBytes+512)
	for _, excluded := range []string{
		"body-only-canary-zephyr",
		"reference-body-only-canary-orchid",
		"context-body-only-canary-violet",
		"provider-target-only-canary-saffron",
		"server-reference-only-canary-cobalt",
	} {
		assert.NotContains(t, string(encoded), excluded)
	}
}

func TestSearchReturnsTypedPathAndRegistryErrors(t *testing.T) {
	registry := newSearchTestRegistry(t, "relevance", searchInstallOrderNormal)
	_, err := registry.Search(SearchRequest{Query: "github", Path: "missing.branch", Limit: 5})
	assert.ErrorIs(t, err, ErrHierarchyPathNotFound)
	registry.Close()
	_, err = registry.Search(SearchRequest{Query: "github", Limit: 5})
	assert.ErrorIs(t, err, ErrRegistryClosed)
}

func TestSearchDoesNotLexicallyFallbackForOperationOutsidePath(t *testing.T) {
	registry := newSearchTestRegistry(t, "relevance", searchInstallOrderNormal)
	response, err := registry.Search(SearchRequest{
		Query: "software.gitlab.ci-debugging/inspect-failed-jobs",
		Path:  "software.github",
		Limit: 5,
	})
	require.NoError(t, err)
	assert.Empty(t, response.Results)
}

func TestSearchLexicalScoreUsesUniqueQueryAndFieldTokens(t *testing.T) {
	query := normalizeSearchText("alpha alpha beta")
	field := normalizeSearchText("alpha beta beta gamma")
	assert.Equal(t, 885_714, lexicalSearchScore(query.tokenSet, field.tokenSet))
	assert.Equal(t, 0, lexicalSearchScore(query.tokenSet, normalizeSearchText("gamma delta").tokenSet))
}

func TestBrowseReturnsTypedUnknownPathError(t *testing.T) {
	registry := newSearchTestRegistry(t, "relevance", searchInstallOrderNormal)
	_, err := registry.Browse("missing.branch")
	assert.ErrorIs(t, err, ErrHierarchyPathNotFound)
}

func TestSearchReadsTheCurrentRegistryGeneration(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeSimplePackage(t, root, "software.github.ci-debugging", "software.github", "inspect", "provider")
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	record, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	registry, err := NewRegistry(record)
	require.NoError(t, err)
	t.Cleanup(registry.Close)

	response, err := registry.Search(SearchRequest{Query: "github checks", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, response.Results)

	manifest := readFileString(t, manifestPath)
	manifest = strings.ReplaceAll(manifest, "GitHub CI debugging", "Database query tuning")
	manifest = strings.ReplaceAll(manifest, "Inspect failing checks.", "Analyze PostgreSQL query latency.")
	manifest = strings.ReplaceAll(manifest, "Inspect failed checks.", "Inspect PostgreSQL query plans.")
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o644))
	replacement, err := loader.Load("software.github.ci-debugging")
	require.NoError(t, err)
	require.NoError(t, registry.Reinstall(replacement))

	response, err = registry.Search(SearchRequest{Query: "github checks", Limit: 5})
	require.NoError(t, err)
	assert.Empty(t, response.Results)
	response, err = registry.Search(SearchRequest{Query: "postgresql query latency", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, response.Results)
	assert.Equal(t, "software.github.ci-debugging", response.Results[0].Card.ID)

	require.NoError(t, registry.Uninstall("software.github.ci-debugging"))
	response, err = registry.Search(SearchRequest{Query: "postgresql query latency", Limit: 5})
	require.NoError(t, err)
	assert.Empty(t, response.Results)
}

func TestSearchIsDeterministicUnderConcurrentQueries(t *testing.T) {
	registry := newSearchTestRegistry(t, "relevance", searchInstallOrderSeeded)
	requests := []SearchRequest{
		{Query: "analyze slow query latency", Limit: 5},
		{Query: "software.github.ci-debugging", Limit: 5},
	}
	want := make([]SearchResponse, len(requests))
	for index, request := range requests {
		response, err := registry.Search(request)
		require.NoError(t, err)
		want[index] = response
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			index := worker % len(requests)
			response, err := registry.Search(requests[index])
			assert.NoError(t, err)
			assert.Equal(t, want[index], response)
		}(worker)
	}
	wait.Wait()
}

func TestSearchReturnsOneCompleteGenerationDuringConcurrentReinstalls(t *testing.T) {
	const capabilityID = "software.github.ci-debugging"
	oldRoot := t.TempDir()
	writeSimplePackage(t, oldRoot, capabilityID, "software.github", "inspect-old", "provider-old")
	newRoot := t.TempDir()
	newManifestPath := writeSimplePackage(t, newRoot, capabilityID, "software.github", "inspect-new", "provider-new")
	newManifest := readFileString(t, newManifestPath)
	newManifest = strings.ReplaceAll(newManifest, "GitHub CI debugging", "GitHub workflow investigation")
	newManifest = strings.ReplaceAll(newManifest, "Inspect failing checks.", "Investigate GitHub workflow failures.")
	require.NoError(t, os.WriteFile(newManifestPath, []byte(newManifest), 0o644))

	oldStore := newTestSnapshotStore(t)
	oldLoader, err := NewLoader(oldRoot, oldStore)
	require.NoError(t, err)
	newStore := newTestSnapshotStore(t)
	newLoader, err := NewLoader(newRoot, newStore)
	require.NoError(t, err)
	initial, err := oldLoader.Load(capabilityID)
	require.NoError(t, err)
	registry, err := NewRegistry(initial)
	require.NoError(t, err)
	t.Cleanup(registry.Close)

	request := SearchRequest{Query: capabilityID, Limit: 5}
	oldResponse, err := registry.Search(request)
	require.NoError(t, err)
	replacement, err := newLoader.Load(capabilityID)
	require.NoError(t, err)
	require.NoError(t, registry.Reinstall(replacement))
	newResponse, err := registry.Search(request)
	require.NoError(t, err)
	require.NotEqual(t, oldResponse, newResponse)
	replacement, err = oldLoader.Load(capabilityID)
	require.NoError(t, err)
	require.NoError(t, registry.Reinstall(replacement))

	start := make(chan struct{})
	errorsFound := make(chan error, 9)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-start
		loaders := []*Loader{newLoader, oldLoader}
		for index := 0; index < 100; index++ {
			record, loadErr := loaders[index%len(loaders)].Load(capabilityID)
			if loadErr != nil {
				errorsFound <- loadErr
				return
			}
			if reinstallErr := registry.Reinstall(record); reinstallErr != nil {
				record.release()
				errorsFound <- reinstallErr
				return
			}
		}
	}()
	for reader := 0; reader < 8; reader++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for iteration := 0; iteration < 200; iteration++ {
				response, searchErr := registry.Search(request)
				if searchErr != nil {
					errorsFound <- searchErr
					return
				}
				if !reflect.DeepEqual(response, oldResponse) && !reflect.DeepEqual(response, newResponse) {
					errorsFound <- fmt.Errorf("search returned a mixed generation: %#v", response)
					return
				}
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for concurrentErr := range errorsFound {
		require.NoError(t, concurrentErr)
	}
}
