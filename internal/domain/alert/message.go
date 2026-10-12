package alert

import (
	"fmt"
	"strings"
	"time"
)

// Names are the human names of a target.
type Names struct{ Server, App string }

func (n Names) label() string {
	if n.App == "" {
		return n.Server
	}
	return n.Server + " / " + n.App
}

// Message is a notification's title and body.
type Message struct{ Title, Body string }

// Transition is what a notification describes.
type Transition struct {
	Kind      EventKind
	Info      MetricInfo
	Names     Names
	Value     float64
	Threshold float64
	Duration  time.Duration
	Link      string
}

// FormatValue renders v in the metric's unit ("93%", "2.31", "12.4 MB/s",
// "71 °C", "14.2 GB").
func FormatValue(info MetricInfo, v float64) string {
	switch info.Unit {
	case UnitPercent:
		return fmt.Sprintf("%.0f%%", v)
	case UnitLoad:
		return fmt.Sprintf("%.2f", v)
	case UnitMBps:
		return fmt.Sprintf("%.1f MB/s", v)
	case UnitCelsius:
		return fmt.Sprintf("%.0f °C", v)
	case UnitGB:
		return fmt.Sprintf("%.1f GB", v)
	}
	return fmt.Sprintf("%g", v)
}

// shortDuration renders whole minutes as "10m", whole hours as "1h".
func shortDuration(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

// Compose builds the notification text (spec §3 "Delivery"):
// "🔥 prod-1 · CPU 93% > 90% for 10m" / "✅ Resolved · prod-1 · CPU 41%".
func Compose(t Transition) Message {
	target := t.Names.label()
	var title, line string
	switch {
	case !t.Info.HasThreshold() && t.Kind == EventFired:
		title = fmt.Sprintf("🔥 %s is down for %s", target, shortDuration(t.Duration))
		if t.Info.TargetKind == TargetServer {
			line = fmt.Sprintf("The server has not reported for %d min.", int(t.Duration/time.Minute))
		} else {
			line = fmt.Sprintf("The app's containers have been failing (crashed, restarting or unhealthy) for %d min.", int(t.Duration/time.Minute))
		}
	case !t.Info.HasThreshold():
		title = fmt.Sprintf("✅ Resolved · %s is back up", target)
		line = "It is reporting normally again."
	case t.Kind == EventFired:
		title = fmt.Sprintf("🔥 %s · %s %s > %s for %s", target, t.Info.Label,
			FormatValue(t.Info, t.Value), FormatValue(t.Info, t.Threshold), shortDuration(t.Duration))
		line = fmt.Sprintf("%s averaged %s over %d min (threshold %s).", t.Info.Label,
			FormatValue(t.Info, t.Value), int(t.Duration/time.Minute), FormatValue(t.Info, t.Threshold))
	default:
		title = fmt.Sprintf("✅ Resolved · %s · %s %s", target, t.Info.Label, FormatValue(t.Info, t.Value))
		line = fmt.Sprintf("%s is back to %s (threshold %s).", t.Info.Label,
			FormatValue(t.Info, t.Value), FormatValue(t.Info, t.Threshold))
	}
	lines := []string{"Server: " + t.Names.Server}
	if t.Names.App != "" {
		lines = append(lines, "App: "+t.Names.App)
	}
	lines = append(lines, line)
	if t.Link != "" {
		lines = append(lines, t.Link)
	}
	return Message{Title: title, Body: strings.Join(lines, "\n")}
}

// Link is the web page of a target: the server's Monitoring page or the
// app's Metrics tab, under WEB_URL. Empty without WEB_URL.
func Link(webURL string, t Target) string {
	base := strings.TrimRight(webURL, "/")
	if base == "" {
		return ""
	}
	if t.AppID == "" {
		return base + "/server/" + t.ServerID + "/monitoring"
	}
	return base + "/server/" + t.ServerID + "/app/" + t.AppID + "?tab=metrics"
}
