package monitor

import (
	"testing"
	"time"
)

const sampleProcOutput = `cpu  3357 0 4313 1362393 0 0 0 0 0 0
cpu0 500 0 600 340000 0 0 0 0 0 0
intr 1149305482 0 0 0 0 0 0 0 0 0
ctxt 1990087399
btime 1640000000
processes 5000
procs_running 1

MemTotal:       16384000 kB
MemFree:          500000 kB
MemAvailable:   8000000 kB
Buffers:          200000 kB
Cached:          3000000 kB

0.52 0.43 0.38 2/123 4567

Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes       packets
  eth0: 1234567890    5000    0    0    0     0          0         0 9876543210    4000    0    0    0     0       0          0
    lo: 1000    10    0    0    0     0          0         0 1000    10    0    0    0     0       0          0
Filesystem     1024-blocks   Used Available Capacity Mounted on
/dev/sda1       52428800  3000000  49428800       6% /
/dev/sdb1      104857600 95000000   9857600      91% /data
`

func TestParseProcOutput(t *testing.T) {
	ts := time.Now()
	s := ParseProcOutput(sampleProcOutput, ts)

	if s.TS != ts {
		t.Errorf("TS = %v, want %v", s.TS, ts)
	}
	if s.CPUUsage <= 0 || s.CPUUsage >= 100 {
		t.Errorf("CPUUsage = %f, want 0<x<100", s.CPUUsage)
	}
	if s.MemTotalKB != 16384000 {
		t.Errorf("MemTotalKB = %d, want 16384000", s.MemTotalKB)
	}
	if s.MemUsedKB != 8384000 {
		t.Errorf("MemUsedKB = %d, want 8384000", s.MemUsedKB)
	}
	expMemUsage := float64(8384000) / float64(16384000) * 100
	if s.MemUsage < expMemUsage-0.1 || s.MemUsage > expMemUsage+0.1 {
		t.Errorf("MemUsage = %.1f, want %.1f", s.MemUsage, expMemUsage)
	}
	if s.Load1 != 0.52 {
		t.Errorf("Load1 = %f, want 0.52", s.Load1)
	}
	if len(s.Disks) != 2 {
		t.Fatalf("Disks len = %d, want 2", len(s.Disks))
	}
	if s.Disks[0].Mount != "/" || s.Disks[0].UsagePct != 6 {
		t.Errorf("disk[0] = %+v", s.Disks[0])
	}
	if s.Disks[1].UsagePct != 91 {
		t.Errorf("disk[1] UsagePct = %f, want 91", s.Disks[1].UsagePct)
	}
	if s.MaxDiskUsage() != 91 {
		t.Errorf("MaxDiskUsage = %f, want 91", s.MaxDiskUsage())
	}
	if len(s.Net) != 1 {
		t.Fatalf("Net len = %d, want 1 (lo excluded)", len(s.Net))
	}
	if s.Net[0].Name != "eth0" {
		t.Errorf("net[0].Name = %q, want eth0", s.Net[0].Name)
	}
	if s.Net[0].RxBytes != 1234567890 {
		t.Errorf("net[0].RxBytes = %d, want 1234567890", s.Net[0].RxBytes)
	}
}

func TestTimeSeriesRingBuffer(t *testing.T) {
	ts := NewTimeSeries()

	// Empty series.
	if _, ok := ts.Latest(); ok {
		t.Fatal("empty series should have no latest")
	}

	// Push samples.
	base := time.Now()
	for i := 0; i < 5; i++ {
		ts.Push(Sample{TS: base.Add(time.Duration(i) * time.Second), CPUUsage: float64(i)})
	}
	all := ts.All()
	if len(all) != 5 {
		t.Fatalf("len = %d, want 5", len(all))
	}
	if all[0].CPUUsage != 0 || all[4].CPUUsage != 4 {
		t.Errorf("ordering wrong: [0]=%.0f [4]=%.0f", all[0].CPUUsage, all[4].CPUUsage)
	}
	latest, ok := ts.Latest()
	if !ok || latest.CPUUsage != 4 {
		t.Errorf("latest = %.0f, want 4", latest.CPUUsage)
	}
	recent := ts.Recent(2)
	if len(recent) != 2 || recent[0].CPUUsage != 3 || recent[1].CPUUsage != 4 {
		t.Errorf("recent = %v", recent)
	}
}

func TestCPUDelta(t *testing.T) {
	// Two snapshots: idle stays, busy grows ? usage should be 50%.
	// prev: user=100 nice=0 system=100 idle=200 iowait=0  ? total=400 idle=200
	// cur:  user=200 nice=0 system=200 idle=200 iowait=0  ? total=600 idle=200
	// delta: total=200 idle=0 ? busy=200 ? 100%... no:
	// Actually idle delta=0, total delta=200 ? busy% = 100%.
	pct := cpuUsageFromDelta(400, 200, 600, 200)
	if pct != 100 {
		t.Errorf("CPU = %f, want 100", pct)
	}
	// 50% case: prev total=400 idle=200, cur total=600 idle=300
	// delta total=200 idle=100 ? busy=100 ? 50%
	pct = cpuUsageFromDelta(400, 200, 600, 300)
	if pct != 50 {
		t.Errorf("CPU = %f, want 50", pct)
	}
}
