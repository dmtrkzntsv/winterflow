package util

import (
	"errors"
	"fmt"
	"os"
)

func FileExists(path string) (bool, error) {
	if path == "" {
		return false, fmt.Errorf("path is empty")
	}

	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, err
}
