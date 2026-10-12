package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServiceValidates(t *testing.T) {
	cases := map[string]string{
		"telegram://123456:ABCdef@telegram?chats=@ops": "telegram",
		"generic+https://hooks.example.com/x":          "generic",
		"ntfy://ntfy.sh/topic":                         "ntfy",
	}
	for raw, want := range cases {
		got, err := Service(raw)
		if err != nil || got != want {
			t.Errorf("%s: %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "not a url", "bogus://x", strings.Repeat("a", 3000)} {
		if _, err := Service(raw); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
}

func TestValidateErrorIsRedacted(t *testing.T) {
	raw := "slack://hook:SECRETTOKEN123/B/X"
	_, err := Service(raw)
	if err == nil {
		t.Fatal("malformed slack URL accepted")
	}
	if strings.Contains(err.Error(), "SECRETTOKEN123") {
		t.Fatalf("secret leaked: %v", err)
	}
}

func TestRedactRemovesURLAndSecrets(t *testing.T) {
	raw := "smtp://mailer:hunter2hunter2@mail.example.com:587/?fromaddress=a@b.c&toaddresses=ops@example.com"
	msg := "dial " + raw + " failed for mailer:hunter2hunter2 and ops@example.com on port 587"
	got := Redact(msg, raw)
	for _, leak := range []string{raw, "hunter2hunter2", "ops@example.com"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaked in %q", leak, got)
		}
	}
	if !strings.Contains(got, "587") {
		t.Errorf("short parts should stay: %q", got)
	}
}

func TestSendPostsTitleAndBody(t *testing.T) {
	got := make(chan map[string]string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]string
		_ = json.Unmarshal(b, &m)
		got <- m
	}))
	defer srv.Close()
	raw := "generic://" + strings.TrimPrefix(srv.URL, "http://") + "/hook?template=json&disabletls=yes"
	if err := NewShoutrrr(5*time.Second, true).Send(context.Background(), raw, "T", "B"); err != nil {
		t.Fatal(err)
	}
	m := <-got
	if m["title"] != "T" || m["message"] != "B" {
		t.Fatalf("payload = %v", m)
	}
}

func TestSendTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	raw := "generic://" + strings.TrimPrefix(srv.URL, "http://") + "/hook?disabletls=yes"
	start := time.Now()
	err := NewShoutrrr(200*time.Millisecond, true).Send(context.Background(), raw, "T", "B")
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}

func TestSendStopsOnContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	raw := "generic://" + strings.TrimPrefix(srv.URL, "http://") + "/hook?disabletls=yes"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := NewShoutrrr(time.Minute, true).Send(ctx, raw, "T", "B"); err == nil {
		t.Fatal("no error after ctx ended")
	}
}

func TestSendErrorIsRedacted(t *testing.T) {
	raw := "generic://127.0.0.1:9/hook-SECRETPATH?disabletls=yes"
	err := NewShoutrrr(2*time.Second, true).Send(context.Background(), raw, "T", "B")
	if err == nil {
		t.Fatal("send to a closed port succeeded")
	}
	if strings.Contains(err.Error(), "SECRETPATH") {
		t.Fatalf("secret leaked: %v", err)
	}
}
