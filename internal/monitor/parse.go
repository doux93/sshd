package monitor

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseProcOutput turns one iteration of the collector loop's output into a
// Sample. The input is the concatenated text of:
//
//	/proc/stat /proc/meminfo /proc/loadavg /proc/net/dev; df -P
//
// separated by newlines. The marker line "---SSHX---" (if present) is
// ignored ? it exists only to delimit iterations in the stream.
//
// The parser is deliberately lenient: any field it cannot find stays at its
// zero value. This lets it cope with partial output from a truncated read.
func ParseProcOutput(text string, ts time.Time) Sample {
	s := Sample{TS: ts}
	lines := strings.Split(text, "\n")

	var cpuLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "cpu ") {
			cpuLine = l
			break
		}
	}
	if cpuLine != "" {
		s.cpuTotal, s.cpuIdle = parseCPUStats(cpuLine)
		// Boot-average as fallback; the collector overrides with a delta
		// once it has a previous sample.
		if s.cpuTotal > 0 {
			s.CPUUsage = cpuUsageFromDelta(0, 0, s.cpuTotal, s.cpuIdle)
		}
	}

	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "MemTotal:"):
			s.MemTotalKB = parseUintKB(l)
		case strings.HasPrefix(l, "MemAvailable:"):
			avail := parseUintKB(l)
			if s.MemTotalKB > avail {
				s.MemUsedKB = s.MemTotalKB - avail
			}
			if s.MemTotalKB > 0 {
				s.MemUsage = float64(s.MemUsedKB) / float64(s.MemTotalKB) * 100
			}
		}
	}

	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) < 3 {
			continue
		}
		// /proc/loadavg: "0.52 0.43 0.38 2/123 4567"
		// The first three fields are floats; the 4th contains "/".
		if _, err := strconv.ParseFloat(fields[0], 64); err != nil {
			continue
		}
		if _, err := strconv.ParseFloat(fields[1], 64); err != nil {
			continue
		}
		s.Load1, _ = strconv.ParseFloat(fields[0], 64)
		s.Load5, _ = strconv.ParseFloat(fields[1], 64)
		if len(fields) > 2 {
			s.Load15, _ = strconv.ParseFloat(fields[2], 64)
		}
		break
	}

	s.Disks = parseDisks(lines)
	s.Net = parseNet(lines)
	return s
}

// parseCPUUsage computes busy% from the aggregate cpu line in /proc/stat.
// The values are cumulative since boot; the percentage is idle/total.
// Without a previous sample we can only report the boot-average, which is
// still useful as a rough indicator. The collector uses deltas for accuracy.
// parseCPUStats extracts the aggregate cpu jiffies from a /proc/stat line.
// Returns total and idle jiffies (idle includes iowait).
func parseCPUStats(line string) (total, idle uint64) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return 0, 0
	}
	var vals []uint64
	for _, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0
		}
		vals = append(vals, v)
	}
	for _, v := range vals {
		total += v
	}
	// idle is column index 3 of the aggregate /proc/stat line
	// (user nice system idle iowait irq softirq steal ...). iowait is
	// NOT idle time; including it understates CPU usage on I/O-bound hosts.
	idle = vals[3]
	return total, idle
}

// cpuUsageFromDelta computes busy% between two cumulative snapshots.
func cpuUsageFromDelta(prevTotal, prevIdle, curTotal, curIdle uint64) float64 {
	dt := curTotal - prevTotal
	if dt == 0 {
		return 0
	}
	di := curIdle - prevIdle
	busy := dt - di
	return float64(busy) / float64(dt) * 100
}

var memRe = regexp.MustCompile(`:\s*(\d+)\s*kB`)

func parseUintKB(line string) uint64 {
	m := memRe.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseUint(m[1], 10, 64)
	return v
}

// parseDisks extracts df -P output. Format:
//
//	Filesystem     1024-blocks  Used  Available  Capacity  Mounted on
//	/dev/sda1         52428800  30000  49428800       1%  /
func parseDisks(lines []string) []DiskUsage {
	var disks []DiskUsage
	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) < 6 {
			continue
		}
		// Mount point must start with "/" ? this filters out /proc
		// lines and the df header.
		if !strings.HasPrefix(fields[len(fields)-1], "/") {
			continue
		}
		total, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		used, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			continue
		}
		// Capacity field is like "45%"; strip the %.
		pctStr := strings.TrimSuffix(fields[4], "%")
		pct, _ := strconv.ParseFloat(pctStr, 64)
		mount := strings.Join(fields[5:], " ")
		disks = append(disks, DiskUsage{
			Mount:    mount,
			TotalKB:  total,
			UsedKB:   used,
			UsagePct: pct,
		})
	}
	return disks
}

// parseNet extracts /proc/net/dev interface stats.
// Format: "  eth0: rxbytes rxpackets ... txbytes ..."
func parseNet(lines []string) []NetDev {
	var devs []NetDev
	inNetSection := false
	for _, l := range lines {
		if strings.Contains(l, "Inter-|") || strings.Contains(l, "face |bytes") {
			inNetSection = true
			continue
		}
		if !inNetSection {
			continue
		}
		idx := strings.Index(l, ":")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(l[:idx])
		if name == "lo" {
			continue // skip loopback
		}
		rest := strings.Fields(l[idx+1:])
		if len(rest) < 9 {
			continue
		}
		rx, err := strconv.ParseUint(rest[0], 10, 64)
		if err != nil {
			continue
		}
		tx, err := strconv.ParseUint(rest[8], 10, 64)
		if err != nil {
			continue
		}
		devs = append(devs, NetDev{Name: name, RxBytes: rx, TxBytes: tx})
	}
	return devs
}
