package template

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// HashInput is what a create's replay check compares: the secrets by name
// only (ECIES ciphertexts differ on every encryption).
type HashInput struct {
	ServerID   string
	TemplateID string
	// Revision as requested: 0 is "the latest".
	Revision    int64
	Params      map[string]string
	SecretNames []string
	// Backups are the call's storage overrides.
	Backups map[string]string
}

// RequestHash is the hex SHA-256 of the input's canonical JSON (maps
// marshal with sorted keys; names are sorted).
func RequestHash(in HashInput) string {
	names := append([]string{}, in.SecretNames...)
	sort.Strings(names)
	params, backups := in.Params, in.Backups
	if params == nil {
		params = map[string]string{}
	}
	if backups == nil {
		backups = map[string]string{}
	}
	raw, _ := json.Marshal(struct {
		ServerID    string            `json:"server_id"`
		TemplateID  string            `json:"template_id"`
		Revision    int64             `json:"revision"`
		Params      map[string]string `json:"params"`
		SecretNames []string          `json:"secret_names"`
		Backups     map[string]string `json:"backups"`
	}{in.ServerID, in.TemplateID, in.Revision, params, names, backups})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
