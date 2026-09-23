// New package, not folded into pagination or handlers, because both §10 and
// §14 will eventually be imported from every handler package -- same
// reasoning as internal/pagination getting its own package.

package freshness

import (
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"context"
	"time"
)

// watermarkSafetyMargin covers a real race: Postgres assigns an id at INSERT,
// not at COMMIT, so a slow transaction can commit AFTER a later id has already
// committed and become visible. A reader who saw MAX(id) = 101 may never see
// id 100 once it finally commits, because 100 is now "behind" a watermark the
// client already holds. Subtracting a margin re-sends a few recent rows rather
// than risking a silent gap -- duplicates are harmless if the client that
// consumes this is idempotent, a gap is not.
const watermarkSafetyMargin = 5

type FreshnessInfo struct {
	LastUpdatedAt *time.Time `json:"last_updated_at,omitempty"`
	LagSeconds    float64    `json:"lag_seconds"`
}

type LiveInfo struct {
	Watermark int64 `json:"watermark"`
}

// CurrentWatermark returns the highest safely-observable event id.
func CurrentWatermark(ctx context.Context) (int64, error) {
	var maxID int64

	if err := database.DB.WithContext(ctx).Model(&models.EventEnvelope{}).Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
		return 0, err
	}

	watermark := maxID - watermarkSafetyMargin

	if watermark < 0 {
		watermark = 0
	}

	return watermark, nil
}

func FreshnessFrom(lastUpdated time.Time) FreshnessInfo {
	if lastUpdated.IsZero() {
		return FreshnessInfo{}
	}

	lag := time.Since(lastUpdated).Seconds()

	return FreshnessInfo{LastUpdatedAt: &lastUpdated, LagSeconds: lag}
}
