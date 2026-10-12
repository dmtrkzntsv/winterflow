package alert

import (
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/metrics"
)

func f(v float64) *float64 { return &v }
func n(v int) *int         { return &v }

func TestLookupCatalog(t *testing.T) {
	if _, ok := Lookup(TargetServer, MetricTemperature); !ok {
		t.Fatal("server temperature missing")
	}
	if _, ok := Lookup(TargetApp, MetricLoad1); ok {
		t.Fatal("apps have no load metric")
	}
	info, _ := Lookup(TargetApp, MetricDisk)
	if info.Unit != UnitGB || !info.HasThreshold() {
		t.Fatalf("app disk = %+v", info)
	}
	down, _ := Lookup(TargetServer, MetricDown)
	if down.HasThreshold() {
		t.Fatal("down has no threshold")
	}
	if len(Catalog()) != 13 {
		t.Fatalf("catalog size = %d", len(Catalog()))
	}
}

func TestRuleValidate(t *testing.T) {
	ok := []Rule{
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 90, DurationMinutes: 10},
		{TargetKind: TargetServer, Metric: MetricLoad5, Threshold: 12.5, DurationMinutes: 1},
		{TargetKind: TargetApp, Metric: MetricDisk, Threshold: 250, DurationMinutes: 60},
		{TargetKind: TargetServer, Metric: MetricDown, DurationMinutes: 5},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []Rule{
		{TargetKind: "host", Metric: MetricCPU, Threshold: 90, DurationMinutes: 10},
		{TargetKind: TargetApp, Metric: MetricLoad1, Threshold: 1, DurationMinutes: 10},
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 0, DurationMinutes: 10},
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 101, DurationMinutes: 10},
		{TargetKind: TargetServer, Metric: MetricTemperature, Threshold: 151, DurationMinutes: 10},
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 90, DurationMinutes: 0},
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 90, DurationMinutes: 61},
	}
	for _, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v accepted", r)
		}
	}
	down := Rule{TargetKind: TargetServer, Metric: MetricDown, Threshold: 7, DurationMinutes: 5}
	down.Normalize()
	if down.Threshold != 0 {
		t.Fatalf("down threshold kept: %v", down.Threshold)
	}
}

func TestOverrideValidateAndEffective(t *testing.T) {
	rule := Rule{ID: "r1", TargetKind: TargetApp, Metric: MetricMemory, Threshold: 90, DurationMinutes: 10, Enabled: true}
	if err := (Override{RuleID: "r1", ServerID: "s"}).Validate(rule); err == nil {
		t.Fatal("app rule override without app_id accepted")
	}
	if err := (Override{RuleID: "r1", ServerID: "s", AppID: "a", Threshold: f(120)}).Validate(rule); err == nil {
		t.Fatal("threshold above 100% accepted")
	}
	if err := (Override{RuleID: "r1", ServerID: "s", AppID: "a", DurationMinutes: n(0)}).Validate(rule); err == nil {
		t.Fatal("duration 0 accepted")
	}
	srvRule := Rule{ID: "r2", TargetKind: TargetServer, Metric: MetricDown, DurationMinutes: 5, Enabled: true}
	if err := (Override{RuleID: "r2", ServerID: "s", AppID: "a"}).Validate(srvRule); err == nil {
		t.Fatal("server rule override with app_id accepted")
	}
	if err := (Override{RuleID: "r2", ServerID: "s", Threshold: f(3)}).Validate(srvRule); err == nil {
		t.Fatal("down override with a threshold accepted")
	}

	base := Effective(rule, nil)
	if !base.Enabled || base.Threshold != 90 || base.Duration != 10*time.Minute || base.ChannelIDs != nil {
		t.Fatalf("no override = %+v", base)
	}
	o := Override{RuleID: "r1", ServerID: "s", AppID: "a", Threshold: f(95), ChannelIDs: []string{}}
	got := Effective(rule, &o)
	if got.Threshold != 95 || got.Duration != 10*time.Minute || got.ChannelIDs == nil || len(got.ChannelIDs) != 0 {
		t.Fatalf("override = %+v", got)
	}
	o.Disabled = true
	if Effective(rule, &o).Enabled {
		t.Fatal("disabled override still enabled")
	}
	rule.Enabled = false
	if Effective(rule, nil).Enabled {
		t.Fatal("disabled rule still enabled")
	}
}

