package commands

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/runcrate/cli/internal/api"
)

func TestParsePeriodFrom(t *testing.T) {
	cases := []struct {
		in        string
		wantEmpty bool
		wantLabel string
	}{
		{"", true, ""},
		{"7d", false, "7d"},
		{"24h", false, "24h"},
		{"2w", false, "2w"},
		{"bad", true, ""},
		{"0d", true, ""},
		{"-3d", true, ""},
		{"5x", true, ""},
	}
	for _, c := range cases {
		from, label := parsePeriodFrom(c.in)
		if c.wantEmpty && from != "" {
			t.Errorf("parsePeriodFrom(%q) from = %q, want empty", c.in, from)
		}
		if !c.wantEmpty && from == "" {
			t.Errorf("parsePeriodFrom(%q) from is empty, want a timestamp", c.in)
		}
		if label != c.wantLabel {
			t.Errorf("parsePeriodFrom(%q) label = %q, want %q", c.in, label, c.wantLabel)
		}
	}
}

// topUpServer answers POST /api/v1/billing/topups with reply, after recording
// the request body.
func topUpServer(t *testing.T, reply func(body map[string]any) string) (*httptest.Server, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/billing/topups" {
			t.Errorf("request = %s %s, want POST /api/v1/billing/topups", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("body is not JSON: %v", err)
		}
		w.Write([]byte(reply(got)))
	}))
	t.Cleanup(server.Close)
	return server, &got
}

