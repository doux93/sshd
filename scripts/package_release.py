"""Package only tested binaries and user documentation; never include local data."""
import hashlib
import os
from pathlib import Path
import re
import sys
import zipfile

root = Path(__file__).resolve().parents[1]
version = (root / 'VERSION').read_text().strip()
if not re.fullmatch(r'\d+\.\d+\.\d+(?:-[a-z0-9.]+)?', version):
    raise SystemExit('Invalid VERSION')
os_name = sys.argv[1]
if os_name not in ('Windows', 'Linux'):
    raise SystemExit('Expected Windows or Linux')
name = 'sshx.exe' if os_name == 'Windows' else 'sshx'
binary = root / '.testbin' / name
out = root / 'release-dist'
out.mkdir(exist_ok=True)
archive = out / f'sshx-{os_name}-amd64.zip'
with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as z:
    info = zipfile.ZipInfo(name)
    info.create_system = 3
    info.external_attr = (0o100755 << 16)
    z.writestr(info, binary.read_bytes(), compress_type=zipfile.ZIP_DEFLATED)
    for source, target in [('docs/QUICKSTART.md', 'QUICKSTART.md'), ('VERSION', 'VERSION')]:
        z.write(root / source, target)
    z.writestr('BUILD.txt', f'sshx {version}\n{os_name} amd64\nCommit: {os.environ.get("GITHUB_SHA", "local")}\n')
digest = hashlib.sha256(archive.read_bytes()).hexdigest()
(archive.with_suffix('.zip.sha256')).write_text(f'{digest}  {archive.name}\n')
print(archive.name, digest)
