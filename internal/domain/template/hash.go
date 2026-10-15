package template

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"winterflow/internal/domain/model"
)

// HashInput is what a create's replay check compares: the secrets by name
// only (ECIES ciphertexts differ on every encryption).
type HashInput struct {
	ServerID   string
	TemplateID string
	// Revision as requested: 0 is "the latest".
	Revision    int64
	Name        string
	Values      map[string]string
	SecretNames []string
	Hosts       []model.InstanceHost
	Backups     map[string]BackupChoice
}

// RequestHash is the hex SHA-256 of the input's canonical JSON (maps
// marshal with sorted keys; secret names are sorted; hosts keep their
// order).
func RequestHash(in HashInput) string {
	names := append([]string{}, in.SecretNames...)
	sort.Strings(names)
	values, backups, hosts := in.Values, in.Backups, in.Hosts
	if values == nil {
		values = map[string]string{}
	}
	if backups == nil {
		backups = map[string]BackupChoice{}
	}
	if hosts == nil {
		hosts = []model.InstanceHost{}
	}
	raw, _ := json.Marshal(struct {
		ServerID    string                  `json:"server_id"`
		TemplateID  string                  `json:"template_id"`
		Revision    int64                   `json:"revision"`
		Name        string                  `json:"name"`
		Values      map[string]string       `json:"values"`
		SecretNames []string                `json:"secret_names"`
		Hosts       []model.InstanceHost    `json:"hosts"`
		Backups     map[string]BackupChoice `json:"backups"`
	}{in.ServerID, in.TemplateID, in.Revision, in.Name, values, names, hosts, backups})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
