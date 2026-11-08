package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/finb/bark-server/v2/database"
	"github.com/finb/bark-server/v2/sse"
	"github.com/gofiber/fiber/v2"
)

// Keeps NATs and proxies from dropping an idle stream, and lets the app notice a dead connection.
const sseHeartbeat = 25 * time.Second

func init() {
	// Weight 100: stream endpoints must win over the "/:device_key" push routes.
	registerRouteWithWeight("stream", 100, func(router fiber.Router) {
		go func() {
			for range time.Tick(10 * time.Minute) {
				sse.Cleanup()
			}
		}()
		router.Get("/stream", doStream)
		router.Get("/stream/auth", doStreamAuth)
	})
}

func isAndroidDevice(key, token string) bool {
	record, err := db.DeviceTokenByKey(key)
	return err == nil && record != nil && record.Token == token && record.Type == database.DeviceTypeAndroid
}

func doStreamAuth(c *fiber.Ctx) error {
	key, token := c.Get("X-Bark-Key"), c.Get("X-Bark-Device-Token")
	if key == "" || token == "" {
		return c.Status(400).JSON(failed(400, "device key and token are required"))
	}
	if !isAndroidDevice(key, token) {
		return c.Status(403).JSON(failed(403, "device key and token do not match"))
	}
	return c.SendStatus(204)
}

// doStream serves Android pushes as Server-Sent Events. The app proves it owns the key with the
// device token it registered: keys sit in scripts and webhooks, and must not be enough to read messages.
func doStream(c *fiber.Ctx) error {
	key, token := c.Query("key"), c.Query("devicetoken")
	if key == "" || token == "" {
		return c.Status(400).JSON(failed(400, "key and devicetoken are required"))
	}
	if !isAndroidDevice(key, token) {
		return c.Status(403).JSON(failed(403, "device key and token do not match"))
	}
	last, _ := strconv.ParseInt(c.Get("Last-Event-ID", c.Query("last_event_id")), 10, 64)
	backlog, events, cancel := sse.Subscribe(key, last)

	// The server's write timeout (3s by default) would cut a long-lived response, so take over the
	// connection and set a deadline per write instead.
	c.Context().HijackSetNoResponse(true)
	c.Context().Hijack(func(conn net.Conn) {
		defer cancel()
		w := bufio.NewWriter(conn)
		write := func(s string) bool {
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := w.WriteString(s); err != nil {
				return false
			}
			return w.Flush() == nil
		}
		send := func(e sse.Event) bool {
			b, _ := json.Marshal(e.Data)
			return write(fmt.Sprintf("id: %d\ndata: %s\n\n", e.ID, b))
		}
		if !write("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nCache-Control: no-cache\r\nX-Accel-Buffering: no\r\nConnection: close\r\n\r\n") {
			return
		}
		for _, e := range backlog {
			if !send(e) {
				return
			}
		}
		ping := time.NewTicker(sseHeartbeat)
		defer ping.Stop()
		for {
			select {
			case e := <-events:
				if !send(e) {
					return
				}
			case <-ping.C:
				if !write(": ping\n\n") {
					return
				}
			}
		}
	})
	return nil
}
