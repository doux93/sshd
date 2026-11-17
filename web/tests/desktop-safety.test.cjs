const { test } = require("node:test");
const assert = require("node:assert/strict");
const { fixture } = require("./ui-helper.cjs");
const tick = () => new Promise(setImmediate);

test("AI shortcut preserves Ctrl+K by default, persists opt-in and ignores composition", () => {
  const storage = new Map();
  const s = fixture(storage).load("src/ai/shortcut.ts");
  const event = {
    type: "keydown",
    code: "KeyK",
    ctrlKey: true,
    shiftKey: false,
    altKey: false,
    metaKey: false,
    isComposing: false,
  };
  assert.equal(s.matchesCommandShortcut(event), false);
  assert.equal(s.matchesCommandShortcut({ ...event, shiftKey: true }), true);
  for (const patch of [
    { isComposing: true },
    { altKey: true },
    { metaKey: true },
    { type: "keyup" },
  ])
    assert.equal(
      s.matchesCommandShortcut({ ...event, shiftKey: true, ...patch }),
      false,
    );
  s.setCommandShortcut("ctrl-k");
  assert.equal(s.matchesCommandShortcut(event), true);
  assert.equal(s.matchesCommandShortcut({ ...event, shiftKey: true }), false);
  assert.equal(
    fixture(storage).load("src/ai/shortcut.ts").matchesCommandShortcut(event),
    true,
  );
  storage.set("sshx.ai.shortcut", JSON.stringify("invalid"));
  assert.equal(
    fixture(storage).load("src/ai/shortcut.ts").matchesCommandShortcut(event),
    false,
  );
});

test("upload leave warning follows queued, transferring and saving jobs and is removed on disposal", async () => {
  const f = fixture();
  const { UploadQueue } = f.load("src/sftp/transfers.ts");
  const { protectPendingUploads } = f.load("src/sftp/leaveGuard.ts");
  const calls = [];
  const queue = new UploadQueue(
    (...args) =>
      new Promise((resolve, reject) => calls.push({ args, resolve, reject })),
  );
  const listeners = new Set();
  const target = {
    addEventListener: (_, fn) => listeners.add(fn),
    removeEventListener: (_, fn) => listeners.delete(fn),
  };
  const warning = () => {
    const event = {
      prevented: false,
      returnValue: null,
      preventDefault() {
        this.prevented = true;
      },
    };
    for (const listener of listeners) listener(event);
    return event.prevented;
  };
  const dispose = protectPendingUploads(queue, target);
  assert.equal(listeners.size, 0);
  queue.enqueue("a", "host", "/tmp", [
    { name: "first", size: 10 },
    { name: "second", size: 10 },
  ]);
  assert.equal(warning(), true);
  calls[0].args[3](10, 10);
  assert.equal(queue.getSnapshot()[0].status, "saving");
  assert.equal(warning(), true);
  calls[0].resolve();
  await tick();
  assert.equal(warning(), true); // second file still in progress
  calls[1].reject(Error("offline"));
  await tick();
  assert.equal(warning(), false);
  assert.equal(listeners.size, 0);
  queue.retry(queue.getSnapshot()[1].id);
  assert.equal(warning(), true);
  dispose();
  assert.equal(listeners.size, 0);
  calls[2].resolve();
  await tick();
  assert.equal(listeners.size, 0);
});

test("cancelled logout preserves active uploads; confirmed logout cancels active and queued jobs", async () => {
  const f = fixture();
  const { UploadQueue } = f.load("src/sftp/transfers.ts");
  const { confirmUploadLogout } = f.load("src/sftp/leaveGuard.ts");
  const queue = new UploadQueue(
    (session, path, file, progress, signal) =>
      new Promise((_, reject) =>
        signal.addEventListener("abort", () => reject(Error("aborted"))),
      ),
  );
  queue.enqueue("a", "host", "/tmp", [
    { name: "one", size: 10 },
    { name: "two", size: 10 },
  ]);
  assert.equal(
    confirmUploadLogout(queue, () => false),
    false,
  );
  assert.equal(queue.getSnapshot()[0].status, "uploading");
  assert.equal(queue.getSnapshot()[1].status, "queued");
  assert.equal(
    confirmUploadLogout(queue, () => true),
    true,
  );
  await tick();
  assert.ok(queue.getSnapshot().every((job) => job.status === "cancelled"));
  assert.equal(
    confirmUploadLogout(queue, () => {
      throw Error("unnecessary prompt");
    }),
    true,
  );
});
