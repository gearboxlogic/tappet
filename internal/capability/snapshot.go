package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
)

// StoreLimits bound concurrent package staging and retained immutable bytes.
type StoreLimits struct {
	StagingBytes  int64
	SnapshotBytes int64
}

func DefaultStoreLimits() StoreLimits {
	return StoreLimits{
		StagingBytes:  256 << 20,
		SnapshotBytes: 1 << 30,
	}
}

// SnapshotStore owns private immutable package bytes. Callers receive copies,
// never writable aliases to retained objects.
type SnapshotStore struct {
	mu               sync.Mutex
	limits           StoreLimits
	stagingReserved  int64
	snapshotReserved int64
	snapshotUsed     int64
	objects          map[[sha256.Size]byte]*snapshotObject
}

type snapshotObject struct {
	data []byte
	refs int
}

func NewSnapshotStore(limits StoreLimits) (*SnapshotStore, error) {
	if limits.StagingBytes <= 0 || limits.SnapshotBytes <= 0 {
		return nil, errors.New("snapshot store limits must be positive")
	}
	return &SnapshotStore{limits: limits, objects: make(map[[sha256.Size]byte]*snapshotObject)}, nil
}

type stagingTransaction struct {
	store       *SnapshotStore
	reserved    int64
	artifacts   map[string]stagedArtifact
	closed      bool
	packageSize int64
}

type stagedArtifact struct {
	data   []byte
	digest [sha256.Size]byte
}

func (s *SnapshotStore) begin() *stagingTransaction {
	return &stagingTransaction{store: s, artifacts: make(map[string]stagedArtifact)}
}

func (t *stagingTransaction) reserve(size int64) error {
	if size < 0 {
		return errors.New("negative artifact size")
	}
	if t.packageSize+size > PackageArtifactMax {
		return fmt.Errorf("package artifacts exceed %d bytes", PackageArtifactMax)
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	if t.store.stagingReserved+size > t.store.limits.StagingBytes {
		return errors.New("staging quota exhausted")
	}
	if t.store.snapshotUsed+t.store.snapshotReserved+size > t.store.limits.SnapshotBytes {
		return errors.New("snapshot quota exhausted")
	}
	t.store.stagingReserved += size
	t.store.snapshotReserved += size
	t.reserved += size
	t.packageSize += size
	return nil
}

func (t *stagingTransaction) stageReserved(name string, reader io.Reader, declaredSize, maximum int64) error {
	if t.closed {
		return errors.New("staging transaction is closed")
	}
	if declaredSize < 0 || declaredSize > maximum {
		return fmt.Errorf("declared artifact size %d exceeds %d", declaredSize, maximum)
	}
	if _, exists := t.artifacts[name]; exists {
		return fmt.Errorf("artifact path %q is staged more than once", name)
	}

	limited := io.LimitReader(reader, declaredSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("copy staged bytes: %w", err)
	}
	if int64(len(data)) != declaredSize {
		if int64(len(data)) > declaredSize {
			return fmt.Errorf("artifact grew beyond its %d-byte reservation", declaredSize)
		}
		return fmt.Errorf("artifact changed size during staging: expected %d bytes, copied %d", declaredSize, len(data))
	}
	if int64(len(data)) > maximum {
		return fmt.Errorf("artifact exceeds %d bytes", maximum)
	}
	privateCopy := append([]byte(nil), data...)
	t.artifacts[name] = stagedArtifact{data: privateCopy, digest: sha256.Sum256(privateCopy)}
	return nil
}

func (t *stagingTransaction) bytes(name string) ([]byte, bool) {
	artifact, ok := t.artifacts[name]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), artifact.data...), true
}

func (t *stagingTransaction) abort() {
	if t.closed {
		return
	}
	t.closed = true
	t.store.mu.Lock()
	t.store.stagingReserved -= t.reserved
	t.store.snapshotReserved -= t.reserved
	t.store.mu.Unlock()
	for name, artifact := range t.artifacts {
		clear(artifact.data)
		delete(t.artifacts, name)
	}
}

