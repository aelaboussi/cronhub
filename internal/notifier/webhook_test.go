package notifier

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aelaboussi/cronhub/internal/ports"
)

func TestWebhookFiresOnFailureOnly(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	}))
	defer srv.Close()

	w := NewWebhook("alerts", srv.URL, true)

	// success + failuresOnly => must NOT post
	got = ""
	_ = w.Notify(ports.NotifyEvent{JobName: "ok", Result: ports.RunResult{ExitCode: 0}})
	if got != "" {
		t.Errorf("expected no POST on success, got: %s", got)
	}

	// failure => must post with job name, exit code, success=false
	_ = w.Notify(ports.NotifyEvent{JobName: "boom", Result: ports.RunResult{ExitCode: 3}})
	if !strings.Contains(got, `"job":"boom"`) {
		t.Errorf("payload missing job name: %s", got)
	}
	if !strings.Contains(got, `"exit_code":3`) {
		t.Errorf("payload missing exit code: %s", got)
	}
	if !strings.Contains(got, `"success":false`) {
		t.Errorf("payload missing success=false: %s", got)
	}
}
