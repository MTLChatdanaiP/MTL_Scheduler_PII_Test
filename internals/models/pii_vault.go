package models

import "gorm.io/gorm"

type PIIVault struct {
	gorm.Model

	JobId          string
	Type           string
	Index          int
	EncryptedValue string
}