func (t *stagingTransaction) commit() (*Snapshot, error) {
	if t.closed {
		return nil, errors.New("staging transaction is closed")
	}
	t.closed = true
	t.store.mu.Lock()
	defer t.store.mu.Unlock()

	digests := make(map[string][sha256.Size]byte, len(t.artifacts))
	newBytes := int64(0)
	for name, artifact := range t.artifacts {
		digests[name] = artifact.digest
		if _, exists := t.store.objects[artifact.digest]; !exists {
			newBytes += int64(len(artifact.data))
		}
	}
	if t.store.snapshotUsed+newBytes > t.store.limits.SnapshotBytes {
		t.store.stagingReserved -= t.reserved
		t.store.snapshotReserved -= t.reserved
		return nil, errors.New("snapshot quota exhausted at commit")
	}
	for _, artifact := range t.artifacts {
		object, exists := t.store.objects[artifact.digest]
		if !exists {
			object = &snapshotObject{data: artifact.data}
			t.store.objects[artifact.digest] = object
			t.store.snapshotUsed += int64(len(artifact.data))
		} else {
			clear(artifact.data)
		}
		object.refs++
	}
	t.store.stagingReserved -= t.reserved
	t.store.snapshotReserved -= t.reserved
	return &Snapshot{store: t.store, digests: digests}, nil
}

// Snapshot retains one immutable package generation.
type Snapshot struct {
	store   *SnapshotStore
	digests map[string][sha256.Size]byte
	once    sync.Once
}

// artifactLease retains one immutable content object independently of the
// package generation that originally referenced it.
type artifactLease struct {
	store  *SnapshotStore
	digest [sha256.Size]byte
	once   sync.Once
}

func (s *Snapshot) Read(name string) ([]byte, bool) {
	digest, ok := s.digests[name]
	if !ok {
		return nil, false
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	object, ok := s.store.objects[digest]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), object.data...), true
}

func (s *Snapshot) acquireArtifact(name string, expectedBytes int64, expectedSHA256 string) (*artifactLease, bool) {
	digest, ok := s.digests[name]
	if !ok {
		return nil, false
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	object, ok := s.store.objects[digest]
	if !ok || int64(len(object.data)) != expectedBytes || hex.EncodeToString(digest[:]) != expectedSHA256 {
		return nil, false
	}
	object.refs++
	return &artifactLease{store: s.store, digest: digest}, true
}

func (l *artifactLease) readRange(offset, maxBytes int64) ([]byte, bool) {
	if l == nil || offset < 0 || maxBytes < 0 {
		return nil, false
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	object, ok := l.store.objects[l.digest]
	if !ok || offset > int64(len(object.data)) {
		return nil, false
	}
	end := offset + maxBytes
	if end < offset || end > int64(len(object.data)) {
		end = int64(len(object.data))
	}
	return append([]byte(nil), object.data[offset:end]...), true
}

func (l *artifactLease) release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.store.mu.Lock()
		defer l.store.mu.Unlock()
		object := l.store.objects[l.digest]
		if object == nil {
			return
		}
		object.refs--
		if object.refs == 0 {
			l.store.snapshotUsed -= int64(len(object.data))
			clear(object.data)
			delete(l.store.objects, l.digest)
		}
	})
}

func (s *Snapshot) release() {
	s.once.Do(func() {
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		for _, digest := range s.digests {
			object := s.store.objects[digest]
			if object == nil {
				continue
			}
			object.refs--
			if object.refs == 0 {
				s.store.snapshotUsed -= int64(len(object.data))
				clear(object.data)
				delete(s.store.objects, digest)
			}
		}
	})
}

type StoreStats struct {
	StagingReserved  int64
	SnapshotReserved int64
	SnapshotUsed     int64
	Objects          int
}

func (s *SnapshotStore) Stats() StoreStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StoreStats{
		StagingReserved:  s.stagingReserved,
		SnapshotReserved: s.snapshotReserved,
		SnapshotUsed:     s.snapshotUsed,
		Objects:          len(s.objects),
	}
}
