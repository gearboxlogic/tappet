package capability

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const readHandleCollisionAttempts = 4

var attemptIDPattern = regexp.MustCompile(`^read:[A-Za-z0-9_-]{22,123}$`)

type ReadRequest struct {
	CapabilityID string `json:"capability_id,omitempty"`
	Ref          string `json:"ref"`
	AttemptID    string `json:"attempt_id,omitempty"`
	Offset       int64  `json:"offset"`
	MaxBytes     int    `json:"max_bytes"`
}

type ReadResponse struct {
	Ref             string `json:"ref"`
	ContinuationRef string `json:"continuation_ref,omitempty"`
	Offset          int64  `json:"offset"`
	DataBase64      string `json:"data_base64"`
	NextOffset      int64  `json:"next_offset"`
	Complete        bool   `json:"complete"`
	MediaType       string `json:"media_type"`
	StoredBytes     int64  `json:"stored_bytes"`
	SHA256          string `json:"sha256"`
	ExpiresAt       string `json:"expires_at"`
}

type ReadLeaseStore struct {
	mu          sync.Mutex
	registry    *Registry
	limits      ReadLeaseLimits
	now         func() time.Time
	random      io.Reader
	closed      bool
	reads       map[string]*readLease
	attempts    map[[sha256.Size]byte]string
	pinnedBytes int64
	replayBytes int64
}

type readLease struct {
	id          string
	scopeDigest [sha256.Size]byte
	attemptKey  [sha256.Size]byte
	artifact    Artifact
	object      *artifactLease
	created     time.Time
	idleUntil   time.Time
	expiresAt   time.Time
	final       bool
	replay      readReplay
}

type readReplay struct {
	inputRef string
	offset   int64
	maxBytes int
	response ReadResponse
	encoded  []byte
}

func NewReadLeaseStore(registry *Registry, limits ReadLeaseLimits) (*ReadLeaseStore, error) {
	return newReadLeaseStore(registry, limits, time.Now, rand.Reader)
}

func newReadLeaseStore(registry *Registry, limits ReadLeaseLimits, now func() time.Time, random io.Reader) (*ReadLeaseStore, error) {
	if registry == nil || now == nil || random == nil {
		return nil, errors.New("read lease store dependencies are required")
	}
	if err := validateReadLeaseLimits(limits); err != nil {
		return nil, err
	}
	return &ReadLeaseStore{
		registry: registry, limits: limits, now: now, random: random,
		reads: make(map[string]*readLease), attempts: make(map[[sha256.Size]byte]string),
	}, nil
}

// Read uses an explicit caller scope. The portable single-tenant adapter uses
// one broker-instance scope; transport sessions and bearer tokens are not
// identities and must not be substituted here.
func (s *ReadLeaseStore) Read(scope string, request ReadRequest) (ReadResponse, error) {
	if err := validateReadBounds(scope, request); err != nil {
		return ReadResponse{}, err
	}
	if strings.HasPrefix(request.Ref, "read:") {
		return s.readContinuation(scope, request)
	}
	return s.readInitial(scope, request)
}

func validateReadBounds(scope string, request ReadRequest) error {
	if scope == "" || len(scope) > 256 {
		return fmt.Errorf("%w: caller scope is invalid", ErrReadRequestInvalid)
	}
	if request.Ref == "" || len(request.Ref) > OpaqueReferenceMaxBytes {
		return fmt.Errorf("%w: ref is invalid", ErrReadRequestInvalid)
	}
	if request.Offset < 0 || request.MaxBytes <= 0 || request.MaxBytes > ReadChunkMax {
		return fmt.Errorf("%w: offset or max_bytes is invalid", ErrReadRequestInvalid)
	}
	if request.Offset > int64(^uint64(0)>>1)-int64(request.MaxBytes) {
		return fmt.Errorf("%w: byte range overflows", ErrReadRequestInvalid)
	}
	if len(request.AttemptID) > ReadAttemptMaxBytes {
		return fmt.Errorf("%w: attempt_id is invalid", ErrReadRequestInvalid)
	}
	return nil
}

