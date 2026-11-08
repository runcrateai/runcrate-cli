package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/runcrate/cli/internal/api"
)

var scheduleTestNow = time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestParseAtWallTimeAcrossDST(t *testing.T) {
	london := mustLoad(t, "Europe/London")
	newYork := mustLoad(t, "America/New_York")
	tests := []struct {
		name    string
		in      string
		loc     *time.Location
		want    string // UTC
		shifted bool
	}{
		{"winter, GMT", "2026-02-01 02:00", london, "2026-02-01T02:00:00Z", false},
		{"summer, BST", "2026-10-21 02:00", london, "2026-10-21T01:00:00Z", false},
		{"seconds and T", "2026-10-21T02:00:30", london, "2026-10-21T01:00:30Z", false},
		{"day of spring forward, after the gap", "2026-03-29 03:00", london, "2026-03-29T02:00:00Z", false},
		{"inside the spring gap moves forward", "2026-03-29 01:30", london, "2026-03-29T01:30:00Z", true},
		{"fall back repeated hour is the first one (BST)", "2026-10-25 01:30", london, "2026-10-25T00:30:00Z", false},
		{"after fall back, GMT", "2026-10-25 02:30", london, "2026-10-25T02:30:00Z", false},
		{"New York spring gap", "2026-03-08 02:30", newYork, "2026-03-08T07:30:00Z", true},
		{"New York repeated hour, EDT first", "2026-11-01 01:30", newYork, "2026-11-01T05:30:00Z", false},
		{"explicit offset ignores the zone", "2026-10-21T02:00:00+01:00", newYork, "2026-10-21T01:00:00Z", false},
		{"explicit Z", "2026-10-21T02:00Z", newYork, "2026-10-21T02:00:00Z", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, notice, err := parseAt(tt.in, tt.loc, scheduleTestNow)
			if err != nil {
				t.Fatalf("parseAt(%q): %v", tt.in, err)
			}
			if s := got.UTC().Format(time.RFC3339); s != tt.want {
				t.Errorf("parseAt(%q) = %s, want %s", tt.in, s, tt.want)
			}
			if (notice != "") != tt.shifted {
				t.Errorf("notice = %q, shifted want %v", notice, tt.shifted)
			}
		})
	}

	// The shifted London time reads 02:30 BST on the clock.
	got, notice, _ := parseAt("2026-03-29 01:30", london, scheduleTestNow)
	if wall := got.In(london).Format("15:04 MST"); wall != "02:30 BST" {
		t.Errorf("gap time lands at %s, want 02:30 BST", wall)
	}
	if !strings.Contains(notice, "02:30 BST") {
		t.Errorf("notice = %q", notice)
	}

	for _, bad := range []string{"2026-01-09 23:00", "tomorrow", "2026-10-21", "21/10/2026 02:00"} {
		if _, _, err := parseAt(bad, time.UTC, scheduleTestNow); err == nil {
			t.Errorf("parseAt(%q): expected an error", bad)
		}
	}
}

func TestLocalTimezoneFromTZ(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")
	if got, err := localTimezone(); err != nil || got != "Asia/Tokyo" {
		t.Errorf("localTimezone() = %q, %v", got, err)
	}
	t.Setenv("TZ", "")
	if got, _ := localTimezone(); got != "UTC" {
		t.Errorf("empty TZ = %q, want UTC", got)
	}
	if _, err := resolveTimezone("Mars/Olympus"); err == nil {
		t.Error("resolveTimezone accepted an unknown zone")
	}
}

func parseScheduleCreate(t *testing.T, argv ...string) (api.CreateScheduleRequest, string, error) {
	t.Helper()
	cmd := newScheduleCreateCmd()
	if err := cmd.ParseFlags(argv); err != nil {
		t.Fatalf("ParseFlags(%q): %v", argv, err)
	}
	lookup := func(k string) (string, bool) {
		if k == "HF_TOKEN" {
			return "from-shell", true
		}
		return "", false
	}
	return buildScheduleRequest(cmd, cmd.Flags().Args(), lookup, scheduleTestNow)
}

func asJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	return m
}

