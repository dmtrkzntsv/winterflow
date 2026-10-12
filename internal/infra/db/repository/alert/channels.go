package alert

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/alert"
)

// ListChannels returns an org's channels, oldest first.
func (r *Repo) ListChannels(ctx context.Context, orgID string) ([]alert.Channel, error) {
	rows, err := r.q.ListNotificationChannels(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]alert.Channel, 0, len(rows))
	for _, row := range rows {
		out = append(out, r.channel(row))
	}
	return out, nil
}

// GetChannel returns one of the org's channels or alert.ErrNotFound.
func (r *Repo) GetChannel(ctx context.Context, orgID, id string) (alert.Channel, error) {
	row, err := r.q.GetNotificationChannel(ctx, r.tx.Querier(ctx), getChannelParams{OrganizationID: orgID, ChannelID: id})
	if err != nil {
		return alert.Channel{}, notFound(err)
	}
	return r.channel(row), nil
}

// CreateChannel stores a new channel, minting its id and creation time.
func (r *Repo) CreateChannel(ctx context.Context, ch alert.Channel) (alert.Channel, error) {
	ch.ID = r.newID()
	ch.CreatedAt = time.Now().UTC().Truncate(time.Second)
	err := r.q.InsertNotificationChannel(ctx, r.tx.Querier(ctx), insertChanParams{
		ChannelID: ch.ID, OrganizationID: ch.OrgID, Name: ch.Name, Service: ch.Service,
		Fields: encodeFields(ch.Fields), UrlEncrypted: ch.URLEncrypted,
		IsDefault: ch.IsDefault, Enabled: ch.Enabled, CreatedAt: stamp(ch.CreatedAt),
	})
	if err != nil {
		return alert.Channel{}, err
	}
	ch.LastError, ch.LastSentAt = "", nil
	return ch, nil
}

// UpdateChannel rewrites a channel's editable fields (name, service,
// fields, URL, default, enabled, last error); alert.ErrNotFound when the
// org has no such channel.
func (r *Repo) UpdateChannel(ctx context.Context, ch alert.Channel) error {
	n, err := r.q.UpdateNotificationChannel(ctx, r.tx.Querier(ctx), updateChanParams{
		Name: ch.Name, Service: ch.Service, Fields: encodeFields(ch.Fields), UrlEncrypted: ch.URLEncrypted,
		IsDefault: ch.IsDefault, Enabled: ch.Enabled, LastError: ch.LastError,
		OrganizationID: ch.OrgID, ChannelID: ch.ID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return alert.ErrNotFound
	}
	return nil
}

// DeleteChannel removes one of the org's channels. Rules that still list
// it simply skip it at delivery time.
func (r *Repo) DeleteChannel(ctx context.Context, orgID, id string) error {
	n, err := r.q.DeleteNotificationChannel(ctx, r.tx.Querier(ctx), deleteChanParams{OrganizationID: orgID, ChannelID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return alert.ErrNotFound
	}
	return nil
}

// RecordDelivery stores a send's outcome on the channel: success clears
// the last error and stamps last_sent_at; a failure keeps last_sent_at.
func (r *Repo) RecordDelivery(ctx context.Context, channelID string, at time.Time, sendErr string) error {
	if sendErr == "" {
		ts := stamp(at)
		return r.q.RecordChannelSuccess(ctx, r.tx.Querier(ctx), successParams{LastSentAt: &ts, ChannelID: channelID})
	}
	return r.q.RecordChannelFailure(ctx, r.tx.Querier(ctx), failureParams{LastError: sendErr, ChannelID: channelID})
}

func (r *Repo) channel(row channelRow) alert.Channel {
	ch := alert.Channel{
		ID: row.ChannelID, OrgID: row.OrganizationID, Name: row.Name, Service: row.Service,
		Fields: map[string]string{}, IsDefault: row.IsDefault, Enabled: row.Enabled,
		LastError: row.LastError, CreatedAt: utc(row.CreatedAt), URLEncrypted: row.UrlEncrypted,
	}
	if err := json.Unmarshal([]byte(row.Fields), &ch.Fields); err != nil {
		r.log.Warn("alerts: undecodable channel fields", "channel_id", row.ChannelID, "error", err)
		ch.Fields = map[string]string{}
	}
	if row.LastSentAt != nil {
		t := utc(*row.LastSentAt)
		ch.LastSentAt = &t
	}
	return ch
}

func encodeFields(f map[string]string) string {
	if len(f) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(f) // a map[string]string always marshals
	return string(b)
}
