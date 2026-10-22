// Package redisenv writes a hub's Redis credentials as an env file
// (REDIS_USERNAME / REDIS_PASSWORD), the file the hub installers hand to
// `winterflow-api hub-trust` and `winterflow-hub join` with --redis-env-out.
package redisenv

import (
	"errors"
	"strings"

	"winterflow/pkg/fsutil"
)

// Write replaces path with the two lines REDIS_USERNAME=<username> and
// REDIS_PASSWORD=<password>, atomically and mode 0600 (it holds a
// password). A value that is empty or holds a character an env file would
// need quoting for is refused, so the file reads the same to systemd and
// to godotenv.
func Write(path, username, password string) error {
	if path == "" {
		return errors.New("redis env file: no path")
	}
	for _, v := range []string{username, password} {
		if v == "" || strings.ContainsAny(v, " \t\r\n\"'\\$#=`") {
			return errors.New("redis env file: a credential is empty or holds a character that needs quoting")
		}
	}
	content := "REDIS_USERNAME=" + username + "\nREDIS_PASSWORD=" + password + "\n"
	return fsutil.WriteReader(path, ".redis-env-*", strings.NewReader(content))
}
