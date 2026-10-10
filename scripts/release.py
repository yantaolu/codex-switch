#!/usr/bin/env python3
"""Build local macOS release candidates, checksums and a source Homebrew formula."""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile

root = Path(__file__).resolve().parents[1]
# Only maintained public documents enter release packages.
public_documents = ('README.md', 'CHANGELOG.md', 'docs/usage.md',
                    'docs/providers.md', 'docs/troubleshooting.md',
                    'docs/architecture.md', 'docs/development.md',
                    'docs/releasing.md')
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--version', required=True)
parser.add_argument('--output', type=Path, default=root / 'dist')
parser.add_argument('--arch', choices=('arm64', 'amd64'), help='Build only one architecture; default builds both')
args = parser.parse_args()
if not re.fullmatch(r'\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?', args.version):
    parser.error('version must be a semantic version')
output = args.output.resolve()
output.mkdir(parents=True, exist_ok=True)

def run(argv, **kw):
    return subprocess.check_output(argv, cwd=root, text=True, **kw).strip()

# Include licenses for modules actually linked into the application.
licenses = output / 'licenses'
licenses.mkdir(exist_ok=True)
modules = {}
for line in run(['go', 'list', '-deps', '-f', '{{if .Module}}{{.Module.Path}} {{.Module.Version}} {{.Module.Dir}}{{end}}', './cmd/codex-switch']).splitlines():
    fields = line.split(' ', 2)
    if len(fields) == 3 and fields[1]:
        modules[fields[0]] = fields[1:]
for module, (version, directory) in modules.items():
    candidates = [p for p in Path(directory).iterdir() if p.is_file() and p.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING'))]
    if not candidates:
        raise SystemExit(f'module license missing: {module}')
    target = licenses / module.replace('/', '_')
    target.mkdir(exist_ok=True)
    for path in candidates:
        shutil.copyfile(path, target / path.name)
goroot = Path(run(['go', 'env', 'GOROOT']))
license_path = next((p for p in (goroot / 'LICENSE', goroot.parent / 'LICENSE') if p.is_file()), None)
if license_path is None:
    raise SystemExit('Go distribution license missing')
shutil.copyfile(license_path, licenses / 'Go-LICENSE')
manifest = output / 'modules.json'
manifest.write_text(json.dumps({m: v[0] for m, v in sorted(modules.items())}, indent=2) + '\n')

def archive(path, entries):
    # Normalize timestamps/ownership and gzip headers for repeatable packaging.
    with path.open('wb') as destination, gzip.GzipFile(filename='', mode='wb', fileobj=destination, mtime=0) as zipped:
        with tarfile.open(fileobj=zipped, mode='w') as tar:
            for name, source in sorted(entries):
                data = source.read_bytes()
                info = tarfile.TarInfo(name)
                info.size = len(data)
                info.mode = 0o755 if source.name == 'codex-switch' else 0o644
                info.mtime = 0
                tar.addfile(info, io.BytesIO(data))

artifacts = []
for arch in (args.arch,) if args.arch else ('arm64', 'amd64'):
    binary_dir = output / f'darwin-{arch}'
    binary_dir.mkdir(exist_ok=True)
    binary = binary_dir / 'codex-switch'
    env = dict(os.environ, GOOS='darwin', GOARCH=arch, CGO_ENABLED='0')
    subprocess.run(['go', 'build', '-trimpath', '-ldflags', f'-s -w -X main.version={args.version}', '-o', str(binary), './cmd/codex-switch'], cwd=root, env=env, check=True)
    package = output / f'codex-switch_{args.version}_darwin_{arch}.tar.gz'
    entries = [('codex-switch', binary), ('LICENSE', root / 'LICENSE'), ('modules.json', manifest)]
    entries += [(name, root / name) for name in public_documents]
    entries += [(str(p.relative_to(output)), p) for p in licenses.rglob('*') if p.is_file()]
    archive(package, entries)
    artifacts.append(package)

# Vendor source dependencies into the release archive so Formula builds are offline
# even on Homebrew versions without a separate dependency-fetch hook.
vendor = output / '_source_vendor'
subprocess.run(['go', 'mod', 'vendor', '-o', str(vendor)], cwd=root, check=True)

# Source release intentionally excludes personal reviews, IDE state and account files.
source_entries = [(f'codex-switch-{args.version}/vendor/{p.relative_to(vendor)}', p) for p in vendor.rglob('*') if p.is_file()]
for name in ('cmd', 'internal'):
    source_entries += [(f'codex-switch-{args.version}/{p.relative_to(root)}', p) for p in (root / name).rglob('*.go')]
for directory in ('scripts', 'packaging'):
    source_entries += [(f'codex-switch-{args.version}/{p.relative_to(root)}', p) for p in (root / directory).rglob('*') if p.is_file() and p.suffix in ('.py', '.in')]
for name in ('go.mod', 'go.sum', 'LICENSE', 'Makefile', *public_documents):
    source_entries.append((f'codex-switch-{args.version}/{name}', root / name))
source_package = output / f'codex-switch_{args.version}_source.tar.gz'
archive(source_package, source_entries)
artifacts.append(source_package)
checksums = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in artifacts}
(output / 'checksums.txt').write_text(''.join(f'{digest}  {name}\n' for name, digest in sorted(checksums.items())))
template = (root / 'packaging/homebrew/codex-switch.rb.in').read_text()
formula = template.replace('@VERSION@', args.version).replace('@SHA256@', checksums[source_package.name])
(output / 'codex-switch.rb').write_text(formula)
print(json.dumps(dict(version=args.version, files=list(checksums), formula=str(output / 'codex-switch.rb')), indent=2))
