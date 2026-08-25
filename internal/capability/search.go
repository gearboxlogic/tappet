package capability

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	SearchRankingVersion  = "lexical-v1"
	SearchQueryMaxBytes   = 4096
	SearchLimitMax        = 100
	searchExactScore      = 1_000_000
	searchMinimumScore    = 375_000
	searchQueryWeight     = 500_000
	searchFieldWeight     = 250_000
	searchEvidencePerByte = 25_000
	searchEvidenceMaximum = 250_000
)

var (
	ErrSearchQueryInvalid    = errors.New("search query is invalid")
	ErrSearchQueryLimit      = errors.New("search query exceeds the byte limit")
	ErrSearchPathInvalid     = errors.New("search path is invalid")
	ErrSearchPathLimit       = errors.New("search path exceeds the byte limit")
	ErrSearchLimitInvalid    = errors.New("search result limit is invalid")
	ErrHierarchyPathNotFound = errors.New("hierarchy path not found")
)

type SearchRequest struct {
	Query string `json:"query"`
	Path  string `json:"path,omitempty"`
	Limit int    `json:"limit"`
}

type SearchResponse struct {
	RankingVersion string         `json:"ranking_version"`
	Results        []SearchResult `json:"results"`
}

type CapabilityCard struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	Description string           `json:"description"`
	Path        string           `json:"path"`
	Tags        []string         `json:"tags"`
	Counts      CapabilityCounts `json:"counts"`
}

type CapabilityCounts struct {
	Skills     int `json:"skills"`
	Operations int `json:"operations"`
	References int `json:"references"`
}

type SearchResult struct {
	Card         CapabilityCard `json:"card"`
	Score        int            `json:"score"`
	MatchKind    string         `json:"match_kind"`
	MatchedField string         `json:"matched_field"`
	Reason       string         `json:"reason"`
}

type normalizedSearchText struct {
	exact               string
	tokens              []string
	tokenSet            map[string]struct{}
	tokenBytes          int
	qualifiedIdentifier bool
}

type searchMatch struct {
	tier         int
	score        int
	kind         string
	matchedField string
	reason       string
}

// Search returns compact cards derived only from installed package metadata.
// It does not read artifacts, resolve provider metadata, or start providers.
func (r *Registry) Search(request SearchRequest) (SearchResponse, error) {
	response := SearchResponse{RankingVersion: SearchRankingVersion, Results: []SearchResult{}}
	if request.Limit <= 0 || request.Limit > SearchLimitMax {
		return response, fmt.Errorf("%w: must be between 1 and %d", ErrSearchLimitInvalid, SearchLimitMax)
	}
	query, err := normalizeSearchQuery(request.Query)
	if err != nil {
		return response, err
	}
	path := request.Path
	if path == "/" {
		path = ""
	}
	if !utf8.ValidString(path) {
		return response, ErrSearchPathInvalid
	}
	if len(path) > MaxHierarchyPathBytes {
		return response, ErrSearchPathLimit
	}
	if path != "" && !capabilityIDPattern.MatchString(path) {
		return response, ErrSearchPathInvalid
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return response, ErrRegistryClosed
	}
	if _, exists := r.nodes[path]; !exists {
		return response, fmt.Errorf("%w: %s", ErrHierarchyPathNotFound, path)
	}
	if query.exact == "" || len(query.tokens) == 0 {
		return response, nil
	}
	query.qualifiedIdentifier = r.hasExactSearchIdentifierLocked(query.exact)

	results := make([]rankedSearchResult, 0, len(r.entries))
	for id, generation := range r.entries {
		if generation.retired || !capabilityInPath(id, path) {
			continue
		}
		match, ok := matchCapability(query, generation.record)
		if !ok {
			continue
		}
		results = append(results, rankedSearchResult{
			SearchResult: SearchResult{
				Card:         capabilityCard(generation.record),
				Score:        match.score,
				MatchKind:    match.kind,
				MatchedField: match.matchedField,
				Reason:       match.reason,
			},
			tier: match.tier,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].tier != results[j].tier {
			return results[i].tier < results[j].tier
		}
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Card.ID < results[j].Card.ID
	})
	if len(results) > request.Limit {
		results = results[:request.Limit]
	}
	response.Results = make([]SearchResult, len(results))
	for index := range results {
		response.Results[index] = results[index].SearchResult
	}
	return response, nil
}

