package fcm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/finb/bark-server/v2/apns"
)

func TestBuildMessage(t *testing.T) {
	msg := &apns.PushMessage{
		Id:          "abc",
		DeviceToken: "tok",
		Title:       "t",
		Body:        "b",
		Sound:       "1107.caf",
		ExtParams: map[string]interface{}{
			"id":           "abc",
			"Group":        "g",
			"badge":        float64(3),
			"level":        "critical",
			"from":         "x",
			"notification": "x",
			"message_type": "x",
			"google.cid":   "x",
			"gcm.n.e":      "x",
			"title":        "spoofed",
		},
	}
	m := buildMessage(msg)
	data := m["data"].(map[string]string)
	want := map[string]string{
		"id": "abc", "group": "g", "badge": "3", "level": "critical",
		"title": "t", "subtitle": "", "body": "b", "sound": "1107.caf",
	}
	if len(data) != len(want) {
		t.Fatalf("data = %v, want %v", data, want)
	}
	for k, v := range want {
		if data[k] != v {
			t.Errorf("data[%q] = %q, want %q", k, data[k], v)
		}
	}
	if m["token"] != "tok" {
		t.Errorf("token = %v", m["token"])
	}
	if _, ok := m["notification"]; ok {
		t.Error("message must be data-only")
	}
	android := m["android"].(map[string]any)
	if android["priority"] != "HIGH" || android["ttl"] != "86400s" || android["collapse_key"] != "abc" {
		t.Errorf("android = %v", android)
	}

	// no id -> no collapse_key; delete=1 stays a plain data message
	m = buildMessage(&apns.PushMessage{Body: "Empty Message", ExtParams: map[string]interface{}{"delete": "1"}})
	if _, ok := m["android"].(map[string]any)["collapse_key"]; ok {
		t.Error("collapse_key set without id")
	}
	if m["data"].(map[string]string)["delete"] != "1" {
		t.Errorf("delete data = %v", m["data"])
	}
}

func TestPush(t *testing.T) {
	oldEndpoint := endpoint
	t.Cleanup(func() { endpoint, client, projectID = oldEndpoint, nil, "" })

	client = nil
	if code, err := Push(&apns.PushMessage{}); code != 500 || err == nil || err.Error() != "fcm not configured" {
		t.Fatalf("unconfigured: code=%d err=%v", code, err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proj" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var req struct {
			Message map[string]json.RawMessage `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var token string
		_ = json.Unmarshal(req.Message["token"], &token)
		switch token {
		case "ok":
			w.Write([]byte(`{"name":"projects/proj/messages/1"}`))
		case "gone":
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`))
		case "unreg400":
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"x","details":[{"errorCode":"UNREGISTERED"}]}}`))
		default:
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"bad payload","status":"INVALID_ARGUMENT","details":[{"errorCode":"INVALID_ARGUMENT"}]}}`))
		}
	}))
	defer srv.Close()
	endpoint, client, projectID = srv.URL+"/%s", srv.Client(), "proj"

	cases := []struct {
		token string
		code  int
		err   string
	}{
		{"ok", 200, ""},
		{"gone", 410, "UNREGISTERED: Requested entity was not found."},
		{"unreg400", 410, "UNREGISTERED: x"},
		{"bad", 400, "INVALID_ARGUMENT: bad payload"},
	}
	for _, c := range cases {
		code, err := Push(&apns.PushMessage{DeviceToken: c.token})
		if code != c.code || (c.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s: code=%d err=%v, want %d %q", c.token, code, err, c.code, c.err)
		}
	}
}
