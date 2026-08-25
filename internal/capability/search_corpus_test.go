package capability

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const searchCorpusPath = "../../testdata/search/v1"

type searchCorpusManifest struct {
	Version              string            `json:"version"`
	RankingVersionTarget string            `json:"ranking_version_target"`
	NormalizationVersion string            `json:"normalization_version"`
	Catalogs             map[string]string `json:"catalogs"`
	Partitions           map[string]string `json:"partitions"`
	IndependentReview    struct {
		Status string `json:"status"`
		Scope  string `json:"scope"`
		Rounds []struct {
			Round  int    `json:"round"`
			Date   string `json:"date"`
			Result string `json:"result"`
			Note   string `json:"note"`
		} `json:"rounds"`
	} `json:"independent_review"`
	Counts struct {
		Catalogs map[string]struct {
			Capabilities int `json:"capabilities"`
			Operations   int `json:"operations"`
		} `json:"catalogs"`
		Partitions map[string]int `json:"partitions"`
	} `json:"counts"`
	GeneratedCoverage struct {
		ExactCapabilityID bool     `json:"exact_capability_id_for_every_capability"`
		ExactOperationID  bool     `json:"exact_fully_qualified_operation_id_for_every_operation"`
		InstallOrders     []string `json:"install_orders"`
	} `json:"generated_coverage"`
}

type searchCorpusCase struct {
	ID                string   `json:"id"`
	Catalog           string   `json:"catalog,omitempty"`
	Kind              string   `json:"kind"`
	Query             string   `json:"query"`
	Path              *string  `json:"path,omitempty"`
	RequiredID        string   `json:"required_id,omitempty"`
	AcceptedIDs       []string `json:"accepted_ids,omitempty"`
	ExpectedIDs       []string `json:"expected_ids,omitempty"`
	ExpectedMatchKind string   `json:"expected_match_kind,omitempty"`
	ExpectedError     string   `json:"expected_error,omitempty"`
	Limit             int      `json:"limit,omitempty"`
	Notes             string   `json:"notes,omitempty"`
}

type normalizationFixtures struct {
	Valid []struct {
		Input  string   `json:"input"`
		Exact  string   `json:"exact"`
		Tokens []string `json:"tokens"`
	} `json:"valid"`
	InvalidUTF8HexFixture string `json:"invalid_utf8_hex_fixture"`
}

func TestSearchCorpusV1Integrity(t *testing.T) {
	want := readSearchCorpusHashes(t)
	got := make(map[string]string)
	repoRoot := filepath.Clean(filepath.Join(searchCorpusPath, "..", "..", ".."))
	require.NoError(t, filepath.WalkDir(searchCorpusPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() == "SHA256SUMS" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		got[filepath.ToSlash(relative)] = hex.EncodeToString(digest[:])
		return nil
	}))
	assert.Equal(t, want, got)
}

