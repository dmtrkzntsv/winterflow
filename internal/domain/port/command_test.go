package port

import (
	"context"
	"testing"
	"time"
)

func TestCommandResultErr(t *testing.T) {
	if err := (CommandResult{Success: true, Error: "ignored"}).Err(); err != nil {
		t.Fatalf("success = %v", err)
	}
	if err := (CommandResult{Error: "boom"}).Err(); err == nil || err.Error() != "boom" {
		t.Fatalf("failure = %v", err)
	}
	if err := (CommandResult{}).Err(); err == nil || err.Error() != "the agent reported a failure" {
		t.Fatalf("failure without a message = %v", err)
	}
}

func TestNewResultChanKeepsOneAndNeverBlocks(t *testing.T) {
	ch, deliver := NewResultChan()
	deliver(CommandResult{Success: true})
	deliver(CommandResult{Error: "dropped"}) // full: must not block
	if res := <-ch; !res.Success {
		t.Fatalf("delivered = %+v", res)
	}
}

func TestAwaitResult(t *testing.T) {
	done, deliver := NewResultChan()
	deliver(CommandResult{Success: true})
	if res, ok := AwaitResult(context.Background(), done, time.Minute); !ok || !res.Success {
		t.Fatalf("delivered = %+v, %v", res, ok)
	}
	start := time.Now()
	if _, ok := AwaitResult(context.Background(), make(chan CommandResult), 20*time.Millisecond); ok || time.Since(start) > 5*time.Second {
		t.Fatal("the bound did not end the wait")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := AwaitResult(ctx, make(chan CommandResult), time.Minute); ok {
		t.Fatal("a gone client did not end the wait")
	}
}
