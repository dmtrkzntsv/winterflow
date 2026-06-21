package dockercompose

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"winterflow/internal/domain/command"
)

// ansiRegexp matches ANSI escape sequences (e.g. \x1b[31m) so we can strip
// colour codes from container output.
var ansiRegexp = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// GetLogs returns recent log entries for an app by shelling out to
// `docker compose logs`. It parses the "<container> | <timestamp> <message>"
// format compose emits with --timestamps, strips ANSI codes, and classifies a
// best-effort log level. (v1 used the Docker SDK; v2 stays CLI-only.)
func (r *Repository) GetLogs(ctx context.Context, in command.GetLogsRequest) (command.GetLogsResponse, error) {
	resp := command.GetLogsResponse{AppID: in.AppID, Logs: []command.LogEntry{}}

	runDir := path.Join(r.cfg.GetAppsDir(), in.AppID)
	if _, err := os.Stat(runDir); err != nil {
		// Not deployed: no logs, not an error.
		return resp, nil
	}

	args := []string{"compose", "--project-name", projectName(in.AppID), "logs", "--no-color", "--timestamps"}
	if in.Tail > 0 {
		args = append(args, "--tail", strconv.Itoa(int(in.Tail)))
	} else {
		args = append(args, "--tail", "all")
	}
	if in.Since > 0 {
		args = append(args, "--since", strconv.FormatInt(in.Since, 10))
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = runDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Some compose versions exit non-zero when there are no containers; if we
		// captured nothing, return an empty (non-error) result so the UI shows
		// "no logs" rather than an error.
		if stdout.Len() == 0 {
			r.log.Warn("compose logs failed", "app_id", in.AppID, "stderr", strings.TrimSpace(stderr.String()))
			return resp, nil
		}
	}

	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if entry, ok := parseComposeLogLine(scanner.Text()); ok {
			resp.Logs = append(resp.Logs, entry)
		}
	}
	return resp, nil
}

// parseComposeLogLine parses a single `docker compose logs --timestamps` line of
// the form "<container>  | <rfc3339> <message>" into a LogEntry. The container
// prefix and timestamp are both optional/best-effort.
func parseComposeLogLine(line string) (command.LogEntry, bool) {
	line = sanitizeLog(line)
	if strings.TrimSpace(line) == "" {
		return command.LogEntry{}, false
	}

	entry := command.LogEntry{Timestamp: time.Now().Unix()}

	// Split off the "container | " prefix compose adds.
	if i := strings.Index(line, "|"); i > 0 {
		entry.Container = strings.TrimSpace(line[:i])
		line = strings.TrimSpace(line[i+1:])
	}

	// Leading RFC3339 timestamp from --timestamps.
	if sp := strings.SplitN(line, " ", 2); len(sp) == 2 {
		if ts, err := time.Parse(time.RFC3339Nano, sp[0]); err == nil {
			entry.Timestamp = ts.Unix()
			line = sp[1]
		}
	}

	entry.Message = line
	entry.Level = detectLogLevel(line)
	return entry, true
}

// sanitizeLog strips ANSI codes and a UTF-8 BOM and guarantees valid UTF-8.
func sanitizeLog(msg string) string {
	msg = ansiRegexp.ReplaceAllString(msg, "")
	msg = strings.TrimPrefix(msg, "\uFEFF")
	return strings.ToValidUTF8(strings.TrimRight(msg, "\r\n"), "")
}

// detectLogLevel performs best-effort level detection from common textual
// prefixes (ported from v1).
func detectLogLevel(msg string) command.LogLevel {
	upper := strings.ToUpper(msg)
	switch {
	case strings.HasPrefix(upper, "TRACE"):
		return command.LogLevelTrace
	case strings.HasPrefix(upper, "DEBUG"):
		return command.LogLevelDebug
	case strings.HasPrefix(upper, "INFO"):
		return command.LogLevelInfo
	case strings.HasPrefix(upper, "WARN"), strings.HasPrefix(upper, "WARNING"):
		return command.LogLevelWarn
	case strings.HasPrefix(upper, "ERROR"):
		return command.LogLevelError
	case strings.HasPrefix(upper, "FATAL"):
		return command.LogLevelFatal
	default:
		return command.LogLevelUnknown
	}
}
