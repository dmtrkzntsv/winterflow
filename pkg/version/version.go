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
