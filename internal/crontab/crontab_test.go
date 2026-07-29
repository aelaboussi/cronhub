package crontab

import (
	"strings"
	"testing"
)

func TestParseSample(t *testing.T) {
	sample := `# my crontab
CRON_TZ=Africa/Casablanca
FOO=bar
*/5 * * * * /usr/bin/backup.sh --now
0 3 * * 1 echo "weekly report"   # name: weekly-report
@daily /opt/cleanup.sh
@reboot /opt/onboot.sh
bogus line here
`
	res, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Timezone != "Africa/Casablanca" {
		t.Errorf("timezone = %q, want Africa/Casablanca", res.Timezone)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(res.Entries))
	}
	// entry 0: interval backup
	if res.Entries[0].Schedule != "*/5 * * * *" || res.Entries[0].Command != "/usr/bin/backup.sh --now" {
		t.Errorf("entry0 = %+v", res.Entries[0])
	}
	if res.Entries[0].Name != "job1" {
		t.Errorf("entry0 name = %q, want job1", res.Entries[0].Name)
	}
	// entry 1: named weekly report
	if res.Entries[1].Name != "weekly-report" {
		t.Errorf("entry1 name = %q, want weekly-report", res.Entries[1].Name)
	}
	if res.Entries[1].Command != `echo "weekly report"` {
		t.Errorf("entry1 command = %q", res.Entries[1].Command)
	}
	// entry 2: @daily shortcut
	if res.Entries[2].Schedule != "0 0 * * *" {
		t.Errorf("entry2 schedule = %q, want 0 0 * * *", res.Entries[2].Schedule)
	}
	// warnings should mention FOO and @reboot and the bogus line
	joined := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"FOO", "@reboot", "got 3 fields"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q; got:\n%s", want, joined)
		}
	}
	t.Logf("\n%s", RenderTOML(res))
}
