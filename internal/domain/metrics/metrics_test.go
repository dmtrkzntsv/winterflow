package metrics

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func f(x float64) *float64 { return &x }

func TestServerValuesFlattensAndOmitsMissingRates(t *testing.T) {
	s := ServerSample{
		MemUsed: 4e9, MemTotal: 8e9, Load1: 0.5,
		Disks: []Disk{{Mount: "/", Used: 30, Total: 100}, {Mount: "/data", Used: 90, Total: 100}},
		Temps: map[string]float64{"Composite": 41, "Package id 0": 55},
		Temp:  f(55),
	}
	v := s.Values()
	if _, ok := v["cpu"]; ok {
		t.Fatal("first sample has no cpu rate; the field must be absent, not zero")
	}
	if _, ok := v["net_rx_bps"]; ok {
		t.Fatal("missing rate stored")
	}
	if v["disk_used"] != 120 || v["disk_total"] != 200 || v["disk_pct"] != 90 {
		t.Fatalf("disk fields = %v", v)
	}
	if v["temp"] != 55 || v["temp:Composite"] != 41 || v["temp:Package id 0"] != 55 {
		t.Fatalf("temps = %v", v)
	}
	if _, ok := v["docker_images"]; ok {
		t.Fatal("docker_df stored before the first scan")
	}
}

func TestServerValuesDropsNaNAndCapsSensors(t *testing.T) {
	temps := map[string]float64{}
	for i := 0; i < 40; i++ {
		temps[string(rune('a'+i%26))+string(rune('a'+i/26))] = float64(i)
	}
	v := ServerSample{CPU: f(math.NaN()), Temps: temps}.Values()
	if _, ok := v["cpu"]; ok {
		t.Fatal("NaN stored")
	}
	n := 0
	for k := range v {
		if len(k) > len(TempPrefix) && k[:len(TempPrefix)] == TempPrefix {
			n++
		}
	}
	if n != maxSensors {
		t.Fatalf("stored %d sensors, want %d", n, maxSensors)
	}
	if _, err := json.Marshal(v); err != nil {
		t.Fatal(err)
	}
}

func TestAppValues(t *testing.T) {
	v := AppSample{AppID: "a", CPU: f(12.5), MemUsed: f(100), Disk: f(5e9), ContainersUnavailable: true}.Values()
	if v["cpu"] != 12.5 || v["mem_used"] != 100 || v["disk"] != 5e9 || v["unavailable"] != 1 {
		t.Fatalf("app values = %v", v)
	}
	if _, ok := v["mem_limit"]; ok {
		t.Fatal("no limit stored as a value")
	}
}

func TestSampleTimeFallsBackOnSkew(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 30, 0, time.UTC)
	if got := SampleTime(Sample{TS: now.Add(-40 * time.Second).Unix()}, now); !got.Equal(now.Add(-40 * time.Second)) {
		t.Fatalf("in-range ts = %v", got)
	}
	for _, ts := range []int64{0, -5, now.Add(time.Hour).Unix(), now.Add(-time.Hour).Unix()} {
		if got := SampleTime(Sample{TS: ts}, now); !got.Equal(now) {
			t.Fatalf("ts %d -> %v, want now", ts, got)
		}
	}
}