func (s *ReadLeaseStore) readInitial(scope string, request ReadRequest) (ReadResponse, error) {
	if request.CapabilityID == "" || len(request.CapabilityID) > MaxCapabilityIDBytes || !attemptIDPattern.MatchString(request.AttemptID) {
		return ReadResponse{}, fmt.Errorf("%w: initial reads require capability_id and a 128-bit attempt_id", ErrReadRequestInvalid)
	}
	if !strings.HasPrefix(request.Ref, "artifact:v1:") {
		return ReadResponse{}, ErrArtifactReferenceStale
	}
	key := readAttemptKey(scope, request)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ReadResponse{}, ErrServiceClosed
	}
	now := s.now()
	s.sweepLocked(now)
	if id, exists := s.attempts[key]; exists {
		lease := s.reads[id]
		if lease != nil && lease.replay.inputRef == request.Ref && lease.replay.offset == request.Offset && lease.replay.maxBytes == request.MaxBytes {
			response := cloneReadResponse(lease.replay.response)
			s.mu.Unlock()
			return response, nil
		}
		s.mu.Unlock()
		return ReadResponse{}, ErrContinuationConflict
	}
	s.mu.Unlock()

	capabilityLease, err := s.registry.Lookup(request.CapabilityID)
	if err != nil {
		return ReadResponse{}, err
	}
	artifact, object, ok := capabilityLease.Record().acquireArtifact(request.Ref)
	capabilityLease.Release()
	if !ok || !readableArtifactKind(artifact.Kind) {
		if object != nil {
			object.release()
		}
		return ReadResponse{}, ErrArtifactReferenceStale
	}
	if request.Offset > artifact.Bytes {
		object.release()
		return ReadResponse{}, fmt.Errorf("%w: offset exceeds artifact size", ErrReadRequestInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		object.release()
		return ReadResponse{}, ErrServiceClosed
	}
	now = s.now()
	s.sweepLocked(now)
	if id, exists := s.attempts[key]; exists {
		object.release()
		lease := s.reads[id]
		if lease != nil && lease.replay.inputRef == request.Ref && lease.replay.offset == request.Offset && lease.replay.maxBytes == request.MaxBytes {
			return cloneReadResponse(lease.replay.response), nil
		}
		return ReadResponse{}, ErrContinuationConflict
	}
	if len(s.reads) >= s.limits.MaxCount || s.pinnedBytes+artifact.Bytes > s.limits.MaxPinnedBytes || s.replayBytes+ReadReplayReservation > s.limits.MaxReplayBytes {
		object.release()
		return ReadResponse{}, ErrServiceQuota
	}
	id, err := s.newUniqueIDLocked()
	if err != nil {
		object.release()
		return ReadResponse{}, err
	}
	lease := &readLease{
		id: id, scopeDigest: sha256.Sum256([]byte(scope)), attemptKey: key,
		artifact: artifact, object: object, created: now, idleUntil: now.Add(s.limits.Idle),
	}
	response, err := s.prepareLocked(lease, request.Ref, request.Offset, request.MaxBytes)
	if err != nil {
		object.release()
		return ReadResponse{}, err
	}
	s.reads[id] = lease
	s.attempts[key] = id
	s.pinnedBytes += artifact.Bytes
	s.replayBytes += ReadReplayReservation
	return response, nil
}

func (s *ReadLeaseStore) readContinuation(scope string, request ReadRequest) (ReadResponse, error) {
	if request.CapabilityID != "" || request.AttemptID != "" {
		return ReadResponse{}, fmt.Errorf("%w: continuation reads omit capability_id and attempt_id", ErrReadRequestInvalid)
	}
	id, ok := parseReadHandle(request.Ref)
	if !ok {
		return ReadResponse{}, ErrContinuationInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ReadResponse{}, ErrServiceClosed
	}
	now := s.now()
	s.sweepLocked(now)
	lease := s.reads[id]
	if lease == nil {
		return ReadResponse{}, ErrContinuationStale
	}
	if lease.scopeDigest != sha256.Sum256([]byte(scope)) {
		return ReadResponse{}, ErrContinuationStale
	}
	if request.Offset == lease.replay.offset {
		if lease.replay.inputRef == request.Ref && lease.replay.maxBytes == request.MaxBytes {
			return cloneReadResponse(lease.replay.response), nil
		}
		return ReadResponse{}, ErrContinuationConflict
	}
	if lease.final || request.Offset != lease.replay.response.NextOffset {
		return ReadResponse{}, ErrContinuationConflict
	}
	return s.prepareLocked(lease, request.Ref, request.Offset, request.MaxBytes)
}