func TestDefaultRulesAreValid(t *testing.T) {
	rules := DefaultRules()
	if len(rules) != 5 {
		t.Fatalf("defaults = %d", len(rules))
	}
	for _, r := range rules {
		if err := r.Validate(); err != nil || !r.Enabled || r.ChannelIDs != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
}

func TestServerValues(t *testing.T) {
	got := ServerValues(metrics.Values{
		"cpu": 42, "mem_used": 3e9, "mem_total": 4e9, "disk_pct": 71,
		"load1": 1.5, "load5": 1, "load15": 0.5, "net_rx_bps": 3e6, "net_tx_bps": 1e6, "temp": 64,
	})
	want := map[Metric]float64{
		MetricCPU: 42, MetricMemory: 75, MetricDisk: 71, MetricLoad1: 1.5, MetricLoad5: 1,
		MetricLoad15: 0.5, MetricBandwidth: 4, MetricTemperature: 64,
	}
	for m, v := range want {
		if got[m] != v {
			t.Errorf("%s = %v, want %v", m, got[m], v)
		}
	}
	sparse := ServerValues(metrics.Values{"mem_used": 1, "mem_total": 0, "net_rx_bps": 5})
	for _, m := range []Metric{MetricCPU, MetricMemory, MetricBandwidth, MetricTemperature} {
		if _, ok := sparse[m]; ok {
			t.Errorf("%s present without its inputs", m)
		}
	}
}

func TestAppValuesMemoryFallsBackToHost(t *testing.T) {
	limited := AppValues(metrics.Values{"cpu": 3, "mem_used": 512e6, "mem_limit": 1024e6, "disk": 25e9}, 8e9)
	if limited[MetricMemory] != 50 || limited[MetricCPU] != 3 || limited[MetricDisk] != 25 {
		t.Fatalf("limited = %v", limited)
	}
	host := AppValues(metrics.Values{"mem_used": 2e9}, 8e9)
	if host[MetricMemory] != 25 {
		t.Fatalf("host fallback = %v", host)
	}
	if _, ok := AppValues(metrics.Values{"mem_used": 2e9}, 0)[MetricMemory]; ok {
		t.Fatal("memory without limit or host total")
	}
	if got := AppValues(metrics.Values{metrics.UnavailableField: 1}, 8e9); len(got) != 0 {
		t.Fatalf("unavailable app = %v", got)
	}
}

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func fill(b *Buffer, from time.Time, mins int, v float64) {
	for i := 0; i < mins; i++ {
		b.Add(Point{At: from.Add(time.Duration(i) * time.Minute), Values: map[Metric]float64{MetricCPU: v}})
	}
}

func TestAverageNeedsEightyPercentCoverage(t *testing.T) {
	var b Buffer
	fill(&b, t0, 7, 95) // 12:00..12:06
	if _, ok := b.Average(MetricCPU, 10*time.Minute); ok {
		t.Fatal("7 of 10 samples decided")
	}
	b.Add(Point{At: t0.Add(7 * time.Minute), Values: map[Metric]float64{MetricCPU: 75}})
	avg, ok := b.Average(MetricCPU, 10*time.Minute)
	if !ok || avg != (7*95+75)/8.0 {
		t.Fatalf("8 of 10: avg=%v ok=%v", avg, ok)
	}
	// Only the window counts: a 2-minute window sees 12:06 and 12:07.
	avg, ok = b.Average(MetricCPU, 2*time.Minute)
	if !ok || avg != 85 {
		t.Fatalf("2m: avg=%v ok=%v", avg, ok)
	}
	if _, ok := b.Average(MetricMemory, time.Minute); ok {
		t.Fatal("a metric no point carries decided")
	}
}

func TestBufferIgnoresSameOrOlderMinute(t *testing.T) {
	var b Buffer
	if !b.Add(Point{At: t0.Add(30 * time.Second), Values: map[Metric]float64{MetricCPU: 10}}) {
		t.Fatal("first point rejected")
	}
	if b.Add(Point{At: t0.Add(50 * time.Second), Values: map[Metric]float64{MetricCPU: 99}}) {
		t.Fatal("same minute accepted twice")
	}
	if b.Add(Point{At: t0.Add(-time.Minute), Values: map[Metric]float64{MetricCPU: 99}}) {
		t.Fatal("older minute accepted")
	}
	fill(&b, t0.Add(time.Minute), BufferSize+5, 1)
	if b.Len() != BufferSize {
		t.Fatalf("len = %d", b.Len())
	}
}

func TestRouteChannels(t *testing.T) {
	all := []Channel{
		{ID: "a", IsDefault: true, Enabled: true},
		{ID: "b", IsDefault: true, Enabled: false},
		{ID: "c", Enabled: true},
	}
	ids := func(cs []Channel) string {
		var s []string
		for _, c := range cs {
			s = append(s, c.ID)
		}
		return strings.Join(s, ",")
	}
	if got := ids(RouteChannels(all, nil)); got != "a" {
		t.Errorf("defaults = %q", got)
	}
	if got := ids(RouteChannels(all, []string{})); got != "" {
		t.Errorf("history only = %q", got)
	}
	if got := ids(RouteChannels(all, []string{"c", "b", "gone"})); got != "c" {
		t.Errorf("explicit = %q", got)
	}
}

func TestComposeMessages(t *testing.T) {
	cpu, _ := Lookup(TargetServer, MetricCPU)
	fired := Compose(Transition{Kind: EventFired, Info: cpu, Names: Names{Server: "prod-1"}, Value: 93.4,
		Threshold: 90, Duration: 10 * time.Minute, Link: "https://wf.example/server/s1/monitoring"})
	if fired.Title != "🔥 prod-1 · CPU 93% > 90% for 10m" {
		t.Errorf("fired title = %q", fired.Title)
	}
	if !strings.Contains(fired.Body, "Server: prod-1") || !strings.Contains(fired.Body, "https://wf.example/server/s1/monitoring") {
		t.Errorf("fired body = %q", fired.Body)
	}
	resolved := Compose(Transition{Kind: EventResolved, Info: cpu, Names: Names{Server: "prod-1"}, Value: 41, Threshold: 90, Duration: 10 * time.Minute})
	if resolved.Title != "✅ Resolved · prod-1 · CPU 41%" {
		t.Errorf("resolved title = %q", resolved.Title)
	}
	down, _ := Lookup(TargetApp, MetricDown)
	appDown := Compose(Transition{Kind: EventFired, Info: down, Names: Names{Server: "prod-1", App: "nextcloud"}, Duration: time.Hour})
	if appDown.Title != "🔥 prod-1 / nextcloud is down for 1h" || !strings.Contains(appDown.Body, "App: nextcloud") {
		t.Errorf("app down = %+v", appDown)
	}
	up := Compose(Transition{Kind: EventResolved, Info: down, Names: Names{Server: "prod-1"}})
	if up.Title != "✅ Resolved · prod-1 is back up" {
		t.Errorf("server up title = %q", up.Title)
	}
	disk, _ := Lookup(TargetApp, MetricDisk)
	if got := FormatValue(disk, 14.25); got != "14.2 GB" {
		t.Errorf("disk = %q", got)
	}
}

func TestLink(t *testing.T) {
	if got := Link("https://wf.example/", Target{ServerID: "s1"}); got != "https://wf.example/server/s1/monitoring" {
		t.Errorf("server link = %q", got)
	}
	if got := Link("https://wf.example", Target{ServerID: "s1", AppID: "a1"}); got != "https://wf.example/server/s1/app/a1?tab=metrics" {
		t.Errorf("app link = %q", got)
	}
	if got := Link("", Target{ServerID: "s1"}); got != "" {
		t.Errorf("no WEB_URL = %q", got)
	}
}
