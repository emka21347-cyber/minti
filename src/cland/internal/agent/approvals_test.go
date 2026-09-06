package agent

import (
	"context"
	"testing"
	"time"
)

func TestChannelApproverResolveApprove(t *testing.T) {
	appr := NewChannelApprover(2 * time.Second)
	done := make(chan struct {
		d   Decision
		err error
	}, 1)
	go func() {
		d, err := appr.Await(context.Background(), ApprovalRequest{CallID: "c1"})
		done <- struct {
			d   Decision
			err error
		}{d, err}
	}()

	// Wait for the call to register, then resolve it.
	waitForPending(t, appr, "c1")
	if err := appr.Resolve("c1", DecisionApprove); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := <-done
	if got.err != nil || got.d != DecisionApprove {
		t.Errorf("Await = %v,%v; want approve,nil", got.d, got.err)
	}
}

func TestChannelApproverResolveDeny(t *testing.T) {
	appr := NewChannelApprover(2 * time.Second)
	res := make(chan Decision, 1)
	go func() {
		d, _ := appr.Await(context.Background(), ApprovalRequest{CallID: "c1"})
		res <- d
	}()
	waitForPending(t, appr, "c1")
	_ = appr.Resolve("c1", DecisionDeny)
	if d := <-res; d != DecisionDeny {
		t.Errorf("decision = %v, want deny", d)
	}
}

func TestChannelApproverTimeout(t *testing.T) {
	appr := NewChannelApprover(50 * time.Millisecond)
	d, err := appr.Await(context.Background(), ApprovalRequest{CallID: "c1"})
	if err != ErrApprovalTimeout {
		t.Errorf("err = %v, want ErrApprovalTimeout", err)
	}
	if d != DecisionDeny {
		t.Errorf("timeout decision = %v, want deny (fail-closed)", d)
	}
}

func TestChannelApproverContextCancel(t *testing.T) {
	appr := NewChannelApprover(5 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		_, err := appr.Await(ctx, ApprovalRequest{CallID: "c1"})
		res <- err
	}()
	waitForPending(t, appr, "c1")
	cancel()
	if err := <-res; err == nil {
		t.Error("expected ctx cancellation error")
	}
}

func TestChannelApproverResolveUnknown(t *testing.T) {
	appr := NewChannelApprover(time.Second)
	if err := appr.Resolve("nope", DecisionApprove); err != ErrNoPendingApproval {
		t.Errorf("Resolve(unknown) = %v, want ErrNoPendingApproval", err)
	}
}

func waitForPending(t *testing.T, a *ChannelApprover, callID string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		for _, id := range a.Pending() {
			if id == callID {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("call %q never became pending", callID)
}
