package public

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNormalizeHTMLLanguage(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"hyphen language": {
			input: "zh-CN",
			want:  "zh-CN",
		},
		"underscore language": {
			input: "zh_CN",
			want:  "zh-CN",
		},
		"reject script injection": {
			input: `zh-CN" autofocus`,
		},
		"reject too short": {
			input: "z",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := normalizeHTMLLanguage(tt.input); got != tt.want {
				t.Fatalf("normalizeHTMLLanguage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReplaceHTMLLanguage(t *testing.T) {
	tests := map[string]struct {
		html     string
		language string
		want     string
	}{
		"replace existing lang": {
			html:     `<html lang="en"><head></head></html>`,
			language: "zh-CN",
			want:     `<html lang="zh-CN"><head></head></html>`,
		},
		"insert missing lang": {
			html:     `<html><head></head></html>`,
			language: "ja_JP",
			want:     `<html lang="ja-JP"><head></head></html>`,
		},
		"ignore invalid lang": {
			html:     `<html lang="en"><head></head></html>`,
			language: `zh-CN" autofocus`,
			want:     `<html lang="en"><head></head></html>`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := replaceHTMLLanguage(tt.html, tt.language); got != tt.want {
				t.Fatalf("replaceHTMLLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProtectedFrontendPath(t *testing.T) {
	tests := map[string]bool{
		"/admin":              true,
		"/admin/dashboard":    true,
		"/terminal":           true,
		"/terminal/session/1": true,
		"/administrator":      false,
		"/terminals":          false,
		"/":                   false,
	}
	for requestPath, want := range tests {
		if got := isProtectedFrontendPath(requestPath); got != want {
			t.Fatalf("isProtectedFrontendPath(%q) = %v, want %v", requestPath, got, want)
		}
	}
}

func TestProtectThirdPartyThemeHTML(t *testing.T) {
	input := `<html><head><script id="vite-plugin-pwa:register-sw" src="/registerSW.js"></script></head><body>theme</body></html>`
	got := protectThirdPartyThemeHTML(input)
	if strings.Contains(got, `vite-plugin-pwa:register-sw`) {
		t.Fatal("third-party theme still contains the known Vite PWA registration tag")
	}
	if !strings.Contains(got, `id="komari-theme-sw-cleanup"`) {
		t.Fatal("third-party theme does not contain the root service-worker cleanup guard")
	}
	if strings.Count(got, `id="komari-theme-sw-cleanup"`) != 1 {
		t.Fatal("third-party theme cleanup guard was injected more than once")
	}

	got = protectThirdPartyThemeHTML(got)
	if strings.Count(got, `id="komari-theme-sw-cleanup"`) != 1 {
		t.Fatal("third-party theme cleanup guard is not idempotent")
	}
}

func TestEmbeddedDistDoesNotEmbedRawFiles(t *testing.T) {
	if _, err := PublicFS.ReadFile("defaultTheme/dist/index.html"); err == nil {
		t.Fatal("PublicFS still embeds the raw frontend files")
	}
	if content, ok := defaultDistFiles[IndexFile]; !ok || len(content) == 0 {
		t.Fatalf("embedded dist does not contain a non-empty %q", IndexFile)
	}
}

func newThemeTestRouter(t *testing.T, theme string) *gin.Engine {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)
	if err := config.Set(config.ThemeKey, theme); err != nil {
		t.Fatalf("set custom theme: %v", err)
	}

	router := gin.New()
	Static(router.Group("/"), func(handlers ...gin.HandlerFunc) {
		router.NoRoute(handlers...)
	})
	return router
}

func TestStaticBlocksThirdPartyRootServiceWorker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())

	themeDir := filepath.Join("data", "theme", "custom", "dist")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatalf("create custom theme directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "index.html"), []byte(`<html><head></head><body>custom theme</body></html>`), 0o644); err != nil {
		t.Fatalf("write custom theme index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "sw.js"), []byte("custom service worker"), 0o644); err != nil {
		t.Fatalf("write custom service worker: %v", err)
	}

	router := newThemeTestRouter(t, "custom")

	workerRequest := httptest.NewRequest("GET", "/sw.js", nil)
	workerRequest.Header.Set("Service-Worker", "script")
	workerRecorder := httptest.NewRecorder()
	router.ServeHTTP(workerRecorder, workerRequest)
	if workerRecorder.Code != 404 {
		t.Fatalf("third-party service worker status = %d, want 404", workerRecorder.Code)
	}
	if !strings.Contains(workerRecorder.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("third-party service worker rejection cache-control = %q, want no-store", workerRecorder.Header().Get("Cache-Control"))
	}

	normalRequest := httptest.NewRequest("GET", "/sw.js", nil)
	normalRecorder := httptest.NewRecorder()
	router.ServeHTTP(normalRecorder, normalRequest)
	if normalRecorder.Code != 200 {
		t.Fatalf("normal theme asset status = %d, want 200", normalRecorder.Code)
	}
	body, err := io.ReadAll(normalRecorder.Result().Body)
	if err != nil {
		t.Fatalf("read normal theme asset: %v", err)
	}
	if string(body) != "custom service worker" {
		t.Fatalf("normal theme asset body = %q, want custom asset", string(body))
	}
}

func TestStaticProtectedRouteCannotBeOverriddenByTheme(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())

	themeDir := filepath.Join("data", "theme", "custom", "dist")
	if err := os.MkdirAll(filepath.Join(themeDir, "admin"), 0o755); err != nil {
		t.Fatalf("create custom theme admin directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "index.html"), []byte(`<html><head></head><body>custom theme index</body></html>`), 0o644); err != nil {
		t.Fatalf("write custom theme index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "admin", "dashboard"), []byte("custom admin override"), 0o644); err != nil {
		t.Fatalf("write custom admin override: %v", err)
	}

	router := newThemeTestRouter(t, "custom")
	request := httptest.NewRequest("GET", "/admin/dashboard", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("protected route status = %d, want 200", recorder.Code)
	}
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatalf("read protected route: %v", err)
	}
	bodyText := string(body)
	if strings.Contains(bodyText, "custom admin override") || strings.Contains(bodyText, "custom theme index") {
		t.Fatal("protected admin route was served by the third-party theme")
	}
	if strings.Contains(bodyText, `vite-plugin-pwa:register-sw`) {
		t.Fatal("protected admin route still registers a root service worker")
	}
	if !strings.Contains(recorder.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("protected route cache-control = %q, want no-store", recorder.Header().Get("Cache-Control"))
	}
}

func TestStaticProtectedRefererUsesDefaultAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())

	themeDir := filepath.Join("data", "theme", "custom", "dist")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatalf("create custom theme directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "index.html"), []byte(`<html><head></head><body>custom theme index</body></html>`), 0o644); err != nil {
		t.Fatalf("write custom theme index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "registerSW.js"), []byte("custom register worker"), 0o644); err != nil {
		t.Fatalf("write custom register worker: %v", err)
	}

	router := newThemeTestRouter(t, "custom")
	request := httptest.NewRequest("GET", "/registerSW.js", nil)
	request.Header.Set("Referer", "http://example.test/admin/dashboard")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("protected asset status = %d, want 200", recorder.Code)
	}
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatalf("read protected asset: %v", err)
	}
	if strings.Contains(string(body), "custom register worker") {
		t.Fatal("protected admin asset was overridden by the third-party theme")
	}
}

func TestStaticRestrictedDoesNotServeCustomAssetOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())
	assetPath := filepath.Join("data", "theme", "custom", "dist", "assets")
	if err := os.MkdirAll(assetPath, 0o755); err != nil {
		t.Fatalf("create custom theme asset directory: %v", err)
	}
	const assetName = "about-D4JKo971.css"
	if err := os.WriteFile(filepath.Join(assetPath, assetName), []byte("custom override"), 0o644); err != nil {
		t.Fatalf("write custom theme asset: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)
	if err := config.Set(config.ThemeKey, "custom"); err != nil {
		t.Fatalf("set custom theme: %v", err)
	}

	router := gin.New()
	StaticRestricted(router.Group("/"), func(handlers ...gin.HandlerFunc) {
		router.NoRoute(handlers...)
	})
	for _, requestPath := range []string{"/assets/" + assetName} {
		request := httptest.NewRequest("GET", requestPath, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != 200 {
			t.Fatalf("restricted asset %s status = %d, want 200", requestPath, recorder.Code)
		}
		body, err := io.ReadAll(recorder.Result().Body)
		if err != nil {
			t.Fatalf("read restricted asset %s: %v", requestPath, err)
		}
		if string(body) == "custom override" {
			t.Fatalf("restricted listener served a custom theme asset override for %s", requestPath)
		}
	}

	indexRequest := httptest.NewRequest("GET", "/database-recovery", nil)
	indexRecorder := httptest.NewRecorder()
	router.ServeHTTP(indexRecorder, indexRequest)
	indexBody, err := io.ReadAll(indexRecorder.Result().Body)
	if err != nil {
		t.Fatalf("read restricted index: %v", err)
	}
	if strings.Contains(string(indexBody), `vite-plugin-pwa:register-sw`) {
		t.Fatal("restricted index still registers a service worker")
	}
}