func (r *Registry) hasExactSearchIdentifierLocked(query string) bool {
	for capabilityID, generation := range r.entries {
		if generation.retired {
			continue
		}
		if normalizeExact(capabilityID) == query {
			return true
		}
		for _, operation := range generation.record.operations {
			if normalizeExact(capabilityID+"/"+operation.ID) == query {
				return true
			}
		}
	}
	return false
}

type rankedSearchResult struct {
	SearchResult
	tier int
}

func capabilityInPath(capabilityID, path string) bool {
	return path == "" || capabilityID == path || strings.HasPrefix(capabilityID, path+".")
}

func capabilityCard(record *Record) CapabilityCard {
	metadata := record.Metadata()
	references := len(record.context)
	for _, skill := range record.skills {
		references += len(skill.Resources)
	}
	return CapabilityCard{
		ID:          metadata.ID,
		Name:        metadata.Name,
		Version:     metadata.Version,
		Description: metadata.Description,
		Path:        record.parent,
		Tags:        metadata.Tags,
		Counts: CapabilityCounts{
			Skills:     len(record.skills),
			Operations: len(record.operations),
			References: references,
		},
	}
}

func matchCapability(query normalizedSearchText, record *Record) (searchMatch, bool) {
	metadata := record.metadata
	if normalizeExact(metadata.ID) == query.exact {
		return exactSearchMatch(1, "exact_capability_id", "capability_id"), true
	}
	for _, operation := range record.operations {
		if normalizeExact(metadata.ID+"/"+operation.ID) == query.exact {
			return exactSearchMatch(1, "exact_operation_id", "operation_id"), true
		}
	}
	if query.qualifiedIdentifier {
		return searchMatch{}, false
	}
	if normalizeExact(metadata.Name) == query.exact {
		return exactSearchMatch(2, "exact_capability_name", "capability_name"), true
	}
	for _, tag := range metadata.Tags {
		if normalizeExact(tag) == query.exact {
			return exactSearchMatch(3, "exact_tag", "tag"), true
		}
	}
	for _, operation := range record.operations {
		if normalizeExact(operation.ID) == query.exact {
			return exactSearchMatch(3, "exact_local_operation_id", "local_operation_id"), true
		}
	}
	for _, skill := range record.skills {
		if normalizeExact(skill.Metadata.Name) == query.exact {
			return exactSearchMatch(3, "exact_skill_name", "skill_name"), true
		}
	}

	capabilityFields := []searchField{
		{name: "capability_name", value: metadata.Name},
		{name: "capability_description", value: metadata.Description},
	}
	operationFields := make([]searchField, 0, len(record.operations))
	for _, operation := range record.operations {
		operationFields = append(operationFields, searchField{name: "operation_description", value: operation.Description})
	}
	skillFields := make([]searchField, 0, len(record.skills)*2)
	for _, skill := range record.skills {
		skillFields = append(skillFields,
			searchField{name: "skill_name", value: skill.Metadata.Name},
			searchField{name: "skill_description", value: skill.Metadata.Description},
		)
	}
	if match, ok := bestLexicalMatch(query, 4, "lexical_capability", capabilityFields); ok {
		return match, true
	}
	if match, ok := bestLexicalMatch(query, 5, "lexical_operation", operationFields); ok {
		return match, true
	}
	return bestLexicalMatch(query, 5, "lexical_skill", skillFields)
}

func exactSearchMatch(tier int, kind, field string) searchMatch {
	return searchMatch{
		tier:         tier,
		score:        searchExactScore,
		kind:         kind,
		matchedField: field,
		reason:       searchReason(kind),
	}
}

type searchField struct {
	name  string
	value string
}

func bestLexicalMatch(query normalizedSearchText, tier int, kind string, fields []searchField) (searchMatch, bool) {
	bestScore := 0
	bestField := ""
	for _, field := range fields {
		normalized := normalizeSearchText(field.value)
		score := lexicalSearchScore(query.tokenSet, normalized.tokenSet)
		if score > bestScore {
			bestScore = score
			bestField = field.name
		}
	}
	if bestScore < searchMinimumScore {
		return searchMatch{}, false
	}
	return searchMatch{
		tier:         tier,
		score:        bestScore,
		kind:         kind,
		matchedField: bestField,
		reason:       searchReason(kind),
	}, true
}

