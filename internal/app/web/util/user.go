package util

import (
	"net/http"

	"github.com/go-pkgz/auth/v2/token"
)

func GetUserID(r *http.Request) (string, error) {
	user, err := token.GetUserInfo(r)
	if err != nil {
		return "", err
	}

	return user.ID, nil
}
