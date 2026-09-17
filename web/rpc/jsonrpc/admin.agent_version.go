package jsonrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari/pkg/rpc"
)

const (
	agentReleaseSource       = "komari-monitor/komari-agent"
	agentLatestReleaseAPI    = "https://api.github.com/repos/komari-monitor/komari-agent/releases/latest"
	agentReleaseCacheTTL     = 30 * time.Minute
	agentAutoUpdateIntervalH = 6
)

type agentRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
}

type agentReleaseStatus struct {
	LatestVersion           string    `json:"latest_version"`
	ReleaseURL              string    `json:"release_url"`
	PublishedAt             time.Time `json:"published_at"`
	CheckedAt               time.Time `json:"checked_at"`
	Source                  string    `json:"source"`
	Stale                   bool      `json:"stale"`
	CheckError              string    `json:"check_error,omitempty"`
	CacheTTLSeconds         int64     `json:"cache_ttl_seconds"`
	AutoUpdateDefault       bool      `json:"auto_update_default"`
	AutoUpdateIntervalHours int       `json:"auto_update_interval_hours"`
	RemoteUpgradeSupported  bool      `json:"remote_upgrade_supported"`
	RemoteUpgradeReason     string    `json:"remote_upgrade_reason"`
}

var agentReleaseCache = struct {
	sync.RWMutex
	status agentReleaseStatus
}{status: agentReleaseStatus{Source: agentReleaseSource}}

func init() {
	RegisterWithGroupAndMeta("getAgentReleaseStatus", rpc.RoleAdmin, adminGetAgentReleaseStatus, &rpc.MethodMeta{
		Name:    "admin:getAgentReleaseStatus",
		Summary: "Get latest stable Komari Agent release metadata",
		Params: []rpc.ParamMeta{
			{Name: "refresh", Type: "boolean", Required: false, Description: "Bypass the in-memory release cache"},
		},
		Returns: "{ latest_version, release_url, published_at, checked_at, source, stale, auto_update_default, auto_update_interval_hours, remote_upgrade_supported }",
	})
}

func adminGetAgentReleaseStatus(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Refresh bool `json:"refresh"`
	}
	_ = req.BindParams(&params)

	now := time.Now().UTC()
	agentReleaseCache.RLock()
	cached := agentReleaseCache.status
	agentReleaseCache.RUnlock()

	if !params.Refresh && cached.LatestVersion != "" && now.Sub(cached.CheckedAt) < agentReleaseCacheTTL {
		return cached, nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	release, err := fetchLatestAgentRelease(client, agentLatestReleaseAPI)
	if err != nil {
		if cached.LatestVersion != "" {
			cached.Stale = true
			cached.CheckError = err.Error()
			return cached, nil
		}
		return nil, rpc.MakeError(rpc.InternalError, "failed to check latest agent release: "+err.Error(), nil)
	}

	status := buildAgentReleaseStatus(release, now)
	agentReleaseCache.Lock()
	agentReleaseCache.status = status
	agentReleaseCache.Unlock()
	return status, nil
}

func fetchLatestAgentRelease(client *http.Client, endpoint string) (agentRelease, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return agentRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "komari-agent-version-manager")

	resp, err := client.Do(req)
	if err != nil {
		return agentRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return agentRelease{}, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}

	var release agentRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return agentRelease{}, err
	}
	if release.Draft || release.Prerelease {
		return agentRelease{}, fmt.Errorf("latest release is not a stable release")
	}
	release.TagName = normalizeAgentVersion(release.TagName)
	if release.TagName == "" {
		return agentRelease{}, fmt.Errorf("latest release has an empty version")
	}
	return release, nil
}

func normalizeAgentVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimPrefix(version, "V")
	return version
}

func buildAgentReleaseStatus(release agentRelease, checkedAt time.Time) agentReleaseStatus {
	return agentReleaseStatus{
		LatestVersion:           normalizeAgentVersion(release.TagName),
		ReleaseURL:              release.HTMLURL,
		PublishedAt:             release.PublishedAt.UTC(),
		CheckedAt:               checkedAt.UTC(),
		Source:                  agentReleaseSource,
		Stale:                   false,
		CacheTTLSeconds:         int64(agentReleaseCacheTTL / time.Second),
		AutoUpdateDefault:       true,
		AutoUpdateIntervalHours: agentAutoUpdateIntervalH,
		RemoteUpgradeSupported:  false,
		RemoteUpgradeReason:     "the current agent protocol has no dedicated update RPC; agents update themselves at startup and every 6 hours unless --disable-auto-update is set",
	}
}
