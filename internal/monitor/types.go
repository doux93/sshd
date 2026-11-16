package monitor

import "time"

// Sample is one point-in-time snapshot of host metrics. All percentages are
// 0-100; absolute values use the unit in the field name.
type Sample struct {
	TS         time.Time `json:"ts"`
	CPUUsage   float64   `json:"cpuUsage"`   // 0-100, all cores averaged
	MemTotalKB uint64    `json:"memTotalKB"` // physical memory
	MemUsedKB  uint64    `json:"memUsedKB"`  // total - available
	MemUsage   float64   `json:"memUsage"`   // 0-100
	Load1      float64   `json:"load1"`      // 1-minute load average
	Load5      float64   `json:"load5"`
	Load15     float64   `json:"load15"`
	// Disk is per mount point; the usage percentage is what alerts fire on.
	Disks []DiskUsage `json:"disks"`
	// Network is per interface, cumulative bytes since boot. Rates are
	// derived by the consumer from successive samples.
	Net []NetDev `json:"net"`

	// Unexported raw jiffies for delta-based CPU calculation. Not
	// serialised; the collector uses them to compute CPUUsage across
	// successive samples.
	cpuTotal uint64
	cpuIdle  uint64
}

// DiskUsage is one mounted filesystem.
type DiskUsage struct {
	Mount    string  `json:"mount"`
	TotalKB  uint64  `json:"totalKB"`
	UsedKB   uint64  `json:"usedKB"`
	UsagePct float64 `json:"usagePct"` // 0-100
}

// NetDev is one network interface's cumulative counters.
type NetDev struct {
	Name    string `json:"name"`
	RxBytes uint64 `json:"rxBytes"`
	TxBytes uint64 `json:"txBytes"`
}

// MaxDiskUsage returns the highest usage percentage across all mounts, or
// 0 if there are none. This is what "disk > 90%" alerts evaluate against.
func (s Sample) MaxDiskUsage() float64 {
	max := 0.0
	for _, d := range s.Disks {
		if d.UsagePct > max {
			max = d.UsagePct
		}
	}
	return max
}
