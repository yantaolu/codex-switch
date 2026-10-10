#!/usr/bin/env python3
"""Register a private publishing App and configure its scoped Actions credentials."""
import base64
from http.server import BaseHTTPRequestHandler, HTTPServer
import html
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request
import webbrowser

SOURCE = 'yantaolu/codex-switch'
TARGET = 'yantaolu/homebrew-tap'


def request(path, token=None, data=None):
    headers = {'Accept': 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if data is not None:
        headers['Content-Type'] = 'application/json'
    req = urllib.request.Request('https://api.github.com/' + path, headers=headers,
                                 data=json.dumps(data).encode() if data is not None else None)
    with urllib.request.urlopen(req, timeout=30) as response:
        return json.load(response)


def jwt(app_id, pem):
    def encode(value):
        return base64.urlsafe_b64encode(value).rstrip(b'=')
    now = int(time.time())
    payload = encode(b'{"alg":"RS256","typ":"JWT"}') + b'.' + encode(json.dumps({
        'iat': now - 60, 'exp': now + 540, 'iss': str(app_id)}).encode())
    signature = subprocess.run(['openssl', 'dgst', '-sha256', '-sign', str(pem)],
                               input=payload, capture_output=True, check=True).stdout
    return (payload + b'.' + encode(signature)).decode()


def main():
    state = secrets.token_urlsafe(32)
    complete = False
    app = None
    with tempfile.TemporaryDirectory(prefix='codex-switch-tap-app-') as temporary:
        pem = Path(temporary) / 'private-key.pem'

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass  # Callback codes and credentials never enter logs.

            def page(self, body, status=200):
                content = ('<!doctype html><meta charset="utf-8"><title>Codex Switch Tap</title>'
                           '<style>body{font:16px system-ui;max-width:760px;margin:60px auto;padding:20px;line-height:1.8}'
                           'button{font:inherit;padding:8px 20px}a{color:#1681de}</style>' + body).encode()
                self.send_response(status)
                self.send_header('Content-Type', 'text/html; charset=utf-8')
                self.send_header('Cache-Control', 'no-store')
                self.send_header('Referrer-Policy', 'no-referrer')
                self.send_header('X-Frame-Options', 'DENY')
                self.send_header('Content-Length', str(len(content)))
                self.end_headers()
                self.wfile.write(content)

            def do_GET(self):
                nonlocal app
                parsed = urllib.parse.urlsplit(self.path)
                query = urllib.parse.parse_qs(parsed.query)
                if query.get('state') != [state]:
                    return self.page('无效的配置入口。', 403)
                if parsed.path == '/start' and app is None:
                    manifest = {'name': 'codex-switch-tap-' + secrets.token_hex(3),
                                'url': 'https://github.com/' + SOURCE, 'public': False,
                                'hook_attributes': {'url': 'https://github.com/' + SOURCE, 'active': False},
                                'redirect_url': base + '/callback',
                                'default_permissions': {'contents': 'write', 'pull_requests': 'write'},
                                'default_events': []}
                    return self.page('<h1>配置 Tap 自动发布</h1><p>注册私人 GitHub App，'
                                     '仅授予代码及 PR 写入权限。下一步只安装到 homebrew-tap。</p>'
                                     f'<form method="post" action="https://github.com/settings/apps/new?state={state}">'
                                     f'<input type="hidden" name="manifest" value="{html.escape(json.dumps(manifest), quote=True)}">'
                                     '<button>在 GitHub 注册</button></form>')
                if parsed.path == '/callback' and app is None and len(query.get('code', [])) == 1:
                    try:
                        app = request('app-manifests/' + urllib.parse.quote(query['code'][0], safe='') + '/conversions', data={})
                        descriptor = os.open(pem, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                        with os.fdopen(descriptor, 'w') as output:
                            output.write(app['pem'])
                        print(f'App 已注册：{app["slug"]}；请仅选择 homebrew-tap 安装。', flush=True)
                    except Exception as error:
                        app = None
                        return self.page(f'注册回调失败（{type(error).__name__}），请重新启动脚本。', 500)
                if app is not None:
                    install = 'https://github.com/apps/' + app['slug'] + '/installations/new'
                    return self.page('<h1>安装到指定仓库</h1><p>选择 Only select repositories，仅选择 '
                                     '<strong>homebrew-tap</strong>，点击 Install 后返回此页。</p>'
                                     f'<p><a target="_blank" rel="noreferrer" href="{install}">打开 App 安装页</a></p>'
                                     f'<form method="post" action="/finish?state={state}"><button>核验安装并启用</button></form>')
                self.page('页面不存在。', 404)

            def do_POST(self):
                nonlocal complete
                parsed = urllib.parse.urlsplit(self.path)
                if parsed.path != '/finish' or urllib.parse.parse_qs(parsed.query).get('state') != [state] or app is None:
                    return self.page('无效的操作。', 403)
                try:
                    token = jwt(app['id'], pem)
                    installations = request('app/installations', token)
                    matches = [item for item in installations if item['account']['login'] == 'yantaolu' and not item['suspended_at']]
                    if len(matches) != 1 or matches[0]['repository_selection'] != 'selected':
                        raise ValueError('Expected one selected-repository installation')
                    installation = matches[0]['id']
                    access = request(f'app/installations/{installation}/access_tokens', token, {
                        'permissions': {'contents': 'write', 'pull_requests': 'write'}})['token']
                    repositories = request('installation/repositories', access)
                    if repositories['total_count'] != 1 or repositories['repositories'][0]['full_name'] != TARGET:
                        raise ValueError('Install this App only on homebrew-tap')
                    subprocess.run(['gh', 'secret', 'set', 'TAP_APP_PRIVATE_KEY', '--repo', SOURCE],
                                   input=app['pem'], text=True, capture_output=True, check=True)
                    subprocess.run(['gh', 'variable', 'set', 'TAP_APP_ID', '--repo', SOURCE, '--body', str(app['id'])],
                                   text=True, capture_output=True, check=True)
                    # Revoke the verification token immediately; Actions mints its own short-lived tokens.
                    req = urllib.request.Request('https://api.github.com/installation/token',
                                                 headers={'Authorization': 'Bearer ' + access}, method='DELETE')
                    with urllib.request.urlopen(req, timeout=30):
                        pass
                    complete = True
                    self.page('<h1>配置完成</h1><p>App 仅授权 homebrew-tap。Actions 凭据已保存，临时私钥将自动删除。可以关闭此页。</p>')
                    print(f'完成：TAP_APP_ID={app["id"]}；私钥已保存到 GitHub Actions Secret，不输出密钥。', flush=True)
                except Exception as error:
                    self.page(f'<h1>尚未完成</h1><p>请检查 App 仅安装到 homebrew-tap，以及 gh 登录状态。'
                              f'错误类型：{type(error).__name__}。修改后可以重试。</p>'
                              f'<form method="post" action="/finish?state={state}"><button>重试核验</button></form>', 400)

        server = HTTPServer(('127.0.0.1', 0), Handler)
        server.timeout = 1
        base = f'http://127.0.0.1:{server.server_port}'
        url = base + '/start?state=' + state
        print('一次性配置入口：' + url, flush=True)
        webbrowser.open(url)
        deadline = time.monotonic() + 1800
        try:
            while not complete and time.monotonic() < deadline:
                server.handle_request()
        finally:
            server.server_close()
        if not complete:
            raise SystemExit('配置超时；临时私钥已删除。可在 GitHub App 设置中生成新私钥后手动配置。')


if __name__ == '__main__':
    main()
