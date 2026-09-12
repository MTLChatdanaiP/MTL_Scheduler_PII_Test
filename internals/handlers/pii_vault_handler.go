package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/pii"
)

func GetDecryptedPII(c *gin.Context) {

	jobId := c.Param("job_id")
	ctx := c.Request.Context()

	var vaultEntries []models.PIIVault
	database.DB.WithContext(ctx).Where("job_id = ?", jobId).Find(&vaultEntries)

	results := make([]gin.H, 0, len(vaultEntries))
	for _, entry := range vaultEntries {
		decrypted, err := pii.Decrypt(entry.EncryptedValue)
		if err != nil {
			continue
		}

		results = append(results, gin.H{
			"type":  entry.Type,
			"index": entry.Index,
			"value": decrypted,
		})
	}

	c.JSON(http.StatusOK, results)
}
