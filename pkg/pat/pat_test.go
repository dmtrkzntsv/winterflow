package pat

import (
	"regexp"
	"testing"
)

func TestHashStable(t *testing.T) {
	if Hash("wfp_x") != Hash("wfp_x") {
		t.Error("Hash not deterministic")
	}
	if Hash("a") == Hash("b") {
		t.Error("distinct inputs collide")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(Hash("wfp_x")) {
		t.Error("hash is not hex sha256")
	}
}
