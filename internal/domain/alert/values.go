package alert

import "winterflow/internal/domain/metrics"

// ServerValues extracts the server alert metrics from one sample's stored
// values. A metric whose inputs are missing is absent, never zero.
func ServerValues(v metrics.Values) map[Metric]float64 {
	out := map[Metric]float64{}
	copyField(out, v, MetricCPU, "cpu")
	if used, ok := v["mem_used"]; ok {
		if total := v["mem_total"]; total > 0 {
			out[MetricMemory] = 100 * used / total
		}
	}
	copyField(out, v, MetricDisk, "disk_pct")
	copyField(out, v, MetricLoad1, "load1")
	copyField(out, v, MetricLoad5, "load5")
	copyField(out, v, MetricLoad15, "load15")
	rx, okRx := v["net_rx_bps"]
	tx, okTx := v["net_tx_bps"]
	if okRx && okTx {
		out[MetricBandwidth] = (rx + tx) / 1e6
	}
	copyField(out, v, MetricTemperature, "temp")
	return out
}

// AppValues extracts the app alert metrics. Memory is a percentage of the
// container limit, else of the host's memory (hostMemTotal from the same
// sample); disk is GB. An app whose container metrics are unavailable
// yields nothing.
func AppValues(v metrics.Values, hostMemTotal float64) map[Metric]float64 {
	out := map[Metric]float64{}
	if v[metrics.UnavailableField] == 1 {
		return out
	}
	copyField(out, v, MetricCPU, "cpu")
	if used, ok := v["mem_used"]; ok {
		if limit := v["mem_limit"]; limit > 0 {
			out[MetricMemory] = 100 * used / limit
		} else if hostMemTotal > 0 {
			out[MetricMemory] = 100 * used / hostMemTotal
		}
	}
	if disk, ok := v["disk"]; ok {
		out[MetricDisk] = disk / 1e9
	}
	return out
}

func copyField(out map[Metric]float64, v metrics.Values, m Metric, field string) {
	if x, ok := v[field]; ok {
		out[m] = x
	}
}
