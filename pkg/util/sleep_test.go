package util

import (
	"context"
	"testing"
	"time"
)

func TestSleepReturnsFalseOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Sleep(ctx, time.Hour) {
		t.Error("Sleep should return false when ctx is already canceled")
	}
}

func TestSleepReturnsTrueOnElapse(t *testing.T) {
	if !Sleep(context.Background(), time.Millisecond) {
		t.Error("Sleep should return true after the duration elapses")
	}
}
