package util

func RefString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func DerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
