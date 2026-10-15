package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strconv"
)

// Digest identifies a set of files: "sha256:" and the hex SHA-256 over the
// files in name order, each written as <len(name)>:<name><len(content)>:<content>,
// so no split of a name and its content reads as another file set. Only
// the files count: a new revision of a catalog template is made when its
// digest changes, never for a fixed description.
func Digest(files map[string]string) string {
	h := sha256.New()
	for _, name := range slices.Sorted(maps.Keys(files)) {
		content := files[name]
		h.Write([]byte(strconv.Itoa(len(name)) + ":" + name + strconv.Itoa(len(content)) + ":"))
		h.Write([]byte(content))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