func TestTopUpSendsTheAmountAndTheGivenKey(t *testing.T) {
	server, body := topUpServer(t, func(map[string]any) string {
		return `{"data":{"id":"t1","status":"succeeded","amount_usd":50,"idempotency_key":"k-1",
			"invoice_number":"RC-000123","balance_usd":104.12,"created_at":"2026-10-26T10:00:00Z"}}`
	})

	res, err := api.NewClient("rc_live_x", server.URL).TopUp(50, "k-1")
	if err != nil {
		t.Fatalf("TopUp() error: %v", err)
	}
	if (*body)["amount_usd"] != 50.0 || (*body)["idempotency_key"] != "k-1" {
		t.Fatalf("body = %v, want amount_usd 50 and idempotency_key k-1", *body)
	}

	var out bytes.Buffer
	if err := renderTopUp(&out, res); err != nil {
		t.Fatalf("renderTopUp() error: %v", err)
	}
	for _, want := range []string{"Added $50.00", "Balance: $104.12", "RC-000123", "k-1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestTopUpMakesAKeyWhenNoneIsGiven(t *testing.T) {
	server, body := topUpServer(t, func(b map[string]any) string {
		return `{"data":{"id":"t2","status":"processing","amount_usd":25,"idempotency_key":"` +
			b["idempotency_key"].(string) + `","created_at":"2026-10-26T10:00:00Z"}}`
	})

	res, err := api.NewClient("rc_live_x", server.URL).TopUp(25, "")
	if err != nil {
		t.Fatalf("TopUp() error: %v", err)
	}
	sent, _ := (*body)["idempotency_key"].(string)
	uuidV4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidV4.MatchString(sent) {
		t.Fatalf("sent key %q, want a fresh UUIDv4", sent)
	}
	if res.IdempotencyKey != sent {
		t.Errorf("result key %q, want the key that was sent (%q) so a retry can reuse it", res.IdempotencyKey, sent)
	}

	var out bytes.Buffer
	if err := renderTopUp(&out, res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "runcrate billing topup 25 --idempotency-key "+sent) {
		t.Errorf("processing top-up should say how to check it with the same key:\n%s", out.String())
	}
}

func TestTopUpRequiresActionShowsTheConfirmLink(t *testing.T) {
	const link = "https://www.runcrate.ai/dashboard/billing?action=confirm-top-up&t=abc"
	server, _ := topUpServer(t, func(map[string]any) string {
		return `{"data":{"id":"t3","status":"requires_action","amount_usd":200,"idempotency_key":"k-3",
			"invoice_number":null,"confirm_url":"` + link + `","expires_at":"2026-10-27T10:00:00Z","created_at":"2026-10-26T10:00:00Z"}}`
	})

	res, err := api.NewClient("rc_live_x", server.URL).TopUp(200, "k-3")
	if err != nil {
		t.Fatalf("TopUp() error: %v", err)
	}
	var out bytes.Buffer
	if err := renderTopUp(&out, res); err != nil {
		t.Fatalf("requires_action is not a failure, got %v", err)
	}
	for _, want := range []string{link, "owner or manager", "Nothing is charged", "2026-10-27T10:00:00Z", "--idempotency-key k-3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestTopUpDeclinedIsAnErrorWithTheReason(t *testing.T) {
	err := renderTopUp(&bytes.Buffer{}, &api.TopUp{Status: api.TopUpDeclined, AmountUsd: 50, Reason: "card_declined",
		Message: "The card was declined. Add another card in the dashboard."})
	if err == nil || !strings.Contains(err.Error(), "The card was declined.") {
		t.Errorf("err = %v, want the server's sentence so the command exits non-zero", err)
	}
}

func TestTopUpRefusalsShowTheServerMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"error":{"code":"TOPUP_CAP_REACHED","message":"This key's daily top-up cap of $500 is reached.","details":{"cap":"daily","remaining_usd":0}}}`))
	}))
	defer server.Close()

	_, err := api.NewClient("rc_live_x", server.URL).TopUp(50, "k-4")
	if err = topUpError(err, "k-4"); err == nil || !strings.Contains(err.Error(), "daily top-up cap of $500 is reached") {
		t.Errorf("err = %v, want the API's message", err)
	}
}

func TestTopUpWithNoAnswerTellsTheKeyToSendAgain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()

	_, err := api.NewClient("rc_live_x", url).TopUp(50, "k-5")
	if err = topUpError(err, "k-5"); err == nil || !strings.Contains(err.Error(), "--idempotency-key k-5") {
		t.Errorf("err = %v, want the key to send again", err)
	}
}

// Off sends enabled=false with the amounts already set (the server needs them),
// or the defaults when none were ever set.
func TestAutoRechargeOffSendsTheCurrentAmounts(t *testing.T) {
	cases := []struct {
		current               string
		wantThreshold, wantTo float64
	}{
		{`{"enabled":true,"threshold_usd":30,"target_usd":120,"last_failure_reason":null,"last_failure_at":null}`, 30, 120},
		{`{"enabled":false,"threshold_usd":null,"target_usd":null,"last_failure_reason":null,"last_failure_at":null}`, 20, 50},
	}
	for _, c := range cases {
		var put map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/billing/auto-recharge" {
				t.Errorf("path = %s", r.URL.Path)
			}
			switch r.Method {
			case "GET":
				w.Write([]byte(`{"data":` + c.current + `}`))
			case "PUT":
				json.NewDecoder(r.Body).Decode(&put)
				w.Write([]byte(`{"data":{"enabled":false,"threshold_usd":30,"target_usd":120}}`))
			default:
				t.Errorf("method = %s", r.Method)
			}
		}))

		a, err := autoRechargeOff(api.NewClient("rc_live_x", server.URL))
		server.Close()
		if err != nil {
			t.Fatalf("autoRechargeOff() error: %v", err)
		}
		if put["enabled"] != false || put["threshold_usd"] != c.wantThreshold || put["target_usd"] != c.wantTo {
			t.Errorf("PUT body = %v, want enabled false, threshold %v, target %v", put, c.wantThreshold, c.wantTo)
		}
		if a.Enabled {
			t.Errorf("result = %+v, want off", a)
		}
	}
}

func TestBalanceShowsBurnAndRunwayOnlyWhenSent(t *testing.T) {
	floating, burn, runway := 52.5, 1.5, 35.0
	var full bytes.Buffer
	renderBalance(&full, &api.Balance{Credits: 54, ActiveUsageCost: 1.5, FloatingBalanceUsd: &floating, BurnUsdPerHour: &burn, RunwayHours: &runway})
	for _, want := range []string{"$54.00", "$52.50", "$1.50/hr", "35.0 hours"} {
		if !strings.Contains(full.String(), want) {
			t.Errorf("balance missing %q:\n%s", want, full.String())
		}
	}

	var old bytes.Buffer
	renderBalance(&old, &api.Balance{Credits: 54})
	if strings.Contains(old.String(), "Burn") || strings.Contains(old.String(), "Runway") {
		t.Errorf("a server without the fields should show the balance only:\n%s", old.String())
	}
}
