package handlers

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

// GetDecryptedPII returns the raw values vaulted for a job.
//
// RFC-006 §33: this is the most sensitive read in the system, so every call is
// audited (who, which job, how many values) and the values are only returned once
// that record is safely stored. "No audit, no read": if the audit row cannot be
// written the request fails and nothing is returned.
//
// The response is still a plain JSON array. A row that cannot be decrypted used to
// be skipped silently; it is now counted, logged and reported in the
// X-PII-Undecryptable header, which does not change the body shape for any client.
func GetDecryptedPII(c *gin.Context) {

	jobId := c.Param("job_id")
	ctx := c.Request.Context()

	var vaultEntries []models.PIIVault
	database.DB.WithContext(ctx).Where("job_id = ?", jobId).Find(&vaultEntries)

	results := make([]gin.H, 0, len(vaultEntries))
	undecryptable := 0
	for _, entry := range vaultEntries {
		decrypted, err := pii.Decrypt(entry.EncryptedValue)
		if err != nil {
			undecryptable++
			slog.Warn("vault entry could not be decrypted", "job_id", jobId, "type", entry.Type, "index", entry.Index, "error", err)
			continue
		}

		results = append(results, gin.H{
			"type":  entry.Type,
			"index": entry.Index,
			"value": decrypted,
		})
	}

	if err := pii.RecordAccess(ctx, c.GetString("actor"), pii.AuditPIIRawValueRead, "JOB", jobId, len(results)); err != nil {
		slog.Error("could not write the audit record for a raw PII read, refusing the read", "job_id", jobId, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "audit record could not be written, raw values were not returned"})
		return
	}
	events.LogEvent(ctx, jobId, "pii.raw_value_read", "api")

	if undecryptable > 0 {
		c.Header("X-PII-Undecryptable", strconv.Itoa(undecryptable))
	}

	c.JSON(http.StatusOK, results)
}
