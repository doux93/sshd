const { test } = require("node:test");
const assert = require("node:assert/strict");
const { fixture } = require("./ui-helper.cjs");
const tick = () => new Promise(setImmediate);
const file = (name) => ({ name, size: 1024 });

test("uploads preserve the original target, queue sequentially and wait for server commit", async () => {
  const { UploadQueue } = fixture().load("src/sftp/transfers.ts");
  const calls = [];
  const q = new UploadQueue(
    (...args) =>
      new Promise((resolve, reject) => calls.push({ args, resolve, reject })),
  );
  q.enqueue("session-a", "host-a", "/original", [file("one"), file("two")]);
  q.enqueue("session-b", "host-b", "/other", [file("three")]);
  assert.equal(calls.length, 1);
  calls[0].args[3](1024, 1024);
  assert.equal(q.getSnapshot()[0].status, "saving");
  assert.ok(q.getSnapshot()[0].file);
  calls[0].resolve();
  await tick();
  assert.equal(q.getSnapshot()[0].status, "completed");
  assert.equal(q.getSnapshot()[0].file, null);
  assert.equal(calls[1].args[0], "session-a");
  assert.equal(calls[1].args[1], "/original/two");
  calls[1].resolve();
  await tick();
  assert.equal(calls[2].args[0], "session-b");
  assert.equal(calls[2].args[1], "/other/three");
  calls[2].resolve();
  await tick();
  assert.ok(q.getSnapshot().every((j) => j.status === "completed"));
});

test("cancelling active and queued uploads stops requests and preserves files for retry", async () => {
  const { UploadQueue } = fixture().load("src/sftp/transfers.ts");
  const calls = [];
  const q = new UploadQueue(
    (...args) =>
      new Promise((resolve, reject) => {
        calls.push({ args, resolve, reject });
        args[4].addEventListener("abort", () => reject(Error("aborted")));
      }),
  );
  q.enqueue("a", "host", "/", [file("active"), file("queued")]);
  const [first, second] = q.getSnapshot();
  q.cancel(second.id);
  q.cancel(first.id);
  await tick();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].args[4].aborted, true);
  assert.ok(q.getSnapshot().every((j) => j.status === "cancelled" && j.file));
  q.retry(second.id);
  assert.equal(calls.length, 2);
  assert.equal(calls[1].args[1], "/queued");
  calls[1].resolve();
  await tick();
  q.clearFinished();
  assert.equal(q.getSnapshot().length, 0);
});

test("failed uploads retry the same file and clearing history keeps pending jobs", async () => {
  const { UploadQueue } = fixture().load("src/sftp/transfers.ts");
  const calls = [];
  const q = new UploadQueue(
    (...args) =>
      new Promise((resolve, reject) => calls.push({ args, resolve, reject })),
  );
  q.enqueue("a", "host", "/safe", [file("retry")]);
  calls[0].reject(Error("connection lost"));
  await tick();
  const failed = q.getSnapshot()[0];
  assert.equal(failed.status, "failed");
  q.retry(failed.id);
  q.clearFinished();
  assert.equal(q.getSnapshot().length, 1);
  assert.equal(calls[1].args[2], calls[0].args[2]);
  calls[1].resolve();
  await tick();
  assert.equal(q.getSnapshot()[0].status, "completed");
});

test("saved command data rejects malformed and multiline entries", () => {
  const storage = new Map([
    [
      "sshx.commands",
      JSON.stringify([
        { id: "ok", name: "disk", command: "df -h" },
        { id: "newline", name: "bad", command: "pwd\nrm anything" },
        { id: "escape", name: "bad", command: "pwd\u001b[2J" },
        { id: "long", name: "bad", command: "a".repeat(4097) },
        { id: "broken", command: "pwd" },
        null,
      ]),
    ],
  ]);
  const { readCommands } = fixture(storage).load(
    "src/workspace/CommandLibrary.tsx",
  );
  const rows = readCommands();
  assert.equal(rows.length, 1);
  assert.equal(rows[0].command, "df -h");
});
