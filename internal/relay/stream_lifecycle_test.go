package relay

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/llm/httpclient"
)

type lifecycleTestStream struct {
	closed atomic.Int32
}

func (s *lifecycleTestStream) Next() bool                       { return false }
func (s *lifecycleTestStream) Current() *httpclient.StreamEvent { return nil }
func (s *lifecycleTestStream) Err() error                       { return nil }
func (s *lifecycleTestStream) Close() error {
	s.closed.Add(1)
	return nil
}

func TestUpstreamResponseCloseReleasesSlotOnce(t *testing.T) {
	resetChannelLimits()
	t.Cleanup(resetChannelLimits)

	ctx := context.Background()
	release, err := acquireChannelConcurrency(ctx, 901, 1)
	if err != nil {
		t.Fatalf("acquire first slot: %v", err)
	}
	stream := &lifecycleTestStream{}
	response := &upstreamResponse{events: stream, release: release}

	blockedCtx, cancelBlocked := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancelBlocked()
	if _, err := acquireChannelConcurrency(blockedCtx, 901, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire while stream active = %v, want deadline exceeded", err)
	}

	response.Close()
	response.Close()
	if got := stream.closed.Load(); got != 1 {
		t.Fatalf("stream close calls = %d, want 1", got)
	}

	releaseAgain, err := acquireChannelConcurrency(ctx, 901, 1)
	if err != nil {
		t.Fatalf("slot not released after stream close: %v", err)
	}
	releaseAgain()
}

func TestFinishRoundKeepsStopRequestEffectiveForCommittedStream(t *testing.T) {
	resetRequestsForTest()
	t.Cleanup(resetRequestsForTest)

	roundCtx, cancelRound := context.WithCancelCause(context.Background())
	lifecycle := newRoundLifecycle(cancelRound)
	stream := &lifecycleTestStream{}
	var releases atomic.Int32
	response := &upstreamResponse{events: stream, release: func() { releases.Add(1) }}
	lifecycle.Attach(response)

	request := newRequestState("group", `{}`, "127.0.0.1", "...ABCD", "test")
	request.startRound(lifecycle.Stop, RoundTarget{MemberID: 1, ChannelID: 2})
	request.finishRound(AttemptSuccess, "", "")
	request.markCommitted()

	request.StopRequest()
	select {
	case <-roundCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("StopRequest did not cancel committed stream context")
	}
	if !request.IsStopRequested() {
		t.Fatal("stopRequested flag not set")
	}
	if got := stream.closed.Load(); got != 1 {
		t.Fatalf("stream close calls = %d, want 1", got)
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("release calls = %d, want 1", got)
	}

	request.releaseRoundLifecycle()
	if got := releases.Load(); got != 1 {
		t.Fatalf("release calls after lifecycle cleanup = %d, want 1", got)
	}
}
