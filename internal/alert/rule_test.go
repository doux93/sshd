package alert

import (
	"testing"
	"time"
)

func TestEvalRuleDiskFires(t *testing.T) {
	r := Rule{Metric: "disk", Op: ">", Threshold: 90}
	a, err := EvalRule(r, 95, 0)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("expected alert")
	}
	if a.Value != 95 {
		t.Errorf("value = %f, want 95", a.Value)
	}
	if a.Level != LevelCritical {
		t.Errorf("level = %q, want critical", a.Level)
	}
}

func TestEvalRuleDoesNotFireBelowThreshold(t *testing.T) {
	r := Rule{Metric: "cpu", Op: ">", Threshold: 80}
	a, _ := EvalRule(r, 50, 0)
	if a != nil {
		t.Fatal("should not fire below threshold")
	}
}

func TestEvalRuleDurationNotSustained(t *testing.T) {
	r := Rule{Metric: "cpu", Op: ">", Threshold: 80, Duration: "5m"}
	a, _ := EvalRule(r, 90, 2*time.Minute)
	if a != nil {
		t.Fatal("should not fire before duration")
	}
	a, _ = EvalRule(r, 90, 5*time.Minute)
	if a == nil {
		t.Fatal("should fire after duration")
	}
}

func TestStateTrackerSilence(t *testing.T) {
	st := NewStateTracker(30 * time.Minute)
	r := Rule{Metric: "disk", Op: ">", Threshold: 90}
	now := time.Now()

	a := st.Eval("node1", r, 95, now)
	if a == nil {
		t.Fatal("first fire should produce alert")
	}
	a = st.Eval("node1", r, 95, now.Add(10*time.Minute))
	if a != nil {
		t.Fatal("should be silenced")
	}
	a = st.Eval("node1", r, 95, now.Add(31*time.Minute))
	if a == nil {
		t.Fatal("should fire after silence expires")
	}
}

func TestStateTrackerClearsOnRecovery(t *testing.T) {
	st := NewStateTracker(30 * time.Minute)
	r := Rule{Metric: "cpu", Op: ">", Threshold: 80}
	now := time.Now()

	st.Eval("node1", r, 90, now)
	a := st.Eval("node1", r, 30, now.Add(time.Minute))
	if a != nil {
		t.Fatal("should not fire after recovery")
	}
	st.Eval("node1", r, 90, now.Add(2*time.Minute))
	a = st.Eval("node1", r, 90, now.Add(3*time.Minute))
	if a != nil {
		t.Fatal("should be silenced within 30min of first fire")
	}
	a = st.Eval("node1", r, 90, now.Add(33*time.Minute))
	if a == nil {
		t.Fatal("should fire after silence expires and condition sustained")
	}
}

func TestEvalAllMultipleRules(t *testing.T) {
	st := NewStateTracker(30 * time.Minute)
	rules := []Rule{
		{Metric: "cpu", Op: ">", Threshold: 80},
		{Metric: "disk", Op: ">", Threshold: 90},
	}
	// Extractor returns fixed values simulating a sample with high CPU and disk.
	extractor := func(metric string) (float64, error) {
		switch metric {
		case "cpu":
			return 95, nil
		case "disk":
			return 95, nil
		default:
			return 0, nil
		}
	}
	alerts := st.EvalAll("node1", rules, extractor, time.Now())
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(alerts))
	}
}

// Regression test: earlier versions keyed state by concatenating nodeID and
// rule with a '|' separator and filtering with strings.HasPrefix. That
// leaked state between similarly-named nodes — prod-01's "is firing" would
// return true for prod-010.
func TestStateIsolatedAcrossSimilarNodeIDs(t *testing.T) {
	st := NewStateTracker(time.Minute)
	now := time.Now()

	st.Eval("prod-01", Rule{Metric: "disk", Op: ">", Threshold: 90}, 95, now)
	if !st.IsFiring("prod-01") {
		t.Fatal("prod-01 should be firing")
	}
	if st.IsFiring("prod-010") {
		t.Fatal("prod-010 should NOT be firing — state leaked across prefix")
	}
}
