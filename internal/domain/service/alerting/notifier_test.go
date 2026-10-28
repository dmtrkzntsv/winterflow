package alerting

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"winterflow/internal/domain/alert"
)

// fakeSender records sends; "fail://" URLs fail and "hang://" URLs block
// until their context ends.
type fakeSender struct {
	mu   sync.Mutex
	sent []string
}

func (s *fakeSender) Send(ctx context.Context, rawURL, title, _ string) error {
	s.mu.Lock()
	s.sent = append(s.sent, rawURL+"|"+title)
	s.mu.Unlock()
	switch {
	case strings.HasPrefix(rawURL, "fail://"):
		return errors.New("service said no")
	case strings.HasPrefix(rawURL, "hang://"):
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

// plainBox "decrypts" by dropping a "sealed:" prefix; anything else is
// corrupt.
type plainBox struct{}

func (plainBox) Open(sealed string) (string, error) {
	if url, ok := strings.CutPrefix(sealed, "sealed:"); ok {
		return url, nil
	}
	return "", errors.New("cannot decrypt the stored URL (was ENCRYPTION_KEY changed?); re-enter it")
}

func channel(id string, def, enabled bool, url string) alert.Channel {
	return alert.Channel{ID: id, OrgID: org, Name: "ch " + id, IsDefault: def, Enabled: enabled, URLEncrypted: url}
}

func TestDeliverSendsToRoutedChannels(t *testing.T) {
	store := newStore()
	store.channels = []alert.Channel{
		channel("a", true, true, "sealed:ok://a"),
		channel("b", true, false, "sealed:ok://b"),
		channel("c", false, true, "sealed:fail://c"),
		{ID: "x", OrgID: "other-org", IsDefault: true, Enabled: true, URLEncrypted: "sealed:ok://x"},
	}
	sender := &fakeSender{}
	n := NewNotifier(store, sender, plainBox{}, testLog)
	ctx := context.Background()

	n.deliver(ctx, Job{EventID: "e1", OrgID: org, Title: "T"})
	n.deliver(ctx, Job{EventID: "e2", OrgID: org, ChannelIDs: []string{"c", "gone"}, Title: "T"})
	n.deliver(ctx, Job{EventID: "e3", OrgID: org, ChannelIDs: []string{}, Title: "T"})

	if want := []string{"ok://a|T", "fail://c|T"}; !reflect.DeepEqual(sender.sent, want) {
		t.Fatalf("sent = %v", sender.sent)
	}
	if d := store.delivered["e1"]; len(d) != 1 || !d[0].OK || d[0].ChannelName != "ch a" {
		t.Fatalf("e1 = %+v", d)
	}
	if d := store.delivered["e2"]; len(d) != 1 || d[0].OK || d[0].Error != "service said no" {
		t.Fatalf("e2 = %+v", d)
	}
	if d, ok := store.delivered["e3"]; !ok || d == nil || len(d) != 0 {
		t.Fatalf("e3 = %#v (want an empty, non-nil list)", d)
	}
	if want := []string{"a:", "c:service said no"}; !reflect.DeepEqual(store.records, want) {
		t.Fatalf("channel records = %v", store.records)
	}
}

func TestDeliverRecordsDecryptFailure(t *testing.T) {
	store := newStore()
	store.channels = []alert.Channel{channel("a", true, true, "garbage")}
	sender := &fakeSender{}
	NewNotifier(store, sender, plainBox{}, testLog).deliver(context.Background(), Job{EventID: "e1", OrgID: org})
	if len(sender.sent) != 0 {
		t.Fatalf("sent with an undecryptable URL: %v", sender.sent)
	}
	if d := store.delivered["e1"]; len(d) != 1 || !strings.Contains(d[0].Error, "ENCRYPTION_KEY") {
		t.Fatalf("deliveries = %+v", d)
	}
}

func TestDeliverWithoutEncryptionKey(t *testing.T) {
	store := newStore()
	store.channels = []alert.Channel{channel("a", true, true, "sealed:ok://a")}
	sender := &fakeSender{}
	NewNotifier(store, sender, nil, testLog).deliver(context.Background(), Job{EventID: "e1", OrgID: org})
	if len(sender.sent) != 0 || store.delivered["e1"][0].Error != encryptionUnavailable {
		t.Fatalf("sent=%v deliveries=%+v", sender.sent, store.delivered["e1"])
	}
}

func TestSendTimesOut(t *testing.T) {
	store := newStore()
	n := NewNotifier(store, &fakeSender{}, plainBox{}, testLog)
	n.timeout = 50 * time.Millisecond
	start := time.Now()
	got := n.SendTo(context.Background(), channel("h", true, true, "sealed:hang://h"), "T", "B")
	if got == "" || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%q after %s", got, time.Since(start))
	}
	if got := n.SendURL(context.Background(), "hang://draft", "T", "B"); got == "" {
		t.Fatal("draft send did not time out")
	}
}

func TestEnqueueDropsWhenFull(t *testing.T) {
	n := NewNotifier(newStore(), &fakeSender{}, plainBox{}, testLog)
	for i := 0; i < QueueSize; i++ {
		if !n.Enqueue(Job{EventID: "e"}) {
			t.Fatalf("job %d dropped early", i)
		}
	}
	if n.Enqueue(Job{EventID: "overflow"}) {
		t.Fatal("a full queue accepted a job")
	}
}

func TestRunDrainsTheQueue(t *testing.T) {
	store := newStore()
	store.channels = []alert.Channel{channel("a", true, true, "sealed:ok://a")}
	n := NewNotifier(store, &fakeSender{}, plainBox{}, testLog)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()
	n.Enqueue(Job{EventID: "e1", OrgID: org})
	deadline := time.Now().Add(5 * time.Second)
	for {
		store.mu.Lock()
		_, ok := store.delivered["e1"]
		store.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job not delivered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

// A multibyte rune straddling the cut must not be split: Postgres rejects
// invalid UTF-8 and the delivery would not be recorded.
func TestErrorTextCutsAtRuneBoundary(t *testing.T) {
	msg := strings.Repeat("a", maxErrorLen-1) + "é" + strings.Repeat("b", 50)
	got := errorText(errors.New(msg))
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got[len(got)-8:])
	}
	if len(got) > maxErrorLen+len("…") {
		t.Fatalf("len %d exceeds the bound", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("no ellipsis: %q", got[len(got)-8:])
	}
	if got := errorText(errors.New("bad \xff byte")); !utf8.ValidString(got) {
		t.Fatalf("invalid input passed through: %q", got)
	}
}
