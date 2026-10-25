"""Optional standalone HTTP UI/API. Unix socket callers retain host-owned trust."""
import hashlib
import hmac
import mimetypes
import os
import secrets
import socket
import time
from pathlib import Path

from aiohttp import web


class WebAccess:
    def __init__(self, directory):
        self.directory = Path(directory)
        self.ui = Path(os.environ.get('TDM_UI_DIR', '/app/ui'))
        token_path = self.directory / 'admin-token'
        self.token = os.environ.get('TDM_ADMIN_TOKEN', '')
        if not self.token:
            if not token_path.exists():
                fd = os.open(token_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
                with os.fdopen(fd, 'w') as handle:
                    handle.write(secrets.token_urlsafe(32))
            self.token = token_path.read_text().strip()
        if len(self.token) < 24:
            raise RuntimeError('TDM_ADMIN_TOKEN 至少需要 24 个字符')
        self.signing_key = hashlib.sha256(self.token.encode()).digest()

    def valid_cookie(self, value):
        try:
            expires, nonce, signature = value.split('.')
            expected = hmac.new(self.signing_key, f'{expires}.{nonce}'.encode(), hashlib.sha256).hexdigest()
            return time.time() < int(expires) <= time.time() + 86400 and hmac.compare_digest(signature, expected)
        except (ValueError, TypeError):
            return False

    def new_cookie(self):
        payload = f'{int(time.time()) + 86400}.{secrets.token_hex(16)}'
        return payload + '.' + hmac.new(self.signing_key, payload.encode(), hashlib.sha256).hexdigest()

    @web.middleware
    async def middleware(self, request, handler):
        peer = request.transport.get_extra_info('socket')
        if peer is not None and peer.family == socket.AF_UNIX:
            request['actor'] = request.headers.get('X-Drops-Actor', '')[:100]
            return await handler(request)
        # No TCP caller may impersonate the trusted Unix adapter using a header.
        request['actor'] = 'standalone-admin'
        path = request.path
        if path == '/internal/scan' or path.startswith('/internal/'):
            raise web.HTTPNotFound()
        if path.startswith('/api/'):
            origin = request.headers.get('Origin')
            expected = os.environ.get('TDM_PUBLIC_ORIGIN') or f'{request.scheme}://{request.host}'
            if origin and origin != expected:
                raise web.HTTPForbidden(reason='Origin mismatch')
            if path == '/api/session' and request.method == 'POST':
                body = await request.json()
                if not isinstance(body, dict) or not isinstance(body.get('token'), str) or not hmac.compare_digest(body['token'].encode(), self.token.encode()):
                    raise web.HTTPUnauthorized()
                result = web.json_response({'authenticated': True})
                result.set_cookie('tdm_session', self.new_cookie(), httponly=True, samesite='Strict',
                                  secure=expected.startswith('https://'), max_age=86400, path='/')
                return result
            bearer = request.headers.get('Authorization', '')
            token_valid = bearer.startswith('Bearer ') and hmac.compare_digest(bearer[7:].encode(), self.token.encode())
            cookie_valid = self.valid_cookie(request.cookies.get('tdm_session', ''))
            if not token_valid and not cookie_valid:
                raise web.HTTPUnauthorized()
            if cookie_valid and not token_valid and request.method not in ('GET', 'HEAD') and origin != expected:
                raise web.HTTPForbidden(reason='Origin required')
        elif path != '/health' and path != '/' and not path.startswith('/assets/'):
            # Legacy unprefixed routes exist exclusively for the private socket.
            raise web.HTTPNotFound()
        response = await handler(request)
        response.headers.update({'X-Content-Type-Options': 'nosniff', 'Referrer-Policy': 'no-referrer',
            'Content-Security-Policy': "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"})
        return response

    def install(self, app):
        app.middlewares.append(self.middleware)
        # Reuse the identical backend handlers for standalone and embedded UIs.
        for route in list(app.router.routes()):
            path = route.resource.canonical
            if path == '/health' or path.startswith('/internal/'):
                continue
            app.router.add_route(route.method, '/api' + path, route.handler)

        async def session(request):
            return web.json_response({'authenticated': True})

        async def logout(request):
            result = web.json_response({'authenticated': False})
            result.del_cookie('tdm_session', path='/')
            return result

        async def index(request):
            return web.FileResponse(self.ui / 'index.html', headers={'Cache-Control': 'no-store'})

        async def asset(request):
            name = request.match_info['name']
            file = (self.ui / 'assets' / name).resolve()
            if file.parent != (self.ui / 'assets').resolve() or not file.is_file():
                raise web.HTTPNotFound()
            result = web.FileResponse(file, headers={'Cache-Control': 'public, max-age=31536000, immutable'})
            result.content_type = mimetypes.guess_type(file.name)[0] or 'application/octet-stream'
            return result

        async def openapi(request):
            return web.FileResponse(Path(__file__).parent / 'docs/openapi.json', headers={'Cache-Control': 'no-store'})

        app.router.add_get('/api/openapi.json', openapi)
        app.router.add_get('/api/session', session)
        app.router.add_delete('/api/session', logout)
        app.router.add_get('/', index)
        app.router.add_get('/assets/{name}', asset)