func TestBuildScheduleRequestKinds(t *testing.T) {
	t.Setenv("TZ", "America/New_York")

	// No command: an instance, cron in the machine's zone, overlap defaults to skip.
	req, _, err := parseScheduleCreate(t, "--cron", " 0  2 * * 1-5 ", "--name", "box",
		"--gpu", "H100:2", "--max-runtime", "4h", "--idle-stop", "30m", "--volume", "vol-2")
	if err != nil {
		t.Fatalf("instance: %v", err)
	}
	want := map[string]any{
		"kind":     "instance",
		"cron":     "0 2 * * 1-5",
		"timezone": "America/New_York",
		"overlap":  "skip",
		"name":     "box",
		"spec": map[string]any{
			"name": "box", "gpu_type": "H100", "gpu_count": float64(2),
			"max_runtime_minutes": float64(240), "idle_shutdown_minutes": float64(30), "storage_id": "vol-2",
		},
	}
	if got := asJSON(t, req); !jsonEqual(got, want) {
		t.Errorf("instance body = %v\nwant %v", got, want)
	}

	// A command after --: a job, --at in --tz sent as a UTC instant plus the zone.
	req, _, err = parseScheduleCreate(t, "--at", "2026-10-21 02:00", "--tz", "Europe/London",
		"--gpu", "A4000", "--max-runtime", "90m", "--env", "HF_TOKEN", "--volume", "vol-1",
		"--", "python", "train.py", "--epochs", "3")
	if err != nil {
		t.Fatalf("job: %v", err)
	}
	want = map[string]any{
		"kind":     "job",
		"run_at":   "2026-10-21T01:00:00Z",
		"timezone": "Europe/London",
		"spec": map[string]any{
			"command": "python train.py --epochs 3", "gpu_type": "A4000", "gpu_count": float64(1),
			"max_runtime_minutes": float64(90), "env": map[string]any{"HF_TOKEN": "from-shell"},
			"storage_id": "vol-1",
		},
	}
	if got := asJSON(t, req); !jsonEqual(got, want) {
		t.Errorf("job body = %v\nwant %v", got, want)
	}
}

