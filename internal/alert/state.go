package alert

import (
	"fmt"
	"sync"
	"time"
)

// DefaultSilence is how long after firing before the same rule can fire
// again (design doc: 30 min default).
const DefaultSilence = 30 * time.Minute

// StateTracker tracks per-rule sustained duration and silence windows.
//
// The maps are keyed per-node then per-rule (two-level), not concatenated
// with a separator. Earlier versions used a single map keyed by
// `"nodeID|rule..."` and filtered with `strings.HasPrefix` — that failed
// when two node IDs shared a prefix (e.g. "prod-01" and "prod-010" would
// have one node's "is firing" report the other's state).
type StateTracker struct {
	mu        sync.Mutex
	silence   time.Duration
	since     map[nodeKey]time.Time
	lastFired map[nodeKey]time.Time
	firing    map[nodeKey]bool
}

// nodeKey is a (nodeID, rule) pair. Using a struct key makes the two-level
// grouping structural rather than string-prefix, so it cannot leak across
// similarly-named nodes.
type nodeKey struct {
	nodeID string
	rule   string
}

// ruleKey turns a Rule into the per-rule half of a nodeKey. The nodeID is
// supplied separately.
func ruleKey(r Rule) string {
	return fmt.Sprintf("%s|%s|%v|%s", r.Metric, r.Op, r.Threshold, r.Duration)
}

func NewStateTracker(silence time.Duration) *StateTracker {
	if silence <= 0 {
		silence = DefaultSilence
	}
	return &StateTracker{
		silence:   silence,
		since:     make(map[nodeKey]time.Time),
		lastFired: make(map[nodeKey]time.Time),
		firing:    make(map[nodeKey]bool),
	}
}

// Eval evaluates a rule against a metric value. It returns a non-nil Alert
// when the rule fires. value is pre-extracted by the caller (decoupling
// alert from monitor).
func (st *StateTracker) Eval(nodeID string, r Rule, value float64, now time.Time) *Alert {
	st.mu.Lock()
	defer st.mu.Unlock()

	k := nodeKey{nodeID: nodeID, rule: ruleKey(r)}
	conditionMet := compare(value, r.Op, r.Threshold)

	if !conditionMet {
		delete(st.since, k)
		if st.firing[k] {
			st.firing[k] = false
		}
		return nil
	}

	sustained := time.Duration(0)
	if t, ok := st.since[k]; ok {
		sustained = now.Sub(t)
	} else {
		st.since[k] = now
	}

	a, err := EvalRule(r, value, sustained)
	if err != nil || a == nil {
		return nil
	}

	if last, ok := st.lastFired[k]; ok && now.Sub(last) < st.silence {
		return nil
	}

	st.lastFired[k] = now
	st.firing[k] = true
	a.NodeID = nodeID
	return a
}

// IsFiring reports whether any rule for the given node is currently firing.
func (st *StateTracker) IsFiring(nodeID string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, v := range st.firing {
		if v && k.nodeID == nodeID {
			return true
		}
	}
	return false
}

// EvalAll evaluates all rules for a node. extractor converts a metric name
// to a value; this is the bridge to monitor.Sample without importing it.
func (st *StateTracker) EvalAll(nodeID string, rules []Rule, extractor func(string) (float64, error), now time.Time) []Alert {
	var alerts []Alert
	for _, r := range rules {
		value, err := extractor(r.Metric)
		if err != nil {
			continue
		}
		if a := st.Eval(nodeID, r, value, now); a != nil {
			alerts = append(alerts, *a)
		}
	}
	return alerts
}
