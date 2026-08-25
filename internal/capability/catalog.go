package capability

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type Catalog struct {
	registry *Registry
	key      [sha256.Size]byte
}

func NewCatalog(registry *Registry) (*Catalog, error) {
	return newCatalog(registry, rand.Reader)
}

func newCatalog(registry *Registry, random io.Reader) (*Catalog, error) {
	if registry == nil {
		return nil, errors.New("capability registry is required")
	}
	if random == nil {
		return nil, errors.New("catalog random source is required")
	}
	catalog := &Catalog{registry: registry}
	if _, err := io.ReadFull(random, catalog.key[:]); err != nil {
		return nil, fmt.Errorf("initialize catalog cursor key: %w", err)
	}
	return catalog, nil
}

type CatalogSearchRequest struct {
	Query       string `json:"query"`
	Path        string `json:"path,omitempty"`
	Limit       int    `json:"limit"`
	ChildLimit  int    `json:"child_limit"`
	ChildCursor string `json:"child_cursor,omitempty"`
}

type CatalogSearchResponse struct {
	RankingVersion  string           `json:"ranking_version"`
	Results         []SearchResult   `json:"results"`
	Path            string           `json:"path"`
	Children        []HierarchyEntry `json:"children"`
	TotalChildren   int              `json:"total_children"`
	NextChildCursor string           `json:"next_child_cursor,omitempty"`
}

func (c *Catalog) Search(request CatalogSearchRequest) (CatalogSearchResponse, error) {
	if request.ChildLimit < 0 || request.ChildLimit > SearchLimitMax {
		return CatalogSearchResponse{}, fmt.Errorf("%w: child_limit must be between 0 and %d", ErrSearchLimitInvalid, SearchLimitMax)
	}
	path := request.Path
	if path == "/" {
		path = ""
	}
	offset := 0
	var expectedRevision *uint64
	if request.ChildCursor != "" {
		if len(request.ChildCursor) > OpaqueReferenceMaxBytes {
			return CatalogSearchResponse{}, ErrCatalogCursorInvalid
		}
		cursor, err := c.parseCursor(request.ChildCursor)
		if err != nil || cursor.Path != path || cursor.Offset < 0 {
			return CatalogSearchResponse{}, ErrCatalogCursorInvalid
		}
		expectedRevision = &cursor.Revision
		offset = cursor.Offset
	}
	search, view, revision, err := c.registry.searchAndBrowse(SearchRequest{Query: request.Query, Path: path, Limit: request.Limit}, expectedRevision)
	if err != nil {
		return CatalogSearchResponse{}, err
	}
	if offset > len(view.Children) {
		return CatalogSearchResponse{}, ErrCatalogCursorInvalid
	}
	end := offset + request.ChildLimit
	if end > len(view.Children) {
		end = len(view.Children)
	}
	response := CatalogSearchResponse{
		RankingVersion: search.RankingVersion,
		Results:        search.Results,
		Path:           view.Path,
		Children:       append([]HierarchyEntry(nil), view.Children[offset:end]...),
		TotalChildren:  len(view.Children),
	}
	if request.ChildLimit > 0 && end < len(view.Children) {
		response.NextChildCursor = c.makeCursor(catalogCursor{Path: path, Revision: revision, Offset: end})
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return CatalogSearchResponse{}, err
	}
	if len(encoded) > BrokerResponseMaxBytes {
		return CatalogSearchResponse{}, ErrServiceQuota
	}
	return response, nil
}

type catalogCursor struct {
	Path     string `json:"p"`
	Revision uint64 `json:"r"`
	Offset   int    `json:"o"`
}

func (c *Catalog) makeCursor(cursor catalogCursor) string {
	payload, _ := json.Marshal(cursor)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, c.key[:])
	_, _ = io.WriteString(mac, "catalog:v1:"+encoded)
	return "catalog:v1:" + encoded + ":" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (c *Catalog) parseCursor(token string) (catalogCursor, error) {
	parts := strings.Split(token, ":")
	if len(parts) != 4 || parts[0] != "catalog" || parts[1] != "v1" {
		return catalogCursor{}, ErrCatalogCursorInvalid
	}
	macBytes, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return catalogCursor{}, err
	}
	mac := hmac.New(sha256.New, c.key[:])
	_, _ = io.WriteString(mac, "catalog:v1:"+parts[2])
	if !hmac.Equal(macBytes, mac.Sum(nil)) {
		return catalogCursor{}, ErrCatalogCursorInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return catalogCursor{}, err
	}
	var cursor catalogCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return catalogCursor{}, err
	}
	if cursor.Offset < 0 {
		return catalogCursor{}, ErrCatalogCursorInvalid
	}
	return cursor, nil
}
