package model

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func s3Storage(mut func(*S3Storage)) Storage {
	c := &S3Storage{Endpoint: "https://s3.example.com", Region: "eu-central-1", Bucket: "backups",
		AccessKeyID: "AKIA", SecretAccessKey: "CIPHERTEXT"}
	if mut != nil {
		mut(c)
	}
	return Storage{Name: "hetzner", Type: StorageS3, S3: c}
}

func TestValidateStorageName(t *testing.T) {
	for _, ok := range []string{"a", "backup-disk", "s3", "x1-2", strings.Repeat("a", 32)} {
		if err := ValidateStorageName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "A", "a_b", "a.b", "a b", strings.Repeat("a", 33)} {
		if err := ValidateStorageName(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestStorageValidate(t *testing.T) {
	local := func(p string) Storage {
		return Storage{Name: "disk", Type: StorageLocal, Local: &LocalStorage{Path: p}}
	}
	valid := map[string]Storage{
		"local":                local("/mnt/backup"),
		"local trailing slash": local("/mnt/backup/"),
		"s3":                   s3Storage(nil),
		"s3 no region":         s3Storage(func(c *S3Storage) { c.Region = "" }),
		"s3 prefix":            s3Storage(func(c *S3Storage) { c.Prefix = "tenants/eu" }),
		"http loopback":        s3Storage(func(c *S3Storage) { c.Endpoint = "http://127.0.0.1:9000" }),
		"http private":         s3Storage(func(c *S3Storage) { c.Endpoint = "http://192.168.1.10:9000/" }),
		"http localhost":       s3Storage(func(c *S3Storage) { c.Endpoint = "http://localhost:9000" }),
		"placeholder secret":   s3Storage(func(c *S3Storage) { c.SecretAccessKey = EncryptedSecret }),
		"local path at cap":    local("/mnt/" + strings.Repeat("a", maxLocalPathLen-5)),
		"endpoint at cap":      s3Storage(func(c *S3Storage) { c.Endpoint = "https://" + strings.Repeat("a", maxEndpointLen-12) + ".com" }),
	}
	for name, s := range valid {
		if err := s.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	invalid := map[string]struct {
		s    Storage
		want string
	}{
		"bad name":          {Storage{Name: "Disk", Type: StorageLocal, Local: &LocalStorage{Path: "/mnt"}}, "not a valid storage name"},
		"bad type":          {Storage{Name: "x", Type: "ftp"}, "local or s3"},
		"local without":     {Storage{Name: "x", Type: StorageLocal}, "local settings only"},
		"local with s3":     {Storage{Name: "x", Type: StorageLocal, Local: &LocalStorage{Path: "/mnt"}, S3: &S3Storage{}}, "local settings only"},
		"s3 without":        {Storage{Name: "x", Type: StorageS3}, "s3 settings only"},
		"relative":          {local("backups"), "absolute path"},
		"root":              {local("/"), "cannot be /"},
		"nul":               {local("/mnt/ba\x00ckup"), "control characters"},
		"newline":           {local("/mnt/ba\nckup"), "control characters"},
		"carriage return":   {local("/mnt/ba\rckup"), "control characters"},
		"tab":               {local("/mnt/ba\tckup"), "control characters"},
		"escape":            {local("/mnt/ba\x1bckup"), "control characters"},
		"delete":            {local("/mnt/ba\x7fckup"), "control characters"},
		"root dotted":       {local("/mnt/.."), "cannot be /"},
		"http public":       {s3Storage(func(c *S3Storage) { c.Endpoint = "http://s3.example.com" }), "use https"},
		"ftp":               {s3Storage(func(c *S3Storage) { c.Endpoint = "ftp://s3.example.com" }), "must start with https"},
		"endpoint path":     {s3Storage(func(c *S3Storage) { c.Endpoint = "https://s3.example.com/bucket" }), "URL like"},
		"endpoint userinfo": {s3Storage(func(c *S3Storage) { c.Endpoint = "https://u:p@s3.example.com" }), "URL like"},
		"endpoint empty":    {s3Storage(func(c *S3Storage) { c.Endpoint = "" }), "URL like"},
		"bucket":            {s3Storage(func(c *S3Storage) { c.Bucket = "Bad_Bucket" }), "bucket name"},
		"bucket dots":       {s3Storage(func(c *S3Storage) { c.Bucket = "a..b" }), "bucket name"},
		"region":            {s3Storage(func(c *S3Storage) { c.Region = "EU Central" }), "region"},
		"prefix leading":    {s3Storage(func(c *S3Storage) { c.Prefix = "/x/" }), "valid prefix"},
		"prefix dotdot":     {s3Storage(func(c *S3Storage) { c.Prefix = "a/../b/" }), "valid prefix"},
		"prefix empty seg":  {s3Storage(func(c *S3Storage) { c.Prefix = "a//b/" }), "valid prefix"},
		"no key id":         {s3Storage(func(c *S3Storage) { c.AccessKeyID = "" }), "access key id"},
		"no secret":         {s3Storage(func(c *S3Storage) { c.SecretAccessKey = "" }), "secret access key"},
		"path too long":     {local("/mnt/" + strings.Repeat("a", maxLocalPathLen-4)), "at most 512"},
		"endpoint too long": {s3Storage(func(c *S3Storage) { c.Endpoint = "https://" + strings.Repeat("a", maxEndpointLen-11) + ".com" }), "at most 255"},
		"key id nul":        {s3Storage(func(c *S3Storage) { c.AccessKeyID = "AK\x00IA" }), "control characters"},
		"key id escape":     {s3Storage(func(c *S3Storage) { c.AccessKeyID = "AK\x1bIA" }), "control characters"},
		"key id delete":     {s3Storage(func(c *S3Storage) { c.AccessKeyID = "AK\x7fIA" }), "control characters"},
		"key id bell":       {s3Storage(func(c *S3Storage) { c.AccessKeyID = "AK\aIA" }), "control characters"},
		"key id space":      {s3Storage(func(c *S3Storage) { c.AccessKeyID = "AK IA" }), "cannot contain spaces"},
		"prefix too long":   {s3Storage(func(c *S3Storage) { c.Prefix = strings.Repeat("p/", 255) + "pp" }), "valid prefix"},
	}
	for name, tc := range invalid {
		err := tc.s.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// The longest valid storage still fits server_storages.location
// (VARCHAR(1024)): one oversized row would fail the whole index sync.
func TestStorageLocationFitsTheIndex(t *testing.T) {
	longest := []Storage{
		{Name: "disk", Type: StorageLocal, Local: &LocalStorage{Path: "/" + strings.Repeat("a", maxLocalPathLen-1)}},
		s3Storage(func(c *S3Storage) {
			c.Endpoint = "https://" + strings.Repeat("a", maxEndpointLen-12) + ".com"
			c.Bucket = strings.Repeat("b", 63)
			c.Prefix = strings.Repeat("p/", 256)
		}),
	}
	for _, s := range longest {
		if err := s.Validate(); err != nil {
			t.Fatalf("%s: %v", s.Type, err)
		}
		if n := len(s.Normalized().Location()); n > 1024 {
			t.Fatalf("%s: location is %d bytes", s.Type, n)
		}
	}
}

func TestStorageNormalizedAndLocation(t *testing.T) {
	s := s3Storage(func(c *S3Storage) { c.Endpoint = " https://s3.example.com/ "; c.Prefix = "tenants" }).Normalized()
	if s.S3.Endpoint != "https://s3.example.com" || s.S3.Prefix != "tenants/" {
		t.Fatalf("normalized = %+v", s.S3)
	}
	if got := s.Location(); got != "https://s3.example.com/backups/tenants/" {
		t.Fatalf("location = %q", got)
	}
	l := Storage{Name: "disk", Type: StorageLocal, Local: &LocalStorage{Path: "/mnt/usb/backups/"}}.Normalized()
	if l.Local.Path != "/mnt/usb/backups" || l.Location() != "/mnt/usb/backups" {
		t.Fatalf("local = %+v", l.Local)
	}
	if (Storage{Type: StorageS3}).Location() != "" {
		t.Fatal("an incomplete storage has no location")
	}
	if got := s.Summary(); got != (StorageSummary{Name: "hetzner", Type: StorageS3, Location: "https://s3.example.com/backups/tenants/"}) {
		t.Fatalf("summary = %+v", got)
	}
}

func TestStorageMasked(t *testing.T) {
	s := s3Storage(nil)
	m := s.Masked()
	if m.S3.SecretAccessKey != EncryptedSecret || s.S3.SecretAccessKey != "CIPHERTEXT" {
		t.Fatalf("masked %q, original %q", m.S3.SecretAccessKey, s.S3.SecretAccessKey)
	}
	l := Storage{Name: "d", Type: StorageLocal, Local: &LocalStorage{Path: "/mnt"}}
	if m := l.Masked(); m.Local == l.Local || m.Local.Path != "/mnt" {
		t.Fatal("masking must copy, not alias")
	}
}

func TestScanStorageRefs(t *testing.T) {
	compose := []byte(`services:
  app:
    environment:
      - S3_BUCKET=${WF_STORAGE_HETZNER_BUCKET}
      - KEY=$WF_STORAGE_HETZNER_SECRET_ACCESS_KEY
      - DEFAULTED=${WF_STORAGE_BACKUP_DISK_PATH:-/tmp}
      - LITERAL=$${WF_STORAGE_ESCAPED_PATH}
      - REF_AFTER_ESCAPE=$$$WF_STORAGE_X_TYPE
      - B=${WF_BACKUP_DB_PREFIX}
      - DUP=${WF_STORAGE_HETZNER_BUCKET}
      - LOWER=${wf_storage_x_type}
`)
	got := ScanStorageRefs(compose, []byte("x: ${WF_STORAGE_OTHER_TYPE}"))
	want := []string{"WF_BACKUP_DB_PREFIX", "WF_STORAGE_BACKUP_DISK_PATH", "WF_STORAGE_HETZNER_BUCKET",
		"WF_STORAGE_HETZNER_SECRET_ACCESS_KEY", "WF_STORAGE_OTHER_TYPE", "WF_STORAGE_X_TYPE"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("refs = %v\nwant  %v", got, want)
	}
	if got := ScanStorageRefs(nil); len(got) != 0 {
		t.Fatalf("no content: %v", got)
	}
}

func TestParseStorageRef(t *testing.T) {
	cases := map[string]StorageRef{
		"WF_STORAGE_HETZNER_BUCKET":            {Storage: "hetzner", Field: FieldBucket},
		"WF_STORAGE_BACKUP_DISK_PATH":          {Storage: "backup-disk", Field: FieldPath},
		"WF_STORAGE_A_SECRET_ACCESS_KEY":       {Storage: "a", Field: FieldSecretAccessKey},
		"WF_STORAGE_A_SECRET_ACCESS_KEY_ID":    {Storage: "a-secret", Field: FieldAccessKeyID},
		"WF_STORAGE_X1_TYPE":                   {Storage: "x1", Field: FieldType},
		"WF_STORAGE_MY_PREFIX_PREFIX":          {Storage: "my-prefix", Field: FieldPrefix},
		"WF_STORAGE_EU_ENDPOINT":               {Storage: "eu", Field: FieldEndpoint},
		"WF_STORAGE_EU_REGION":                 {Storage: "eu", Field: FieldRegion},
		"WF_STORAGE_HETZNER_ACCESS_KEY_ID":     {Storage: "hetzner", Field: FieldAccessKeyID},
		"WF_STORAGE_HETZNER_SECRET_ACCESS_KEY": {Storage: "hetzner", Field: FieldSecretAccessKey},
	}
	for v, want := range cases {
		want.Var = v
		got, err := ParseStorageRef(v)
		if err != nil || got != want {
			t.Errorf("%s: %+v, %v; want %+v", v, got, err, want)
		}
	}
	if !(StorageRef{Field: FieldSecretAccessKey}).Secret() || (StorageRef{Field: FieldBucket}).Secret() {
		t.Fatal("only the secret access key is a secret")
	}
	for v, want := range map[string]string{
		"WF_STORAGE_HETZNER_COLOR": "is not a storage variable",
		"WF_STORAGE_TYPE":          "is not a storage variable",
		"WF_STORAGE__X_TYPE":       "is not a storage variable",
		"WF_BACKUP_DB_PATH":        `there is no backup "db" in this app`,
		"WF_BACKUP_MEDIA_FILES":    `there is no backup "media-files" in this app`,
		"PATH":                     "is not a storage variable",
	} {
		_, err := ParseStorageRef(v)
		if err == nil || !errors.Is(err, ErrStorageRef) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", v, err, want)
		}
	}
}

func TestCheckStorageRefs(t *testing.T) {
	exists := func(n string) bool { return n == "hetzner" }
	refs, err := CheckStorageRefs([]string{"WF_STORAGE_HETZNER_BUCKET", "WF_STORAGE_HETZNER_TYPE"}, exists)
	if err != nil || len(refs) != 2 || refs[0].Field != FieldBucket {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	_, err = CheckStorageRefs([]string{"WF_STORAGE_HETZNER_BUCKET", "WF_STORAGE_GONE_PATH"}, exists)
	if !errors.Is(err, ErrStorageRef) || err.Error() != `unresolved storage variable: ${WF_STORAGE_GONE_PATH}: there is no storage "gone" on this server` {
		t.Fatalf("err = %v", err)
	}
	if _, err := CheckStorageRefs([]string{"WF_BACKUP_DB_PATH"}, exists); !errors.Is(err, ErrStorageRef) {
		t.Fatalf("backup ref: %v", err)
	}
}

func TestStorageFieldValue(t *testing.T) {
	s := s3Storage(func(c *S3Storage) { c.Prefix = "p/" })
	l := Storage{Name: "d", Type: StorageLocal, Local: &LocalStorage{Path: "/mnt/b"}}
	cases := []struct {
		s     Storage
		field string
		want  string
	}{
		{s, FieldType, "s3"}, {s, FieldPrefix, "p/"}, {s, FieldPath, ""}, {s, FieldEndpoint, "https://s3.example.com"},
		{s, FieldRegion, "eu-central-1"}, {s, FieldBucket, "backups"}, {s, FieldAccessKeyID, "AKIA"},
		{s, FieldSecretAccessKey, "plain"}, {s, "NOPE", ""},
		{l, FieldType, "local"}, {l, FieldPath, "/mnt/b"}, {l, FieldPrefix, ""}, {l, FieldEndpoint, ""},
		{l, FieldRegion, ""}, {l, FieldBucket, ""}, {l, FieldAccessKeyID, ""}, {l, FieldSecretAccessKey, ""},
	}
	for _, tc := range cases {
		if got := StorageFieldValue(tc.s, tc.field, "plain"); got != tc.want {
			t.Errorf("%s %s = %q, want %q", tc.s.Type, tc.field, got, tc.want)
		}
	}
	if got := StorageVarName("backup-disk", FieldPath); got != "WF_STORAGE_BACKUP_DISK_PATH" {
		t.Fatalf("var name = %q", got)
	}
}

func TestReservedVariableError(t *testing.T) {
	for _, n := range []string{"WF_STORAGE_X_PATH", "WF_BACKUP_DB_PATH", "WF_STORAGE_"} {
		if err := ReservedVariableError(n); !errors.Is(err, ErrReservedVariable) || !strings.Contains(err.Error(), n) {
			t.Errorf("%s: %v", n, err)
		}
	}
	for _, n := range []string{"PORT", "WF_OTHER", "MY_WF_STORAGE_X"} {
		if err := ReservedVariableError(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
}

func TestDetectComposeFiles(t *testing.T) {
	has := func(names ...string) func(string) bool {
		set := map[string]bool{}
		for _, n := range names {
			set[n] = true
		}
		return func(n string) bool { return set[n] }
	}
	cases := []struct {
		files []string
		want  []string
	}{
		{nil, nil},
		{[]string{"compose.override.yml"}, nil},
		{[]string{"docker-compose.yml"}, []string{"docker-compose.yml"}},
		{[]string{"compose.yml", "compose.yaml"}, []string{"compose.yaml"}},
		{[]string{"docker-compose.yaml", "docker-compose.yml"}, []string{"docker-compose.yml"}},
		{[]string{"compose.yml", "docker-compose.override.yml", "compose.override.yaml"}, []string{"compose.yml", "compose.override.yaml"}},
	}
	for _, tc := range cases {
		if got := DetectComposeFiles(has(tc.files...)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: got %v, want %v", tc.files, got, tc.want)
		}
	}
}
