// Runs the actual published Windows executable. The Linux endpoint is an
// isolated WSL2 Ubuntu OpenSSH server, with its own temporary user and keys.
// It does not connect to user infrastructure or use real user credentials.
const assert = require('node:assert/strict');
const { spawn, spawnSync, execFileSync } = require('node:child_process');
const { createHash, randomBytes } = require('node:crypto');
const fs = require('node:fs');
const net = require('node:net');
const { tmpdir } = require('node:os');
const { resolve, join } = require('node:path');
const { StringDecoder } = require('node:string_decoder');
const { chromium, expect } = require('../web/node_modules/@playwright/test');

assert.equal(process.platform, 'win32', 'This acceptance must run natively on Windows');
assert.equal(process.env.GITHUB_ACTIONS, 'true', 'Disposable GitHub runner required; do not run this fixture against a personal WSL distribution');
const distribution = process.env.WSL_DISTRO || 'Ubuntu-24.04';
assert.ok(['Ubuntu-22.04', 'Ubuntu-24.04'].includes(distribution), 'Unsupported acceptance distribution');
const root = resolve(__dirname, '..');
const output = join(root, 'acceptance-results');
fs.mkdirSync(output, { recursive: true });
const local = fs.mkdtempSync(join(tmpdir(), 'sshx 中文 验收 '));
const runID = randomBytes(6).toString('hex');
const user = 'sx_' + runID;
const remote = '/tmp/sshx-acceptance-' + runID;
const password = randomBytes(24).toString('hex');
const envValue = 'keep_' + runID;
const evidence = { release: process.env.RELEASE_TAG || 'v0.3.0', distribution, scope: 'Native Windows executable -> WSL2 Ubuntu OpenSSH/tmux', checks: [], success: false };
const wsClients = new Set();
const proxySockets = new Set();
let app, url, browser, page, terminal, proxy, fixtureStarted = false;
const binary = join(local, 'sshx 工作台.exe');
const database = join(local, '工作台 数据.db');
const keyPath = join(local, '连接 私钥');
fs.copyFileSync(join(root, '.acceptance-download/unpacked/sshx.exe'), binary);
const version = execFileSync(binary, ['--version'], { encoding: 'utf8' }).trim();
assert.ok(version.startsWith('sshx ' + evidence.release.slice(1) + ' ('), 'Unexpected executable version');
evidence.executable = version;

