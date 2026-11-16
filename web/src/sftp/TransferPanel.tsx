import { formatSize } from "./formatSize";
import { Icon } from "../components/Icon";
import { uploadQueue, useTransfers, isPending } from "./transfers";
export function TransferPanel({ onClose }: { onClose(): void }) {
  const jobs = useTransfers();
  const label = {
    queued: "等待上传",
    uploading: "正在上传",
    saving: "正在保存到主机",
    completed: "已完成",
    failed: "上传失败",
    cancelled: "已取消",
  };
  return (
    <aside
      aria-label="上传队列"
      className="workspace-panel absolute inset-y-0 right-0 z-30 w-96 max-w-full shadow-2xl"
    >
      <header className="panel-heading">
        <div>
          <strong>上传队列</strong>
          <p className="mt-1 text-xs text-slate-400">
            切换会话仍继续上传；关闭整个网页将中断任务。
          </p>
        </div>
        <button
          aria-label="收起上传队列"
          className="icon-button"
          onClick={onClose}
        >
          <Icon name="close" />
        </button>
      </header>
      <div className="flex justify-between border-b border-slate-800 px-4 py-2 text-xs">
        <span>{jobs.filter(isPending).length} 个进行中</span>
        <button
          className="text-sky-300"
          onClick={() => uploadQueue.clearFinished()}
        >
          清理已结束
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3 space-y-3">
        {jobs.length === 0 && (
          <p className="empty-copy">
            在文件面板选择或拖入文件。下载任务由浏览器管理。
          </p>
        )}
        {jobs.map((job) => (
          <article
            key={job.id}
            className="rounded-lg border border-slate-700 p-3"
          >
            <div className="flex items-center gap-2">
              <strong className="min-w-0 flex-1 truncate text-sm">
                {job.name}
              </strong>
              <span className="shrink-0 text-xs text-slate-400">
                {label[job.status]}
              </span>
            </div>
            <p
              className="mt-2 truncate font-mono text-[11px] text-slate-400"
              title={`${job.target}:${job.path}`}
            >
              {job.target} · {job.path}
            </p>
            <progress
              aria-label={`上传进度 ${job.name}`}
              className="mt-2 h-1.5 w-full accent-sky-500"
              max={100}
              value={
                job.status === "completed"
                  ? 100
                  : job.size
                    ? Math.min(100, (job.loaded / job.size) * 100)
                    : 0
              }
            />
            <div className="mt-2 flex justify-between text-xs text-slate-400">
              <span>
                {formatSize(job.loaded)} / {formatSize(job.size)}
              </span>
              {isPending(job) ? (
                <button
                  className="text-sky-300"
                  onClick={() => uploadQueue.cancel(job.id)}
                >
                  取消上传
                </button>
              ) : (
                ["failed", "cancelled"].includes(job.status) && (
                  <button
                    className="text-sky-300"
                    onClick={() => uploadQueue.retry(job.id)}
                  >
                    重试上传
                  </button>
                )
              )}
            </div>
            {job.error && (
              <p className="mt-2 break-words text-xs text-amber-300">
                {job.error}
              </p>
            )}
          </article>
        ))}
      </div>
    </aside>
  );
}
