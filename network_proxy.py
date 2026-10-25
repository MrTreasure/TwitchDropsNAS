"""Optional external HTTP proxy or isolated, explicitly selected mihomo node."""
import os
import secrets
import subprocess
import tempfile
from pathlib import Path
from urllib.parse import urlsplit

import yaml


class ConfiguredProxy:
    def __init__(self):
        self.port = int(os.environ.get('TDM_PROXY_PORT', '28197'))
        self.source = os.environ.get('TDM_PROXY_SOURCE', '')
        self.node_name = os.environ.get('TDM_PROXY_NODE', '')
        self.url = os.environ.get('TDM_PROXY_URL', '')
        self.controller = int(os.environ.get('TDM_PROXY_CONTROLLER_PORT', '28198')) if self.source else None
        self.label = os.environ.get('TDM_PROXY_LABEL', '已配置代理' if self.source or self.url else '未配置代理')[:100]
        self.secret = secrets.token_urlsafe(32)
        self.process = None
        self.directory = None
        if self.source and self.url:
            raise RuntimeError('选择外部代理或指定 mihomo 节点中的一种')
        if self.source and not self.node_name:
            raise RuntimeError('使用 mihomo 配置时必须显式指定 TDM_PROXY_NODE')
        if self.source:
            self.url = f'http://127.0.0.1:{self.port}'
        if self.url and urlsplit(self.url).scheme not in ('http', 'https'):
            raise RuntimeError('TDM_PROXY_URL 仅支持 HTTP/HTTPS 代理地址')

    def start(self):
        if not self.source or self.process and self.process.poll() is None:
            return
        config = yaml.safe_load(Path(self.source).read_text())
        node = next((n for n in config.get('proxies', []) if n.get('name') == self.node_name), None)
        if node is None:
            raise RuntimeError('未找到指定的 mihomo 节点')
        if self.directory:
            self.directory.cleanup()
        self.directory = tempfile.TemporaryDirectory(prefix='tdm-proxy-')
        runtime = {
            'mixed-port': self.port, 'bind-address': '127.0.0.1', 'allow-lan': False,
            'mode': 'rule', 'log-level': 'silent', 'ipv6': False,
            'external-controller': f'127.0.0.1:{self.controller}', 'secret': self.secret,
            'proxies': [node], 'rules': [f'MATCH,{self.node_name}'],
        }
        # Node credentials remain in the original mounted source and anonymous
        # memory. No generated on-disk proxy config is written.
        fd = os.memfd_create('twitch-drops-proxy', 0)
        try:
            os.write(fd, yaml.safe_dump(runtime).encode())
            os.lseek(fd, 0, 0)
            self.process = subprocess.Popen(
                [os.environ.get('TDM_MIHOMO', '/usr/local/bin/mihomo'), '-d', self.directory.name, '-f', f'/proc/self/fd/{fd}'],
                pass_fds=(fd,), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            )
        finally:
            os.close(fd)

    def close(self):
        if self.process and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
        self.process = None
        if self.directory:
            self.directory.cleanup()
            self.directory = None
