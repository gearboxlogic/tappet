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
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	DescribeSkills     = "skills"
	DescribeOperations = "operations"
	DescribeReferences = "references"
)

type ArtifactDescription struct {
	Ref       string `json:"ref"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
}

type SkillDescription struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	License       string              `json:"license,omitempty"`
	Compatibility string              `json:"compatibility,omitempty"`
	Artifact      ArtifactDescription `json:"artifact"`
}

type OperationDescription struct {
	ID            string `json:"id"`
	Description   string `json:"description"`
	MetadataState string `json:"metadata_state"`
}

type ReferenceDescription struct {
	ID       string              `json:"id"`
	Kind     string              `json:"kind"`
	SkillID  string              `json:"skill_id,omitempty"`
	Artifact ArtifactDescription `json:"artifact"`
}

type Projection struct {
	CapabilityID   string
	Name           string
	Version        string
	Description    string
	ManifestSHA256 string
	Include        []string
	Counts         CapabilityCounts
	Skills         []SkillDescription
	Operations     []OperationDescription
	References     []ReferenceDescription
	bytes          int64
}

// BuildProjection is pure: it copies only requested package-owned structure
// and never reads artifact bodies or provider metadata.
func BuildProjection(record *Record, include []string) (Projection, error) {
	if record == nil {
		return Projection{}, fmt.Errorf("%w: capability record is required", ErrDescribeRequestInvalid)
	}
	canonical, err := normalizeIncludes(include)
	if err != nil {
		return Projection{}, err
	}
	metadata := record.Metadata()
	projection := Projection{
		CapabilityID:   metadata.ID,
		Name:           metadata.Name,
		Version:        metadata.Version,
		Description:    metadata.Description,
		ManifestSHA256: record.ManifestDigest(),
		Include:        canonical,
	}
	for _, section := range canonical {
		switch section {
		case DescribeSkills:
			for _, skill := range record.Skills() {
				projection.Skills = append(projection.Skills, SkillDescription{
					ID:            skill.Path,
					Name:          skill.Metadata.Name,
					Description:   skill.Metadata.Description,
					License:       skill.Metadata.License,
					Compatibility: skill.Metadata.Compatibility,
					Artifact:      describeArtifact(skill.Artifact),
				})
			}
			sort.Slice(projection.Skills, func(i, j int) bool { return projection.Skills[i].ID < projection.Skills[j].ID })
			projection.Counts.Skills = len(projection.Skills)
		case DescribeOperations:
			for _, operation := range record.Operations() {
				projection.Operations = append(projection.Operations, OperationDescription{
					ID: operation.ID, Description: operation.Description, MetadataState: "unavailable",
				})
			}
			projection.Counts.Operations = len(projection.Operations)
		case DescribeReferences:
			for _, artifact := range record.Context() {
				projection.References = append(projection.References, ReferenceDescription{
					ID: artifact.ID, Kind: "context", Artifact: describeArtifact(artifact),
				})
			}
			for _, skill := range record.Skills() {
				for _, artifact := range skill.Resources {
					projection.References = append(projection.References, ReferenceDescription{
						ID:   skill.Path + "/" + artifact.ID,
						Kind: "skill_reference", SkillID: skill.Path, Artifact: describeArtifact(artifact),
					})
				}
			}
			sort.Slice(projection.References, func(i, j int) bool { return projection.References[i].ID < projection.References[j].ID })
			projection.Counts.References = len(projection.References)
		}
	}
	encoded, err := json.Marshal(projection.serializable())
	if err != nil {
		return Projection{}, err
	}
	projection.bytes = int64(len(encoded))
	return projection, nil
}

func normalizeIncludes(include []string) ([]string, error) {
	if len(include) == 0 || len(include) > 3 {
		return nil, fmt.Errorf("%w: include must select one to three sections", ErrDescribeRequestInvalid)
	}
	selected := make(map[string]bool, len(include))
	for _, section := range include {
		if !utf8.ValidString(section) || len(section) > DescribeIncludeMaxBytes {
			return nil, fmt.Errorf("%w: include section is invalid", ErrDescribeRequestInvalid)
		}
		if section != DescribeSkills && section != DescribeOperations && section != DescribeReferences {
			return nil, fmt.Errorf("%w: unknown include section", ErrDescribeRequestInvalid)
		}
		if selected[section] {
			return nil, fmt.Errorf("%w: duplicate include section", ErrDescribeRequestInvalid)
		}
		selected[section] = true
	}
	canonical := make([]string, 0, len(selected))
	for _, section := range []string{DescribeSkills, DescribeOperations, DescribeReferences} {
		if selected[section] {
			canonical = append(canonical, section)
		}
	}
	return canonical, nil
}

func describeArtifact(artifact Artifact) ArtifactDescription {
	return ArtifactDescription{Ref: artifact.Ref, MediaType: artifact.MediaType, Bytes: artifact.Bytes, SHA256: artifact.SHA256}
}

type projectionWire struct {
	CapabilityID   string                 `json:"capability_id"`
	Name           string                 `json:"name"`
	Version        string                 `json:"version"`
	Description    string                 `json:"description"`
	ManifestSHA256 string                 `json:"manifest_sha256"`
	Include        []string               `json:"include"`
	Counts         CapabilityCounts       `json:"counts"`
	Skills         []SkillDescription     `json:"skills,omitempty"`
	Operations     []OperationDescription `json:"operations,omitempty"`
	References     []ReferenceDescription `json:"references,omitempty"`
}

func (p Projection) serializable() projectionWire {
	return projectionWire{
		CapabilityID: p.CapabilityID, Name: p.Name, Version: p.Version, Description: p.Description,
		ManifestSHA256: p.ManifestSHA256, Include: p.Include, Counts: p.Counts,
		Skills: p.Skills, Operations: p.Operations, References: p.References,
	}
}

type DescribeRequest struct {
	CapabilityID string   `json:"capability_id,omitempty"`
	Include      []string `json:"include,omitempty"`
	Limit        int      `json:"limit"`
	Cursor       string   `json:"cursor,omitempty"`
}

type DescribeResponse struct {
	CapabilityID   string                 `json:"capability_id"`
	Name           string                 `json:"name"`
	Version        string                 `json:"version"`
	Description    string                 `json:"description"`
	ManifestSHA256 string                 `json:"manifest_sha256"`
	Include        []string               `json:"include"`
	Counts         CapabilityCounts       `json:"counts"`
	Skills         []SkillDescription     `json:"skills,omitempty"`
	Operations     []OperationDescription `json:"operations,omitempty"`
	References     []ReferenceDescription `json:"references,omitempty"`
	NextCursor     string                 `json:"next_cursor,omitempty"`
}

type ProjectionStore struct {
	mu       sync.Mutex
	registry *Registry
	limits   ProjectionLimits
	now      func() time.Time
	random   io.Reader
	key      [sha256.Size]byte
	closed   bool
	entries  map[string]*projectionEntry
	bytes    int64
}

type projectionEntry struct {
	projection Projection
	created    time.Time
	idleUntil  time.Time
}

type projectionCursor struct {
	ID     string `json:"i"`
	Offset int    `json:"o"`
	Limit  int    `json:"l"`
}

func NewProjectionStore(registry *Registry, limits ProjectionLimits) (*ProjectionStore, error) {
	return newProjectionStore(registry, limits, time.Now, rand.Reader)
}

func newProjectionStore(registry *Registry, limits ProjectionLimits, now func() time.Time, random io.Reader) (*ProjectionStore, error) {
	if registry == nil || now == nil || random == nil {
		return nil, errors.New("projection store dependencies are required")
	}
	if err := validateProjectionLimits(limits); err != nil {
		return nil, err
	}
	store := &ProjectionStore{registry: registry, limits: limits, now: now, random: random, entries: make(map[string]*projectionEntry)}
	if _, err := io.ReadFull(random, store.key[:]); err != nil {
		return nil, fmt.Errorf("initialize projection cursor key: %w", err)
	}
	return store, nil
}

func (s *ProjectionStore) Describe(request DescribeRequest) (DescribeResponse, error) {
	if request.Limit <= 0 || request.Limit > DescribeLimitMax {
		return DescribeResponse{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrDescribeRequestInvalid, DescribeLimitMax)
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return DescribeResponse{}, ErrServiceClosed
	}
	if request.Cursor == "" {
		if request.CapabilityID == "" || len(request.CapabilityID) > MaxCapabilityIDBytes || !utf8.ValidString(request.CapabilityID) || !capabilityIDPattern.MatchString(request.CapabilityID) {
			return DescribeResponse{}, fmt.Errorf("%w: capability_id is invalid", ErrDescribeRequestInvalid)
		}
		include, err := normalizeIncludes(request.Include)
		if err != nil {
			return DescribeResponse{}, err
		}
		lease, err := s.registry.Lookup(request.CapabilityID)
		if err != nil {
			return DescribeResponse{}, err
		}
		projection, buildErr := BuildProjection(lease.Record(), include)
		lease.Release()
		if buildErr != nil {
			return DescribeResponse{}, buildErr
		}
		return s.firstPage(projection, request.Limit)
	}
	if request.CapabilityID != "" || len(request.Include) != 0 {
		return DescribeResponse{}, fmt.Errorf("%w: cursor requests omit capability_id and include", ErrDescribeRequestInvalid)
	}
	return s.cursorPage(request.Cursor, request.Limit)
}

func (s *ProjectionStore) firstPage(projection Projection, limit int) (DescribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return DescribeResponse{}, ErrServiceClosed
	}
	now := s.now()
	s.sweepLocked(now)
	count := projectionItemCount(projection)
	if count <= limit {
		response := pageProjection(projection, 0, count, "")
		if checkDescribeResponseSize(response) == nil {
			return response, nil
		}
	}
	if len(s.entries) >= s.limits.MaxCount || s.bytes+projection.bytes > s.limits.MaxBytes {
		return DescribeResponse{}, ErrServiceQuota
	}
	id, err := s.newUniqueIDLocked()
	if err != nil {
		return DescribeResponse{}, fmt.Errorf("create projection cursor: %w", err)
	}
	if _, exists := s.entries[id]; exists {
		return DescribeResponse{}, errors.New("projection cursor collision")
	}
	entry := &projectionEntry{projection: projection, created: now, idleUntil: now.Add(s.limits.Idle)}
	s.entries[id] = entry
	s.bytes += projection.bytes
	response, consumed, err := s.boundedPage(projection, 0, limit, id)
	if err != nil {
		delete(s.entries, id)
		s.bytes -= projection.bytes
		return DescribeResponse{}, err
	}
	if consumed == count {
		delete(s.entries, id)
		s.bytes -= projection.bytes
	}
	return response, nil
}

func (s *ProjectionStore) cursorPage(token string, limit int) (DescribeResponse, error) {
	cursor, err := s.parseCursor(token)
	if err != nil {
		return DescribeResponse{}, ErrDescribeCursorInvalid
	}
	if cursor.Limit != limit {
		return DescribeResponse{}, ErrDescribeCursorInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return DescribeResponse{}, ErrServiceClosed
	}
	now := s.now()
	s.sweepLocked(now)
	entry := s.entries[cursor.ID]
	if entry == nil {
		return DescribeResponse{}, ErrDescribeCursorStale
	}
	count := projectionItemCount(entry.projection)
	if cursor.Offset <= 0 || cursor.Offset >= count {
		return DescribeResponse{}, ErrDescribeCursorInvalid
	}
	response, _, err := s.boundedPage(entry.projection, cursor.Offset, limit, cursor.ID)
	if err != nil {
		return DescribeResponse{}, err
	}
	entry.idleUntil = now.Add(s.limits.Idle)
	return response, nil
}

func (s *ProjectionStore) boundedPage(projection Projection, offset, limit int, id string) (DescribeResponse, int, error) {
	count := projectionItemCount(projection)
	remaining := count - offset
	if remaining <= 0 {
		return DescribeResponse{}, 0, ErrDescribeCursorInvalid
	}
	requestedLimit := limit
	itemsLimit := limit
	if itemsLimit > remaining {
		itemsLimit = remaining
	}
	for items := itemsLimit; items > 0; items-- {
		next := ""
		if offset+items < count {
			next = s.makeCursor(projectionCursor{ID: id, Offset: offset + items, Limit: requestedLimit})
		}
		response := pageProjection(projection, offset, items, next)
		if checkDescribeResponseSize(response) == nil {
			return response, items, nil
		}
	}
	return DescribeResponse{}, 0, ErrServiceQuota
}

func projectionItemCount(projection Projection) int {
	return len(projection.Skills) + len(projection.Operations) + len(projection.References)
}

func pageProjection(projection Projection, offset, limit int, next string) DescribeResponse {
	response := DescribeResponse{
		CapabilityID: projection.CapabilityID, Name: projection.Name, Version: projection.Version,
		Description: projection.Description, ManifestSHA256: projection.ManifestSHA256,
		Include: append([]string(nil), projection.Include...), Counts: projection.Counts,
		NextCursor: next,
	}
	remaining := limit
	copySection := func(length int, appendRange func(int, int)) {
		if remaining == 0 {
			return
		}
		if offset >= length {
			offset -= length
			return
		}
		end := offset + remaining
		if end > length {
			end = length
		}
		appendRange(offset, end)
		remaining -= end - offset
		offset = 0
	}
	copySection(len(projection.Skills), func(start, end int) {
		response.Skills = append([]SkillDescription(nil), projection.Skills[start:end]...)
	})
	copySection(len(projection.Operations), func(start, end int) {
		response.Operations = append([]OperationDescription(nil), projection.Operations[start:end]...)
	})
	copySection(len(projection.References), func(start, end int) {
		response.References = append([]ReferenceDescription(nil), projection.References[start:end]...)
	})
	return response
}

func (s *ProjectionStore) makeCursor(cursor projectionCursor) string {
	payload, _ := json.Marshal(cursor)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = io.WriteString(mac, "describe:v1:"+encoded)
	return "describe:v1:" + encoded + ":" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *ProjectionStore) parseCursor(token string) (projectionCursor, error) {
	if len(token) > OpaqueReferenceMaxBytes {
		return projectionCursor{}, ErrDescribeCursorInvalid
	}
	parts := strings.Split(token, ":")
	if len(parts) != 4 || parts[0] != "describe" || parts[1] != "v1" {
		return projectionCursor{}, ErrDescribeCursorInvalid
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return projectionCursor{}, err
	}
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = io.WriteString(mac, "describe:v1:"+parts[2])
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return projectionCursor{}, ErrDescribeCursorInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return projectionCursor{}, err
	}
	var cursor projectionCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.ID == "" || cursor.Offset <= 0 || cursor.Limit <= 0 || cursor.Limit > DescribeLimitMax {
		return projectionCursor{}, ErrDescribeCursorInvalid
	}
	return cursor, nil
}

func (s *ProjectionStore) newUniqueIDLocked() (string, error) {
	for range readHandleCollisionAttempts {
		id, err := randomOpaqueID(s.random)
		if err != nil {
			return "", fmt.Errorf("%w: random source failed", ErrHandleGeneration)
		}
		if _, exists := s.entries[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: repeated collision", ErrHandleGeneration)
}

func checkDescribeResponseSize(response DescribeResponse) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if len(encoded) > BrokerResponseMaxBytes {
		return ErrServiceQuota
	}
	return nil
}

func (s *ProjectionStore) sweepLocked(now time.Time) {
	for id, entry := range s.entries {
		if !now.Before(minTime(entry.idleUntil, entry.created.Add(s.limits.Absolute))) {
			delete(s.entries, id)
			s.bytes -= entry.projection.bytes
		}
	}
}

func (s *ProjectionStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	clear(s.entries)
	s.bytes = 0
}

type ProjectionStats struct {
	Projections int
	Bytes       int64
}

func (s *ProjectionStore) Stats() ProjectionStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
	return ProjectionStats{Projections: len(s.entries), Bytes: s.bytes}
}

func randomOpaqueID(random io.Reader) (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(random, value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
