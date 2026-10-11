package model

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Storages: places backups and apps can write to, registered per server. The
// agent's {AGENT_DATA_DIR}/storages/ is the source of truth; the API mirrors
// name, type and location (never a secret) into its server_storages index.

// ErrStorageRef marks a save whose compose file references a WF_STORAGE_ /
// WF_BACKUP_ variable that resolves to nothing on the target server; the HTTP
// layer maps it to 400.
var ErrStorageRef = errors.New("unresolved storage variable")

// ErrReservedVariable marks a user variable whose name a storage provides;
// the HTTP layer maps it to 400.
var ErrReservedVariable = errors.New("reserved variable name")

// StorageType is the kind of a storage.
type StorageType string

const (
	StorageLocal StorageType = "local" // a folder on the server
	StorageS3    StorageType = "s3"    // an S3-compatible bucket
)

// Storage is one registration. Exactly the sub-struct matching Type is set.
type Storage struct {
	Name  string        `json:"name"`
	Type  StorageType   `json:"type"`
	Local *LocalStorage `json:"local,omitempty"`
	S3    *S3Storage    `json:"s3,omitempty"`
}

// LocalStorage is a folder on the server: absolute, cleaned, not "/", and
// (checked on the agent) not under AGENT_DATA_DIR. The folder must exist —
// winterflow never creates it, so an unmounted disk fails instead of filling
// the root filesystem.
type LocalStorage struct {
	Path string `json:"path"`
}

// S3Storage is an S3-compatible bucket. SecretAccessKey is ECIES ciphertext
// in transit and at rest, or EncryptedPlaceholder ("keep the stored one");
// reads always answer the placeholder.
type S3Storage struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	PathStyle       bool   `json:"path_style"`
}

// StorageSummary is what the API's index keeps of a storage: never a secret.
type StorageSummary struct {
	Name     string      `json:"name"`
	Type     StorageType `json:"type"`
	Location string      `json:"location"`
}

// EncryptedSecret is the masked value of a stored secret (the same sentinel
// app secrets use: command.EncryptedPlaceholder).
const EncryptedSecret = "<encrypted>"

// Length caps (bytes). They keep Location — the folder, or
// endpoint/bucket/prefix (at most 255+1+63+1+512 bytes) — inside the API
// index's server_storages.location VARCHAR(1024): one oversized row would
// fail the whole index sync of its server.
const (
	maxLocalPathLen = 512
	maxEndpointLen  = 255
	maxPrefixLen    = 512
)

var (
	storageNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	bucketRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	regionRE      = regexp.MustCompile(`^[a-z0-9_-]{0,64}$`)
)

// ValidateStorageName checks a storage name: lowercase letters, digits and
// inner hyphens, at most 32 characters. Underscores are refused so two names
// never map to the same WF_STORAGE_ variable.
func ValidateStorageName(name string) error {
	if !storageNameRE.MatchString(name) {
		return fmt.Errorf("%q is not a valid storage name: use 1-32 lowercase letters, digits and inner hyphens, like backup-disk", name)
	}
	return nil
}

// Normalized returns s with the forms the agent stores: a cleaned local
// path, an endpoint without a trailing slash, and a prefix ending in "/"
// (or empty). It never fails; Validate judges the result.
func (s Storage) Normalized() Storage {
	out := s
	if s.Local != nil {
		l := *s.Local
		if p := strings.TrimSpace(l.Path); p != "" {
			l.Path = filepath.Clean(p)
		}
		out.Local = &l
	}
	if s.S3 != nil {
		c := *s.S3
		c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
		c.Region = strings.TrimSpace(c.Region)
		c.Bucket = strings.TrimSpace(c.Bucket)
		c.AccessKeyID = strings.TrimSpace(c.AccessKeyID)
		c.Prefix = strings.TrimSpace(c.Prefix)
		if c.Prefix != "" && !strings.HasSuffix(c.Prefix, "/") {
			c.Prefix += "/"
		}
		out.S3 = &c
	}
	return out
}

