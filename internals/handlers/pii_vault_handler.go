package handlers

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/pii"
)

func GetDecryptedPII(c *gin.Context) {
	adminKey := c.GetHeader("X-Admin-Key") //replace with actual auth later
	expectedKey := os.Getenv("ADMIN_KEY")
	if expectedKey == "" || adminKey != expectedKey {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized",
		})
		return
	}

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
