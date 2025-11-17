package util

import (
	"github.com/google/uuid"
)

func GenerateID() string {
	uuid.New()
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return id.String()
}
