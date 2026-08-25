package capability

import (
	"errors"
	"time"
)

const (
	DescribeLimitMax        = 100
	DescribeIncludeMaxBytes = 64
	ReadChunkMax            = 64 << 10
	BrokerResponseMaxBytes  = 1 << 20
	OpaqueReferenceMaxBytes = 2_048
	ReadAttemptMaxBytes     = 128
	ReadReplayReservation   = 128 << 10

	ProjectionCountMax     = 128
	ProjectionBytesMax     = 64 << 20
	ReadLeaseCountMax      = 256
	PinnedArtifactBytesMax = 512 << 20
	ReplayBytesMax         = 32 << 20
)

var (
	ErrCatalogCursorInvalid   = errors.New("catalog cursor is invalid")
	ErrCatalogCursorStale     = errors.New("catalog cursor is stale")
	ErrDescribeRequestInvalid = errors.New("describe request is invalid")
	ErrDescribeCursorInvalid  = errors.New("describe cursor is invalid")
	ErrDescribeCursorStale    = errors.New("describe cursor is stale")
	ErrServiceQuota           = errors.New("capability service quota exceeded")
	ErrServiceClosed          = errors.New("capability service is closed")
	ErrReadRequestInvalid     = errors.New("read request is invalid")
	ErrArtifactReferenceStale = errors.New("artifact reference is stale")
	ErrContinuationInvalid    = errors.New("continuation reference is invalid")
	ErrContinuationStale      = errors.New("continuation reference is stale")
	ErrContinuationConflict   = errors.New("continuation request conflicts with the committed chunk")
	ErrHandleGeneration       = errors.New("opaque handle generation failed")
)

type ProjectionLimits struct {
	MaxCount int
	MaxBytes int64
	Idle     time.Duration
	Absolute time.Duration
}

func DefaultProjectionLimits() ProjectionLimits {
	return ProjectionLimits{MaxCount: ProjectionCountMax, MaxBytes: ProjectionBytesMax, Idle: 5 * time.Minute, Absolute: time.Hour}
}

type ReadLeaseLimits struct {
	MaxCount       int
	MaxPinnedBytes int64
	MaxReplayBytes int64
	Idle           time.Duration
	Absolute       time.Duration
	FinalReplay    time.Duration
}

func DefaultReadLeaseLimits() ReadLeaseLimits {
	return ReadLeaseLimits{
		MaxCount:       ReadLeaseCountMax,
		MaxPinnedBytes: PinnedArtifactBytesMax,
		MaxReplayBytes: ReplayBytesMax,
		Idle:           5 * time.Minute,
		Absolute:       time.Hour,
		FinalReplay:    5 * time.Minute,
	}
}

func validateProjectionLimits(limits ProjectionLimits) error {
	if limits.MaxCount <= 0 || limits.MaxCount > ProjectionCountMax || limits.MaxBytes <= 0 || limits.MaxBytes > ProjectionBytesMax {
		return errors.New("projection count or byte limit is outside the supported range")
	}
	if limits.Idle <= 0 || limits.Absolute < limits.Idle || limits.Absolute > time.Hour {
		return errors.New("projection lifetimes are invalid")
	}
	return nil
}

func validateReadLeaseLimits(limits ReadLeaseLimits) error {
	if limits.MaxCount <= 0 || limits.MaxCount > ReadLeaseCountMax || limits.MaxPinnedBytes <= 0 || limits.MaxPinnedBytes > PinnedArtifactBytesMax || limits.MaxReplayBytes <= 0 || limits.MaxReplayBytes > ReplayBytesMax {
		return errors.New("read lease count or byte limit is outside the supported range")
	}
	if limits.Idle <= 0 || limits.Absolute < limits.Idle || limits.Absolute > time.Hour || limits.FinalReplay <= 0 || limits.FinalReplay > 5*time.Minute {
		return errors.New("read lease lifetimes are invalid")
	}
	return nil
}

func minTime(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}
