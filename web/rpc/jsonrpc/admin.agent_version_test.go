package jsonrpc

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchLatestAgentRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "komari-agent-version-manager" {
			t.Fatalf("unexpected user agent: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name":"v1.5.11",
			"html_url":"https://github.com/komari-monitor/komari-agent/releases/tag/1.5.11",
			"published_at":"2026-09-17T06:41:53Z",
			"draft":false,
			"prerelease":false
		}`))
	}))
	defer server.Close()

	release, err := fetchLatestAgentRelease(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchLatestAgentRelease returned error: %v", err)
	}
	if release.TagName != "1.5.11" {
		t.Fatalf("expected normalized version 1.5.11, got %q", release.TagName)
	}
	if release.HTMLURL == "" {
		t.Fatal("expected release URL")
	}
}

func TestFetchLatestAgentReleaseRejectsBadResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "http error", status: http.StatusServiceUnavailable, body: `{}`},
		{name: "empty tag", status: http.StatusOK, body: `{"tag_name":""}`},
		{name: "prerelease", status: http.StatusOK, body: `{"tag_name":"1.6.0-rc1","prerelease":true}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			if _, err := fetchLatestAgentRelease(server.Client(), server.URL); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestBuildAgentReleaseStatus(t *testing.T) {
	checkedAt := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	publishedAt := checkedAt.Add(-time.Hour)
	status := buildAgentReleaseStatus(agentRelease{
		TagName:     "1.5.11",
		HTMLURL:     "https://example.test/release",
		PublishedAt: publishedAt,
	}, checkedAt)

	if status.LatestVersion != "1.5.11" {
		t.Fatalf("unexpected version: %q", status.LatestVersion)
	}
	if !status.AutoUpdateDefault || status.AutoUpdateIntervalHours != 6 {
		t.Fatalf("unexpected auto-update metadata: %+v", status)
	}
	if status.RemoteUpgradeSupported {
		t.Fatal("remote upgrade must remain disabled until the agent protocol provides a dedicated update RPC")
	}
	if status.CacheTTLSeconds != int64((30*time.Minute)/time.Second) {
		t.Fatalf("unexpected cache ttl: %d", status.CacheTTLSeconds)
	}
}
