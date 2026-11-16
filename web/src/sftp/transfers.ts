import { useSyncExternalStore } from "react";
import { api } from "../api/client";
export type TransferStatus =
  | "queued"
  | "uploading"
  | "saving"
  | "completed"
  | "failed"
  | "cancelled";
export interface Transfer {
  id: string;
  sessionId: string;
  target: string;
  path: string;
  name: string;
  size: number;
  loaded: number;
  status: TransferStatus;
  error: string;
  file: File | null;
}
type Upload = (
  session: string,
  path: string,
  file: File,
  progress: (loaded: number, total: number) => void,
  signal: AbortSignal,
) => Promise<void>;
export class UploadQueue {
  private jobs: Transfer[] = [];
  private listeners = new Set<() => void>();
  private controller: AbortController | null = null;
  private running = false;
  private activeId: string | null = null;
  constructor(private upload: Upload) {}
  getSnapshot = () => this.jobs;
  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };
  private update(id: string, patch: Partial<Transfer>) {
    this.jobs = this.jobs.map((j) => (j.id === id ? { ...j, ...patch } : j));
    this.emit();
  }
  private emit() {
    for (const fn of this.listeners) fn();
  }
  enqueue(sessionId: string, target: string, directory: string, files: File[]) {
    if (this.jobs.filter((j) => j.file).length + files.length > 100)
      throw new Error("上传队列最多保留 100 个文件，请先清理已结束的任务。");
    const base = directory.replace(/\/+$/, "");
    this.jobs = [
      ...this.jobs,
      ...files.map((file) => ({
        id: crypto.randomUUID(),
        sessionId,
        target,
        path: `${base}/${file.name}`,
        name: file.name,
        size: file.size,
        loaded: 0,
        status: "queued" as const,
        error: "",
        file,
      })),
    ];
    this.emit();
    void this.pump();
  }
  cancel(id: string) {
    const job = this.jobs.find((j) => j.id === id);
    if (!job || !["queued", "uploading", "saving"].includes(job.status)) return;
    this.update(id, {
      status: "cancelled",
      error: "已取消请求。若文件已进入保存阶段，请刷新目标目录确认结果。",
    });
    if (id === this.activeId) this.controller?.abort();
  }
  retry(id: string) {
    const job = this.jobs.find((j) => j.id === id);
    if (
      !job?.file ||
      !["failed", "cancelled"].includes(job.status) ||
      id === this.activeId
    )
      return;
    this.update(id, { status: "queued", loaded: 0, error: "" });
    void this.pump();
  }
  clearFinished() {
    this.jobs = this.jobs.filter(
      (j) =>
        ["queued", "uploading", "saving"].includes(j.status) ||
        j.id === this.activeId,
    );
    this.emit();
  }
  private async pump() {
    if (this.running) return;
    this.running = true;
    try {
      for (;;) {
        const job = this.jobs.find((j) => j.status === "queued" && j.file);
        if (!job) break;
        this.activeId = job.id;
        const controller = new AbortController();
        this.controller = controller;
        this.update(job.id, { status: "uploading" });
        try {
          await this.upload(
            job.sessionId,
            job.path,
            job.file!,
            (loaded) => {
              if (!controller.signal.aborted)
                this.update(job.id, {
                  loaded: Math.min(job.size, loaded),
                  status: loaded >= job.size ? "saving" : "uploading",
                });
            },
            controller.signal,
          );
          if (!controller.signal.aborted)
            this.update(job.id, {
              status: "completed",
              loaded: job.size,
              file: null,
            });
        } catch (error) {
          if (!controller.signal.aborted)
            this.update(job.id, {
              status: "failed",
              error: error instanceof Error ? error.message : String(error),
            });
        } finally {
          this.controller = null;
          this.activeId = null;
          this.emit();
        }
      }
    } finally {
      this.running = false;
    }
  }
}
export const uploadQueue = new UploadQueue(
  (session, path, file, progress, signal) =>
    api.uploadFile(session, path, file, progress, signal),
);
export function useTransfers() {
  return useSyncExternalStore(uploadQueue.subscribe, uploadQueue.getSnapshot);
}
export const isPending = (job: Transfer) =>
  ["queued", "uploading", "saving"].includes(job.status);
