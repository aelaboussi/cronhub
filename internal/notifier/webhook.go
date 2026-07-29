// Package notifier: Webhook posts job outcomes as JSON to a URL. It demonstrates
// the extension pattern — a notifier carries its own config (here, the URL and a
// failures-only toggle) in its struct, set at construction, so the ports.Notifier
// interface stays minimal. Slack/Discord/custom endpoints all accept webhooks,
// so this one impl covers most integration needs.
package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/aelaboussi/cronhub/internal/ports"
)

type Webhook struct {
	name         string
	url          string
	failuresOnly bool
	client       *http.Client
}

// NewWebhook builds a webhook notifier. name is how jobs reference it in config
// (e.g. notify = ["webhook"]); url is the POST target; failuresOnly=true sends
// only when a job errored or exited non-zero (recommended, to avoid noise).
func NewWebhook(name, url string, failuresOnly bool) *Webhook {
	return &Webhook{
		name:         name,
		url:          url,
		failuresOnly: failuresOnly,
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *Webhook) Name() string { return w.name }

type webhookPayload struct {
	Job      string `json:"job"`
	Success  bool   `json:"success"`
	ExitCode int    `json:"exit_code"`
	Duration string `json:"duration"`
	Error    string `json:"error,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

func (w *Webhook) Notify(ev ports.NotifyEvent) error {
	r := ev.Result
	success := r.Err == nil && r.ExitCode == 0
	if w.failuresOnly && success {
		return nil
	}

	p := webhookPayload{
		Job:      ev.JobName,
		Success:  success,
		ExitCode: r.ExitCode,
		Duration: r.Duration.String(),
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
	}
	if r.Err != nil {
		p.Error = r.Err.Error()
	}

	body, err := json.Marshal(p)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook %q: status %d", w.name, resp.StatusCode)
	}
	return nil
}

var _ ports.Notifier = (*Webhook)(nil)
