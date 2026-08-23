package version

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	version = "0.0.0"
)

const versionRegex = `(\d+\.\d+\.\d+)`

func GetVersion() string {
	return version
}

func GetNumericVersion() int {
	return ParseNumericVersion(version)
}

func ParseNumericVersion(semVer string) int {
	// Calver tags (v260823.4) are two-part, so the three-part regex below
	// never strips their v prefix — do it explicitly or Atoi zeroes the date.
	semVer = strings.TrimPrefix(semVer, "v")
	matches := regexp.MustCompile(versionRegex).FindStringSubmatch(semVer)
	if len(matches) > 1 {
		semVer = matches[1]
	}

	parts := strings.Split(semVer, ".")
	result := 0
	for _, part := range parts {
		num, _ := strconv.Atoi(part)
		result = result*1000 + num
	}
	return result
}

func IsSmallerThan(semVer string) bool {
	return GetNumericVersion() < ParseNumericVersion(semVer)
}
