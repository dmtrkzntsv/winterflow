package sysmetrics

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// readHwmon reads every temp*_input under <root>/sys/class/hwmon, in °C,
// labelled by temp*_label or else the chip's name; a repeated label gets a
// " #2", " #3" suffix. Readings outside (0, 150) °C are sensor noise and
// dropped. ok is false when no sensor reads.
func readHwmon(root string) (temps map[string]float64, hottest float64, ok bool) {
	base := filepath.Join(root, "sys", "class", "hwmon")
	chips, err := os.ReadDir(base)
	if err != nil {
		return nil, 0, false
	}
	sort.Slice(chips, func(i, j int) bool { return chips[i].Name() < chips[j].Name() })
	temps = map[string]float64{}
	for _, chip := range chips {
		dir := filepath.Join(base, chip.Name())
		name := readTrim(filepath.Join(dir, "name"))
		inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		sort.Strings(inputs)
		for _, in := range inputs {
			raw := readTrim(in)
			milli, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				continue
			}
			c := milli / 1000
			if c <= 0 || c >= 150 {
				continue
			}
			label := readTrim(strings.TrimSuffix(in, "_input") + "_label")
			if label == "" {
				label = name
			}
			if label == "" {
				label = chip.Name()
			}
			key := label
			for n := 2; ; n++ {
				if _, dup := temps[key]; !dup {
					break
				}
				key = label + " #" + strconv.Itoa(n)
			}
			temps[key] = c
			if !ok || c > hottest {
				hottest = c
			}
			ok = true
		}
	}
	if !ok {
		return nil, 0, false
	}
	return temps, hottest, true
}

// readTrim returns a small file's trimmed content, "" when unreadable.
func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
