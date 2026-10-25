// Package alert sends operational alerts to the team's webhook (Slack
// compatible) and always writes them to the log.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

type Alerter interface {
	Alert(ctx context.Context, subject, detail string)
}

type Webhook struct {
	url  string
	http *http.Client
}

func NewWebhook(url string) *Webhook {
	return &Webhook{url: url, http: &http.Client{Timeout: 10 * time.Second}}
}

func (w *Webhook) Alert(ctx context.Context, subject, detail string) {
	log.Error().Str("alert", subject).Msg(detail)
	if w.url == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"text": "[stellar-anchor] " + subject + ": " + detail})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.http.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("failed to deliver alert")
		return
	}
	resp.Body.Close()
}
