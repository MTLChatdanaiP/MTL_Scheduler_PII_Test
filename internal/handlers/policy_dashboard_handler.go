package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

// RFC-006 §31 Dashboard Requirements. 3 of its 7 items (active policy version/status,
// finding counts by rule/action/type, scanner policy-version drift) already have an
// endpoint or are computed on the frontend from existing data; these 3 handlers cover
// the rest: policy revision history, safe rule summaries, and the detector inventory.

// GetPolicyHistory covers both "policy revision history" and "validation/compile
// failures" at once: every activation attempt is recorded, successful or not.
func GetPolicyHistory(c *gin.Context) {
	var activations []models.PolicyActivation
	q := database.DB.WithContext(c.Request.Context()).Order("activated_at DESC").Limit(50)
	if err := q.Find(&activations).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"activations": activations})
}

type RuleSummary struct {
	ID           string   `json:"id"`
	Priority     int      `json:"priority"`
	Sources      []string `json:"sources"`
	PIITypes     []string `json:"pii_types"`
	ActionType   string   `json:"action_type"`
	MaskStrategy string   `json:"mask_strategy,omitempty"`
}

// GetPolicyRules is deliberately "safe": it returns the shape of each rule (what it
// matches on, what it does) and never anything a rule's match conditions could contain
// that might itself be sensitive, such as a literal field path into real payload data.
func GetPolicyRules(c *gin.Context) {
	policy := pii.GetLoadedPolicy()
	out := make([]RuleSummary, 0, len(policy.Spec.Rules))
	for _, r := range policy.Spec.Rules {
		// A nil slice (a rule that matches by detectorIds instead of listing
		// sources/piiTypes, which is common in this system) marshals to JSON
		// null, not []. The frontend guards against that too, but sending a
		// real empty array is the correct fix at the source.
		sources, piiTypes := r.Match.Sources, r.Match.PIITypes
		if sources == nil {
			sources = []string{}
		}
		if piiTypes == nil {
			piiTypes = []string{}
		}
		out = append(out, RuleSummary{
			ID:           r.ID,
			Priority:     r.Priority,
			Sources:      sources,
			PIITypes:     piiTypes,
			ActionType:   r.Action.Type,
			MaskStrategy: r.Action.Mask.Strategy,
		})
	}
	c.JSON(http.StatusOK, gin.H{"rules": out})
}

type DetectorSummary struct {
	ID                string  `json:"id"`
	PIIType           string  `json:"pii_type"`
	Type              string  `json:"type"`
	Enabled           bool    `json:"enabled"`
	MinimumConfidence float64 `json:"minimum_confidence"`
}

// GetPolicyDetectors lists every detector the currently loaded policy defines. Pattern
// text is left out: it is policy configuration rather than PII itself, but a regex is
// still implementation detail an operator browsing detectors does not need to see.
func GetPolicyDetectors(c *gin.Context) {
	policy := pii.GetLoadedPolicy()
	out := make([]DetectorSummary, 0, len(policy.Spec.Detectors))
	for _, d := range policy.Spec.Detectors {
		out = append(out, DetectorSummary{
			ID: d.ID, PIIType: d.PIIType, Type: d.Type, Enabled: d.Enabled, MinimumConfidence: d.MinimumConfidence,
		})
	}
	c.JSON(http.StatusOK, gin.H{"detectors": out})
}
