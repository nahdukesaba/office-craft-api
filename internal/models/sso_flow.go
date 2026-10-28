package models

import "time"

type BrowserSSOFlow struct {
	StateHash []byte
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}
