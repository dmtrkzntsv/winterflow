package command

import (
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
)

func TestBackupRequestsValidate(t *testing.T) {
	ok := []interface{ Validate() error }{
		UpdateBackupsRequest{AppID: "a1", Backups: []recipe.BackupConfig{{Name: "db", Enabled: true, Storage: "s3"}, {Name: "media"}}},
		UpdateBackupsRequest{AppID: "a1"},
		ListRestorePointsRequest{AppID: "a1", Backup: "db"},
		RunBackupRequest{AppID: "a1", Backup: "db"},
		DeleteRestorePointRequest{AppID: "a1", Backup: "db", Point: "20261011T120000Z-manual"},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	for _, tc := range []struct {
		r    interface{ Validate() error }
		want string
	}{
		{UpdateBackupsRequest{AppID: ".."}, "invalid app id"},
		{UpdateBackupsRequest{AppID: "a1", Backups: []recipe.BackupConfig{{Name: "DB"}}}, "backups[0].name"},
		{UpdateBackupsRequest{AppID: "a1", Backups: []recipe.BackupConfig{{Name: "db"}, {Name: "db"}}}, `backups[1].name: "db" is listed twice`},
		{UpdateBackupsRequest{AppID: "a1", Backups: []recipe.BackupConfig{{Name: "db", Enabled: true}}}, "backups[0].storage: an enabled backup needs a storage"},
		{ListRestorePointsRequest{AppID: "a/b", Backup: "db"}, "invalid app id"},
		{RunBackupRequest{AppID: "a1", Backup: ""}, "is not a valid backup name"},
		{DeleteRestorePointRequest{AppID: "a1", Backup: "db", Point: "../x"}, "invalid restore point id"},
	} {
		if err := tc.r.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.r, err, tc.want)
		}
	}
}

func TestValidPointID(t *testing.T) {
	for _, id := range []string{"20261011T120000Z-scheduled", "20261011T120000Z-manual", "20261011T120000Z-pre-restore"} {
		if !ValidPointID(id) {
			t.Errorf("%s should be valid", id)
		}
	}
	for _, id := range []string{"", "20261011T120000Z", "20261011T120000Z-other", "2026101aT120000Z-manual", "20261011X120000Z-manual",
		"20261011T120000Y-manual", "20261011T120000Z_manual", "../20261011T120000Z-manual"} {
		if ValidPointID(id) {
			t.Errorf("%s should be invalid", id)
		}
	}
}

func TestDetached(t *testing.T) {
	if !TypeBackupRun.Detached() || TypeBackupPoints.Detached() || TypeAppSave.Detached() {
		t.Fatal("only backup.run waits for long work")
	}
}

// A config blob that is not a JSON object is refused: the agent keeps keys
// in it (a `null` one crashed backup.update).
func TestSaveAppRequestRefusesNonObjectConfig(t *testing.T) {
	for _, cfg := range []string{"null", "[]", "5", `"x"`, "{", " null "} {
		err := SaveAppRequest{App: AppPayload{AppID: "a1", Config: []byte(cfg)}}.Validate()
		if err == nil || !strings.Contains(err.Error(), "JSON object") {
			t.Errorf("config %q: err = %v", cfg, err)
		}
	}
	for _, cfg := range []string{"", "{}", ` {"name":"x"} `} {
		if err := (SaveAppRequest{App: AppPayload{AppID: "a1", Config: []byte(cfg)}}).Validate(); err != nil {
			t.Errorf("config %q: %v", cfg, err)
		}
	}
}
