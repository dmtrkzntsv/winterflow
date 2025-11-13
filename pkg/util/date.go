package util

import (
	"time"
	"winterflow/internal/infra/db/types"
)

func NewDateTime() types.DateTime {
	return types.DateTime(time.Now())
}
