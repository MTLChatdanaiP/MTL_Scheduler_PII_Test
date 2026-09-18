package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/pagination"
)

type PIIListResponse struct {
	PIIs []PIIFindingItem    `json:"piis"`
	Page pagination.PageInfo `json:"page"`
}

func SearchPII(c *gin.Context) {
	query := database.DB.WithContext(c.Request.Context()).Model(&models.PIIRecord{})

	//query = query.Joins("JOIN tasks ON tasks.job_id = pii_records.job_id")

	query = ApplyQueryFilters(c, query, []QueryFilter{
		{Param: "pii_type", Column: "pii_records.type"},
		{Param: "source", Column: "pii_records.source"},
		{Param: "policy_action", Column: "pii_records.policy_action"},
		{Param: "run_id", Column: "pii_records.job_id"},
		{Param: "detector_id", Column: "pii_records.detector_id"},
	})

	// RFC-008 §12 Pagination
	p, err := pagination.ParseParams(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query, total, err := pagination.ApplyPagination(query, p, "pii_records.created_at", &models.PIIRecord{})
	if err != nil {
		fmt.Println("[PII] failed to pii records:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to paginate pii records"})
		return
	}

	var results []models.PIIRecord
	if err := query.Find(&results).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to search findings"})
		return
	}

	piis := make([]PIIFindingItem, 0, len(results))

	for _, r := range results {
		piis = append(piis, PIIFindingItem{
			Type:         r.Type,
			DetectorID:   r.DetectorID,
			Confidence:   r.Confidence,
			Source:       r.Source,
			Index:        r.Index,
			PolicyAction: r.PolicyAction,
		})
	}

	var lastTS time.Time
	var lastID uint

	if len(results) > 0 {
		lastTS = results[len(results)-1].CreatedAt
		lastID = results[len(results)-1].ID
	}

	page := pagination.BuildPageInfo(p, len(results), lastTS, lastID, total)

	c.JSON(http.StatusOK, PIIListResponse{PIIs: piis, Page: page})
}
