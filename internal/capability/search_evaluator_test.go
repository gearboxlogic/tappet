package capability

import (
	"encoding/json"
	"math/rand"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type searchInstallOrder string

const (
	searchInstallOrderNormal  searchInstallOrder = "normal"
	searchInstallOrderReverse searchInstallOrder = "reversed"
	searchInstallOrderSeeded  searchInstallOrder = "seeded-random"
)

var searchInstallOrders = []searchInstallOrder{
	searchInstallOrderNormal,
	searchInstallOrderReverse,
	searchInstallOrderSeeded,
}

type searchEvaluationReport struct {
	Partition          string                        `json:"partition"`
	Catalog            string                        `json:"catalog"`
	InstallOrder       searchInstallOrder            `json:"install_order"`
	Queries            int                           `json:"queries"`
	Unambiguous        int                           `json:"unambiguous"`
	SuccessAt1         int                           `json:"success_at_1"`
	SuccessAt5         int                           `json:"success_at_5"`
	MeanReciprocalRank float64                       `json:"mean_reciprocal_rank"`
	Ranks              map[string]int                `json:"ranks"`
	WrongRankOne       map[string]string             `json:"wrong_rank_one"`
	Ambiguous          int                           `json:"ambiguous"`
	AmbiguousPassed    int                           `json:"ambiguous_passed"`
	PerDomain          map[string]searchDomainReport `json:"per_domain"`
	reciprocalRankSum  float64
}

type searchDomainReport struct {
	Queries    int `json:"queries"`
	SuccessAt5 int `json:"success_at_5"`
}

func TestSearchLexicalV1GeneratedExactCoverage(t *testing.T) {
	for _, catalog := range []string{"relevance", "contract"} {
		for _, order := range searchInstallOrders {
			t.Run(catalog+"/"+string(order), func(t *testing.T) {
				registry := newSearchTestRegistry(t, catalog, order)
				for _, capabilityID := range registry.CapabilityIDs() {
					lease, err := registry.Lookup(capabilityID)
					require.NoError(t, err)
					operations := lease.Record().Operations()
					lease.Release()

					response, err := registry.Search(SearchRequest{Query: capabilityID, Limit: 5})
					require.NoError(t, err)
					assertSearchRankOne(t, response, capabilityID, "exact_capability_id")
					for _, operation := range operations {
						response, err := registry.Search(SearchRequest{Query: capabilityID + "/" + operation.ID, Limit: 5})
						require.NoError(t, err)
						assertSearchRankOne(t, response, capabilityID, "exact_operation_id")
					}
				}
			})
		}
	}
}

func TestSearchLexicalV1Contract(t *testing.T) {
	evaluateSearchCorpusPartition(t, "contract")
}

// Calibration is the only relevance partition used to select lexical-v1's
// weights and minimum score. Acceptance remains unopened by this evaluator
// until those constants are frozen in the search contract.
func TestSearchLexicalV1Calibration(t *testing.T) {
	evaluateSearchCorpusPartition(t, "calibration")
}

func TestSearchLexicalV1Acceptance(t *testing.T) {
	evaluateSearchCorpusPartition(t, "acceptance")
}

func evaluateSearchCorpusPartition(t *testing.T, partition string) {
	manifest := decodeSearchCorpusJSON[searchCorpusManifest](t, "corpus.json")
	cases := readSearchCorpusCases(t, manifest.Partitions[partition])
	byCatalog := make(map[string][]searchCorpusCase)
	for _, queryCase := range cases {
		catalog := queryCase.Catalog
		if catalog == "" {
			catalog = "relevance"
		}
		byCatalog[catalog] = append(byCatalog[catalog], queryCase)
	}

	for catalog, catalogCases := range byCatalog {
		var baseline map[string]SearchResponse
		for _, order := range searchInstallOrders {
			t.Run(catalog+"/"+string(order), func(t *testing.T) {
				registry := newSearchTestRegistry(t, catalog, order)
				responses := make(map[string]SearchResponse, len(catalogCases))
				report := newSearchEvaluationReport(partition, catalog, order)
				for _, queryCase := range catalogCases {
					response, err := registry.Search(SearchRequest{Query: queryCase.Query, Path: corpusCasePath(queryCase), Limit: queryCase.Limit})
					assertSearchCorpusCase(t, queryCase, response, err)
					if err == nil {
						responses[queryCase.ID] = response
						report.observe(queryCase, response)
					}
				}
				if baseline == nil {
					baseline = responses
				} else {
					assert.Equal(t, baseline, responses)
				}
				report.finalize()
				encoded, err := json.Marshal(report)
				require.NoError(t, err)
				t.Log(string(encoded))
			})
		}
	}
}

func newSearchEvaluationReport(partition, catalog string, order searchInstallOrder) *searchEvaluationReport {
	return &searchEvaluationReport{
		Partition:    partition,
		Catalog:      catalog,
		InstallOrder: order,
		Ranks:        make(map[string]int),
		WrongRankOne: make(map[string]string),
		PerDomain:    make(map[string]searchDomainReport),
	}
}

func (report *searchEvaluationReport) observe(queryCase searchCorpusCase, response SearchResponse) {
	report.Queries++
	switch queryCase.Kind {
	case "unambiguous":
		report.Unambiguous++
		rank := 0
		for index, result := range response.Results {
			if result.Card.ID == queryCase.RequiredID {
				rank = index + 1
				break
			}
		}
		report.Ranks[queryCase.ID] = rank
		domain := strings.SplitN(queryCase.RequiredID, ".", 2)[0]
		domainReport := report.PerDomain[domain]
		domainReport.Queries++
		if rank > 0 && rank <= 5 {
			report.SuccessAt5++
			report.reciprocalRankSum += 1 / float64(rank)
			domainReport.SuccessAt5++
		}
		if rank == 1 {
			report.SuccessAt1++
		} else if len(response.Results) > 0 {
			report.WrongRankOne[queryCase.ID] = response.Results[0].Card.ID
		}
		report.PerDomain[domain] = domainReport
	case "ambiguous":
		report.Ambiguous++
		if slices.Equal(searchSortedCopy(queryCase.AcceptedIDs), searchSortedCopy(searchResultIDs(response))) {
			report.AmbiguousPassed++
		}
	}
}

func (report *searchEvaluationReport) finalize() {
	if report.Unambiguous > 0 {
		report.MeanReciprocalRank = report.reciprocalRankSum / float64(report.Unambiguous)
	}
}

func searchSortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	slices.Sort(result)
	return result
}

