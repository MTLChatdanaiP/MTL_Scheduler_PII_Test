package handlers

import (
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

type ActivePolicyResponse struct {
	Name           string                  `json:"name"`
	Version        int                     `json:"version"`
	Checksum       string                  `json:"checksum"`
	DetectorCount  int                     `json:"detector_count"`
	RuleCount      int                     `json:"rule_count"`
	LastActivation models.PolicyActivation `json:"last_activation"`

	// RFC-006 §32: whether the file on disk still matches the active policy. Additive.
	Drift pii.DriftStatus `json:"drift"`
}

func GetActivePolicy(c *gin.Context) {

	fmt.Println("[Database] Fetching Active Policy")
	var activePolicyReponse ActivePolicyResponse
	var activePolicy models.PolicyActivation
	ap_err := database.DB.WithContext(c.Request.Context()).Where("result = ?", "SUCCESS").Order("activated_at DESC").First(&activePolicy)
	if ap_err == nil {
		activePolicyReponse.LastActivation = activePolicy
	} else {
		fmt.Println("no policy activation record found:", ap_err)
	}

	policy := pii.GetLoadedPolicy()
	metadata := policy.Metadata

	activePolicyReponse.Name = metadata.Name
	activePolicyReponse.Version = metadata.Version
	activePolicyReponse.Checksum = metadata.Checksum
	activePolicyReponse.DetectorCount = len(policy.Spec.Detectors)
	activePolicyReponse.RuleCount = len(policy.Spec.Rules)
	activePolicyReponse.Drift = pii.CheckPolicyDrift(pii.DefaultPolicyPath)

	c.JSON(http.StatusOK, activePolicyReponse)
}

func PostReloadPolicy(c *gin.Context) {
	ctx := c.Request.Context()

	// RFC-006 §33: the activation is audited with the authenticated principal, which
	// the auth middleware stores under "actor". It used to record the literal "api".
	policy, err := pii.ActivatePolicyAs(ctx, "policies/default.json", "MANUAL_RELOAD", "api", c.GetString("actor"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"name":     policy.Metadata.Name,
		"version":  policy.Metadata.Version,
		"checksum": policy.Metadata.Checksum,
	})
}
