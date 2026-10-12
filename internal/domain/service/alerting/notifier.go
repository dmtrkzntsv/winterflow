package alerting

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"winterflow/internal/domain/alert"
	"winterflow/pkg/logger"
)

const (
	// QueueSize bounds the notifications waiting for the sender; a full
	// queue drops (and logs) new ones instead of blocking evaluation.
	QueueSize = 256
	// SendTimeout bounds one channel send.
	SendTimeout = 10 * time.Second
	// maxErrorLen bounds a stored delivery error.
	maxErrorLen = 500
	// encryptionUnavailable is the delivery error without JWT_SECRET.
	encryptionUnavailable = "channel encryption is unavailable: set JWT_SECRET"
)

// Sender delivers one notification to a channel URL (notify.Shoutrrr).
// Its errors must already be free of the URL's secrets.
type Sender interface {
	Send(ctx context.Context, rawURL, title, body string) error
}

// Opener decrypts a stored channel URL (secretbox.Box).
type Opener interface {
	Open(sealed string) (string, error)
}

// ChannelStore is the slice of the alert repository the notifier needs.
type ChannelStore interface {
	ListChannels(ctx context.Context, orgID string) ([]alert.Channel, error)
	RecordDelivery(ctx context.Context, channelID string, at time.Time, sendErr string) error
	SetDeliveries(ctx context.Context, eventID string, ds []alert.Delivery) error
}

// Notifier delivers queued notifications one at a time from a single
// goroutine (Run), so a slow channel delays the queue instead of piling up
// goroutines. Every send has SendTimeout; there are no retries.
type Notifier struct {
	store   ChannelStore
	sender  Sender
	box     Opener
	log     *logger.Logger
	queue   chan Job
	timeout time.Duration
	now     func() time.Time
}

// NewNotifier builds a notifier. A nil box (no JWT_SECRET) records every
// delivery as failed instead of sending.
func NewNotifier(store ChannelStore, sender Sender, box Opener, log *logger.Logger) *Notifier {
	return &Notifier{
		store: store, sender: sender, box: box, log: log,
		queue: make(chan Job, QueueSize), timeout: SendTimeout, now: time.Now,
	}
}

// Enqueue queues a job without blocking; it reports false (and logs) when
// the queue is full.
func (n *Notifier) Enqueue(j Job) bool {
	select {
	case n.queue <- j:
		return true
	default:
		n.log.Warn("alerts: notification queue full, dropping", "event_id", j.EventID)
		return false
	}
}

// Run delivers queued jobs until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-n.queue:
			n.deliver(ctx, j)
		}
	}
}

// deliver sends j to its routed channels and records the outcomes on the
// event and on each channel.
func (n *Notifier) deliver(ctx context.Context, j Job) {
	lctx, cancel := context.WithTimeout(ctx, storeTimeout)
	channels, err := n.store.ListChannels(lctx, j.OrgID)
	cancel()
	if err != nil {
		n.log.Warn("alerts: load channels failed", "org_id", j.OrgID, "error", err)
		return
	}
	ds := []alert.Delivery{}
	for _, ch := range alert.RouteChannels(channels, j.ChannelIDs) {
		errText := n.SendTo(ctx, ch, j.Title, j.Body)
		ds = append(ds, alert.Delivery{ChannelID: ch.ID, ChannelName: ch.Name, OK: errText == "", Error: errText})
	}
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := n.store.SetDeliveries(sctx, j.EventID, ds); err != nil {
		n.log.Warn("alerts: record deliveries failed", "event_id", j.EventID, "error", err)
	}
}

// SendTo decrypts the channel's URL, sends, and records the outcome on the
// channel. It returns the error text ("" on success); the text never holds
// the URL.
func (n *Notifier) SendTo(ctx context.Context, ch alert.Channel, title, body string) string {
	errText := n.send(ctx, ch, title, body)
	rctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := n.store.RecordDelivery(rctx, ch.ID, n.now(), errText); err != nil {
		n.log.Warn("alerts: record channel delivery failed", "channel_id", ch.ID, "error", err)
	}
	return errText
}

// SendURL sends to a draft URL (the Test button before saving); nothing
// is recorded.
func (n *Notifier) SendURL(ctx context.Context, rawURL, title, body string) string {
	sctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	return errorText(n.sender.Send(sctx, rawURL, title, body))
}

func (n *Notifier) send(ctx context.Context, ch alert.Channel, title, body string) string {
	if n.box == nil {
		return encryptionUnavailable
	}
	rawURL, err := n.box.Open(ch.URLEncrypted)
	if err != nil {
		return errorText(err)
	}
	sctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	return errorText(n.sender.Send(sctx, rawURL, title, body))
}

// errorText is err's message, bounded and valid UTF-8 (Postgres rejects
// anything else); "" for nil.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ToValidUTF8(err.Error(), "\uFFFD")
	if len(s) > maxErrorLen {
		cut := maxErrorLen
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}