function check(name, details) {
  evidence.checks.push({ name, ...(details ? { details } : {}) });
  console.log('PASS: ' + name);
}
function wsl(args, options = {}) {
  return spawnSync('C:\\Windows\\System32\\wsl.exe', ['-d', distribution, '-u', 'root', '--', ...args], { encoding: 'utf8', timeout: 30000, ...options });
}
function wslOK(args, options) {
  const r = wsl(args, options);
  if (r.error || r.status !== 0) throw Error('WSL fixture command failed: ' + (r.error?.message || r.stderr));
  return r.stdout.trim();
}
async function until(predicate, message, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise(r => setTimeout(r, 50));
  }
  throw Error(message);
}
async function startApp() {
  app = spawn(binary, ['--addr', '127.0.0.1:0', '--data', database, '--open-browser=false'], { stdio: ['ignore', 'pipe', 'pipe'] });
  url = await new Promise((resolveURL, reject) => {
    let data = '';
    const timer = setTimeout(() => reject(Error('sshx startup timed out')), 15000);
    const read = chunk => {
      data = (data + chunk.toString()).slice(-16000);
      const match = data.match(/http:\/\/127\.0\.0\.1:\d+\/\?token=[^\s]+/);
      if (match) { clearTimeout(timer); resolveURL(new URL(match[0])); }
    };
    app.stdout.on('data', read); app.stderr.on('data', read);
    app.once('error', err => { clearTimeout(timer); reject(err); });
    app.once('exit', code => { clearTimeout(timer); reject(Error('sshx exited during startup: ' + code)); });
  });
}
async function stopApp() {
  if (!app || app.exitCode !== null || app.signalCode !== null) return;
  const child = app;
  await new Promise((resolveExit, reject) => {
    const timer = setTimeout(() => reject(Error('sshx did not exit')), 10000);
    child.once('exit', () => { clearTimeout(timer); resolveExit(); });
    // On Windows this forcibly terminates the process, exercising crash recovery.
    child.kill('SIGKILL');
  });
}
async function api(path, { method = 'GET', data, body, token } = {}) {
  return fetch(url.origin + '/api' + path, {
    method, body: body ?? (data === undefined ? undefined : JSON.stringify(data)),
    headers: { 'X-Auth-Token': token ?? url.searchParams.get('token'), 'Content-Type': data === undefined ? 'application/octet-stream' : 'application/json' },
    signal: AbortSignal.timeout(35000),
  });
}
async function json(path, options, status = 200) {
  const r = await api(path, options);
  assert.equal(r.status, status, path + ': ' + await r.clone().text());
  return r.json();
}
async function ok(path, options, status = 204) {
  const r = await api(path, options);
  assert.equal(r.status, status, path + ': ' + await r.text());
}
async function attach(id) {
  const target = new URL('/ws', url); target.protocol = 'ws:';
  target.searchParams.set('token', url.searchParams.get('token'));
  const ws = new WebSocket(target); ws.binaryType = 'arraybuffer'; wsClients.add(ws);
  const state = { ws, output: '', attached: false, error: null };
  const decoder = new StringDecoder('utf8');
  ws.addEventListener('error', () => { state.error = 'WebSocket error'; });
  ws.addEventListener('message', event => {
    if (typeof event.data === 'string') {
      const message = JSON.parse(event.data);
      if (message.type === 'attached' && message.channelId === 1) state.attached = true;
      if (message.type === 'error') state.error = message.message;
    } else {
      const bytes = Buffer.from(event.data);
      if (bytes[0] === 1) state.output = (state.output + decoder.write(bytes.subarray(1))).slice(-100000);
    }
  });
  await until(() => ws.readyState === WebSocket.OPEN || state.error, 'WebSocket open timed out');
  assert.equal(state.error, null);
  ws.send(JSON.stringify({ type: 'attach', sessionId: id, channelId: 1, cols: 140, rows: 32, sizeMode: 'negotiate' }));
  await until(() => state.attached || state.error, 'Terminal attach timed out');
  assert.equal(state.error, null);
  state.send = command => { state.output = ''; ws.send(Buffer.concat([Buffer.from([1]), Buffer.from(command + '\r')])); };
  return state;
}
async function measure(terminal, tag, initialize = false) {
  const setup = initialize ? `export SSHX_ACCEPTANCE=${envValue}; cd "$HOME"; sleep 300 & export SSHX_JOB=$!; ` : '';
  terminal.send(setup + `kill -0 "$SSHX_JOB" && printf '\\n${tag}:%s:%s:%s:%s\\n' "$$" "$SSHX_ACCEPTANCE" "$PWD" "$SSHX_JOB"`);
  const pattern = new RegExp(tag + ':([0-9]+):' + envValue + ':/home/' + user + ':([0-9]+)');
  await until(() => pattern.test(terminal.output), 'Original shell/job marker missing: ' + tag);
  const match = terminal.output.match(pattern);
  return { shellPID: match[1], jobPID: match[2] };
}

