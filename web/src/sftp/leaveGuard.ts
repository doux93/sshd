import { type UploadQueue, isPending } from "./transfers";

// Subscribe directly so protection starts with enqueue, before React renders.
// Only active jobs require a browser navigation warning; finished history does not.
export function protectPendingUploads(queue: UploadQueue, target: Window) {
  let registered = false;
  const warn = (event: BeforeUnloadEvent) => {
    if (!queue.getSnapshot().some(isPending)) return;
    event.preventDefault();
    event.returnValue = "";
  };
  const sync = () => {
    const pending = queue.getSnapshot().some(isPending);
    if (pending === registered) return;
    if (pending) target.addEventListener("beforeunload", warn);
    else target.removeEventListener("beforeunload", warn);
    registered = pending;
  };
  const unsubscribe = queue.subscribe(sync);
  sync();
  return () => {
    unsubscribe();
    if (registered) target.removeEventListener("beforeunload", warn);
  };
}

// Ask before revoking credentials. A cancelled logout must leave uploads running.
export function confirmUploadLogout(
  queue: UploadQueue,
  ask: (message: string) => boolean,
) {
  const pending = queue.getSnapshot().filter(isPending);
  if (!pending.length) return true;
  if (
    !ask(
      `还有 ${pending.length} 个上传任务未完成。退出登录会中断这些任务，是否继续？`,
    )
  )
    return false;
  for (const job of pending) queue.cancel(job.id);
  return true;
}
