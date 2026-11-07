package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/finb/bark-server/v2/apns"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const scope = "https://www.googleapis.com/auth/firebase.messaging"

// endpoint is a format string taking the project ID; tests point it at an httptest server.
var endpoint = "https://fcm.googleapis.com/v1/projects/%s/messages:send"

var (
	client    *http.Client
	projectID string
)

// Init loads a Firebase service account JSON file and prepares an auto-refreshing OAuth2 client.
func Init(credentialsPath string) error {
	raw, err := os.ReadFile(credentialsPath)
	if err != nil {
		return fmt.Errorf("failed to read fcm credentials: %v", err)
	}
	creds, err := google.CredentialsFromJSONWithType(context.Background(), raw, google.ServiceAccount, scope)
	if err != nil {
		return fmt.Errorf("failed to parse fcm credentials: %v", err)
	}
	if creds.ProjectID == "" {
		return fmt.Errorf("fcm credentials missing project_id")
	}
	c := oauth2.NewClient(context.Background(), creds.TokenSource)
	c.Timeout = 10 * time.Second
	client, projectID = c, creds.ProjectID
	return nil
}

// isReserved reports keys FCM rejects in a data payload.
func isReserved(key string) bool {
	switch key {
	case "from", "notification", "message_type":
		return true
	}
	return strings.HasPrefix(key, "google") || strings.HasPrefix(key, "gcm")
}

// Data is the payload the Android app receives over FCM and SSE alike: every extra parameter
// (lowercased, stringified) plus title/subtitle/body/sound.
func Data(msg *apns.PushMessage) map[string]string {
	data := make(map[string]string, len(msg.ExtParams)+4)
	for k, v := range msg.ExtParams {
		// Change all parameter names to lowercase to prevent inconsistent capitalization (same as apns.Push)
		k = strings.ToLower(k)
		if !isReserved(k) {
			data[k] = fmt.Sprintf("%v", v)
		}
	}
	data["title"] = msg.Title
	data["subtitle"] = msg.Subtitle
	data["body"] = msg.Body
	data["sound"] = msg.Sound
	return data
}

// buildMessage builds a data-only FCM message; the Android app renders the notification itself.
func buildMessage(msg *apns.PushMessage, data map[string]string) map[string]any {
	android := map[string]any{"priority": "HIGH", "ttl": "86400s"}
	if msg.Id != "" {
		android["collapse_key"] = msg.Id
	}
	return map[string]any{"token": msg.DeviceToken, "data": data, "android": android}
}

// Push sends msg via FCM HTTP v1. Returns 410 for unregistered tokens so the caller can clean them up.
func Push(msg *apns.PushMessage, data map[string]string) (int, error) {
	if client == nil {
		return 500, fmt.Errorf("fcm not configured")
	}
	body, err := json.Marshal(map[string]any{"message": buildMessage(msg, data)})
	if err != nil {
		return 500, err
	}
	resp, err := client.Post(fmt.Sprintf(endpoint, projectID), "application/json", bytes.NewReader(body))
	if err != nil {
		return 500, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return 200, nil
	}

	var e struct {
		Error struct {
			Message string `json:"message"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = json.Unmarshal(raw, &e)

	reason := resp.Status
	unregistered := resp.StatusCode == 404
	for _, d := range e.Error.Details {
		if d.ErrorCode != "" {
			reason = d.ErrorCode
		}
		unregistered = unregistered || d.ErrorCode == "UNREGISTERED"
	}
	if e.Error.Message != "" {
		reason += ": " + e.Error.Message
	}
	if unregistered {
		return 410, fmt.Errorf("%s", reason)
	}
	return resp.StatusCode, fmt.Errorf("%s", reason)
}