// Validate checks a storage as Normalized would store it.
func (s Storage) Validate() error {
	s = s.Normalized()
	if err := ValidateStorageName(s.Name); err != nil {
		return err
	}
	switch s.Type {
	case StorageLocal:
		if s.Local == nil || s.S3 != nil {
			return errors.New("a local storage needs local settings only")
		}
		return s.Local.validate()
	case StorageS3:
		if s.S3 == nil || s.Local != nil {
			return errors.New("an s3 storage needs s3 settings only")
		}
		return s.S3.validate()
	default:
		return fmt.Errorf("storage type must be local or s3, got %q", s.Type)
	}
}

func (l LocalStorage) validate() error {
	if !filepath.IsAbs(l.Path) {
		return fmt.Errorf("the folder must be an absolute path like /mnt/backup, got %q", l.Path)
	}
	// A control character would break the storage.env line (and every deploy
	// that references the storage), so refuse it at save time.
	if hasControl(l.Path) {
		return fmt.Errorf("the folder path cannot contain control characters (newline, tab, NUL, ...), got %q", l.Path)
	}
	if len(l.Path) > maxLocalPathLen {
		return fmt.Errorf("the folder path can be at most %d bytes long, got %d", maxLocalPathLen, len(l.Path))
	}
	if l.Path == "/" {
		return errors.New("the folder cannot be / itself")
	}
	return nil
}

func (c S3Storage) validate() error {
	if err := validateEndpoint(c.Endpoint); err != nil {
		return err
	}
	if !regionRE.MatchString(c.Region) {
		return fmt.Errorf("%q is not a valid region", c.Region)
	}
	if !bucketRE.MatchString(c.Bucket) || strings.Contains(c.Bucket, "..") {
		return fmt.Errorf("%q is not a valid bucket name", c.Bucket)
	}
	if err := validatePrefix(c.Prefix); err != nil {
		return err
	}
	if c.AccessKeyID == "" || len(c.AccessKeyID) > 128 || strings.Contains(c.AccessKeyID, " ") {
		return errors.New("the access key id is required and cannot contain spaces")
	}
	// Like a local path: a control character would fail every deploy that
	// references the key id.
	if hasControl(c.AccessKeyID) {
		return errors.New("the access key id cannot contain control characters (newline, tab, NUL, ...)")
	}
	if c.SecretAccessKey == "" {
		return errors.New("the secret access key is required")
	}
	return nil
}

// validateEndpoint accepts https://host[:port], and http:// only for a
// loopback or private address (MinIO on the LAN): credentials must not cross
// the internet in clear text.
func validateEndpoint(endpoint string) error {
	if len(endpoint) > maxEndpointLen {
		return fmt.Errorf("the endpoint can be at most %d bytes long, got %d", maxEndpointLen, len(endpoint))
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("the endpoint must be a URL like https://s3.example.com, got %q", endpoint)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if privateHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("use https for %s: plain http is only allowed for a loopback or private address", u.Host)
	default:
		return fmt.Errorf("the endpoint must start with https://, got %q", endpoint)
	}
}

// hasControl reports whether s has a C0 control character or DEL.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func privateHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// validatePrefix accepts "" or "a/b/" style prefixes: no leading slash, no
// empty, "." or ".." segments.
func validatePrefix(p string) error {
	if p == "" {
		return nil
	}
	bad := len(p) > maxPrefixLen || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00\r\n\t")
	for _, seg := range strings.Split(strings.TrimSuffix(p, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			bad = true
		}
	}
	if bad {
		return fmt.Errorf("%q is not a valid prefix: use folder names separated by /, like winterflow/", p)
	}
	return nil
}

// Location is the human summary the index and the UI show: the folder, or
// endpoint/bucket/prefix.
func (s Storage) Location() string {
	switch {
	case s.Type == StorageLocal && s.Local != nil:
		return s.Local.Path
	case s.Type == StorageS3 && s.S3 != nil:
		return s.S3.Endpoint + "/" + s.S3.Bucket + "/" + s.S3.Prefix
	}
	return ""
}