func TestSearchCorpusV1PackagesAndCases(t *testing.T) {
	manifest := decodeSearchCorpusJSON[searchCorpusManifest](t, "corpus.json")
	assert.Equal(t, "v1", manifest.Version)
	assert.Equal(t, "lexical-v1", manifest.RankingVersionTarget)
	assert.Equal(t, "unicode-nfc-fold-v1", manifest.NormalizationVersion)
	assert.Equal(t, "approved", manifest.IndependentReview.Status)
	assert.NotEmpty(t, manifest.IndependentReview.Scope)
	require.Len(t, manifest.IndependentReview.Rounds, 3)
	for index, round := range manifest.IndependentReview.Rounds {
		assert.Equal(t, index+1, round.Round)
		assert.NotEmpty(t, round.Date)
		assert.Contains(t, []string{"approved", "rejected"}, round.Result)
		assert.NotEmpty(t, round.Note)
	}
	assert.True(t, manifest.GeneratedCoverage.ExactCapabilityID)
	assert.True(t, manifest.GeneratedCoverage.ExactOperationID)
	assert.Equal(t, []string{"normal", "reversed", "seeded-random"}, manifest.GeneratedCoverage.InstallOrders)

	catalogs := make(map[string]map[string]*Record, len(manifest.Catalogs))
	for name, directory := range manifest.Catalogs {
		store, err := NewSnapshotStore(DefaultStoreLimits())
		require.NoError(t, err)
		loader, err := NewLoader(filepath.Join(searchCorpusPath, directory), store)
		require.NoError(t, err)
		records, err := loader.LoadAll()
		require.NoError(t, err)
		t.Cleanup(func() {
			for _, record := range records {
				record.release()
			}
		})
		byID := make(map[string]*Record, len(records))
		operationCount := 0
		for _, record := range records {
			id := record.Metadata().ID
			require.NotContains(t, byID, id)
			byID[id] = record
			operationCount += len(record.Operations())
		}
		expectedCount, ok := manifest.Counts.Catalogs[name]
		require.True(t, ok, "missing counts for catalog %q", name)
		assert.Equal(t, expectedCount.Capabilities, len(records), name)
		assert.Equal(t, expectedCount.Operations, operationCount, name)
		catalogs[name] = byID
	}
	require.Contains(t, catalogs, "relevance")
	require.Contains(t, catalogs, "contract")
	assert.Len(t, manifest.Counts.Catalogs, len(catalogs))

	seenCaseIDs := make(map[string]string)
	seenRelevanceQueries := make(map[string]string)
	for _, partition := range []string{"calibration", "acceptance", "contract", "diagnostic"} {
		file, ok := manifest.Partitions[partition]
		require.True(t, ok, "missing %s partition", partition)
		cases := readSearchCorpusCases(t, file)
		expectedCount, ok := manifest.Counts.Partitions[partition]
		require.True(t, ok, "missing counts for partition %q", partition)
		assert.Equal(t, expectedCount, len(cases), partition)
		for _, queryCase := range cases {
			require.NotEmpty(t, queryCase.ID)
			if prior, exists := seenCaseIDs[queryCase.ID]; exists {
				t.Fatalf("duplicate case ID %q in %s and %s", queryCase.ID, prior, partition)
			}
			seenCaseIDs[queryCase.ID] = partition
			if partition == "calibration" || partition == "acceptance" {
				if prior, exists := seenRelevanceQueries[queryCase.Query]; exists {
					t.Fatalf("duplicate query text in %s and %s: %q", prior, partition, queryCase.Query)
				}
				seenRelevanceQueries[queryCase.Query] = partition
			}
			validateSearchCorpusCase(t, partition, queryCase, catalogs)
		}
	}
}

func TestSearchCorpusV1NormalizationFixtures(t *testing.T) {
	fixtures := decodeSearchCorpusJSON[normalizationFixtures](t, "normalization.json")
	require.NotEmpty(t, fixtures.Valid)
	for _, fixture := range fixtures.Valid {
		require.True(t, utf8.ValidString(fixture.Input))
		require.True(t, utf8.ValidString(fixture.Exact))
		for _, token := range fixture.Tokens {
			require.NotEmpty(t, token)
			require.True(t, utf8.ValidString(token))
		}
	}
	hexBytes, err := os.ReadFile(filepath.Join(searchCorpusPath, fixtures.InvalidUTF8HexFixture))
	require.NoError(t, err)
	decoded, err := hex.DecodeString(strings.TrimSpace(string(hexBytes)))
	require.NoError(t, err)
	assert.False(t, utf8.Valid(decoded))
}

func readSearchCorpusHashes(t *testing.T) map[string]string {
	t.Helper()
	file, err := os.Open(filepath.Join(searchCorpusPath, "SHA256SUMS"))
	require.NoError(t, err)
	defer file.Close()

	hashes := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		require.Len(t, parts, 2)
		_, err := hex.DecodeString(parts[0])
		require.NoError(t, err)
		require.Len(t, parts[0], sha256.Size*2)
		require.NotContains(t, hashes, parts[1])
		hashes[parts[1]] = parts[0]
	}
	require.NoError(t, scanner.Err())
	return hashes
}