func (s *ReadLeaseStore) prepareLocked(lease *readLease, inputRef string, offset int64, maxBytes int) (ReadResponse, error) {
	chunk, ok := lease.object.readRange(offset, int64(maxBytes))
	if !ok {
		return ReadResponse{}, ErrArtifactReferenceStale
	}
	next := offset + int64(len(chunk))
	complete := next == lease.artifact.Bytes
	now := s.now()
	absolute := lease.created.Add(s.limits.Absolute)
	idleUntil := lease.idleUntil
	expiresAt := lease.expiresAt
	if complete {
		expiresAt = minTime(now.Add(s.limits.FinalReplay), absolute)
	} else {
		idleUntil = now.Add(s.limits.Idle)
		expiresAt = minTime(idleUntil, absolute)
	}
	response := ReadResponse{
		Ref: lease.artifact.Ref, Offset: offset, DataBase64: base64.StdEncoding.EncodeToString(chunk),
		NextOffset: next, Complete: complete, MediaType: lease.artifact.MediaType,
		StoredBytes: lease.artifact.Bytes, SHA256: lease.artifact.SHA256,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339Nano),
	}
	if !complete {
		response.ContinuationRef = "read:v1:" + lease.id
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return ReadResponse{}, err
	}
	if len(encoded) > BrokerResponseMaxBytes || len(encoded) > ReadReplayReservation {
		return ReadResponse{}, ErrServiceQuota
	}
	lease.final = complete
	lease.idleUntil = idleUntil
	lease.expiresAt = expiresAt
	lease.replay = readReplay{
		inputRef: inputRef, offset: offset, maxBytes: maxBytes,
		response: cloneReadResponse(response), encoded: append([]byte(nil), encoded...),
	}
	return response, nil
}

func readableArtifactKind(kind ArtifactKind) bool {
	return kind == ArtifactSkill || kind == ArtifactSkillResource || kind == ArtifactContext
}

func readAttemptKey(scope string, request ReadRequest) [sha256.Size]byte {
	hash := sha256.New()
	for _, value := range []string{scope, request.CapabilityID, request.Ref, request.AttemptID, strconv.FormatInt(request.Offset, 10), strconv.Itoa(request.MaxBytes)} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = io.WriteString(hash, value)
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func (s *ReadLeaseStore) newUniqueIDLocked() (string, error) {
	for range readHandleCollisionAttempts {
		id, err := randomOpaqueID(s.random)
		if err != nil {
			return "", fmt.Errorf("%w: random source failed", ErrHandleGeneration)
		}
		if _, exists := s.reads[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: repeated collision", ErrHandleGeneration)
}

func parseReadHandle(ref string) (string, bool) {
	if len(ref) > OpaqueReferenceMaxBytes || !strings.HasPrefix(ref, "read:v1:") {
		return "", false
	}
	id := strings.TrimPrefix(ref, "read:v1:")
	decoded, err := base64.RawURLEncoding.DecodeString(id)
	return id, err == nil && len(decoded) == 16
}

func cloneReadResponse(response ReadResponse) ReadResponse { return response }

func (s *ReadLeaseStore) sweepLocked(now time.Time) {
	for id, lease := range s.reads {
		if !now.Before(lease.expiresAt) {
			s.dropLocked(id, lease)
		}
	}
}

func (s *ReadLeaseStore) dropLocked(id string, lease *readLease) {
	delete(s.reads, id)
	delete(s.attempts, lease.attemptKey)
	s.pinnedBytes -= lease.artifact.Bytes
	s.replayBytes -= ReadReplayReservation
	lease.object.release()
}

func (s *ReadLeaseStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for id, lease := range s.reads {
		s.dropLocked(id, lease)
	}
}

type ReadLeaseStats struct {
	Leases      int
	Attempts    int
	PinnedBytes int64
	ReplayBytes int64
}

func (s *ReadLeaseStore) Stats() ReadLeaseStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
	return ReadLeaseStats{Leases: len(s.reads), Attempts: len(s.attempts), PinnedBytes: s.pinnedBytes, ReplayBytes: s.replayBytes}
}

// encodedReplay is used by acceptance tests to verify byte-exact retries
// without exposing raw attempt IDs or handles through diagnostics.
func (s *ReadLeaseStore) encodedReplay(ref string) ([]byte, bool) {
	id, ok := parseReadHandle(ref)
	if !ok {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease := s.reads[id]
	if lease == nil {
		return nil, false
	}
	return append([]byte(nil), lease.replay.encoded...), true
}