func lexicalSearchScore(queryTokens, fieldTokens map[string]struct{}) int {
	if len(queryTokens) == 0 || len(fieldTokens) == 0 {
		return 0
	}
	queryBytes := searchTokenBytes(queryTokens)
	fieldBytes := searchTokenBytes(fieldTokens)
	overlapBytes := 0
	for token := range queryTokens {
		if _, exists := fieldTokens[token]; exists {
			overlapBytes += len(token)
		}
	}
	if overlapBytes == 0 {
		return 0
	}
	absoluteEvidence := overlapBytes * searchEvidencePerByte
	if absoluteEvidence > searchEvidenceMaximum {
		absoluteEvidence = searchEvidenceMaximum
	}
	return searchQueryWeight*overlapBytes/queryBytes + searchFieldWeight*overlapBytes/fieldBytes + absoluteEvidence
}

func searchTokenBytes(tokens map[string]struct{}) int {
	total := 0
	for token := range tokens {
		total += len(token)
	}
	return total
}

func searchReason(kind string) string {
	switch kind {
	case "exact_capability_id":
		return "exact capability ID"
	case "exact_operation_id":
		return "exact operation ID"
	case "exact_capability_name":
		return "exact capability name"
	case "exact_tag":
		return "exact tag"
	case "exact_local_operation_id":
		return "exact local operation ID"
	case "exact_skill_name":
		return "exact skill name"
	case "lexical_capability":
		return "capability metadata overlap"
	case "lexical_operation":
		return "operation description overlap"
	case "lexical_skill":
		return "skill metadata overlap"
	default:
		return ""
	}
}

func normalizeSearchQuery(value string) (normalizedSearchText, error) {
	if !utf8.ValidString(value) {
		return normalizedSearchText{}, ErrSearchQueryInvalid
	}
	normalized := normalizeSearchText(value)
	if len(normalized.exact) > SearchQueryMaxBytes || normalized.tokenBytes > SearchQueryMaxBytes {
		return normalizedSearchText{}, ErrSearchQueryLimit
	}
	return normalized, nil
}

func normalizeSearchText(value string) normalizedSearchText {
	value = strings.TrimFunc(norm.NFC.String(value), unicode.IsSpace)
	tokens, tokenSet, tokenBytes := tokenizeSearchText(value)
	return normalizedSearchText{
		exact:      normalizeExact(value),
		tokens:     tokens,
		tokenSet:   tokenSet,
		tokenBytes: tokenBytes,
	}
}

func normalizeExact(value string) string {
	return norm.NFC.String(cases.Fold().String(strings.TrimFunc(norm.NFC.String(value), unicode.IsSpace)))
}

func tokenizeSearchText(value string) ([]string, map[string]struct{}, int) {
	runes := []rune(value)
	tokens := make([]string, 0)
	tokenSet := make(map[string]struct{})
	tokenBytes := 0
	tokenCount := 0
	start := -1
	flush := func(end int) {
		if start < 0 || start == end {
			return
		}
		token := normalizeExact(string(runes[start:end]))
		if token != "" {
			tokens = append(tokens, token)
			tokenSet[token] = struct{}{}
			tokenBytes += len(token)
			tokenCount++
		}
		start = -1
	}
	for index, current := range runes {
		if !unicode.IsLetter(current) && !unicode.IsNumber(current) {
			flush(index)
			continue
		}
		if start < 0 {
			start = index
			continue
		}
		previous := runes[index-1]
		nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
		camelBoundary := unicode.IsUpper(current) && (unicode.IsLower(previous) || unicode.IsUpper(previous) && nextIsLower)
		if camelBoundary && !preserveGitProductBoundary(runes, index) {
			flush(index)
			start = index
		}
	}
	flush(len(runes))
	if tokenCount > 1 {
		tokenBytes += tokenCount - 1
	}
	return tokens, tokenSet, tokenBytes
}

func preserveGitProductBoundary(runes []rune, index int) bool {
	if index < 3 || index+3 > len(runes) {
		return false
	}
	product := string(runes[index-3 : index+3])
	return product == "GitHub" || product == "GitLab"
}
