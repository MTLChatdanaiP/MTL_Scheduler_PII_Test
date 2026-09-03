package handlers

import (
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/pii"
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

	policy := pii.LoadedPolicy
	metadata := policy.Metadata

	activePolicyReponse.Name = metadata.Name
	activePolicyReponse.Version = metadata.Version
	activePolicyReponse.Checksum = metadata.Checksum
	activePolicyReponse.DetectorCount = len(policy.Spec.Detectors)
	activePolicyReponse.RuleCount = len(policy.Spec.Rules)

	c.JSON(http.StatusOK, activePolicyReponse)
}