func TestParseRange(t *testing.T) {
	cases := map[string]Tier{"1h": Tier1, "12h": Tier10, "24h": Tier20, "7d": Tier120, "30d": Tier480}
	for r, want := range cases {
		tier, span, ok := ParseRange(r)
		if !ok || tier != want || span < time.Hour {
			t.Errorf("%s -> %v %v %v", r, tier, span, ok)
		}
		// Each range fits its tier's retention: the chart is never cut short.
		if span > tier.Retention() {
			t.Errorf("%s spans %v but tier %d keeps %v", r, span, tier, tier.Retention())
		}
	}
	for _, bad := range []string{"", "2h", "1H", "30d "} {
		if _, _, ok := ParseRange(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBucketStartAlignsInUTC(t *testing.T) {
	at := time.Date(2026, 10, 11, 17, 47, 13, 0, time.FixedZone("PDT", -7*3600)) // 00:47:13 UTC next day
	cases := map[Tier]time.Time{
		Tier1:   time.Date(2026, 10, 12, 0, 47, 0, 0, time.UTC),
		Tier10:  time.Date(2026, 10, 12, 0, 40, 0, 0, time.UTC),
		Tier20:  time.Date(2026, 10, 12, 0, 40, 0, 0, time.UTC),
		Tier120: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		Tier480: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
	}
	for tier, want := range cases {
		if got := tier.BucketStart(at); !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("tier %d: %v, want %v", tier, got, want)
		}
	}
}

func TestRollupWeightedAverageAndMaxOfMax(t *testing.T) {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	// Two tier-10 rows: 10 samples averaging 20 (peak 50), 5 samples averaging
	// 80 (peak 90). Weighted avg = (200+400)/15 = 40, not the plain 50.
	b := Rollup(start, []Bucket{
		{Start: start, Samples: 10, Data: Values{"cpu": 20, "cpu_max": 50}},
		{Start: start.Add(10 * time.Minute), Samples: 5, Data: Values{"cpu": 80, "cpu_max": 90}},
	})
	if b.Samples != 15 || b.Data["cpu"] != 40 || b.Data["cpu_max"] != 90 {
		t.Fatalf("rollup = %+v", b)
	}
}

func TestRollupFromTier1UsesValuesAsPeaksAndSkipsMissingFields(t *testing.T) {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	b := Rollup(start, []Bucket{
		{Start: start, Samples: 1, Data: Values{"mem_used": 10}}, // first sample: no cpu yet
		{Start: start.Add(time.Minute), Samples: 1, Data: Values{"mem_used": 30, "cpu": 7}},
		{Start: start.Add(2 * time.Minute), Samples: 1, Data: Values{"mem_used": 20, "cpu": 3}},
	})
	if b.Data["mem_used"] != 20 || b.Data["mem_used_max"] != 30 {
		t.Fatalf("mem = %v", b.Data)
	}
	// cpu averages over the two rows that have it, not three.
	if b.Data["cpu"] != 5 || b.Data["cpu_max"] != 7 {
		t.Fatalf("cpu = %v", b.Data)
	}
	if b.Samples != 3 {
		t.Fatalf("samples = %d", b.Samples)
	}
}

func TestRollupIsIdempotentOverItsOwnOutput(t *testing.T) {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	once := Rollup(start, []Bucket{{Start: start, Samples: 4, Data: Values{"cpu": 10, "cpu_max": 12}}})
	twice := Rollup(start, []Bucket{once})
	if twice.Data["cpu"] != 10 || twice.Data["cpu_max"] != 12 || twice.Samples != 4 {
		t.Fatalf("re-rollup changed values: %+v", twice)
	}
}

func TestToPointFillsPeaksForTier1(t *testing.T) {
	start := time.Unix(1_760_000_000, 0).UTC()
	p := ToPoint(Bucket{Start: start, Samples: 1, Data: Values{"cpu": 3}})
	if p["t"] != 1_760_000_000 || p["cpu"] != 3 || p["cpu_max"] != 3 {
		t.Fatalf("point = %v", p)
	}
	p = ToPoint(Bucket{Start: start, Samples: 10, Data: Values{"cpu": 3, "cpu_max": 9}})
	if p["cpu_max"] != 9 {
		t.Fatalf("stored peak overwritten: %v", p)
	}
	picked := p.Pick("cpu", "absent")
	if len(picked) != 3 || picked["t"] != 1_760_000_000 {
		t.Fatalf("pick = %v", picked)
	}
}

func TestLatestStorePrune(t *testing.T) {
	s := NewLatestStore()
	now := time.Now()
	s.Set("old", Latest{At: now.Add(-2 * time.Hour)})
	s.Set("new", Latest{At: now})
	if n := s.Prune(now.Add(-time.Hour)); n != 1 {
		t.Fatalf("pruned %d", n)
	}
	if _, ok := s.Get("old"); ok {
		t.Fatal("stale entry kept")
	}
	s.Forget("new")
	if _, ok := s.Get("new"); ok {
		t.Fatal("forgotten entry kept")
	}
}