func readSearchCorpusCases(t *testing.T, name string) []searchCorpusCase {
	t.Helper()
	file, err := os.Open(filepath.Join(searchCorpusPath, name))
	require.NoError(t, err)
	defer file.Close()

	var cases []searchCorpusCase
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		var queryCase searchCorpusCase
		require.NoError(t, decoder.Decode(&queryCase), "%s:%d", name, line)
		require.NoError(t, requireJSONEOF(decoder), "%s:%d", name, line)
		cases = append(cases, queryCase)
	}
	require.NoError(t, scanner.Err())
	require.NotEmpty(t, cases)
	return cases
}

func validateSearchCorpusCase(t *testing.T, partition string, queryCase searchCorpusCase, catalogs map[string]map[string]*Record) {
	t.Helper()
	if partition == "contract" {
		require.NotEmpty(t, queryCase.Catalog, queryCase.ID)
	} else {
		require.Empty(t, queryCase.Catalog, queryCase.ID)
	}
	catalogName := queryCase.Catalog
	if catalogName == "" {
		catalogName = "relevance"
	}
	catalog, ok := catalogs[catalogName]
	require.True(t, ok, "%s names unknown catalog %q", queryCase.ID, catalogName)
	require.True(t, utf8.ValidString(queryCase.Query), queryCase.ID)
	if queryCase.Kind != "diagnostic" {
		require.Positive(t, queryCase.Limit, queryCase.ID)
		require.LessOrEqual(t, queryCase.Limit, 5, queryCase.ID)
	}
	if queryCase.ExpectedMatchKind != "" {
		require.Equal(t, "contract", partition, queryCase.ID)
		require.Contains(t, searchCorpusMatchKinds, queryCase.ExpectedMatchKind, queryCase.ID)
	}

	contains := func(id string) {
		require.Contains(t, catalog, id, "%s references missing capability", queryCase.ID)
	}
	switch queryCase.Kind {
	case "unambiguous":
		require.NotEmpty(t, queryCase.RequiredID, queryCase.ID)
		contains(queryCase.RequiredID)
	case "ambiguous":
		require.GreaterOrEqual(t, len(queryCase.AcceptedIDs), 2, queryCase.ID)
		require.LessOrEqual(t, len(queryCase.AcceptedIDs), 5, queryCase.ID)
		require.LessOrEqual(t, len(queryCase.AcceptedIDs), queryCase.Limit, queryCase.ID)
		assertUniqueCorpusIDs(t, queryCase.ID, queryCase.AcceptedIDs)
		for _, id := range queryCase.AcceptedIDs {
			contains(id)
		}
	case "negative":
	case "error":
		require.NotEmpty(t, queryCase.ExpectedError, queryCase.ID)
	case "tie":
		require.NotEmpty(t, queryCase.ExpectedIDs, queryCase.ID)
		require.LessOrEqual(t, len(queryCase.ExpectedIDs), queryCase.Limit, queryCase.ID)
		assertUniqueCorpusIDs(t, queryCase.ID, queryCase.ExpectedIDs)
		for _, id := range queryCase.ExpectedIDs {
			contains(id)
		}
	case "diagnostic":
		require.Equal(t, "diagnostic", partition, queryCase.ID)
		require.NotEmpty(t, queryCase.Notes, queryCase.ID)
	default:
		t.Fatalf("%s has unknown kind %q", queryCase.ID, queryCase.Kind)
	}
}

var searchCorpusMatchKinds = map[string]struct{}{
	"exact_capability_id":      {},
	"exact_operation_id":       {},
	"exact_capability_name":    {},
	"exact_tag":                {},
	"exact_local_operation_id": {},
	"exact_skill_name":         {},
	"lexical_capability":       {},
	"lexical_operation":        {},
	"lexical_skill":            {},
}

func assertUniqueCorpusIDs(t *testing.T, caseID string, ids []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		require.NotEmpty(t, id, caseID)
		require.NotContains(t, seen, id, caseID)
		seen[id] = struct{}{}
	}
}

func decodeSearchCorpusJSON[T any](t *testing.T, name string) T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(searchCorpusPath, name))
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value T
	require.NoError(t, decoder.Decode(&value))
	require.NoError(t, requireJSONEOF(decoder))
	return value
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("unexpected trailing JSON value")
	} else if err != io.EOF {
		return err
	}
	return nil
}
