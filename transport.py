"""Low-traffic metadata transport, with an optional explicitly configured proxy."""
import asyncio
import datetime as dt
import json
import time
from urllib.parse import urlsplit

import aiohttp

from storage import now


class ServiceError(Exception):
    def __init__(self, message, code='upstream_error', status=400):
        super().__init__(message)
        self.code, self.status = code, status


class Transport:
    HOSTS = {'id.twitch.tv', 'gql.twitch.tv', 'spade.twitch.tv', 'www.twitch.tv'}

    def __init__(self, store, proxy):
        self.store, self.proxy = store, proxy
        self.session = None
        self.route = 'unknown'
        self.last_direct_probe = 0
        self.direct_successes = 0
        self.proxy_bytes = 0
        self.last_error = None
        self.last_success = None
        self.blocked_until = 0
        self.lock = asyncio.Semaphore(2)

    async def start(self):
        self.proxy.start()
        self.session = aiohttp.ClientSession(
            trust_env=False, cookie_jar=aiohttp.DummyCookieJar(),
            headers={'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36'},
            connector=aiohttp.TCPConnector(limit=4),
        )
        await asyncio.sleep(.2)

    def usage(self):
        current = dt.datetime.now(dt.timezone(dt.timedelta(hours=8)))
        midnight = current.replace(hour=0, minute=0, second=0, microsecond=0).timestamp()
        return {'hourBytes': self.store.traffic_since(time.time()-3600),
                'dayBytes': self.store.traffic_since(midnight),
                'proxyDayBytes': self.store.traffic_since(midnight, 'proxy'),
                'directDayBytes': self.store.traffic_since(midnight, 'direct'),
                'timeZone': 'Asia/Shanghai',
                'basis': 'proxy-tunnel-bytes; direct-response-estimate' if self.proxy.controller else 'all-response-estimate'}

    async def sample_proxy(self):
        if not self.proxy.controller:
            return
        try:
            async with self.session.get(f'http://127.0.0.1:{self.proxy.controller}/connections',
                    headers={'Authorization': f'Bearer {self.proxy.secret}'},
                    timeout=aiohttp.ClientTimeout(total=2)) as response:
                data = await response.json()
                total = int(data['uploadTotal']) + int(data['downloadTotal'])
            difference = max(0, total - self.proxy_bytes)
            self.proxy_bytes = total
            if difference:
                self.store.traffic(time.time(), 'proxy', difference)
        except (aiohttp.ClientError, asyncio.TimeoutError, KeyError, ValueError):
            pass

    async def meter(self):
        while True:
            await asyncio.sleep(5)
            await self.sample_proxy()

    async def _send(self, method, url, route, *, headers=None, data=None, payload=None, cap=262144, timeout=15):
        proxy = self.proxy.url if route == 'proxy' else None
        if route == 'proxy' and not proxy:
            raise ServiceError('直连不可用且尚未配置代理，请检查连接设置', 'proxy_unavailable', 503)
        if proxy and self.proxy.source:
            if not self.proxy.process or self.proxy.process.poll() is not None:
                self.proxy_bytes = 0
                self.proxy.start()
                await asyncio.sleep(.1)
        async with self.session.request(method, url, headers=headers, data=data, json=payload,
                proxy=proxy, allow_redirects=False, timeout=aiohttp.ClientTimeout(total=timeout, sock_connect=4)) as response:
            chunks, length = [], 0
            async for chunk in response.content.iter_chunked(4096):
                length += len(chunk)
                if length > cap:
                    raise ServiceError('上游响应超过大小限制，已停止读取', 'response_limit', 502)
                chunks.append(chunk)
            raw = b''.join(chunks)
            status = response.status
            if route == 'direct' or not self.proxy.controller:
                self.store.traffic(time.time(), route, length + 4096 + len(json.dumps(payload or data or {})))
            if status == 429:
                self.blocked_until = time.time() + 900
                raise ServiceError('Twitch 请求限流，暂停 15 分钟；不切换线路重试', 'rate_limited', 429)
            if status in (403, 503) and ('text/html' in response.headers.get('content-type', '')):
                self.blocked_until = time.time() + 900
                raise ServiceError('Twitch 返回验证或限制页面，请人工检查', 'verification_required', 409)
            try:
                body = json.loads(raw) if raw else {}
            except (ValueError, UnicodeError):
                body = {'text': raw.decode(errors='replace')}
            return status, body

    async def request(self, method, url, *, headers=None, data=None, payload=None, cap=2097152):
        parsed = urlsplit(url)
        if parsed.scheme != 'https' or parsed.hostname not in self.HOSTS or parsed.port not in (None, 443):
            raise ServiceError('此服务只允许访问必要的 Twitch 接口', 'invalid_destination')
        if parsed.hostname == 'www.twitch.tv' or any(x in parsed.path for x in ['.m3u8', '.mp4', '.ts', '/integrity']):
            raise ServiceError('服务禁止下载网页播放器、视频或请求验证绕过接口', 'media_blocked')
        if time.time() < self.blocked_until:
            raise ServiceError('Twitch 限制冷却中，请稍后再试', 'rate_limited', 429)
        async with self.lock:
            await self.sample_proxy()
            config = self.store.get('settings', {})
            mode = config.get('networkMode', 'auto')
            route = 'proxy' if mode == 'proxy' else 'direct' if mode == 'direct' else self.route
            # Probe only an anonymous GET. Never replay OAuth/token writes across routes.
            if mode == 'auto' and (route == 'unknown' or time.time()-self.last_direct_probe > 1800):
                self.last_direct_probe = time.time()
                try:
                    status, _ = await self._send('GET', 'https://id.twitch.tv/oauth2/validate', 'direct', cap=4096, timeout=5)
                    self.direct_successes += 1
                    if route == 'unknown' or self.direct_successes >= 2:
                        route = 'direct'
                except (aiohttp.ClientError, asyncio.TimeoutError):
                    self.direct_successes = 0
                    route = 'proxy'
            if route == 'unknown':
                route = 'proxy'
            try:
                result = await self._send(method, url, route, headers=headers, data=data, payload=payload, cap=cap)
                self.route = route
                self.last_success, self.last_error = now(), None
                return result
            except (aiohttp.ClientError, asyncio.TimeoutError):
                if mode == 'auto' and route == 'direct':
                    self.route = 'proxy'
                self.last_error = '网络连接失败，稍后重试；只使用显式配置的线路'
                raise ServiceError(self.last_error, 'network_error', 503) from None
            finally:
                await self.sample_proxy()

    async def close(self):
        await self.sample_proxy()
        await self.session.close()
        self.proxy.close()