func corpusCasePath(queryCase searchCorpusCase) string {
	if queryCase.Path == nil {
		return ""
	}
	return *queryCase.Path
}

func assertSearchCorpusCase(t *testing.T, queryCase searchCorpusCase, response SearchResponse, err error) {
	t.Helper()
	if queryCase.Kind == "error" {
		require.Error(t, err, queryCase.ID)
		if queryCase.ExpectedError == "path_not_found" {
			assert.ErrorIs(t, err, ErrHierarchyPathNotFound, queryCase.ID)
		}
		return
	}
	require.NoError(t, err, queryCase.ID)
	resultIDs := searchResultIDs(response)
	switch queryCase.Kind {
	case "unambiguous":
		assert.Contains(t, resultIDs, queryCase.RequiredID, queryCase.ID)
	case "ambiguous":
		assert.ElementsMatch(t, queryCase.AcceptedIDs, resultIDs, queryCase.ID)
		require.NotEmpty(t, resultIDs, queryCase.ID)
		assert.Contains(t, queryCase.AcceptedIDs, resultIDs[0], queryCase.ID)
	case "negative":
		assert.Empty(t, resultIDs, queryCase.ID)
	case "tie":
		assert.Equal(t, queryCase.ExpectedIDs, resultIDs, queryCase.ID)
	default:
		t.Fatalf("unsupported evaluated case kind %q", queryCase.Kind)
	}
	if queryCase.ExpectedMatchKind != "" {
		require.NotEmpty(t, response.Results, queryCase.ID)
		assert.Equal(t, queryCase.ExpectedMatchKind, response.Results[0].MatchKind, queryCase.ID)
	}
}

func searchResultIDs(response SearchResponse) []string {
	ids := make([]string, len(response.Results))
	for index, result := range response.Results {
		ids[index] = result.Card.ID
	}
	return ids
}

func assertSearchRankOne(t *testing.T, response SearchResponse, capabilityID, matchKind string) {
	t.Helper()
	require.NotEmpty(t, response.Results)
	assert.Equal(t, capabilityID, response.Results[0].Card.ID)
	assert.Equal(t, matchKind, response.Results[0].MatchKind)
	assert.Equal(t, searchExactScore, response.Results[0].Score)
}

func newSearchTestRegistry(t *testing.T, catalog string, order searchInstallOrder) *Registry {
	t.Helper()
	manifest := decodeSearchCorpusJSON[searchCorpusManifest](t, "corpus.json")
	directory, ok := manifest.Catalogs[catalog]
	require.True(t, ok)
	store, err := NewSnapshotStore(DefaultStoreLimits())
	require.NoError(t, err)
	loader, err := NewLoader(filepath.Join(searchCorpusPath, directory), store)
	require.NoError(t, err)
	records, err := loader.LoadAll()
	require.NoError(t, err)
	switch order {
	case searchInstallOrderNormal:
	case searchInstallOrderReverse:
		slices.Reverse(records)
	case searchInstallOrderSeeded:
		rand.New(rand.NewSource(3)).Shuffle(len(records), func(i, j int) {
			records[i], records[j] = records[j], records[i]
		})
	default:
		t.Fatalf("unknown install order %q", order)
	}
	registry, err := NewRegistry(records...)
	if err != nil {
		for _, record := range records {
			record.release()
		}
	}
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	return registry
}
