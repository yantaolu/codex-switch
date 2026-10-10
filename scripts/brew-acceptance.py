#!/usr/bin/env python3
"""Real brew install/upgrade/cleanup/uninstall, using a unique keg-only fixture.

Creates a temporary tap and one temporary Keychain entry. It never activates
an account in the user's Codex directory or links its binary into PATH.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import time

root = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--use-installed-go', action='store_true', help='Use existing Homebrew Go without upgrading dependencies; record limited dependency acceptance')
p.add_argument('--output', type=Path, default=root / 'dist/validation/brew.json')
args = p.parse_args()
env = dict(os.environ, HOMEBREW_NO_AUTO_UPDATE='1', HOMEBREW_NO_INSTALL_CLEANUP='1', HOMEBREW_NO_ENV_HINTS='1')

def run(argv, extra=None):
    print(' '.join(argv), flush=True)
    try:
        return subprocess.check_output(argv, cwd=root, env=dict(env, **(extra or {})), text=True, stderr=subprocess.STDOUT).strip()
    except subprocess.CalledProcessError as error:
        print(error.output, flush=True)
        raise

identifier = f'codex-switch-acceptance-{os.getpid()}'
tap = f'yantaolu/{identifier}'
fullname = f'{tap}/{identifier}'
prefix = Path(run(['brew', '--prefix']))
cellar = Path(run(['brew', '--cellar'])) / identifier
opt = prefix / 'opt' / identifier / 'bin/codex-switch'
fixture = Path(tempfile.mkdtemp(prefix='codex-switch-brew-'))
key_created = False
installed = False
steps = []
started = time.monotonic()

def phase(name):
    return run(['go', 'test', './internal/switcher', '-run', '^TestHomebrewCredentialAcceptance$', '-count=1', '-timeout', '30s'],
               dict(CXS_BREW_PHASE=name, CXS_BREW_ROOT=str(fixture), CXS_BREW_BIN=str(opt)))

try:
    run(['brew', 'tap-new', '--no-git', tap])
    repository = Path(run(['brew', '--repository', tap]))
    for index, version in enumerate(('0.8.0-rc.1', '0.8.0-rc.2')):
        candidate = fixture / f'package-{index}'
        run(['python3', 'scripts/release.py', '--version', version, '--output', str(candidate)])
        formula = (candidate / 'codex-switch.rb').read_text()
        # Fixture URLs do not carry a stable version recognizable by Homebrew.
        formula = formula.replace('  license "MIT"', f'  version "{version}"\n  license "MIT"')
        formula = formula.replace('class CodexSwitch < Formula', 'class CodexSwitchAcceptance < Formula')
        source = candidate / f'codex-switch_{version}_source.tar.gz'
        formula = re.sub(r'  url "[^"]+"', f'  url "{source.as_uri()}"', formula)
        formula = formula.replace('  depends_on :macos', '  keg_only "Isolated acceptance fixture"\n\n  depends_on :macos')
        if args.use_installed_go:
            compiler = str(Path(run(['go', 'env', 'GOROOT'])) / 'bin/go')
            formula = formula.replace('system "go"', 'system ' + json.dumps(compiler))
            formula = formula.replace('Utils.safe_popen_read("go"', 'Utils.safe_popen_read(' + json.dumps(compiler))
            # This fixture uses a verified existing compiler. The production
            # Formula retains its normal build dependency.
            formula = formula.replace('  depends_on "go" => :build\n', '')
        (repository / 'Formula').mkdir(exist_ok=True)
        (repository / 'Formula' / f'{identifier}.rb').write_text(formula.replace('class CodexSwitchAcceptance < Formula', 'class ' + ''.join(part.capitalize() for part in identifier.split('-')) + ' < Formula'))
        run(['brew', 'install' if index == 0 else 'upgrade', '--build-from-source', fullname])
        installed = True
        run(['brew', 'test', fullname])
        if index == 0:
            key_created = True
            phase('prepare')
            steps.append('installed source formula; helper and Keychain passed')
        else:
            run(['brew', 'cleanup', '--prune=all', fullname])
            kegs = sorted(p.name for p in cellar.iterdir() if p.is_dir())
            if kegs != [version]:
                raise RuntimeError(f'old Cellar version retained: {kegs}')
            phase('verify')
            steps.append('upgraded and removed old Cellar; existing helper/vault/Keychain passed')
    run(['brew', 'uninstall', fullname])
    installed = False
    if not (fixture / 'store/vault.enc').is_file():
        raise RuntimeError('uninstall deleted account data')
    steps.append('uninstall retained account data')
    result = dict(status='passed', brew=run(['brew', '--version']), versions=['0.8.0-rc.1', '0.8.0-rc.2'], steps=steps,
                  elapsed_seconds=round(time.monotonic() - started, 2), isolated=True, dependency_installation='existing Go reused' if args.use_installed_go else 'standard brew dependencies')
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2))
finally:
    if key_created:
        phase('cleanup')
    if installed:
        run(['brew', 'uninstall', fullname])
    run(['brew', 'untap', tap])
    shutil.rmtree(fixture)