(async () => {
  try {
    fixtureStarted = true;
    const linuxPort = Number(wslOK(['bash', '-s'], { input: `set -euo pipefail
mkdir -p '${remote}' /run/sshd
chmod 700 '${remote}'
useradd -m -s /bin/bash '${user}'
printf '%s:%s\\n' '${user}' '${password}' | chpasswd
ssh-keygen -q -t ed25519 -N '' -f '${remote}/host'
ssh-keygen -q -t ed25519 -N '' -f '${remote}/identity'
install -d -m 700 -o '${user}' -g '${user}' '/home/${user}/.ssh'
install -m 600 -o '${user}' -g '${user}' '${remote}/identity.pub' '/home/${user}/.ssh/authorized_keys'
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
cat > '${remote}/sshd_config' <<EOF
ListenAddress 127.0.0.1
Port $port
HostKey ${remote}/host
PidFile ${remote}/sshd.pid
AuthorizedKeysFile .ssh/authorized_keys
PasswordAuthentication yes
KbdInteractiveAuthentication no
UsePAM no
PermitRootLogin no
AllowUsers ${user}
Subsystem sftp internal-sftp
LogLevel ERROR
EOF
/usr/sbin/sshd -f '${remote}/sshd_config' -E '${remote}/sshd.log'
printf '%s' "$port"
` }));
    assert.ok(Number.isInteger(linuxPort) && linuxPort > 0);
    fs.writeFileSync(keyPath, wslOK(['cat', remote + '/identity']) + '\n');
    evidence.linux = wslOK(['uname', '-sr']);
    const osRelease = wslOK(['bash', '-s'], { input: '. /etc/os-release\nprintf "%s|%s" "$ID" "$VERSION_ID"\n' });
    assert.equal(osRelease, 'ubuntu|' + distribution.slice('Ubuntu-'.length), 'Actual Linux distribution differs from matrix target');
    evidence.osRelease = osRelease;
    evidence.openssh = wslOK(['dpkg-query', '-W', 'openssh-server']);
    evidence.tmux = wslOK(['tmux', '-V']);
    check('Isolated Ubuntu OpenSSH server and disposable login created', { distribution, osRelease, openssh: evidence.openssh, tmux: evidence.tmux });
    await until(() => new Promise(resolveReady => {
      const socket = net.connect(linuxPort, '127.0.0.1');
      let banner = '';
      const finish = ready => { socket.destroy(); resolveReady(ready); };
      socket.setTimeout(1000);
      socket.once('error', () => finish(false));
      socket.once('timeout', () => finish(false));
      socket.on('data', chunk => { banner += chunk; if (banner.startsWith('SSH-')) finish(true); });
    }), 'WSL OpenSSH endpoint not reachable from Windows', 20000);

    proxy = net.createServer(client => {
      const upstream = net.connect(linuxPort, '127.0.0.1');
      proxySockets.add(client); proxySockets.add(upstream);
      const close = () => { client.destroy(); upstream.destroy(); proxySockets.delete(client); proxySockets.delete(upstream); };
      client.on('error', close); upstream.on('error', close);
      client.on('close', close); upstream.on('close', close);
      client.pipe(upstream); upstream.pipe(client);
    });
    await new Promise(r => proxy.listen(0, '127.0.0.1', r));
    const config = { host: '127.0.0.1', port: proxy.address().port, user, authType: 'password', persistentTerminal: true };
    await startApp();
    assert.equal((await fetch(url.origin + '/api/nodes')).status, 401);
    let result = await api('/connections/test', { method: 'POST', data: { config, password } });
    assert.equal(result.status, 409, 'First connection must request host-key trust');
    const prompt = (await result.json()).hostKey;
    assert.ok(prompt.fingerprint && prompt.host);
    await json('/connections/test', { method: 'POST', data: { config, password, accepts: { [prompt.host]: prompt.fingerprint } } });
    assert.equal((await json('/nodes')).length, 0);
    assert.equal((await json('/sessions')).length, 0);
    result = await api('/connections/test', { method: 'POST', data: { config, password: 'incorrect-disposable-password' } });
    assert.ok(result.status >= 400);
    assert.equal((await result.json()).code, 'authentication');
    check('Real host-key confirmation and wrong-password rejection; draft test creates no host/session');

    const node = await json('/nodes', { method: 'POST', data: { kind: 'connection', name: 'Linux 验收主机', config, password } }, 201);
    assert.equal(node.config.hasPassword, true);
    assert.equal(JSON.stringify(node).includes(password), false);
    let session = await json('/nodes/' + node.id + '/connect', { method: 'POST', data: {} }, 201);
    assert.ok(session.recoveryId);
    terminal = await attach(session.id);
    const original = await measure(terminal, 'INITIAL', true);
    check('Windows saved credential opens real Linux tmux shell and background job', original);

    browser = await chromium.launch();
    page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
    const browserInput = { attached: 0, receivedBytes: 0, sentBytes: 0, input: '', events: [] };
    const browserStart = Date.now();
    evidence.browserInput = browserInput;
    page.on('websocket', socket => {
      socket.on('framereceived', ({ payload }) => {
        if (typeof payload === 'string') {
          const message = JSON.parse(payload);
          if (message.type === 'attached') {
            browserInput.attached++;
            browserInput.events.push({ event: 'attached', ms: Date.now() - browserStart });
          }
        } else browserInput.receivedBytes += payload.length;
      });
      socket.on('framesent', ({ payload }) => {
        if (typeof payload !== 'string') {
          browserInput.sentBytes += payload.length;
          browserInput.input = (browserInput.input + payload.subarray(1).toString()).slice(-4000);
        }
      });
    });
    await page.goto(url.href);
    await expect(page.getByRole('tab', { name: node.name, exact: true })).toBeVisible();
    await page.locator('.xterm-screen').click();
    browserInput.events.push({ event: 'keyboard-start', ms: Date.now() - browserStart });
    browserInput.focusBefore = await page.evaluate(() => document.activeElement?.className);
    await page.keyboard.type("printf '\\nUI_SUM:%s\\n' \"$((17+25))\"");
    await page.keyboard.press('Enter');
    await until(() => terminal.output.includes('UI_SUM:42'), 'Browser keyboard did not reach Linux shell');
    check('Native Windows Chromium terminal keyboard reaches Linux shell');

    const payload = randomBytes(2 * 1024 * 1024);
    const localFile = join(local, '中文 空格 文件.bin'); fs.writeFileSync(localFile, payload);
    const originalPath = '/home/' + user + '/中文 空格 文件.bin';
    const renamedPath = '/home/' + user + '/已重命名 文件.bin';
    const digest = createHash('sha256').update(payload).digest('hex');
    await page.getByRole('button', { name: '文件', exact: true }).click();
    await expect(page.getByLabel('远端目录')).toHaveValue('/home/' + user);
    await page.locator('input[type=file]').setInputFiles(localFile);
    await expect(page.getByRole('complementary', { name: '上传队列' }).getByText('已完成', { exact: true })).toBeVisible({ timeout: 30000 });
    const query = path => '?session=' + session.id + '&path=' + encodeURIComponent(path);
    let downloaded = await api('/files/download' + query(originalPath));
    assert.equal(downloaded.status, 200);
    assert.equal(createHash('sha256').update(Buffer.from(await downloaded.arrayBuffer())).digest('hex'), digest);
    assert.equal(wslOK(['sha256sum', '--', originalPath]).split(/\s+/)[0], digest);
    await ok('/files/rename?session=' + session.id, { method: 'POST', data: { path: originalPath, newPath: renamedPath } });
    downloaded = await api('/files/download' + query(renamedPath));
    assert.equal(downloaded.status, 200);
    assert.equal(createHash('sha256').update(Buffer.from(await downloaded.arrayBuffer())).digest('hex'), digest);
    check('2 MiB SFTP browser upload, Linux-side hash, download and rename with Chinese/spaced filenames', { bytes: payload.length, sha256: digest });

    const keyNode = await json('/nodes', { method: 'POST', data: { kind: 'connection', name: 'Windows 私钥路径', config: { ...config, authType: 'key', keyPath, persistentTerminal: false } } }, 201);
    const keySession = await json('/nodes/' + keyNode.id + '/connect', { method: 'POST', data: {} }, 201);
    const keyTerminal = await attach(keySession.id);
    keyTerminal.send("printf '\\nKEY_SYSTEM:%s\\n' \"$(uname -s)\"");
    await until(() => keyTerminal.output.includes('KEY_SYSTEM:Linux'), 'Private-key authentication failed');
    await ok('/sessions/' + keySession.id, { method: 'DELETE' });
    await ok('/nodes/' + keyNode.id, { method: 'DELETE' });
    check('Windows private-key path with Chinese characters/spaces authenticates to Linux');

    for (const socket of proxySockets) socket.destroy();
    await until(async () => (await json('/sessions')).find(s => s.id === session.id)?.state === 'closed', 'Broken SSH connection not detected');
    session = await json('/recovery/' + session.recoveryId + '/resume', { method: 'POST', data: {} }, 201);
    terminal = await attach(session.id);
    assert.deepEqual(await measure(terminal, 'NETWORK'), original);
    check('Actual SSH TCP interruption and resume preserve shell PID, background job PID, environment and directory');

    const previousToken = url.searchParams.get('token');
    await stopApp();
    await startApp();
    assert.equal((await api('/nodes', { token: previousToken })).status, 401);
    const saved = (await json('/nodes')).find(n => n.id === node.id);
    assert.equal(saved.config.hasPassword, true);
    assert.equal((await json('/recovery')).length, 1);
    session = await json('/recovery/' + session.recoveryId + '/resume', { method: 'POST', data: {} }, 201);
    terminal = await attach(session.id);
    assert.deepEqual(await measure(terminal, 'RESTART'), original);
    check('Force-killed Windows executable restarts on same database and retrieves saved credential; original Linux shell/job survive');

    await page.goto(url.href);
    await page.getByRole('button', { name: '会话库', exact: true }).click();
    const card = page.getByRole('article', { name: '保活终端 ' + node.name });
    await expect(card).toBeInViewport();
    await card.getByRole('button', { name: '打开保活终端' }).click();
    await expect(page.getByRole('tab', { name: node.name, exact: true })).toBeVisible();
    await page.getByRole('button', { name: '会话库', exact: true }).click();
    await expect(card).toContainText('已接入工作台');
    await page.screenshot({ path: join(output, 'windows-linux-recovery.png'), fullPage: true, animations: 'disabled' });
    check('Browser finds recovered terminal after executable restart');

    await ok('/files/delete?session=' + session.id, { method: 'POST', data: { path: renamedPath } });
    await ok('/recovery/' + session.recoveryId + '/terminate', { method: 'POST', data: {} }, 400);
    assert.deepEqual(await measure(terminal, 'CONFIRMATION'), original);
    await ok('/recovery/' + session.recoveryId + '/terminate', { method: 'POST', data: { confirmed: true } });
    assert.equal((await json('/recovery')).length, 0);
    assert.notEqual(wsl(['runuser', '-u', user, '--', 'tmux', '-L', 'sshx-' + session.recoveryId, 'has-session', '-t', '=terminal']).status, 0);
    await ok('/nodes/' + node.id, { method: 'DELETE' });
    assert.equal((await json('/nodes')).length, 0);
    check('Remote termination requires confirmation, ends actual tmux session, and saved-host cleanup succeeds');
    evidence.success = true;
  } catch (error) {
    evidence.error = error.message;
    if (terminal) evidence.terminalTail = terminal.output.slice(-12000);
    if (page) {
      evidence.focusAfter = await page.evaluate(() => document.activeElement?.className).catch(() => null);
      await page.screenshot({ path: join(output, 'failure.png'), fullPage: true, timeout: 5000 }).catch(() => {});
    }
    process.exitCode = 1;
    console.error('FAIL: ' + error.message);
  } finally {
    if (browser) await browser.close().catch(() => {});
    for (const ws of wsClients) ws.close();
    // Best-effort removal of only this disposable app database's credentials.
    if (app && app.exitCode === null && app.signalCode === null && url) {
      try {
        for (const rec of await json('/recovery')) await api('/recovery/' + rec.id + '/terminate', { method: 'POST', data: { confirmed: true } });
        for (const s of await json('/sessions')) await api('/sessions/' + s.id, { method: 'DELETE' });
        for (const n of await json('/nodes')) await api('/nodes/' + n.id, { method: 'DELETE' });
      } catch { /* runner is disposable; record normal assertion failure above */ }
    }
    await stopApp().catch(() => {});
    for (const socket of proxySockets) socket.destroy();
    if (proxy) await new Promise(r => proxy.close(r));
    if (fixtureStarted) wsl(['bash', '-s'], { input: `if [ -f '${remote}/sshd.pid' ]; then kill "$(cat '${remote}/sshd.pid')" 2>/dev/null || true; fi\npkill -KILL -u '${user}' 2>/dev/null || true\nuserdel -r '${user}' 2>/dev/null || true\nrm -rf -- '${remote}'\n` });
    fs.rmSync(local, { recursive: true, force: true, maxRetries: 5 });
    fs.writeFileSync(join(output, 'results.json'), JSON.stringify(evidence, null, 2) + '\n');
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
