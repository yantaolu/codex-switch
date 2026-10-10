import hashlib
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('sync_tap', Path(__file__).with_name('sync-tap.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


def fixture(version='1.0.1'):
    tag = 'v' + version
    prefix = f'https://github.com/{m.SOURCE}/releases/download/{tag}/'
    blobs = {prefix + f'codex-switch_{version}_{kind}.tar.gz': kind.encode()
             for kind in ('darwin_arm64', 'darwin_amd64', 'source')}
    hashes = {url.rsplit('/', 1)[1]: hashlib.sha256(blob).hexdigest() for url, blob in blobs.items()}
    blobs[prefix + 'checksums.txt'] = ''.join(f'{value}  {key}\n' for key, value in hashes.items()).encode()
    release = {'tag_name': tag, 'draft': False, 'prerelease': False,
               'assets': [{'name': url.rsplit('/', 1)[1], 'browser_download_url': url} for url in blobs]}
    return release, blobs, hashes


def recipe():
    prefix = f'https://github.com/{m.SOURCE}/releases/download/v1.0.0/'
    return ('class CodexSwitch < Formula\n  bottle do\n    root_url "old"\n  end\n\n' +
            ''.join(f'  if Hardware::CPU.{cpu}?\n    url "{prefix}codex-switch_1.0.0_darwin_{arch}.tar.gz"\n    sha256 "' + '0' * 64 + '"\n  end\n\n'
                    for cpu, arch in [('arm', 'arm64'), ('intel', 'amd64')]) +
            '  version "1.0.0" if Version.detect(stable.url).to_s != "1.0.0"\n  def install\n    bin.install "codex-switch"\n  end\nend\n')


class ReleaseTests(unittest.TestCase):
    def test_complete_release_checks_all_archives(self):
        release, blobs, hashes = fixture()
        self.assertEqual(m.release_hashes(release, 'v1.0.1', blobs.__getitem__), hashes)

    def test_missing_intel_or_source_blocks_update(self):
        for name in ('darwin_amd64', 'source'):
            release, blobs, _ = fixture()
            release['assets'] = [item for item in release['assets'] if name not in item['name']]
            with self.assertRaises(ValueError):
                m.release_hashes(release, 'v1.0.1', blobs.__getitem__)

    def test_corrupt_archive_blocks_update(self):
        release, blobs, _ = fixture()
        blobs[release['assets'][0]['browser_download_url']] = b'corrupted'
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            m.release_hashes(release, 'v1.0.1', blobs.__getitem__)

    def test_prerelease_and_draft_block_update(self):
        for flag in ('draft', 'prerelease'):
            release, blobs, _ = fixture()
            release[flag] = True
            with self.assertRaises(ValueError):
                m.release_hashes(release, 'v1.0.1', blobs.__getitem__)

    def test_duplicate_checksum_blocked(self):
        release, blobs, _ = fixture()
        url = next(url for url in blobs if url.endswith('checksums.txt'))
        blobs[url] += blobs[url].splitlines(keepends=True)[0]
        with self.assertRaisesRegex(ValueError, 'duplicate'):
            m.release_hashes(release, 'v1.0.1', blobs.__getitem__)

    def test_foreign_asset_url_blocked(self):
        release, blobs, _ = fixture()
        release['assets'][0]['browser_download_url'] = 'https://example.com/archive'
        with self.assertRaises(ValueError):
            m.release_hashes(release, 'v1.0.1', blobs.__getitem__)

    def test_update_preserves_install_and_clears_old_bottles(self):
        _, _, hashes = fixture()
        updated = m.update_recipe(recipe(), 'v1.0.1', hashes)
        self.assertNotIn('bottle do', updated)
        self.assertNotIn('1.0.0', updated)
        self.assertIn('bin.install "codex-switch"', updated)
        self.assertEqual(m.recipe_version(updated), '1.0.1')

    def test_current_version_is_noop_and_cannot_replace(self):
        hashes = {f'codex-switch_1.0.0_darwin_{arch}.tar.gz': '0'*64 for arch in ('arm64','amd64')}
        self.assertIsNone(m.update_recipe(recipe(), 'v1.0.0', hashes))
        hashes[next(iter(hashes))] = '1'*64
        with self.assertRaisesRegex(ValueError, 'refusing replacement'):
            m.update_recipe(recipe(), 'v1.0.0', hashes)

    def test_stale_version_never_downgrades(self):
        self.assertIsNone(m.update_recipe(recipe(), 'v0.9.9', {}))

    def test_branch_drift_blocks_writes(self):
        release, blobs, _ = fixture()
        writes = []
        def request(path, data=None, source=False):
            if data is not None:
                writes.append(path)
            if '/releases/' in path: return release
            if '/git/ref/' in path: return {'object': {'sha': 'a'*40}}
            if '/pulls?' in path: return []
            if '/matching-refs/' in path: return [{'ref': 'refs/heads/release/codex-switch-v1.0.1', 'object': {'sha': 'b'*40}}]
            raise AssertionError(path)
        with self.assertRaisesRegex(ValueError, 'refusing to overwrite'):
            m.sync('v1.0.1', request, lambda sha: recipe() if sha == 'a'*40 else 'drift', blobs.__getitem__)
        self.assertEqual(writes, [])

    def test_new_version_creates_one_formula_commit_and_retry_reuses_pr(self):
        release, blobs, hashes = fixture()
        expected = m.update_recipe(recipe(), 'v1.0.1', hashes)
        state = {'ref': False, 'pr': False, 'writes': []}
        pr = {'state': 'open', 'head': {'sha': 'b'*40}, 'html_url': 'https://github.com/yantaolu/homebrew-tap/pull/2'}
        def request(path, data=None, source=False):
            if data is not None:
                state['writes'].append(path)
                if path.endswith('/git/trees'):
                    self.assertEqual(data['tree'], [{'path': m.FORMULA, 'mode': '100644', 'type': 'blob', 'content': expected}])
                    return {'sha': 'tree'}
                if path.endswith('/git/commits'): return {'sha': 'b'*40}
                if path.endswith('/git/refs'): state['ref'] = True; return {}
                if path.endswith('/pulls'): state['pr'] = True; return pr
                raise AssertionError(path)
            if '/releases/' in path: return release
            if '/git/ref/' in path: return {'object': {'sha': 'a'*40}}
            if '/pulls?' in path: return [pr] if state['pr'] else []
            if '/matching-refs/' in path:
                return [{'ref': 'refs/heads/release/codex-switch-v1.0.1', 'object': {'sha': 'b'*40}}] if state['ref'] else []
            if '/git/commits/' in path: return {'tree': {'sha': 'base-tree'}}
            if '/compare/' in path: return {'files': [{'filename': m.FORMULA}]}
            raise AssertionError(path)
        content = lambda sha: recipe() if sha == 'a'*40 else expected
        m.sync('v1.0.1', request, content, blobs.__getitem__)
        writes = list(state['writes'])
        self.assertEqual(len(writes), 4)
        m.sync('v1.0.1', request, content, blobs.__getitem__)
        self.assertEqual(state['writes'], writes)


if __name__ == '__main__':
    unittest.main()
