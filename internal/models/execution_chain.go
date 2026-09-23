package models

import (
	"gorm.io/gorm"
)

type ExecutionChain struct {
	gorm.Model

	ExecutionChainId string `json:"execution_chain_id" gorm:"uniqueIndex"`
	SourceRunId      string `json:"source_run_id"` // RFC-001 §4: set only for rerun/replay chains (see §12)
}
