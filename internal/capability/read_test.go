package capability

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testReadScope = "portable-broker-instance"

func TestReadReconstructsEveryReadableArtifactWithinChunkBound(t *testing.T) {
	fixture := newReadableFixture(t, strings.Repeat("body-data-", 40))
	for name, artifact := range fixture.artifacts {
		t.Run(name, func(t *testing.T) {
			store, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte(name), 4096)))
			require.NoError(t, err)
			t.Cleanup(store.Close)
			request := ReadRequest{CapabilityID: fixture.id, Ref: artifact.Ref, AttemptID: attemptID(name), Offset: 0, MaxBytes: 17}
			var reconstructed []byte
			for {
				response, readErr := store.Read(testReadScope, request)
				require.NoError(t, readErr)
				chunk, decodeErr := base64.StdEncoding.DecodeString(response.DataBase64)
				require.NoError(t, decodeErr)
				assert.LessOrEqual(t, len(chunk), request.MaxBytes)
				reconstructed = append(reconstructed, chunk...)
				if response.Complete {
					digest := sha256.Sum256(reconstructed)
					assert.Equal(t, artifact.SHA256, hex.EncodeToString(digest[:]))
					assert.Equal(t, artifact.Bytes, int64(len(reconstructed)))
					break
				}
				request = ReadRequest{Ref: response.ContinuationRef, Offset: response.NextOffset, MaxBytes: 17}
			}
		})
	}
}

func TestReadReconstructsMaximumAcceptedReference(t *testing.T) {
	fixture := newReadableFixture(t, "small")
	fixture.write("2.0.0", "small")
	large := bytes.Repeat([]byte("x"), ArtifactMaxBytes)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.root, fixture.id, "context", "info.txt"), large, 0o644))
	replacement, err := fixture.loader.Load(fixture.id)
	require.NoError(t, err)
	require.NoError(t, fixture.registry.Reinstall(replacement))
	fixture.artifacts = fixture.currentArtifacts(t)
	store, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{1}, 4096)))
	require.NoError(t, err)
	t.Cleanup(store.Close)

	request := ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["context"].Ref, AttemptID: attemptID("maximum-reference"), Offset: 0, MaxBytes: ReadChunkMax}
	var total int
	for {
		response, readErr := store.Read(testReadScope, request)
		require.NoError(t, readErr)
		chunk, decodeErr := base64.StdEncoding.DecodeString(response.DataBase64)
		require.NoError(t, decodeErr)
		assert.LessOrEqual(t, len(chunk), ReadChunkMax)
		total += len(chunk)
		if response.Complete {
			break
		}
		request = ReadRequest{Ref: response.ContinuationRef, Offset: response.NextOffset, MaxBytes: ReadChunkMax}
	}
	assert.Equal(t, ArtifactMaxBytes, total)
}

func TestReadContinuationAndFirstReplaySurviveUninstall(t *testing.T) {
	fixture := newReadableFixture(t, strings.Repeat("large-body-", 50))
	store, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{7}, 4096)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	artifact := fixture.artifacts["skill"]
	initial := ReadRequest{CapabilityID: fixture.id, Ref: artifact.Ref, AttemptID: attemptID("uninstall"), Offset: 0, MaxBytes: 31}
	first, err := store.Read(testReadScope, initial)
	require.NoError(t, err)
	require.False(t, first.Complete)
	require.NoError(t, fixture.registry.Uninstall(fixture.id))
	assert.Equal(t, 1, fixture.snapshot.Stats().Objects)
	assert.Equal(t, artifact.Bytes, fixture.snapshot.Stats().SnapshotUsed)

	retry, err := store.Read(testReadScope, initial)
	require.NoError(t, err)
	assert.Equal(t, first, retry)
	second, err := store.Read(testReadScope, ReadRequest{Ref: first.ContinuationRef, Offset: first.NextOffset, MaxBytes: 31})
	require.NoError(t, err)
	assert.Equal(t, first.NextOffset, second.Offset)
	_, err = store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: artifact.Ref, AttemptID: attemptID("new-after-uninstall"), Offset: 0, MaxBytes: 31})
	assert.ErrorIs(t, err, ErrCapabilityNotFound)
}

