package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"superset/internal/models"
)

// The owner-facing payload must never echo the token of a dead link.
func TestShareJSONHidesDeadTokens(t *testing.T) {
	expired := time.Now().Add(-time.Hour)
	dead := &models.DashboardShare{
		DashboardID: 7, Enabled: true, Token: "secret-token",
		ExpiresAt: &expired, AllowExport: true,
	}
	out := shareJSON(dead)
	if out["token"] != "" {
		t.Fatalf("expired link leaked its token: %v", out["token"])
	}
	if out["url"] != "" {
		t.Fatalf("expired link leaked its url: %v", out["url"])
	}
	if out["expired"] != true {
		t.Fatalf("expired flag should be true, got %v", out["expired"])
	}
}

func TestShareJSONLiveLink(t *testing.T) {
	live := &models.DashboardShare{
		DashboardID: 3, Enabled: true, Token: "abc123",
		PasswordHash: "$2a$10$notarealhash", AllowExport: false,
	}
	out := shareJSON(live)
	if out["token"] != "abc123" {
		t.Fatalf("live link should echo its token, got %v", out["token"])
	}
	if out["has_password"] != true {
		t.Fatal("has_password should be true")
	}
	if out["allow_export"] != false {
		t.Fatal("allow_export should be false")
	}
	if _, leaked := out["password_hash"]; leaked {
		t.Fatal("payload must not contain the password hash")
	}
}

func TestShareJSONNilIsDisabled(t *testing.T) {
	out := shareJSON(nil)
	if out["enabled"] != false {
		t.Fatal("a dashboard without a share row is not shared")
	}
}

// dashboard_share helpers ---------------------------------------------------

func TestDashboardShareActive(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		share models.DashboardShare
		want  bool
	}{
		{"disabled", models.DashboardShare{Enabled: false, Token: "t"}, false},
		{"no token", models.DashboardShare{Enabled: true}, false},
		{"live", models.DashboardShare{Enabled: true, Token: "t"}, true},
		{
			"expired",
			models.DashboardShare{Enabled: true, Token: "t", ExpiresAt: timePtr(now.Add(-time.Minute))},
			false,
		},
		{
			"future expiry",
			models.DashboardShare{Enabled: true, Token: "t", ExpiresAt: timePtr(now.Add(time.Hour))},
			true,
		},
	}
	for _, tc := range cases {
		if got := tc.share.Active(); got != tc.want {
			t.Fatalf("%s: Active() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// export_image helpers ------------------------------------------------------

func TestDashExportFilenameKeepsUnicode(t *testing.T) {
	dm := &models.Dashboard{DashboardTitle: "销售 看板 / Q3"}
	header := dashExportFilename(dm, "pdf")
	if !strings.HasPrefix(header, "attachment;") {
		t.Fatalf("unexpected header: %s", header)
	}
	if !strings.Contains(header, "filename*=UTF-8''") {
		t.Fatalf("unicode title should ride in filename*: %s", header)
	}
	if strings.ContainsAny(header, "\r\n") {
		t.Fatal("header injection")
	}
	// The ASCII fallback must stay printable and quote-free.
	asciiPart := header[strings.Index(header, `filename="`)+len(`filename="`):]
	asciiPart = asciiPart[:strings.Index(asciiPart, `"`)]
	for _, r := range asciiPart {
		if r > 127 || r == '"' || r == '\\' {
			t.Fatalf("unsafe character %q in fallback name %q", r, asciiPart)
		}
	}
	if !strings.HasSuffix(asciiPart, ".pdf") {
		t.Fatalf("missing extension in %q", asciiPart)
	}
}

func TestDashExportFilenameFallsBackToID(t *testing.T) {
	header := dashExportFilename(&models.Dashboard{BaseModel: models.BaseModel{ID: 42}}, "png")
	if !strings.Contains(header, "dashboard-42") {
		t.Fatalf("expected id fallback, got %s", header)
	}
}

func TestIntQueryParam(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 1600},
		{"1920", 1920},
		{"abc", 1600},
		{"-5", 1600},
		{"0", 1600},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "/?value="+tc.raw, nil)
		if got := intQueryParam(r, "value", 1600); got != tc.want {
			t.Fatalf("intQueryParam(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestClampInt(t *testing.T) {
	if clampInt(10, 100, 200) != 100 || clampInt(500, 100, 200) != 200 || clampInt(150, 100, 200) != 150 {
		t.Fatal("clampInt bounds are wrong")
	}
}
