package oauthlogin

import (
	"strings"
	"unicode/utf8"
)

const (
	// nameMaxRunes is the registration form's limit.
	nameMaxRunes = 64
	fallbackName = "User"
)

// deriveName is a new account's name: the provider's display name, else the
// email's local part, else a constant; clamped to 64 runes.
func deriveName(claim, email string) string {
	pick := strings.TrimSpace(claim)
	if pick == "" {
		local := email
		if at := strings.Index(email, "@"); at > 0 {
			local = email[:at]
		}
		pick = strings.TrimSpace(local)
	}
	if pick == "" {
		pick = fallbackName
	}
	if utf8.RuneCountInString(pick) > nameMaxRunes {
		pick = strings.TrimSpace(string([]rune(pick)[:nameMaxRunes]))
	}
	return pick
}