// Summary is the index row of s.
func (s Storage) Summary() StorageSummary {
	return StorageSummary{Name: s.Name, Type: s.Type, Location: s.Location()}
}

// Masked returns a copy whose secret is the placeholder: the only form of a
// stored storage that leaves the agent.
func (s Storage) Masked() Storage {
	if s.S3 != nil {
		c := *s.S3
		c.SecretAccessKey = EncryptedSecret
		s.S3 = &c
	}
	if s.Local != nil {
		l := *s.Local
		s.Local = &l
	}
	return s
}

// --- WF_STORAGE_* variables ---------------------------------------------------

const (
	storageVarPrefix = "WF_STORAGE_"
	backupVarPrefix  = "WF_BACKUP_"
)

// Storage variable fields. A field that does not apply to the storage's type
// resolves to empty.
const (
	FieldType            = "TYPE"
	FieldPrefix          = "PREFIX"
	FieldPath            = "PATH"
	FieldEndpoint        = "ENDPOINT"
	FieldRegion          = "REGION"
	FieldBucket          = "BUCKET"
	FieldAccessKeyID     = "ACCESS_KEY_ID"
	FieldSecretAccessKey = "SECRET_ACCESS_KEY"
)

// storageFields is every field, longest first so suffix matching picks
// ACCESS_KEY_ID before any shorter field could match.
var storageFields = []string{FieldSecretAccessKey, FieldAccessKeyID, FieldEndpoint, FieldPrefix, FieldRegion, FieldBucket, FieldType, FieldPath}

// StorageVarName is the variable for field of the storage named name.
func StorageVarName(name, field string) string {
	return storageVarPrefix + varToken(name) + "_" + field
}

func varToken(name string) string { return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) }

// StorageRef is one parsed WF_STORAGE_<STORAGE>_<FIELD> reference.
type StorageRef struct {
	Var     string // the variable name, e.g. WF_STORAGE_BACKUP_DISK_PATH
	Storage string // the storage name, e.g. backup-disk
	Field   string // e.g. PATH
}

// Secret reports whether the reference resolves to a secret.
func (r StorageRef) Secret() bool { return r.Field == FieldSecretAccessKey }

// storageRefRE finds references the way compose interpolates them: $NAME
// or ${NAME...}.
var storageRefRE = regexp.MustCompile(`\$\{?(WF_(?:STORAGE|BACKUP)_[A-Z0-9_]+)`)