func TestBuildScheduleRequestRefusals(t *testing.T) {
	t.Setenv("TZ", "UTC")
	tests := []struct {
		name    string
		argv    []string
		wantErr string
	}{
		{"instance without max runtime", []string{"--cron", "0 2 * * *", "--name", "box", "--gpu", "H100"}, "--max-runtime is required"},
		{"job without max runtime", []string{"--cron", "0 2 * * *", "--gpu", "H100", "--", "nvidia-smi"}, "--max-runtime is required"},
		{"empty max runtime", []string{"--cron", "0 2 * * *", "--gpu", "H100", "--max-runtime", "", "--", "true"}, "--max-runtime is required"},
		{"no when", []string{"--gpu", "H100", "--max-runtime", "1h", "--", "true"}, "pass --at"},
		{"both whens", []string{"--at", "2026-02-01 02:00", "--cron", "0 2 * * *", "--gpu", "H100", "--max-runtime", "1h", "--", "true"}, "only one of --at"},
		{"overlap with at", []string{"--at", "2026-02-01 02:00", "--overlap", "allow", "--gpu", "H100", "--max-runtime", "1h", "--", "true"}, "--overlap only applies"},
		{"bad overlap", []string{"--cron", "0 2 * * *", "--overlap", "queue", "--gpu", "H100", "--max-runtime", "1h", "--", "true"}, "invalid --overlap"},
		{"four cron fields", []string{"--cron", "0 2 * *", "--gpu", "H100", "--max-runtime", "1h", "--", "true"}, "5 fields"},
		{"bad tz", []string{"--cron", "0 2 * * *", "--tz", "London", "--gpu", "H100", "--max-runtime", "1h", "--", "true"}, "unknown --tz"},
		{"command without --", []string{"--cron", "0 2 * * *", "--gpu", "H100", "--max-runtime", "1h", "nvidia-smi"}, "after --"},
		{"-- with no command", []string{"--cron", "0 2 * * *", "--gpu", "H100", "--max-runtime", "1h", "--"}, "after --"},
		{"instance flag on a job", []string{"--cron", "0 2 * * *", "--gpu", "H100", "--max-runtime", "1h", "--idle-stop", "30m", "--", "true"}, "--idle-stop only applies to instance"},
		{"job flag on an instance", []string{"--cron", "0 2 * * *", "--name", "box", "--gpu", "H100", "--max-runtime", "1h", "--image", "x"}, "--image only applies to job"},
		{"instance needs a name", []string{"--cron", "0 2 * * *", "--gpu", "H100", "--max-runtime", "1h"}, "--name is required"},
		{"job runtime over 7 days", []string{"--cron", "0 2 * * *", "--gpu", "H100", "--max-runtime", "169h", "--", "true"}, "between 1m and 168h"},
		{"instance runtime under 5m", []string{"--cron", "0 2 * * *", "--name", "box", "--gpu", "H100", "--max-runtime", "2m"}, "between 5m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseScheduleCreate(t, tt.argv...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// fakeSchedules records each request and answers with a fixed schedule.
type fakeSchedules struct {
	method, path string
	body         map[string]any
	status       int
	reply        string
}

func (f *fakeSchedules) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.method, f.path, f.body = r.Method, r.URL.Path, nil
	raw, _ := io.ReadAll(r.Body)
	if len(raw) > 0 {
		json.Unmarshal(raw, &f.body)
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
	}
	w.Write([]byte(f.reply))
}

const scheduleReply = `{"data":{"id":"sch-1","name":"nightly","kind":"job","spec":{},"cron":"0 2 * * *","timezone":"UTC","overlap":"skip","enabled":true,"max_runtime_minutes":60}}`

func TestScheduleRequestsOnTheWire(t *testing.T) {
	t.Setenv("TZ", "UTC")
	fake := &fakeSchedules{reply: scheduleReply}
	server := httptest.NewServer(fake)
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	req, _, err := parseScheduleCreate(t, "--cron", "0 2 * * *", "--overlap", "allow", "--gpu", "A4000",
		"--max-runtime", "1h", "--", "nvidia-smi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateSchedule(req); err != nil {
		t.Fatalf("create: %v", err)
	}
	if fake.method != "POST" || fake.path != "/api/v1/schedules" {
		t.Errorf("create sent %s %s", fake.method, fake.path)
	}
	if fake.body["kind"] != "job" || fake.body["cron"] != "0 2 * * *" || fake.body["overlap"] != "allow" ||
		fake.body["timezone"] != "UTC" || fake.body["run_at"] != nil {
		t.Errorf("create body = %v", fake.body)
	}
	if spec, _ := fake.body["spec"].(map[string]any); spec["command"] != "nvidia-smi" || spec["max_runtime_minutes"] != float64(60) {
		t.Errorf("create spec = %v", fake.body["spec"])
	}

	for _, enabled := range []bool{false, true} {
		if _, err := setScheduleEnabled(client, "sch-1", enabled); err != nil {
			t.Fatalf("enabled=%v: %v", enabled, err)
		}
		if fake.method != "PATCH" || fake.path != "/api/v1/schedules/sch-1" || !jsonEqual(fake.body, map[string]any{"enabled": enabled}) {
			t.Errorf("enabled=%v sent %s %s %v", enabled, fake.method, fake.path, fake.body)
		}
	}

	fake.reply = `{"data":{"schedule":{"id":"sch-1","name":"nightly"},"job":{"id":"job-7","name":"nightly","status":"queued"}}}`
	run, err := client.RunScheduleNow("sch-1")
	if err != nil {
		t.Fatalf("run-now: %v", err)
	}
	if fake.method != "POST" || fake.path != "/api/v1/schedules/sch-1/run-now" || fake.body != nil {
		t.Errorf("run-now sent %s %s %v", fake.method, fake.path, fake.body)
	}
	if run.Job == nil || run.Job.ID != "job-7" || run.Instance != nil {
		t.Errorf("run-now result = %+v", run)
	}

	fake.reply = `{"data":{"id":"sch-1","deleted":true}}`
	if res, err := client.DeleteSchedule("sch-1"); err != nil || !res.Deleted || fake.method != "DELETE" {
		t.Errorf("delete = %+v, %v (%s)", res, err, fake.method)
	}

	// v1 errors surface with their message; run-now's 402 is the budget.
	fake.status = http.StatusUnprocessableEntity
	fake.reply = `{"error":{"code":"validation_error","message":"Runs must be at least 15 minutes apart"}}`
	if _, err := client.CreateSchedule(req); err == nil || err.Error() != "Runs must be at least 15 minutes apart (HTTP 422)" {
		t.Errorf("422 error = %v", err)
	}
	fake.status = http.StatusPaymentRequired
	fake.reply = `{"error":{"code":"BUDGET_REACHED","message":"Environment budget reached"}}`
	_, err = client.RunScheduleNow("sch-1")
	var apiErr *api.APIError
	if !api.IsBudgetReached(err) || !errors.As(err, &apiErr) {
		t.Errorf("402 error = %v, want BUDGET_REACHED", err)
	}
}

func strp(s string) *string { return &s }

func TestRenderScheduleTable(t *testing.T) {
	london := mustLoad(t, "Europe/London")
	list := []api.Schedule{
		{
			ID: "sch-1", Name: "nightly-train", Kind: "job", Cron: strp("0 2 * * *"), Timezone: "Europe/London",
			Enabled: true, NextRunAt: strp("2026-10-21T01:00:00.000Z"), LastStatus: strp("skipped_overlap"),
		},
		{
			ID: "sch-2", Name: "morning-box", Kind: "instance", RunAt: strp("2026-10-21T08:00:00Z"), Timezone: "America/New_York",
			Enabled: false,
		},
		{
			ID: "sch-3", Name: "agent-eval", Kind: "job", Cron: strp("0 * * * *"), Timezone: "UTC",
			Enabled: false, LastStatus: strp("credential_revoked"), RunAsCredential: "api_key",
		},
	}
	var out bytes.Buffer
	if err := renderScheduleTable(&out, list, london); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines:\n%s", len(lines), out.String())
	}
	header := strings.Fields(lines[0])
	if strings.Join(header, " ") != "ID NAME KIND WHEN NEXT RUN LAST RESULT ENABLED" {
		t.Errorf("header = %q", lines[0])
	}
	for _, want := range []string{"sch-1", "nightly-train", "job", "0 2 * * * (Europe/London)", "2026-10-21 02:00 BST", "skipped: still running", "yes"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("row 1 %q is missing %q", lines[1], want)
		}
	}
	// A one-off shows its time in its own zone; never run and paused.
	for _, want := range []string{"sch-2", "instance", "once 2026-10-21 04:00 (America/New_York)", "  -  ", "no"} {
		if !strings.Contains(lines[2], want) {
			t.Errorf("row 2 %q is missing %q", lines[2], want)
		}
	}
	// Paused because the API key it was set up with was revoked.
	if !strings.Contains(lines[3], "paused: key revoked") {
		t.Errorf("row 3 %q does not say the key was revoked", lines[3])
	}
}

func TestScheduleRunsAs(t *testing.T) {
	for cred, want := range map[string]string{
		"user":    "u-1 (signed in)",
		"api_key": "u-1 through an API key",
		"agent":   "u-1 through an agent sign-in",
	} {
		s := &api.Schedule{RunAs: strp("u-1"), RunAsCredential: cred}
		if got := scheduleRunsAs(s); got != want {
			t.Errorf("%s: %q, want %q", cred, got, want)
		}
	}
	if got := scheduleRunsAs(&api.Schedule{RunAsCredential: "user"}); got != "" {
		t.Errorf("no run_as: %q, want empty", got)
	}
}

func TestConfirm(t *testing.T) {
	var prompt bytes.Buffer
	for in, want := range map[string]bool{"y\n": true, "YES\n": true, "n\n": false, "\n": false, "y": true} {
		got, err := confirm(strings.NewReader(in), &prompt, "Delete? ")
		if err != nil || got != want {
			t.Errorf("confirm(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := confirm(strings.NewReader(""), &prompt, "Delete? "); err == nil {
		t.Error("confirm with no input should fail so delete asks for --yes")
	}
}