func TestReadContinuationSurvivesReinstallWhileOldStableRefStales(t *testing.T) {
	fixture := newReadableFixture(t, strings.Repeat("old-body-", 50))
	store, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{8}, 4096)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	oldArtifact := fixture.artifacts["skill"]
	initial := ReadRequest{CapabilityID: fixture.id, Ref: oldArtifact.Ref, AttemptID: attemptID("before-reinstall"), Offset: 0, MaxBytes: 23}
	first, err := store.Read(testReadScope, initial)
	require.NoError(t, err)

	fixture.write("2.0.0", strings.Repeat("new-body-", 50))
	replacement, err := fixture.loader.Load(fixture.id)
	require.NoError(t, err)
	require.NoError(t, fixture.registry.Reinstall(replacement))
	continued, err := store.Read(testReadScope, ReadRequest{Ref: first.ContinuationRef, Offset: first.NextOffset, MaxBytes: 23})
	require.NoError(t, err)
	assert.Equal(t, first.NextOffset, continued.Offset)
	_, err = store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: oldArtifact.Ref, AttemptID: attemptID("old-stable"), Offset: 0, MaxBytes: 23})
	assert.ErrorIs(t, err, ErrArtifactReferenceStale)
}

func TestReadIndependentAttemptsAndExactChunkReplays(t *testing.T) {
	fixture := newReadableFixture(t, strings.Repeat("replay-body-", 40))
	store, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte("independent-handles"), 1024)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	artifact := fixture.artifacts["skill"]
	firstRequest := ReadRequest{CapabilityID: fixture.id, Ref: artifact.Ref, AttemptID: attemptID("first"), Offset: 0, MaxBytes: 19}
	first, err := store.Read(testReadScope, firstRequest)
	require.NoError(t, err)
	firstRetry, err := store.Read(testReadScope, firstRequest)
	require.NoError(t, err)
	assert.Equal(t, first, firstRetry)
	firstEncoded, err := json.Marshal(first)
	require.NoError(t, err)
	retained, ok := store.encodedReplay(first.ContinuationRef)
	require.True(t, ok)
	assert.Equal(t, firstEncoded, retained)

	other, err := store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: artifact.Ref, AttemptID: attemptID("second"), Offset: 0, MaxBytes: 19})
	require.NoError(t, err)
	assert.NotEqual(t, first.ContinuationRef, other.ContinuationRef)
	otherScope, err := store.Read("another-broker-instance", firstRequest)
	require.NoError(t, err)
	assert.NotEqual(t, first.ContinuationRef, otherScope.ContinuationRef)
	_, err = store.Read("another-broker-instance", ReadRequest{Ref: first.ContinuationRef, Offset: first.NextOffset, MaxBytes: 19})
	assert.ErrorIs(t, err, ErrContinuationStale)

	request := ReadRequest{Ref: first.ContinuationRef, Offset: first.NextOffset, MaxBytes: 19}
	intermediate, err := store.Read(testReadScope, request)
	require.NoError(t, err)
	intermediateRetry, err := store.Read(testReadScope, request)
	require.NoError(t, err)
	assert.Equal(t, intermediate, intermediateRetry)
	_, err = store.Read(testReadScope, firstRequest)
	assert.ErrorIs(t, err, ErrContinuationConflict)
	for !intermediate.Complete {
		request = ReadRequest{Ref: first.ContinuationRef, Offset: intermediate.NextOffset, MaxBytes: 19}
		intermediate, err = store.Read(testReadScope, request)
		require.NoError(t, err)
	}
	finalRetry, err := store.Read(testReadScope, request)
	require.NoError(t, err)
	assert.Equal(t, intermediate, finalRetry)
	finalEncoded, err := json.Marshal(intermediate)
	require.NoError(t, err)
	retained, ok = store.encodedReplay(first.ContinuationRef)
	require.True(t, ok)
	assert.Equal(t, finalEncoded, retained)
}

