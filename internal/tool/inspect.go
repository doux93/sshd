package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

var inspections = map[string]string{
	"system": "uname -a", "cpu": "cat /proc/stat", "memory": "cat /proc/meminfo",
	"disk": "df -P", "processes": "ps aux", "uptime": "uptime", "network": "cat /proc/net/dev",
}

func registerInspection(r *Registry) {
	r.Register(&Tool{Name: "inspect_system", Desc: "Read one fixed system report without accepting shell syntax.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"report": map[string]any{"type": "string", "enum": []string{"system", "cpu", "memory", "disk", "processes", "uptime", "network"}}}, "required": []string{"report"}, "additionalProperties": false},
		Handler: func(ctx context.Context, cc *CallContext, raw json.RawMessage) (Result, error) {
			var a struct {
				Report string `json:"report"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return Result{}, err
			}
			command, ok := inspections[a.Report]
			if !ok {
				return Result{}, fmt.Errorf("unknown inspection report %q", a.Report)
			}
			if cc.Session == nil {
				return Result{}, fmt.Errorf("no session")
			}
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			out, stderr, code, err := cc.Session.Exec(ctx, command)
			if err != nil {
				return Result{Error: err.Error()}, nil
			}
			return Result{Output: map[string]any{"stdout": string(out), "stderr": string(stderr), "exitCode": code}}, nil
		}})
}
