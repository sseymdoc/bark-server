package main

import (
	"crypto/rand"
	"io"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/finb/bark-server/v2/database"
	"github.com/gofiber/fiber/v2"
)

// Run with production .go files and this file, excluding push_test.go's real-device TestMain.
func TestStreamAuth(t *testing.T) {
	previous := db
	db = database.NewMemBase()
	t.Cleanup(func() { db = previous })
	key, token := "MemoryBaseKey", "sse:"+rand.Text()
	app := fiber.New()
	sort.Sort(routes)
	for _, route := range routes {
		route.Func(app)
	}
	for _, tc := range []struct {
		name, deviceType, key, token, path string
		want                               int
	}{
		{"android", "android", key, token, "/stream/auth", 204},
		{"missing headers", "android", "", "", "/stream/auth", 400},
		{"missing token", "android", key, "", "/stream/auth", 400},
		{"missing key", "android", "", token, "/stream/auth", 400},
		{"wrong token", "android", key, rand.Text(), "/stream/auth", 403},
		{"wrong key", "android", rand.Text(), token, "/stream/auth", 403},
		{"ios", "ios", key, token, "/stream/auth", 403},
		{"watchos", "watchos", key, token, "/stream/auth", 403},
		{"query cannot authenticate", "android", "", "", "/stream/auth?" + url.Values{"key": {key}, "devicetoken": {token}}.Encode(), 400},
		{"stream missing query", "android", key, token, "/stream", 400},
		{"stream wrong token", "android", "", "", "/stream?" + url.Values{"key": {key}, "devicetoken": {rand.Text()}}.Encode(), 403},
		{"stream ios", "ios", "", "", "/stream?" + url.Values{"key": {key}, "devicetoken": {token}}.Encode(), 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.SaveDeviceTokenByKey(key, token, tc.deviceType); err != nil {
				t.Fatal("could not prepare test device")
			}
			req := httptest.NewRequest("GET", tc.path, nil)
			req.Header.Set("X-Bark-Key", tc.key)
			req.Header.Set("X-Bark-Device-Token", tc.token)
			response, err := app.Test(req)
			if err != nil {
				t.Fatal("request failed")
			}
			defer response.Body.Close()
			if response.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.want)
			}
			if strings.HasPrefix(tc.path, "/stream/auth") && response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("auth answer must not be cacheable")
			}
			body, err := io.ReadAll(response.Body)
			if err != nil || (tc.want == 204 && len(body) != 0) {
				t.Fatal("invalid response body")
			}
		})
	}
}