func TestReadConcurrentAdvanceCommitsOneBoundary(t *testing.T) {
	fixture := newReadableFixture(t, strings.Repeat("concurrent-body-", 30))
	store, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte("concurrent-random"), 1024)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	artifact := fixture.artifacts["skill"]
	first, err := store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: artifact.Ref, AttemptID: attemptID("concurrent"), Offset: 0, MaxBytes: 7})
	require.NoError(t, err)

	type outcome struct {
		response ReadResponse
		err      error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for _, maxBytes := range []int{11, 13} {
		go func(limit int) {
			<-start
			response, readErr := store.Read(testReadScope, ReadRequest{Ref: first.ContinuationRef, Offset: first.NextOffset, MaxBytes: limit})
			results <- outcome{response: response, err: readErr}
		}(maxBytes)
	}
	close(start)
	firstOutcome := <-results
	secondOutcome := <-results
	outcomes := []outcome{firstOutcome, secondOutcome}
	var winner ReadResponse
	conflicts := 0
	for _, result := range outcomes {
		if result.err == nil {
			winner = result.response
		} else if errors.Is(result.err, ErrContinuationConflict) {
			conflicts++
		}
	}
	assert.NotEmpty(t, winner.DataBase64)
	assert.Equal(t, 1, conflicts)
	replay, err := store.Read(testReadScope, ReadRequest{Ref: first.ContinuationRef, Offset: winner.Offset, MaxBytes: int(winner.NextOffset - winner.Offset)})
	require.NoError(t, err)
	assert.Equal(t, winner, replay)
}

func TestReadConcurrentIdenticalInitialAttemptsShareOneLease(t *testing.T) {
	fixture := newReadableFixture(t, strings.Repeat("initial-race-", 30))
	store, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte("one-shared-handle"), 4096)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	artifact := fixture.artifacts["skill"]
	request := ReadRequest{CapabilityID: fixture.id, Ref: artifact.Ref, AttemptID: attemptID("same-concurrent-attempt"), Offset: 0, MaxBytes: 7}
	start := make(chan struct{})
	responses := make(chan ReadResponse, 32)
	errorsSeen := make(chan error, 32)
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			response, readErr := store.Read(testReadScope, request)
			responses <- response
			errorsSeen <- readErr
		}()
	}
	close(start)
	wait.Wait()
	close(responses)
	close(errorsSeen)
	for readErr := range errorsSeen {
		require.NoError(t, readErr)
	}
	var expected *ReadResponse
	for response := range responses {
		if expected == nil {
			copy := response
			expected = &copy
			continue
		}
		assert.Equal(t, *expected, response)
	}
	assert.Equal(t, ReadLeaseStats{Leases: 1, Attempts: 1, PinnedBytes: artifact.Bytes, ReplayBytes: ReadReplayReservation}, store.Stats())
}

func TestReadAdvanceSurvivesConcurrentRegistryMutation(t *testing.T) {
	for _, mutation := range []string{"reinstall", "uninstall"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newReadableFixture(t, strings.Repeat("mutation-race-", 40))
			store, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte("mutation-handle"), 4096)))
			require.NoError(t, err)
			t.Cleanup(store.Close)
			artifact := fixture.artifacts["skill"]
			first, err := store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: artifact.Ref, AttemptID: attemptID(mutation), Offset: 0, MaxBytes: 7})
			require.NoError(t, err)

			var replacement *Record
			if mutation == "reinstall" {
				fixture.write("2.0.0", strings.Repeat("replacement-", 40))
				replacement, err = fixture.loader.Load(fixture.id)
				require.NoError(t, err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				<-start
				if mutation == "reinstall" {
					results <- fixture.registry.Reinstall(replacement)
				} else {
					results <- fixture.registry.Uninstall(fixture.id)
				}
			}()
			go func() {
				<-start
				_, readErr := store.Read(testReadScope, ReadRequest{Ref: first.ContinuationRef, Offset: first.NextOffset, MaxBytes: 7})
				results <- readErr
			}()
			close(start)
			require.NoError(t, <-results)
			require.NoError(t, <-results)
		})
	}
}

