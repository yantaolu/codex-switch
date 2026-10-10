#!/usr/bin/env python3
"""Validate a public stable release and propose its binary Homebrew recipe."""
import argparse
import base64
import hashlib
import json
import os
import re
import subprocess
import urllib.request

SOURCE = 'yantaolu/codex-switch'
TAP = 'yantaolu/homebrew-tap'
FORMULA = 'Formula/codex-switch.rb'
VERSION = r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)'


def api(path, data=None, source=False):
    env = os.environ.copy()
    if source and env.get('RELEASE_TOKEN'):
        env['GH_TOKEN'] = env['RELEASE_TOKEN']
    command = ['gh', 'api', path]
    if data is not None:
        command += ['--method', 'POST', '--input', '-']
    result = subprocess.run(command, input=json.dumps(data) if data is not None else None,
                            text=True, capture_output=True, env=env, check=True)
    return json.loads(result.stdout) if result.stdout.strip() else None


def download(url, limit=128 * 1024 * 1024):
    with urllib.request.urlopen(url, timeout=90) as response:
        content = response.read(limit + 1)
    if len(content) > limit:
        raise ValueError('Release asset exceeds download limit')
    return content


def release_hashes(release, tag, fetch=download):
    if not re.fullmatch('v' + VERSION, tag):
        raise ValueError('Expected a stable vMAJOR.MINOR.PATCH tag')
    if release['tag_name'] != tag or release['draft'] or release['prerelease']:
        raise ValueError('Only published stable releases can update the Tap')
    version = tag[1:]
    names = [f'codex-switch_{version}_{kind}.tar.gz'
             for kind in ('darwin_arm64', 'darwin_amd64', 'source')]
    assets = {}
    for asset in release['assets']:
        if asset['name'] in assets:
            raise ValueError('Duplicate release asset')
        assets[asset['name']] = asset['browser_download_url']
    prefix = f'https://github.com/{SOURCE}/releases/download/{tag}/'
    for name in ['checksums.txt', *names]:
        if assets.get(name) != prefix + name:
            raise ValueError(f'Missing or unexpected release asset: {name}')
    hashes = {}
    for line in fetch(assets['checksums.txt']).decode().splitlines():
        match = re.fullmatch(r'([a-f0-9]{64})  (\S+)', line)
        if not match or match[2] in hashes:
            raise ValueError('Malformed or duplicate checksum entry')
        hashes[match[2]] = match[1]
    if set(hashes) != set(names):
        raise ValueError('Checksum manifest must contain both architectures and source')
    for name in names:
        if hashlib.sha256(fetch(assets[name])).hexdigest() != hashes[name]:
            raise ValueError(f'Release checksum mismatch: {name}')
    return hashes


def recipe_version(recipe):
    versions = re.findall(r'  version "(' + VERSION + r')" if Version.detect\(stable.url\).to_s != "\1"', recipe)
    if len(versions) != 1:
        raise ValueError('Unrecognized formula version guard')
    return versions[0][0]


def update_recipe(recipe, tag, hashes):
    old = recipe_version(recipe)
    version = tag[1:]
    if tuple(map(int, version.split('.'))) < tuple(map(int, old.split('.'))):
        return None  # An older release event must not downgrade the Tap.
    updated = re.sub(r'(?ms)^  bottle do\n.*?^  end\n\n?', '', recipe)
    for arch in ('arm64', 'amd64'):
        pattern = (r'  url "https://github.com/' + re.escape(SOURCE) +
                   r'/releases/download/v' + re.escape(old) +
                   r'/codex-switch_' + re.escape(old) + '_' + f'darwin_{arch}' +
                   r'\.tar.gz"\n    sha256 "[a-f0-9]{64}"')
        name = f'codex-switch_{version}_darwin_{arch}.tar.gz'
        replacement = (f'  url "https://github.com/{SOURCE}/releases/download/{tag}/{name}"\n'
                       f'    sha256 "{hashes[name]}"')
        updated, count = re.subn(pattern, replacement, updated)
        if count != 1:
            raise ValueError(f'Unrecognized {arch} formula URL or checksum')
    updated = updated.replace(f'version "{old}" if Version.detect(stable.url).to_s != "{old}"',
                              f'version "{version}" if Version.detect(stable.url).to_s != "{version}"')
    if old == version:
        without_bottle = re.sub(r'(?ms)^  bottle do\n.*?^  end\n\n?', '', recipe)
        if updated != without_bottle:
            raise ValueError('Published version has different checksums; refusing replacement')
        return None
    return updated


def content_at(ref):
    item = api(f'repos/{TAP}/contents/{FORMULA}?ref={ref}')
    return base64.b64decode(item['content']).decode()


def sync(tag, request=api, get_content=content_at, fetch=download):
    release = request(f'repos/{SOURCE}/releases/tags/{tag}', source=True)
    hashes = release_hashes(release, tag, fetch)
    base = request(f'repos/{TAP}/git/ref/heads/main')['object']['sha']
    proposed = update_recipe(get_content(base), tag, hashes)
    if proposed is None:
        print(f'{tag}: Tap already current or newer; nothing changed')
        return
    branch = f'release/codex-switch-{tag}'
    prs = request(f'repos/{TAP}/pulls?state=all&head=yantaolu:{branch}&base=main&per_page=100')
    refs = request(f'repos/{TAP}/git/matching-refs/heads/{branch}')
    exact = [ref for ref in refs if ref['ref'] == f'refs/heads/{branch}']
    if exact:
        head = exact[0]['object']['sha']
        if get_content(head) != proposed:
            raise ValueError('Existing release branch differs; refusing to overwrite it')
        changed = request(f'repos/{TAP}/compare/{base}...{head}')
        if [item['filename'] for item in changed['files']] != [FORMULA]:
            raise ValueError('Release branch must change only the formula')
    else:
        if prs:
            raise ValueError('Release PR already exists without its branch; inspect it before retrying')
        commit = request(f'repos/{TAP}/git/commits/{base}')
        tree = request(f'repos/{TAP}/git/trees', {
            'base_tree': commit['tree']['sha'],
            'tree': [{'path': FORMULA, 'mode': '100644', 'type': 'blob', 'content': proposed}]})
        head = request(f'repos/{TAP}/git/commits', {
            'message': f'codex-switch {tag[1:]}', 'tree': tree['sha'], 'parents': [base]})['sha']
        request(f'repos/{TAP}/git/refs', {'ref': f'refs/heads/{branch}', 'sha': head})
    if prs:
        if len(prs) != 1 or prs[0]['state'] != 'open' or prs[0]['head']['sha'] != head:
            raise ValueError('Existing release PR is closed or differs from the release branch')
        print(f"Existing release PR: {prs[0]['html_url']}")
        return
    pr = request(f'repos/{TAP}/pulls', {
        'title': f'codex-switch {tag[1:]}', 'head': branch, 'base': 'main',
        'body': f'Update from https://github.com/{SOURCE}/releases/tag/{tag}.\n\n'
                'Both native archives and the source archive match checksums.txt.\n'
                'Publish bottles automatically after both macOS test-bot jobs pass.'})
    print(f"Created release PR: {pr['html_url']}")


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tag', required=True)
    args = parser.parse_args()
    if not re.fullmatch('v' + VERSION, args.tag):
        parser.error('Expected vMAJOR.MINOR.PATCH')
    sync(args.tag)