// ScanStorageRefs returns the WF_STORAGE_ / WF_BACKUP_ variable names the
// contents reference, sorted and unique. "$$" is compose's escape for a
// literal "$", so a match preceded by an odd run of "$" is not a reference.
func ScanStorageRefs(contents ...[]byte) []string {
	seen := map[string]bool{}
	for _, c := range contents {
		for _, m := range storageRefRE.FindAllSubmatchIndex(c, -1) {
			dollars := 0
			for i := m[0] - 1; i >= 0 && c[i] == '$'; i-- {
				dollars++
			}
			if dollars%2 == 1 {
				continue
			}
			seen[string(c[m[2]:m[3]])] = true
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// ParseStorageRef splits a referenced variable into storage and field. A
// WF_BACKUP_ reference names a backup of the app; there are none until
// backups exist (C2), so it is refused like any undeclared backup.
func ParseStorageRef(v string) (StorageRef, error) {
	rest, isStorage := strings.CutPrefix(v, storageVarPrefix)
	if !isStorage {
		if backup, ok := strings.CutPrefix(v, backupVarPrefix); ok {
			if token, _, ok := splitField(backup); ok {
				backup = token
			}
			return StorageRef{}, fmt.Errorf("%w: ${%s}: there is no backup %q in this app", ErrStorageRef, v, tokenName(backup))
		}
		return StorageRef{}, fmt.Errorf("%w: %s is not a storage variable", ErrStorageRef, v)
	}
	token, field, ok := splitField(rest)
	name := tokenName(token)
	if !ok || ValidateStorageName(name) != nil {
		return StorageRef{}, fmt.Errorf("%w: ${%s} is not a storage variable: use WF_STORAGE_<NAME>_<FIELD> with FIELD one of %s",
			ErrStorageRef, v, strings.Join([]string{FieldType, FieldPrefix, FieldPath, FieldEndpoint, FieldRegion, FieldBucket, FieldAccessKeyID, FieldSecretAccessKey}, ", "))
	}
	return StorageRef{Var: v, Storage: name, Field: field}, nil
}

// splitField cuts a known field suffix off "<TOKEN>_<FIELD>".
func splitField(s string) (token, field string, ok bool) {
	for _, f := range storageFields {
		if t, found := strings.CutSuffix(s, "_"+f); found && t != "" {
			return t, f, true
		}
	}
	return "", "", false
}

func tokenName(token string) string { return strings.ToLower(strings.ReplaceAll(token, "_", "-")) }

// CheckStorageRefs parses every reference and requires its storage to
// exist; the first problem is returned (wrapping ErrStorageRef).
func CheckStorageRefs(refs []string, exists func(name string) bool) ([]StorageRef, error) {
	out := make([]StorageRef, 0, len(refs))
	for _, v := range refs {
		ref, err := ParseStorageRef(v)
		if err != nil {
			return nil, err
		}
		if !exists(ref.Storage) {
			return nil, fmt.Errorf("%w: ${%s}: there is no storage %q on this server", ErrStorageRef, v, ref.Storage)
		}
		out = append(out, ref)
	}
	return out, nil
}

// StorageFieldValue is the value of field for s; secret is the decrypted
// secret access key (only read for that field).
func StorageFieldValue(s Storage, field, secret string) string {
	switch field {
	case FieldType:
		return string(s.Type)
	case FieldPath:
		if s.Local != nil {
			return s.Local.Path
		}
	case FieldPrefix:
		if s.S3 != nil {
			return s.S3.Prefix
		}
	case FieldEndpoint:
		if s.S3 != nil {
			return s.S3.Endpoint
		}
	case FieldRegion:
		if s.S3 != nil {
			return s.S3.Region
		}
	case FieldBucket:
		if s.S3 != nil {
			return s.S3.Bucket
		}
	case FieldAccessKeyID:
		if s.S3 != nil {
			return s.S3.AccessKeyID
		}
	case FieldSecretAccessKey:
		if s.S3 != nil {
			return secret
		}
	}
	return ""
}

// ReservedVariableError refuses a user variable named like a storage
// variable: those values only ever come from the server's storages.
func ReservedVariableError(name string) error {
	if strings.HasPrefix(name, storageVarPrefix) || strings.HasPrefix(name, backupVarPrefix) {
		return fmt.Errorf("%w: %s: variables starting with WF_STORAGE_ or WF_BACKUP_ are provided by the server's storages", ErrReservedVariable, name)
	}
	return nil
}

// --- compose file detection -----------------------------------------------

// ComposeFileNames are the files docker compose auto-detects, in its order
// of preference; ComposeOverrideFileNames the override files it adds.
var (
	ComposeFileNames         = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}
	ComposeOverrideFileNames = []string{"compose.override.yml", "compose.override.yaml", "docker-compose.override.yml", "docker-compose.override.yaml"}
)

// DetectComposeFiles picks the files docker compose would load from a
// folder without -f: the first ComposeFileNames entry that exists, plus the
// first existing override. nil when there is no compose file.
func DetectComposeFiles(exists func(name string) bool) []string {
	var out []string
	for _, n := range ComposeFileNames {
		if exists(n) {
			out = append(out, n)
			break
		}
	}
	if out == nil {
		return nil
	}
	for _, n := range ComposeOverrideFileNames {
		if exists(n) {
			return append(out, n)
		}
	}
	return out
}