func TestReadIdleAbsoluteAndFinalExpiry(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		fixture := newReadableFixture(t, strings.Repeat("idle-body-", 20))
		clock := newFakeClock(time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC))
		limits := DefaultReadLeaseLimits()
		limits.MaxCount = 1
		store, err := newReadLeaseStore(fixture.registry, limits, clock.Now, bytes.NewReader(bytes.Repeat([]byte{1}, 1024)))
		require.NoError(t, err)
		first, err := store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attemptID("idle"), Offset: 0, MaxBytes: 1})
		require.NoError(t, err)
		clock.Advance(5 * time.Minute)
		_, err = store.Read(testReadScope, ReadRequest{Ref: first.ContinuationRef, Offset: first.NextOffset, MaxBytes: 1})
		assert.ErrorIs(t, err, ErrContinuationStale)
		assert.Equal(t, ReadLeaseStats{}, store.Stats())
		_, err = store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["context"].Ref, AttemptID: attemptID("idle-capacity-reuse"), Offset: 0, MaxBytes: 1})
		require.NoError(t, err)
	})

	t.Run("absolute", func(t *testing.T) {
		fixture := newReadableFixture(t, strings.Repeat("absolute-body-", 80))
		clock := newFakeClock(time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC))
		limits := DefaultReadLeaseLimits()
		limits.Idle = 5 * time.Minute
		limits.Absolute = 20 * time.Minute
		store, err := newReadLeaseStore(fixture.registry, limits, clock.Now, bytes.NewReader(bytes.Repeat([]byte{2}, 1024)))
		require.NoError(t, err)
		response, err := store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attemptID("absolute"), Offset: 0, MaxBytes: 1})
		require.NoError(t, err)
		for range 4 {
			clock.Advance(4 * time.Minute)
			response, err = store.Read(testReadScope, ReadRequest{Ref: response.ContinuationRef, Offset: response.NextOffset, MaxBytes: 1})
			require.NoError(t, err)
		}
		clock.Advance(4 * time.Minute)
		_, err = store.Read(testReadScope, ReadRequest{Ref: response.ContinuationRef, Offset: response.NextOffset, MaxBytes: 1})
		assert.ErrorIs(t, err, ErrContinuationStale)
	})

	t.Run("final", func(t *testing.T) {
		fixture := newReadableFixture(t, "short")
		clock := newFakeClock(time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC))
		limits := DefaultReadLeaseLimits()
		limits.MaxCount = 1
		store, err := newReadLeaseStore(fixture.registry, limits, clock.Now, bytes.NewReader(bytes.Repeat([]byte{3}, 1024)))
		require.NoError(t, err)
		request := ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["context"].Ref, AttemptID: attemptID("final"), Offset: 0, MaxBytes: ReadChunkMax}
		response, err := store.Read(testReadScope, request)
		require.NoError(t, err)
		require.True(t, response.Complete)
		clock.Advance(5 * time.Minute)
		assert.Equal(t, ReadLeaseStats{}, store.Stats())
		newResponse, err := store.Read(testReadScope, request)
		require.NoError(t, err)
		assert.NotEqual(t, response.ExpiresAt, newResponse.ExpiresAt)
		assert.Equal(t, 1, store.Stats().Leases)
	})
}

func TestReadOneChunkReplaySurvivesUninstall(t *testing.T) {
	fixture := newReadableFixture(t, "one-chunk")
	store, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{9}, 1024)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	request := ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["context"].Ref, AttemptID: attemptID("single-uninstall"), Offset: 0, MaxBytes: ReadChunkMax}
	first, err := store.Read(testReadScope, request)
	require.NoError(t, err)
	require.True(t, first.Complete)
	require.NoError(t, fixture.registry.Uninstall(fixture.id))
	retry, err := store.Read(testReadScope, request)
	require.NoError(t, err)
	assert.Equal(t, first, retry)
}

