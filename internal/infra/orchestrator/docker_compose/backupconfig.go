package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"winterflow/internal/domain/backup/recipe"
)

// backupsKey is the key of the backups list (the overrides) in an app's
// committed config.json.
const backupsKey = "backups"

// appBackupConfig is what an app's compose files and config.json say about
// its backups.
type appBackupConfig struct {
	compose   recipe.Compose
	overrides []recipe.BackupConfig
	backups   []recipe.EffectiveBackup
}

// readOverrides is the backups list of the app's config.json (none when it
// has no such key).
func (r *Repository) readOverrides(dir string) ([]recipe.BackupConfig, error) {
	_, raw, err := r.readAppConfig(dir)
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

// appBackups reads and validates an app's backups: the recipe in its compose
// files with the config.json overrides on top. File reads only.
func (r *Repository) appBackups(dir string, files []string) (appBackupConfig, error) {
	contents, err := composeContents(dir, files)
	if err != nil {
		return appBackupConfig{}, fmt.Errorf("read compose files: %w", err)
	}
	comp, err := recipe.ParseCompose(contents...)
	if err != nil {
		return appBackupConfig{}, err
	}
	overrides, err := r.readOverrides(dir)
	if err != nil {
		return appBackupConfig{}, err
	}
	backups, err := recipe.Effective(comp, overrides)
	if err != nil {
		return appBackupConfig{}, err
	}
	return appBackupConfig{compose: comp, overrides: overrides, backups: backups}, nil
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
	return os.WriteFile(filepath.Join(dir, configRel), raw, 0o644)
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

// composeProfiles is what to pass as --profile: nothing while no custom
// backup is on (compose then reads COMPOSE_PROFILES from the env files as
// before); otherwise the user's COMPOSE_PROFILES plus the backups' profiles,
// since an explicit --profile makes compose ignore COMPOSE_PROFILES.
func (r *Repository) composeProfiles(dir string, backups []recipe.EffectiveBackup) []string {
	profiles := backupProfiles(backups)
	if len(profiles) == 0 {
		return nil
	}
	user := ""
	for _, rel := range []string{envRel, envSecretsRel} {
		if raw, err := os.ReadFile(filepath.Join(dir, rel)); err == nil {
			if v, ok := parseEnv(raw)["COMPOSE_PROFILES"]; ok {
				user = v
			}
		}
	}
	for _, p := range strings.Split(user, ",") {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(profiles, p) {
			profiles = append(profiles, p)
		}
	}
	sort.Strings(profiles)
	return profiles
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
	return r.composeExec(ctx, appID, args)
}
