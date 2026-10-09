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

// writeSSE sends one event as its RFC-010 §10 envelope. replayed is true for an event read back from the durable log after a
// reconnect, so its freshness says REPLAYED instead of reporting the (large, harmless) time since it happened as pipeline lag.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, e live.Event, replayed bool) {
	data, err := json.Marshal(live.NewEnvelope(e, time.Now(), replayed))
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

// combineMatch ANDs the scope filter with the optional ?subject= narrowing. nil means "no narrowing".
func combineMatch(scope func(live.Event) bool, subject string) func(live.Event) bool {
	if subject == "" {
		return scope
	}
	return func(e live.Event) bool {
		return e.Subject == subject && (scope == nil || scope(e))
	}
}

func GetLive(serverCtx context.Context, prefixes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		w := c.Writer
		flusher, ok := w.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
			return
		}

		// RFC-010 §15: which scopes did the client ask for? Checked BEFORE any stream header is written so a bad request is a
		// plain HTTP error, not a stream that opens and says nothing.
		scopes, err := live.ParseScopes(c.QueryArray("scope"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		sub := live.Subscription{Scopes: scopes}

		// "Authorization applies to the effective subscription": a scope the caller could not read through REST is refused out loud.
		// A silent empty stream would look exactly like a quiet system.
		for _, need := range sub.Permissions() {
			if !auth.HasScope(c, need) {
				c.JSON(http.StatusForbidden, gin.H{"error": "this API key lacks the scope " + need + ", which the requested subscription needs"})
				return
			}
		}

		// RFC-010 §19: one caller may only hold so many live connections at once.
		caller := c.GetString("actor")
		if caller == "" {
			caller = "anonymous"
		}
		if !live.Limiter.Acquire(caller, live.MaxConnectionsPerKey()) {
			c.Header("Retry-After", "5")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many open live connections for this API key"})
			return
		}
		defer live.Limiter.Release(caller)

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

		// RFC-010 §15 Subscription Scopes: a client may narrow itself to ONE
		// resource (?subject=worker-a) instead of receiving every event of the
		// types it is authorized for. Absent means no narrowing -- exactly
		// today's behaviour for every existing caller.
		//
		// This filters what is DELIVERED, not what is authorized: canSee still
		// applies on top, so narrowing can never widen access.
		subjectFilter := c.Query("subject")
		match := combineMatch(sub.Match(), subjectFilter)

		// Subscribe BEFORE replaying. Anything published while the replay
		// query runs lands in the channel instead of falling into a gap.
		ch := live.GlobalHub.SubscribeMatching(match, sub.WantsSummary())
		defer live.GlobalHub.Unsubscribe(ch)

		w.Write([]byte(": connected\n\n"))
		flusher.Flush()

		// IDs delivered by replay. Live events are skipped only if their
		// exact ID is in here. NOT "id <= max": two concurrent LogEvent
		// calls can publish out of ID order, and a max-based check would
		// drop the lower one.
		delivered := map[uint]bool{}

		if after, ok := parseCursor(c); ok {
			// The two noise types are never streamed live (live.IsNoise), so they must not be replayed either: attempt.heartbeat alone is one
			// row every 10 s per running attempt and would eat the 500-row cap, forcing a resync nobody needed.
			q := database.DB.WithContext(c.Request.Context()).
				Where("id > ?", after).
				Where("event_type NOT IN ?", []string{"task.progress", "attempt.heartbeat"})

			// RFC-010 §15: only the rows of the subscribed scopes, filtered BEFORE the row cap like the hidden-event rule below.
			replayWanted := true
			if !sub.Empty() {
				cond, args, has := sub.ReplaySQL()
				if has {
					q = q.Where(cond, args...)
				} else {
					replayWanted = false // platform.summary only: there are no stored rows for it
				}
			}

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
			if !replayWanted {
				// nothing to replay
			} else if err := q.Order("id ASC").Limit(replayCap + 1).Find(&rows).Error; err != nil {
				fmt.Println("[live] replay query failed:", err)
			} else if len(rows) > replayCap {
				// RFC-010 §11 RESYNC_REQUIRED
				body, _ := json.Marshal(live.NewResyncFrame())
				fmt.Fprintf(w, "event: resync\ndata: %s\n\n", body)
				flusher.Flush()
				live.CountResync()
			} else {
				for _, r := range rows {
					writeSSE(w, flusher, live.Event{
						ID: r.ID, Type: r.EventType, Subject: r.JobId, At: r.OccurredAt.UTC(),
						ChainID: r.ExecutionChainID, AttemptID: r.AttemptID,
					}, true)
					delivered[r.ID] = true
				}
				live.CountReplay(len(rows))

				// The synthetic summary is never replayed, so a client that missed changes is told once that the overview may be stale.
				if len(rows) > 0 && sub.WantsSummary() {
					writeSSE(w, flusher, live.SummaryEvent(), false)
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
				live.MarkHeartbeat()
			case e, open := <-ch:
				if !open {
					return
				}
				if !matchesPrefix(e.Type, prefixes) || !canSee(e.Type) || delivered[e.ID] {
					continue
				}
				// Defence in depth: the hub already filters by scope, but a
				// replayed event takes a different path into this loop.
				if match != nil && !match(e) {
					continue
				}
				writeSSE(w, flusher, e, false)
				if !e.At.IsZero() {
					live.ObserveDeliveryLag(time.Since(e.At))
				}
			}
		}
	}
}

// Kept so cmd/sse_smoke and the existing route keep compiling.
func GetLiveAlerts(serverCtx context.Context) gin.HandlerFunc {
	return GetLive(serverCtx, "alert.")
}