func TestReadCapacityAllowedKindsBoundsRNGAndShutdown(t *testing.T) {
	fixture := newReadableFixture(t, strings.Repeat("quota-body-", 20))
	limits := DefaultReadLeaseLimits()
	limits.MaxCount = 1
	store, err := newReadLeaseStore(fixture.registry, limits, time.Now, bytes.NewReader(bytes.Repeat([]byte{4}, 1024)))
	require.NoError(t, err)
	_, err = store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attemptID("quota-one"), Offset: 0, MaxBytes: 1})
	require.NoError(t, err)
	_, err = store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["context"].Ref, AttemptID: attemptID("quota-two"), Offset: 0, MaxBytes: 1})
	assert.ErrorIs(t, err, ErrServiceQuota)
	assert.Equal(t, ReadLeaseStats{Leases: 1, Attempts: 1, PinnedBytes: fixture.artifacts["skill"].Bytes, ReplayBytes: ReadReplayReservation}, store.Stats())
	store.Close()
	assert.Equal(t, ReadLeaseStats{}, store.Stats())
	_, err = store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attemptID("closed"), Offset: 0, MaxBytes: 1})
	assert.ErrorIs(t, err, ErrServiceClosed)

	manifest := fixture.manifestArtifact(t)
	fresh, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{5}, 1024)))
	require.NoError(t, err)
	t.Cleanup(fresh.Close)
	_, err = fresh.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: manifest.Ref, AttemptID: attemptID("manifest"), Offset: 0, MaxBytes: 10})
	assert.ErrorIs(t, err, ErrArtifactReferenceStale)

	for _, request := range []ReadRequest{
		{CapabilityID: fixture.id, Ref: strings.Repeat("r", OpaqueReferenceMaxBytes+1), AttemptID: attemptID("long-ref"), Offset: 0, MaxBytes: 1},
		{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: "too-short", Offset: 0, MaxBytes: 1},
		{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attemptID("overflow"), Offset: int64(^uint64(0) >> 1), MaxBytes: 2},
	} {
		_, err = fresh.Read(testReadScope, request)
		assert.ErrorIs(t, err, ErrReadRequestInvalid)
	}

	failing, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(nil))
	require.NoError(t, err)
	_, err = failing.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attemptID("rng"), Offset: 0, MaxBytes: 1})
	assert.ErrorIs(t, err, ErrHandleGeneration)

	colliding, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, repeatingReader{'z'})
	require.NoError(t, err)
	t.Cleanup(colliding.Close)
	_, err = colliding.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attemptID("collision-one"), Offset: 0, MaxBytes: 1})
	require.NoError(t, err)
	_, err = colliding.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attemptID("collision-two"), Offset: 0, MaxBytes: 1})
	assert.ErrorIs(t, err, ErrHandleGeneration)

	pinnedLimits := DefaultReadLeaseLimits()
	pinnedLimits.MaxPinnedBytes = fixture.artifacts["skill"].Bytes - 1
	pinned, err := newReadLeaseStore(fixture.registry, pinnedLimits, time.Now, bytes.NewReader(bytes.Repeat([]byte{7}, 1024)))
	require.NoError(t, err)
	t.Cleanup(pinned.Close)
	_, err = pinned.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attemptID("pinned-quota"), Offset: 0, MaxBytes: 1})
	assert.ErrorIs(t, err, ErrServiceQuota)

	replayLimits := DefaultReadLeaseLimits()
	replayLimits.MaxReplayBytes = ReadReplayReservation - 1
	replay, err := newReadLeaseStore(fixture.registry, replayLimits, time.Now, bytes.NewReader(bytes.Repeat([]byte{8}, 1024)))
	require.NoError(t, err)
	t.Cleanup(replay.Close)
	_, err = replay.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attemptID("replay-quota"), Offset: 0, MaxBytes: 1})
	assert.ErrorIs(t, err, ErrServiceQuota)
}

func TestReadErrorsDoNotExposeAttemptOrContinuationValues(t *testing.T) {
	previousOutput := log.Writer()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousOutput) })
	fixture := newReadableFixture(t, strings.Repeat("secret-body-", 20))
	store, err := newReadLeaseStore(fixture.registry, DefaultReadLeaseLimits(), time.Now, bytes.NewReader(bytes.Repeat([]byte{6}, 1024)))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	attempt := attemptID("secret-attempt")
	first, err := store.Read(testReadScope, ReadRequest{CapabilityID: fixture.id, Ref: fixture.artifacts["skill"].Ref, AttemptID: attempt, Offset: 0, MaxBytes: 1})
	require.NoError(t, err)
	_, err = store.Read(testReadScope, ReadRequest{Ref: first.ContinuationRef, Offset: first.NextOffset + 1, MaxBytes: 1})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), attempt)
	assert.NotContains(t, err.Error(), first.ContinuationRef)
	assert.NotContains(t, logs.String(), attempt)
	assert.NotContains(t, logs.String(), first.ContinuationRef)
}

