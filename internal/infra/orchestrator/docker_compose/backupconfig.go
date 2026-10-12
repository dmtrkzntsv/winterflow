package dockercompose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/pkg/fsutil"
)

// stopOffTimeout bounds removing the services of backups that are off (the
// stop itself waits 10 s at most).
const stopOffTimeout = 2 * time.Minute

// backupsKey is the key of the backups list (the overrides) in an app's
// committed config.json.
const backupsKey = "backups"

// appBackupConfig is what an app's compose files and config.json say about
// its backups.
type appBackupConfig struct {
	compose   recipe.Compose
	overrides []recipe.BackupConfig
	backups   []recipe.EffectiveBackup
	// composeOK and overridesOK say that compose / overrides were read and
	// parsed, so they are usable even when the effective list is not (an
	// invalid override still leaves the compose targets and the base).
	composeOK, overridesOK bool
}

// readOverrides is the backups list of the app's config.json (none when it
// has no such key).
func (r *Repository) readOverrides(dir string) ([]recipe.BackupConfig, error) {
	_, raw, err := r.readAppConfig(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // no config.json: no overrides
	}
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Backups []recipe.BackupConfig `json:"backups"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configRel, err)
	}
	return cfg.Backups, nil
}

// backupsBase identifies a stored backups list: a short hash of its
// canonical JSON (none and [] are the same). A client sends the one it read
// with its update; a different one means someone else saved in between.
func backupsBase(overrides []recipe.BackupConfig) string {
	raw := []byte("[]")
	if len(overrides) > 0 {
		var err error
		if raw, err = json.Marshal(overrides); err != nil {
			return ""
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// appBackups reads and validates an app's backups: the recipe in its compose
// files with the config.json overrides on top. File reads only. On an error
// the parts that did read are still returned (composeOK, overridesOK).
func (r *Repository) appBackups(dir string, files []string) (appBackupConfig, error) {
	var cfg appBackupConfig
	contents, readErr := composeContents(dir, files)
	var parseErr error
	if readErr == nil {
		cfg.compose, parseErr = recipe.ParseCompose(contents...)
		cfg.composeOK = parseErr == nil
	}
	overrides, overridesErr := r.readOverrides(dir)
	cfg.overrides, cfg.overridesOK = overrides, overridesErr == nil
	if readErr != nil {
		return cfg, fmt.Errorf("read compose files: %w", readErr)
	}
	if parseErr != nil {
		return cfg, parseErr
	}
	if overridesErr != nil {
		return cfg, overridesErr
	}
	backups, err := recipe.Effective(cfg.compose, overrides)
	if err != nil {
		return cfg, err
	}
	cfg.backups = backups
	return cfg, nil
}

// writeOverrides replaces the backups list in the app's config.json (none:
// the key is removed). Every other key is kept as is.
func (r *Repository) writeOverrides(dir string, overrides []recipe.BackupConfig) error {
	cfg, _, err := r.readAppConfig(dir)
	if err != nil {
		return err
	}
	if len(overrides) == 0 {
		delete(cfg, backupsKey)
	} else {
		cfg[backupsKey] = overrides
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return fsutil.WriteFile(filepath.Join(dir, configRel), raw, 0o644)
}

// keepBackups carries the stored backups list into a new config blob that
// has none: the app editor sends the whole config but doesn't know the
// backups, which are edited on their own (backup.update).
func keepBackups(next, prev []byte) []byte {
	var n map[string]json.RawMessage
	if json.Unmarshal(next, &n) != nil || n == nil {
		return next
	}
	if _, ok := n[backupsKey]; ok {
		return next
	}
	var p map[string]json.RawMessage
	if json.Unmarshal(prev, &p) != nil || p[backupsKey] == nil {
		return next
	}
	n[backupsKey] = p[backupsKey]
	out, err := json.Marshal(n)
	if err != nil {
		return next
	}
	return out
}

// instanceKey is the key of a template instance's marker in config.json;
// backupDefaultsKey the marker's list of backups to add (the API sends it
// on an upgrade that brings backups new to the app).
const (
	instanceKey       = "instance"
	backupDefaultsKey = "backup_defaults"
)

// applyBackupDefaults adds each instance.backup_defaults entry to the
// backups list unless the list already has that name (a stored setting,
// Backups-tab changes included, always wins), and drops backup_defaults
// from the marker: it is an instruction for this save, not state. A blob
// without defaults, or one that doesn't parse, comes back unchanged.
func applyBackupDefaults(cfg []byte) []byte {
	var top map[string]json.RawMessage
	if json.Unmarshal(cfg, &top) != nil || top == nil {
		return cfg
	}
	var marker map[string]json.RawMessage
	if json.Unmarshal(top[instanceKey], &marker) != nil || marker[backupDefaultsKey] == nil {
		return cfg
	}
	var defaults, list []recipe.BackupConfig
	if json.Unmarshal(marker[backupDefaultsKey], &defaults) != nil {
		return cfg
	}
	if raw, ok := top[backupsKey]; ok && json.Unmarshal(raw, &list) != nil {
		return cfg
	}
	have := map[string]bool{}
	for _, b := range list {
		have[b.Name] = true
	}
	for _, d := range defaults {
		if !have[d.Name] {
			list = append(list, d)
			have[d.Name] = true
		}
	}
	delete(marker, backupDefaultsKey)
	rawMarker, err := json.Marshal(marker)
	if err != nil {
		return cfg
	}
	top[instanceKey] = rawMarker
	if len(list) > 0 {
		rawList, err := json.Marshal(list)
		if err != nil {
			return cfg
		}
		top[backupsKey] = rawList
	}
	out, err := json.Marshal(top)
	if err != nil {
		return cfg
	}
	return out
}

// cleanBackups validates the app's backups after a save wrote its files and
// drops overrides of backups the recipe no longer has (warnings).
func (r *Repository) cleanBackups(dir string) ([]string, error) {
	contents, err := composeContents(dir, r.appComposeFiles(dir))
	if err != nil {
		return nil, fmt.Errorf("read compose files: %w", err)
	}
	comp, err := recipe.ParseCompose(contents...)
	if err != nil {
		return nil, err
	}
	overrides, err := r.readOverrides(dir)
	if err != nil {
		return nil, err
	}
	kept, warnings := recipe.Clean(comp, overrides)
	if _, err := recipe.Effective(comp, kept); err != nil {
		return nil, err
	}
	if len(kept) != len(overrides) || (len(kept) > 0 && !reflect.DeepEqual(kept, overrides)) {
		if err := r.writeOverrides(dir, kept); err != nil {
			return nil, err
		}
	}
	return warnings, nil
}

// backupProfiles are the compose profiles of the app's enabled custom
// backups, sorted.
func backupProfiles(backups []recipe.EffectiveBackup) []string {
	var out []string
	for _, b := range backups {
		if b.Type == recipe.TypeCustom && b.Enabled {
			out = append(out, recipe.Profile(b.Name))
		}
	}
	sort.Strings(out)
	return out
}

// userProfiles are the profiles of the user's COMPOSE_PROFILES (.env.secrets
// wins over .env), in file order.
func userProfiles(dir string) []string {
	user := ""
	for _, rel := range []string{envRel, envSecretsRel} {
		if raw, err := os.ReadFile(filepath.Join(dir, rel)); err == nil {
			if v, ok := parseEnv(raw)["COMPOSE_PROFILES"]; ok {
				user = v
			}
		}
	}
	var out []string
	for _, p := range strings.Split(user, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// composeProfiles is what to pass as --profile: nothing while no custom
// backup is on and the user names no wf-backup-* profile (compose then reads
// COMPOSE_PROFILES from the env files as before); otherwise the user's
// COMPOSE_PROFILES plus the enabled backups' profiles, since an explicit
// --profile makes compose ignore COMPOSE_PROFILES. A wf-backup-* profile of
// the user's is dropped unless that backup is on: winterflow owns them, and
// activating the service of a backup that is off would start it with empty
// WF_BACKUP_ values. (If nothing else is left, nothing is passed and the
// deploy removes that service afterwards, see offServices.)
func (r *Repository) composeProfiles(dir string, backups []recipe.EffectiveBackup) []string {
	profiles := backupProfiles(backups)
	owned := len(profiles) > 0
	for _, p := range userProfiles(dir) {
		if strings.HasPrefix(p, recipe.ProfilePrefix) {
			owned = owned || !slices.Contains(profiles, p)
			continue
		}
		if !slices.Contains(profiles, p) {
			profiles = append(profiles, p)
		}
	}
	if !owned || len(profiles) == 0 {
		return nil
	}
	sort.Strings(profiles)
	return profiles
}

// activeProfiles are the profiles a deploy with the given --profile list
// activates: those, or, when none is passed, the user's COMPOSE_PROFILES
// that compose then honours (minus the wf-backup-* ones composeProfiles
// dropped).
func activeProfiles(dir string, passed []string) []string {
	if len(passed) > 0 {
		return passed
	}
	var out []string
	for _, p := range userProfiles(dir) {
		if !strings.HasPrefix(p, recipe.ProfilePrefix) {
			out = append(out, p)
		}
	}
	return out
}

// appProfiles are the --profile flags every compose verb on the app carries
// (the same list a deploy passes, so a profile-activated backup service is
// seen by stop, restart, pull and logs too); none when the recipe can't be
// read.
func (r *Repository) appProfiles(dir string, files []string) []string {
	cfg, err := r.appBackups(dir, files)
	if err != nil {
		return nil
	}
	return r.composeProfiles(dir, cfg.backups)
}

// offServices are the services of disabled custom backups that no active
// profile keeps: `up --remove-orphans` leaves a service whose profile was
// turned off running, so the deploy removes it explicitly.
func offServices(comp recipe.Compose, backups []recipe.EffectiveBackup, active []string) []string {
	var out []string
	for _, b := range backups {
		if b.Type != recipe.TypeCustom || b.Enabled || slices.Contains(out, b.Service) {
			continue
		}
		on := false
		for _, p := range comp.Services[b.Service] {
			on = on || slices.Contains(active, p)
		}
		if !on {
			out = append(out, b.Service)
		}
	}
	sort.Strings(out)
	return out
}

// stopOffServices removes the containers of offServices (one compose call
// with their profiles active so compose knows them). Not an error when
// they don't run.
func (r *Repository) stopOffServices(ctx context.Context, appID string, files []string, cfg appBackupConfig, active []string) error {
	services := offServices(cfg.compose, cfg.backups, active)
	if len(services) == 0 {
		return nil
	}
	var profiles []string
	for _, b := range cfg.backups {
		if b.Type == recipe.TypeCustom && !b.Enabled {
			profiles = append(profiles, recipe.Profile(b.Name))
		}
	}
	args := r.composeArgsFor(appID, files, profiles, append([]string{"rm", "--force", "--stop"}, services...)...)
	ctx, cancel := context.WithTimeout(ctx, stopOffTimeout)
	defer cancel()
	return r.composeExec(ctx, appID, args)
}
