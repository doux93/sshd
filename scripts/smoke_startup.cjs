// Exercise the packaged CLI and Windows local authentication without opening a desktop browser.
const { spawn, execFileSync } = require('node:child_process');
const { mkdtempSync, readFileSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { resolve, join } = require('node:path');
const assert = require('node:assert/strict');
const binary = resolve('.testbin', process.platform === 'win32' ? 'sshx.exe' : 'sshx');
const version = readFileSync('VERSION', 'utf8').trim();
assert.ok(execFileSync(binary, ['--version'], {encoding:'utf8'}).startsWith('sshx ' + version + ' ('));
if (process.platform !== 'win32') process.exit(0);
const dir = mkdtempSync(join(tmpdir(), 'sshx-startup-'));
const child = spawn(binary, ['--addr', '127.0.0.1:0', '--data', join(dir,'test.db'), '--open-browser=false'], {stdio:['ignore','pipe','pipe']});
(async () => {
  try {
    const url = await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(Error('Local startup timed out')), 20000);
      let output = '';
      const read = data => { output += data; const match = output.match(/http:\/\/127\.0\.0\.1:\d+\/\?token=[^\s]+/); if (match) { clearTimeout(timeout); resolve(new URL(match[0])); } };
      child.stdout.on('data', read); child.stderr.on('data', read);
      child.on('error', err => { clearTimeout(timeout); reject(err); });
      child.on('exit', code => { clearTimeout(timeout); reject(Error('Startup exited ' + code)); });
    });
    assert.equal((await fetch(url.origin+'/api/sessions')).status, 401);
    assert.equal((await fetch(url.origin+'/api/sessions', {headers:{'X-Auth-Token':url.searchParams.get('token')}})).status, 200);
    console.log('Windows local startup and token authentication passed');
  } finally {
    if (child.exitCode === null) await new Promise(resolve => { child.once('exit', resolve); child.kill(); });
    rmSync(dir, {recursive:true, force:true, maxRetries:5});
  }
})().catch(err => { console.error(err.message); process.exitCode=1; });
