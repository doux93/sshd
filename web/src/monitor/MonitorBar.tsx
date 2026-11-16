import { useEffect, useState } from "react";
import { wsClient, type MetricsListener } from "../ws/protocol";
import type { MonitorSample } from "../api/client";

interface MonitorBarProps {
  sessionId: string;
}

/** A compact sidebar showing live CPU/mem/load/disk for a session. */
export function MonitorBar({ sessionId }: MonitorBarProps) {
  const [sample, setSample] = useState<MonitorSample | null>(null);

  useEffect(() => {
    setSample(null);
    const onMetrics: MetricsListener = (sid, s) => {
      if (sid === sessionId) {
        setSample(s as MonitorSample);
      }
    };
    // onMetrics returns an unsubscribe; without calling it every remount
    // leaked a listener and old sessions kept receiving samples.
    return wsClient.onMetrics(onMetrics);
  }, [sessionId]);

  if (!sample) {
    return (
      <div className="flex h-full w-full shrink-0 flex-col gap-2 border-l border-slate-700 p-2 text-xs text-slate-400">
        <div className="flex items-center gap-1.5">
          <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-slate-600" />
          等待监控数据…
        </div>
      </div>
    );
  }

  const disk = sample.disks?.reduce(
    (max, d) => (d.usagePct > max ? d.usagePct : max),
    0,
  );

  return (
    <div className="flex h-full w-full shrink-0 flex-col gap-3 overflow-y-auto border-l border-slate-700 p-2 text-xs text-slate-400">
      <Metric label="CPU" value={sample.cpuUsage} unit="%" color="cyan" />
      <Metric label="内存" value={sample.memUsage} unit="%" color="blue" />
      <Metric label="负载" value={sample.load1} color="amber" />
      {disk > 0 && (
        <Metric
          label="磁盘"
          value={disk}
          unit="%"
          color={disk > 90 ? "red" : "green"}
        />
      )}
      {sample.disks?.slice(0, 3).map((d) => (
        <div key={d.mount} className="flex justify-between text-slate-400">
          <span className="truncate">{d.mount}</span>
          <span>{d.usagePct.toFixed(0)}%</span>
        </div>
      ))}
    </div>
  );
}

function Metric({
  label,
  value,
  unit,
  color,
}: {
  label: string;
  value: number;
  unit?: string;
  color: string;
}) {
  const pct = Math.min(100, value);
  const colorMap: Record<string, string> = {
    cyan: "bg-cyan-500",
    blue: "bg-blue-500",
    amber: "bg-amber-500",
    green: "bg-green-500",
    red: "bg-red-500",
  };
  return (
    <div className="flex flex-col gap-1">
      <div className="flex justify-between">
        <span className="text-slate-400">{label}</span>
        <span className="font-mono text-slate-100">
          {value.toFixed(1)}
          {unit}
        </span>
      </div>
      {unit && (
        <div className="h-1.5 overflow-hidden rounded bg-slate-700">
          <div
            className={`h-full rounded ${colorMap[color] ?? "bg-slate-500"}`}
            style={{ width: `${pct}%` }}
          />
        </div>
      )}
    </div>
  );
}