func attemptID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return "read:" + base64.RawURLEncoding.EncodeToString(digest[:16])
}

type repeatingReader struct{ value byte }

func (r repeatingReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = r.value
	}
	return len(buffer), nil
}

type readableFixture struct {
	t         *testing.T
	root      string
	id        string
	loader    *Loader
	registry  *Registry
	snapshot  *SnapshotStore
	artifacts map[string]Artifact
}

func newReadableFixture(t *testing.T, body string) *readableFixture {
	t.Helper()
	root := t.TempDir()
	store := newTestSnapshotStore(t)
	loader, err := NewLoader(root, store)
	require.NoError(t, err)
	fixture := &readableFixture{t: t, root: root, id: "docs.guide", loader: loader}
	fixture.snapshot = store
	fixture.write("1.0.0", body)
	record, err := loader.Load(fixture.id)
	require.NoError(t, err)
	registry, err := NewRegistry(record)
	require.NoError(t, err)
	t.Cleanup(registry.Close)
	fixture.registry = registry
	fixture.artifacts = fixture.currentArtifacts(t)
	return fixture
}

func (f *readableFixture) write(version, body string) {
	f.t.Helper()
	packageDir := filepath.Join(f.root, f.id)
	require.NoError(f.t, os.MkdirAll(filepath.Join(packageDir, "skills", "guide", "references"), 0o755))
	require.NoError(f.t, os.MkdirAll(filepath.Join(packageDir, "context"), 0o755))
	manifest := fmt.Sprintf(`apiVersion: tappet.gearboxlogic.dev/v1alpha1
kind: Capability
metadata:
  id: docs.guide
  name: Documentation guide
  version: %s
  description: Read documentation safely.
spec:
  parent: docs
  skills:
    - path: skills/guide
      resources:
        - id: manual
          kind: reference
          path: references/manual.txt
  operations:
    - id: inspect
      description: Inspect documentation.
      provider: provider
      target: inspect
  context:
    - id: context
      path: context/info.txt
  providers:
    - id: provider
      type: mcp
      serverRef: docs-provider
`, version)
	skill := "---\nname: guide\ndescription: Guide instructions.\n---\n" + body
	require.NoError(f.t, os.WriteFile(filepath.Join(packageDir, "tappet.yaml"), []byte(manifest), 0o644))
	require.NoError(f.t, os.WriteFile(filepath.Join(packageDir, "skills", "guide", "SKILL.md"), []byte(skill), 0o644))
	require.NoError(f.t, os.WriteFile(filepath.Join(packageDir, "skills", "guide", "references", "manual.txt"), []byte("manual:"+body), 0o644))
	require.NoError(f.t, os.WriteFile(filepath.Join(packageDir, "context", "info.txt"), []byte("context:"+body), 0o644))
}

func (f *readableFixture) currentArtifacts(t *testing.T) map[string]Artifact {
	t.Helper()
	lease, err := f.registry.Lookup(f.id)
	require.NoError(t, err)
	defer lease.Release()
	record := lease.Record()
	return map[string]Artifact{
		"skill":           record.Skills()[0].Artifact,
		"skill_reference": record.Skills()[0].Resources[0],
		"context":         record.Context()[0],
	}
}

func (f *readableFixture) manifestArtifact(t *testing.T) Artifact {
	t.Helper()
	lease, err := f.registry.Lookup(f.id)
	require.NoError(t, err)
	defer lease.Release()
	for _, artifact := range lease.Record().artifacts {
		if artifact.Kind == ArtifactManifest {
			return artifact
		}
	}
	t.Fatal("manifest artifact not found")
	return Artifact{}
}
