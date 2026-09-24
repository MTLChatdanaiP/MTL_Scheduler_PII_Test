package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/live"
)

// GetLiveAlerts is the FIRST, minimal RFC-010 endpoint -- alert changes only,
// not the full §21 multi-resource activity feed. Same auth boundary as every
// other endpoint (RFC-010 line 613), enforced identically via
// auth.RequireScope on the route, not anything special here.
//
// WHAT'S DELIBERATELY NOT HERE YET: the resume-cursor contract from §16/§17
// (CONNECT /live?after=<watermark>). This version starts a subscription from
// "now" -- a client that was disconnected and reconnects WILL miss whatever
// happened while it was gone. That's the exact blind-window problem §16
// describes, and it's the very next thing to build once this basic
// connection is proven to work end to end.
func GetLiveAlerts(serverCtx context.Context) gin.HandlerFunc {
	return func(c *gin.Context) {
		w := c.Writer
		flusher, ok := w.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch := live.GlobalHub.Subscribe()
		defer live.GlobalHub.Unsubscribe(ch)

		w.Write([]byte(": connected\n\n"))
		flusher.Flush()

		clientCtx := c.Request.Context()

		for {
			select {
			case <-serverCtx.Done():
				// server is shutting down -- exit immediately so
				// srv.Shutdown() doesn't have to wait/timeout on us
				return
			case <-clientCtx.Done():
				return
			case event, open := <-ch:
				if !open {
					return
				}
				data, err := json.Marshal(event)
				if err != nil {
					continue
				}
				w.Write([]byte("event: " + event.Type + "\n"))
				w.Write([]byte("data: " + string(data) + "\n\n"))
				flusher.Flush()
			}
		}
	}
}
