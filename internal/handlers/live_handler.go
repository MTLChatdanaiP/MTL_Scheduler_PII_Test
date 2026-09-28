package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/auth"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/live"
	"MTL_Scheduler_PII_Test/internal/models"
)

// Max events replayed on reconnect. Beyond this the client is told to
// resync from a fresh snapshot (RFC-010 §17 fallback) instead of us
// replaying an unbounded backlog.
const replayCap = 500

func matchesPrefix(eventType string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if strings.HasPrefix(eventType, p) {
			return true
		}
	}
	return false
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, e live.Event) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	// "id:" makes the browser's EventSource send Last-Event-ID on its own
	// when it reconnects -- that is the native resume mechanism.
	if e.ID != 0 {
		fmt.Fprintf(w, "id: %d\n", e.ID)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)
	flusher.Flush()
}

// ?after=<watermark> for a first connect (from GET /overview), or the
// Last-Event-ID header on an automatic browser reconnect.
func parseCursor(c *gin.Context) (uint, bool) {
	raw := c.Query("after")
	if raw == "" {
		raw = c.GetHeader("Last-Event-ID")
	}
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(n), true
}

func GetLive(serverCtx context.Context, prefixes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		w := c.Writer
		flusher, ok := w.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
			return
		}

		// RFC-010 §25: scopes were placed on the request by auth.RequireScope.
		// They are fixed for the life of this connection; a new connection
		// (every reconnect) re-evaluates them.
		has := func(scope string) bool { return auth.HasScope(c, scope) }
		canSee := func(eventType string) bool {
			need := live.RequiredScope(eventType)
			return need == "" || has(need)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Subscribe BEFORE replaying. Anything published while the replay
		// query runs lands in the channel instead of falling into a gap.
		ch := live.GlobalHub.Subscribe()
		defer live.GlobalHub.Unsubscribe(ch)

		w.Write([]byte(": connected\n\n"))
		flusher.Flush()

		// IDs delivered by replay. Live events are skipped only if their
		// exact ID is in here. NOT "id <= max": two concurrent LogEvent
		// calls can publish out of ID order, and a max-based check would
		// drop the lower one.
		delivered := map[uint]bool{}

		if after, ok := parseCursor(c); ok {
			q := database.DB.WithContext(c.Request.Context()).
				Where("id > ?", after).
				Where("event_type <> ?", "task.progress")

			if len(prefixes) > 0 {
				conds := make([]string, len(prefixes))
				args := make([]interface{}, len(prefixes))
				for i, p := range prefixes {
					conds[i] = "event_type LIKE ?"
					args[i] = p + "%"
				}
				// explicit parentheses: don't depend on GORM to add them
				q = q.Where("("+strings.Join(conds, " OR ")+")", args...)
			}

			// Exclude events this caller may not see BEFORE the row cap is
			// applied, so hidden rows can neither leak nor eat into the
			// 500-event limit and force a needless resync.
			if hidden := live.HiddenEventSQL(has); hidden != "" {
				q = q.Where("NOT (" + hidden + ")")
			}

			var rows []models.EventEnvelope
			if err := q.Order("id ASC").Limit(replayCap + 1).Find(&rows).Error; err != nil {
				fmt.Println("[live] replay query failed:", err)
			} else if len(rows) > replayCap {
				w.Write([]byte("event: resync\ndata: {}\n\n"))
				flusher.Flush()
			} else {
				for _, r := range rows {
					writeSSE(w, flusher, live.Event{
						ID: r.ID, Type: r.EventType, Subject: r.JobId, At: r.OccurredAt.UTC(),
					})
					delivered[r.ID] = true
				}
			}
		}

		// Keepalive so the client's stall watchdog can tell "quiet" from "dead".
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-serverCtx.Done():
				return
			case <-c.Request.Context().Done():
				return
			case <-heartbeat.C:
				w.Write([]byte(": ping\n\n"))
				flusher.Flush()
			case e, open := <-ch:
				if !open {
					return
				}
				if !matchesPrefix(e.Type, prefixes) || !canSee(e.Type) || delivered[e.ID] {
					continue
				}
				writeSSE(w, flusher, e)
			}
		}
	}
}

// Kept so cmd/sse_smoke and the existing route keep compiling.
func GetLiveAlerts(serverCtx context.Context) gin.HandlerFunc {
	return GetLive(serverCtx, "alert.")
}
