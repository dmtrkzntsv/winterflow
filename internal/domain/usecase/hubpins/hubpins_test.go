package hubpins

import (
	"context"
	"errors"
	"testing"
)

type src struct {
	fps []string
	err error
}

func (s src) Active(context.Context) ([]string, error) { return s.fps, s.err }

func TestActive(t *testing.T) {
	ctx := context.Background()
	if got, err := Active(ctx, src{fps: []string{"aa"}}); err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := Active(ctx, src{}); err != nil || got == nil {
		t.Fatalf("empty: %#v %v", got, err)
	}
	if _, err := Active(ctx, src{err: errors.New("down")}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error: %v", err)
	}
	if _, err := Active(ctx, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil: %v", err)
	}
}
