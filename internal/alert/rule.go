package alert

import (
	"fmt"
	"strings"
	"time"
)

// Rule is one alert condition attached to a node.
type Rule struct {
	Metric    string  `json:"metric"`    // "cpuUsage" | "memUsage" | "load1" | "disk" | "host"
	Op        string  `json:"op"`        // ">" | ">=" | "<" | "<="
	Threshold float64 `json:"threshold"` // e.g. 90 for disk>90%
	Duration  string  `json:"duration"`  // e.g. "5m"; must be sustained this long
}

// Level is the alert severity.
type Level string

const (
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

// Alert is a fired alert, ready to notify and display.
type Alert struct {
	NodeID    string    `json:"nodeId"`
	NodeLabel string    `json:"nodeLabel"`
	Rule      Rule      `json:"rule"`
	Value     float64   `json:"value"`
	Level     Level     `json:"level"`
	Message   string    `json:"message"`
	TS        time.Time `json:"ts"`
}

// EvalRule checks a rule against a metric value and the duration the
// condition has been sustained. Returns an Alert if the rule fires.
func EvalRule(r Rule, value float64, sustainedFor time.Duration) (*Alert, error) {
	if !compare(value, r.Op, r.Threshold) {
		return nil, nil
	}
	if r.Duration != "" {
		d, err := time.ParseDuration(r.Duration)
		if err != nil {
			return nil, fmt.Errorf("bad duration %q: %w", r.Duration, err)
		}
		if sustainedFor < d {
			return nil, nil
		}
	}
	level := LevelWarning
	if r.Metric == "disk" && r.Threshold >= 90 {
		level = LevelCritical
	}
	if r.Metric == "cpuUsage" && r.Threshold >= 90 {
		level = LevelCritical
	}
	return &Alert{
		Rule:    r,
		Value:   value,
		Level:   level,
		Message: formatMessage(r, value),
	}, nil
}

// MetricValue extracts a float64 from a Sample for the given metric name.
// Callers pass the extracted value to EvalRule; this decouples alert from
// the monitor package.
type MetricExtractor func(metric string) (float64, error)

func compare(value float64, op string, threshold float64) bool {
	switch op {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	default:
		return false
	}
}

func formatMessage(r Rule, value float64) string {
	return fmt.Sprintf("%s %s %.1f (threshold %.1f)", r.Metric, r.Op, value, r.Threshold)
}

// _ keeps strings imported if future formatters need it.
var _ = strings.ToLower
