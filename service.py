"""NAS Twitch account authorization, generic game queue and trusted progress."""
import asyncio
import contextlib
import datetime as dt
import json
import math
import os
import random
import re
import signal
import sys
import time
import uuid
from pathlib import Path

from aiohttp import web

from goals import capture, default_goal, evaluate, merge_progress
from network_proxy import ConfiguredProxy
from reward_images import apply_import, preview_import, resolve_images, reward_image_url, scope_definitions
from storage import FileCredentials, SecretBook, Store, now
from transport import ServiceError, Transport
from twitch import DEVICE_CLIENT, Twitch
from webui import WebAccess

DEFAULT_SETTINGS = {'networkMode': 'auto', 'checkMinSeconds': 600, 'checkMaxSeconds': 1200}
DEFAULT_TASK = {'id': 'war-thunder', 'game': {'id': None, 'name': 'War Thunder', 'slug': 'war-thunder'},
                'enabled': True, 'priority': 100, 'watchOnly': False, 'autoChannels': False,
                'channels': ['warthunder_esports', 'warthunder_esports_de', 'warthunder_esports_fr',
                             'warthunder_esports_ru', 'warthunder_esports_pl']}


def number(value):
    return value if type(value) in (int, float) and math.isfinite(value) and value >= 0 else None


def date_seconds(value):
    try:
        return dt.datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()
    except (ValueError, TypeError, AttributeError):
        return None


def clean_campaign(raw, sampled_at):
    drops = []
    for drop in (raw.get('timeBasedDrops') or [])[:100]:
        own = drop.get('self') or {}
        minutes = number(own.get('currentMinutesWatched'))
        required = number(drop.get('requiredMinutesWatched'))
        claimed = own.get('isClaimed') if isinstance(own.get('isClaimed'), bool) else None
        benefit_data = [(edge.get('benefit') or {}) for edge in (drop.get('benefitEdges') or [])[:10]]
        benefits = [benefit.get('name', '') for benefit in benefit_data]
        images = [{'name': str(benefit.get('name', ''))[:200], 'url': url}
                  for benefit in benefit_data if (url := reward_image_url(benefit.get('imageAssetURL')))]
        drops.append({'id': str(drop.get('id', '')), 'name': str(drop.get('name', ''))[:200],
            'benefits': [str(x)[:200] for x in benefits if x], 'images': images, 'minutes': minutes, 'requiredMinutes': required,
            'claimed': claimed, 'claimable': bool(own.get('dropInstanceID')) and claimed is False,
            'prerequisites': [str(x.get('id', '')) for x in (drop.get('preconditionDrops') or [])],
            'sampledAt': sampled_at})
    game = raw.get('game') or {}
    allow = raw.get('allow') or {}
    return {'id': str(raw.get('id', '')), 'name': str(raw.get('name', ''))[:200],
            'gameId': str(game.get('id', '')), 'gameName': str(game.get('name', ''))[:100],
            'startsAt': raw.get('startAt'), 'endsAt': raw.get('endAt'), 'status': raw.get('status'),
            'linked': (raw.get('self') or {}).get('isAccountConnected'),
            'allowChannels': [{'id': str(c.get('id', '')), 'login': str(c.get('name', c.get('login', '')))}
                              for c in (allow.get('channels') or [])[:100]] if allow.get('isEnabled') else [],
            'drops': drops, 'sampledAt': sampled_at}


class DropsService:
    def __init__(self):
        self.store = Store(os.environ.get('TDM_DATA_DIR', '/data'))
        book_path = os.environ.get('TDM_PASSWORD_BOOK')
        self.book = SecretBook(book_path) if book_path else FileCredentials(Path(os.environ.get('TDM_DATA_DIR', '/data')) / 'credentials.json')
        self.account = self.book.read()
        saved = self.store.get('settings', {})
        self.store.put('settings', {k: saved.get(k, v) for k, v in DEFAULT_SETTINGS.items()})
        if self.store.get('capabilitiesVersion') != 2:
            self.store.put('inventoryFetchedAt', 0)
            self.store.put('catalogueCache', {})
            self.store.put('progress', {'status': 'unknown', 'sampledAt': None, 'catalogueAvailable': False, 'message': '等待重新核对账号进度读取能力'})
            self.store.put('capabilitiesVersion', 2)
        if self.store.get('tasks') is None:
            self.store.put('tasks', [DEFAULT_TASK])
        tasks = self.store.get('tasks')
        for task in tasks:
            task.setdefault('goal', default_goal())
        self.store.put('tasks', tasks)
        self.net = Transport(self.store, ConfiguredProxy())
        self.twitch = Twitch(self.net)
        self.pending = None
        self.auth_task = None
        self.auth_error = None
        self.scan_lock = asyncio.Lock()
        self.selection_lock = asyncio.Lock()
        self.scan_task = None
        self.current = None
        self.next_selection = 0
        self.next_watch = 0
        self.state = 'paused'
        self.reason = '尚未开始挂机'
        self.last_watch = None
        self.watch_reports = self.store.get('watchReports', 0)
        self.channel_errors = 0
        self.auth_requests = []
        self.background = []
        self.revision = 0
        self.automation_changed = asyncio.Event()
        self.announcements = self.load_announcements()

    def event(self, message, level='info'):
        self.store.event(message, level)

    async def start(self):
        await self.net.start()
        self.background = [asyncio.create_task(self.net.meter()), asyncio.create_task(self.engine())]
        self.event('掉宝服务已启动，使用显式配置的连接策略')

    def check_delay(self):
        settings = self.store.get('settings')
        return random.uniform(settings['checkMinSeconds'], settings['checkMaxSeconds'])

    def load_announcements(self):
        path = Path(os.environ.get('TDM_ANNOUNCEMENTS_FILE', str(Path(__file__).parent / 'catalogue/announcements.json')))
        if not path.exists():
            return []
        entries = json.loads(path.read_text())
        if not isinstance(entries, list) or len(entries) > 100:
            raise RuntimeError('公开活动说明文件无效')
        for entry in entries:
            for reward in entry.get('rewards', []):
                reward['imageUrl'] = reward_image_url(reward.get('imageUrl'))
        # Administrator-owned data only. These records have no Twitch reward IDs,
        # account progress or claim state, and never enter automatic goal evaluation.
        return entries

    def image_scopes(self):
        announcements, campaigns = resolve_images(self.announcements, self.store.get('campaigns', []), self.store.get('rewardImageRegistry', {}))
        return scope_definitions(announcements, campaigns)

    def image_import(self, body, save=False):
        if not isinstance(body, dict) or not isinstance(body.get('scope'), str):
            raise ServiceError('请选择图片所属活动')
        scope = next((s for s in self.image_scopes() if s['id'] == body['scope']), None)
        if not scope:
            raise ServiceError('活动资料已变化，请重新选择活动', 'image_scope_missing', 409)
        plan = preview_import(scope, body.get('input'))
        if save:
            plan['updated'] = apply_import(self.store, plan, now())
            self.event('已导入活动奖励图片资料，仅保存原始 CDN 地址')
        return plan

    async def auto_sync(self):
        async def check():
            self.store.put('automationLastStarted', now())
            try:
                await self.scan()
                self.store.put('automationLastStatus', 'success')
            except Exception:
                self.store.put('automationLastStatus', 'error')
            self.store.put('automationLastFinished', now())
        if self.store.get('automationEnabled', True):
            await check()
        while True:
            enabled = self.store.get('automationEnabled', True)
            delay = self.check_delay()
            self.store.put('automationNextAt', time.time() + delay if enabled else None)
            try:
                if enabled:
                    await asyncio.wait_for(self.automation_changed.wait(), timeout=delay)
                else:
                    await self.automation_changed.wait()
                self.automation_changed.clear()
            except asyncio.TimeoutError:
                if self.store.get('automationEnabled', True):
                    await check()

    async def stop(self):
        tasks = self.background + [x for x in [self.auth_task, self.scan_task] if x]
        for task in tasks:
            task.cancel()
        for task in tasks:
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await task
        await self.net.close()
        self.store.db.close()

    def snapshot(self, actor):
        account = None
        if self.account:
            account = {key: self.account.get(key) for key in ['user_id', 'login', 'validatedAt', 'expiresAt']}
        pending = None
        if self.pending and self.pending['actor'] == actor:
            pending = {key: self.pending[key] for key in ['userCode', 'verificationUri', 'expiresAt']}
        usage = self.net.usage()
        session_progress = self.store.get('currentProgress')
        if not self.account or not self.current or not session_progress or session_progress.get('accountId') != self.account['user_id'] or session_progress.get('channelId') != self.current['channel']['id'] or session_progress.get('streamId') != self.current['channel']['stream']['id']:
            session_progress = None
        settings = self.store.get('settings')
        external = os.environ.get('TDM_EXTERNAL_SCHEDULER', 'false').lower() == 'true'
        games = {str(t['game'].get('id')) for t in self.store.get('tasks')}
        names = {t['game']['name'].casefold() for t in self.store.get('tasks')}
        announcements = [a for a in self.announcements if (str(a['gameId']) in games or a['gameName'].casefold() in names) and (date_seconds(a.get('endsAt')) or 0) > time.time()]
        announcements, campaigns = resolve_images(announcements, self.store.get('campaigns', []), self.store.get('rewardImageRegistry', {}))
        return {'announcements': announcements,
            'rewardImageScopes': [{key: scope[key] for key in ['id', 'name', 'gameName']} | {'rewardCount': len(scope['rewards'])} for scope in self.image_scopes()],
            'schedule': {
            'metadataSource': 'external' if external else 'service',
            'metadataMinSeconds': int(os.environ.get('TDM_EXTERNAL_CHECK_MIN_SECONDS', '600')) if external else settings['checkMinSeconds'],
            'metadataMaxSeconds': int(os.environ.get('TDM_EXTERNAL_CHECK_MAX_SECONDS', '1200')) if external else settings['checkMaxSeconds'],
            'nextMetadataAt': None if external else self.store.get('automationNextAt'),
            'nextChannelAt': self.next_selection if self.next_selection > time.time() and self.store.get('running', False) else None},
            'account': account, 'authorization': {'pending': bool(self.pending), 'device': pending, 'error': self.auth_error},
            'settings': self.store.get('settings'), 'running': self.store.get('running', False),
            'state': self.state, 'reason': self.reason, 'current': self.current,
            'goals': self.goal_states(), 'queueCompletedAt': self.store.get('queueCompletedAt'),
            'tasks': self.store.get('tasks'), 'campaigns': campaigns,
            'progress': self.store.get('progress', {'status': 'unknown', 'sampledAt': None, 'message': '连接 Twitch 后同步官方进度'}),
            'syncing': self.scan_lock.locked() or bool(self.scan_task and not self.scan_task.done()),
            'lastSync': self.store.get('lastSync'), 'lastWatchAt': self.last_watch, 'currentProgress': session_progress,
            'watchReports': self.watch_reports, 'network': {'route': self.net.route, 'proxy': self.net.proxy.label,
                'lastSuccessAt': self.net.last_success, 'lastError': self.net.last_error,
                'cooldownUntil': dt.datetime.fromtimestamp(self.net.blocked_until, dt.timezone.utc).isoformat() if self.net.blocked_until > time.time() else None},
            'traffic': usage,
            'events': self.store.events(), 'sampledAt': now()}

    def goal_states(self):
        campaigns = self.store.get('campaigns', [])
        readable = self.store.get('progress', {}).get('status') == 'available'
        return [evaluate(t, campaigns, readable) for t in self.store.get('tasks')]

    def capture_goals(self):
        tasks = self.store.get('tasks')
        campaigns = self.store.get('campaigns', [])
        catalogue = self.store.get('catalogueCache', {}).get('items', [])
        known = {c['id'] for c in campaigns if c.get('drops')}
        for task in tasks:
            active_ids = {str(c['id']) for c in catalogue if c.get('status') == 'ACTIVE'
                          and str((c.get('game') or {}).get('id')) == str(task['game'].get('id'))}
            if task['enabled'] and active_ids <= known:
                task['goal'] = capture(task, campaigns, now())
        self.store.put('tasks', tasks)

    def finish_if_complete(self):
        enabled = {t['id'] for t in self.store.get('tasks') if t['enabled']}
        states = [g for g in self.goal_states() if g['taskId'] in enabled]
        if self.store.get('running', False) and states and all(g['status'] == 'completed' for g in states):
            self.store.put('running', False)
            self.store.put('queueCompletedAt', now())
            self.current = None
            self.state, self.reason = 'completed', '所有启用游戏的本轮目标已由 Twitch 确认达标，挂机已结束；未领取奖励请手动领取'
            self.event('本轮所有目标已达标，已自动结束挂机；奖励仍需手动领取')
            return True
        return False

    async def begin_auth(self, actor, client_id):
        if self.pending:
            if self.pending['actor'] != actor:
                raise ServiceError('另一位管理员正在授权，请等待或取消当前授权', 'authorization_busy', 409)
            return self.snapshot(actor)
        self.auth_requests = [t for t in self.auth_requests if t > time.time()-600]
        if len(self.auth_requests) >= 4:
            raise ServiceError('授权请求过于频繁，请稍后重试', 'local_rate_limit', 429)
        if not re.fullmatch(r'[A-Za-z0-9]{20,64}', client_id):
            raise ServiceError('Twitch Client ID 格式无效')
        self.auth_requests.append(time.time())
        status, result = await self.net.request('POST', 'https://id.twitch.tv/oauth2/device',
            data={'client_id': client_id, 'scopes': ''}, cap=16384)
        if status != 200:
            raise ServiceError('设备授权申请失败，可在高级设置填写自己的 Twitch 公共应用 Client ID', 'device_auth_unavailable', 409)
        uri = result.get('verification_uri', '')
        code = result.get('user_code', '')
        if not uri.startswith('https://www.twitch.tv/activate') or not re.fullmatch(r'[A-Za-z0-9]{4,16}', code):
            raise ServiceError('Twitch 返回了无法验证的授权地址', 'device_auth_unavailable', 502)
        expires = time.time()+min(1800, int(result.get('expires_in', 600)))
        self.pending = {'actor': actor, 'userCode': code, 'verificationUri': uri,
                        'expiresAt': dt.datetime.fromtimestamp(expires, dt.timezone.utc).isoformat(),
                        'deviceCode': result['device_code'], 'clientId': client_id,
                        'expires': expires, 'interval': max(5, int(result.get('interval', 5)))}
        self.auth_error = None
        self.auth_task = asyncio.create_task(self.poll_auth())
        self.event('已申请 Twitch 设备授权，等待在官方页面确认')
        return self.snapshot(actor)

    async def poll_auth(self):
        pending = self.pending
        failures = 0
        try:
            while self.pending is pending and time.time() < pending['expires']:
                await asyncio.sleep(min(pending['interval'], 60))
                try:
                    status, result = await self.net.request('POST', 'https://id.twitch.tv/oauth2/token', data={
                        'client_id': pending['clientId'], 'device_code': pending['deviceCode'],
                        'grant_type': 'urn:ietf:params:oauth:grant-type:device_code'}, cap=16384)
                except ServiceError as error:
                    failures += 1
                    if failures >= 3 or error.code in ['rate_limited', 'verification_required']:
                        raise
                    pending['interval'] = min(60, pending['interval']+5)
                    continue
                message = result.get('message', result.get('error', ''))
                if message == 'authorization_pending':
                    continue
                if message == 'slow_down':
                    pending['interval'] += 5
                    continue
                if status != 200 or not result.get('access_token'):
                    raise ServiceError('授权被取消、失效或拒绝，请重新连接', 'authorization_failed')
                verified = await self.twitch.validate(result['access_token'])
                account = {'access_token': result['access_token'], 'refresh_token': result.get('refresh_token'),
                    'client_id': verified['client_id'], 'user_id': verified['user_id'], 'login': verified['login'],
                    'validatedAt': now(), 'expiresAt': time.time()+int(verified.get('expires_in', 0))}
                self.book.write(account)
                self.account = account
                self.next_selection = 0
                self.store.put('queueCompletedAt', None)
                self.current = None
                self.store.put('campaigns', [])
                self.store.put('inventoryFetchedAt', 0)
                self.store.put('inventoryFailedAt', 0)
                self.store.put('catalogueFailedAt', 0)
                self.store.put('catalogueCache', {})
                self.store.put('currentProgress', None)
                self.store.put('progress', {'status': 'unknown', 'sampledAt': None, 'message': '已连接，等待首次读取官方进度'})
                self.event('Twitch 授权成功，登录凭据仅保存在 NAS 的私有凭据存储')
                self.pending = None
                self.queue_scan()
                return
            self.auth_error = '授权码已过期，请重新连接'
        except ServiceError as error:
            self.auth_error = str(error)
            self.event('Twitch 授权未完成，请查看账号连接状态', 'warning')
        except asyncio.CancelledError:
            raise
        except Exception:
            self.auth_error = '授权信息保存失败，原有凭据已保留，请重试'
            self.event('授权信息保存失败', 'error')
        finally:
            if self.pending is pending:
                self.pending = None

    async def cancel_auth(self):
        if self.auth_task and not self.auth_task.done():
            self.auth_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await self.auth_task
        self.pending = None

    async def disconnect(self):
        self.revision += 1
        self.store.put('running', False)
        self.current = None
        await self.cancel_auth()
        account = self.account
        self.book.write(None)
        self.account = None
        self.auth_error = None
        self.store.put('campaigns', [])
        self.store.put('progress', {'status': 'unknown', 'sampledAt': None, 'message': '账号已解除连接'})
        self.state, self.reason = 'paused', '账号已解除连接'
        if account:
            try:
                status, _ = await self.net.request('POST', 'https://id.twitch.tv/oauth2/revoke',
                    data={'client_id': account['client_id'], 'token': account['access_token']}, cap=8192)
                if status != 200:
                    raise ServiceError('撤销失败')
            except ServiceError:
                self.auth_error = '已清除 NAS 登录；远端撤销未确认，请在 Twitch 账号设置中检查连接'
        self.event('已停止挂机并解除 NAS 的 Twitch 连接')

    async def validate_account(self):
        if not self.account:
            raise ServiceError('请先连接 Twitch 账号', 'login_required', 409)
        account = self.account
        original = account
        validated = date_seconds(account.get('validatedAt')) or 0
        if time.time()-validated < 3600 and account.get('expiresAt', 0) > time.time()+300:
            return
        try:
            verified = await self.twitch.validate(account['access_token'])
        except ServiceError as error:
            if error.code != 'login_required' or not account.get('refresh_token'):
                raise
            status, result = await self.net.request('POST', 'https://id.twitch.tv/oauth2/token', data={
                'client_id': account['client_id'], 'grant_type': 'refresh_token', 'refresh_token': account['refresh_token']}, cap=16384)
            if status != 200 or not result.get('access_token'):
                raise ServiceError('Twitch 登录已失效，请重新连接', 'login_required', 401)
            account = {**account, 'access_token': result['access_token'],
                       'refresh_token': result.get('refresh_token', account['refresh_token'])}
            verified = await self.twitch.validate(account['access_token'])
        if verified['user_id'] != account['user_id']:
            raise ServiceError('Twitch 授权身份不匹配，请重新连接', 'login_required', 401)
        updated = {**account, 'validatedAt': now(), 'expiresAt': time.time()+int(verified.get('expires_in', 0))}
        if self.account is not original:
            raise ServiceError('账号状态已经改变', 'state_changed', 409)
        self.book.write(updated)
        self.account = updated

    def queue_scan(self):
        if self.scan_task and not self.scan_task.done():
            return
        self.scan_task = asyncio.create_task(self.safe_scan(force=True))

    async def safe_scan(self, force=False):
        try:
            await self.scan(force=force)
        except ServiceError as error:
            self.record_error(error)
        except Exception:
            self.event('同步未完成，保留上次已确认资料', 'error')

    def record_error(self, error):
        if error.code == 'state_changed':
            return
        self.reason = str(error)
        self.state = 'error'
        if error.code in ['login_required', 'verification_required', 'watch_rejected', 'integrity_required']:
            self.store.put('running', False)
            self.current = None
        self.event(str(error), 'warning')

    async def scan(self, force=False):
        async with self.scan_lock:
            if not self.account:
                return {'checked': True, 'connected': False}
            await self.validate_account()
            account = self.account
            tasks = self.store.get('tasks')
            for task in tasks:
                if task['enabled'] and not task['game'].get('id'):
                    task['game'] = await self.twitch.game(task['game']['name'])
            resolved = {t['id']: t for t in tasks}
            fresh_tasks = self.store.get('tasks')
            for task in fresh_tasks:
                checked = resolved.get(task['id'])
                if checked and checked['game']['name'] == task['game']['name']:
                    task['game'] = checked['game']
            tasks = fresh_tasks
            self.store.put('tasks', tasks)
            before_goals = self.goal_states()
            before_campaigns = self.selection_metadata()
            old = self.store.get('progress', {})
            previous = {c['id']: c for c in self.store.get('campaigns', [])}
            campaigns = dict(previous)
            inventory_at = self.store.get('inventoryFetchedAt', 0)
            sampled_at = old.get('sampledAt')
            inventory_ok = old.get('status') == 'available'
            inventory_message = old.get('message', '等待确认账号进度')
            waiting_claim = any(g['blocked'] for g in self.goal_states())
            retry_inventory = force or time.time() - self.store.get('inventoryFailedAt', 0) >= 3600
            if retry_inventory and (force or time.time()-inventory_at >= (300 if waiting_claim else 1800)):
                try:
                    inventory = await self.twitch.inventory(account)
                    sampled_at = now()
                    for raw in inventory['dropCampaignsInProgress']:
                        if raw.get('id'):
                            normalized = merge_progress(clean_campaign(raw, sampled_at), campaigns.get(str(raw['id']), {}))
                            normalized['metadataFetchedAt'] = previous.get(str(raw['id']), {}).get('metadataFetchedAt')
                            campaigns[str(raw['id'])] = normalized
                    self.store.put('inventoryFetchedAt', time.time())
                    self.store.put('inventoryFailedAt', 0)
                    inventory_ok, inventory_message = True, '已读取 Twitch 官方账号进度'
                except ServiceError as error:
                    if error.code != 'capability_unavailable':
                        raise
                    inventory_ok, inventory_message = False, str(error)
                    self.store.put('inventoryFailedAt', time.time())
            catalogue_ok = old.get('catalogueAvailable', False)
            catalogue_message = old.get('catalogueMessage')
            try:
                cache = self.store.get('catalogueCache', {})
                if not force and time.time()-self.store.get('catalogueFailedAt', 0) < 3600:
                    catalogue = cache.get('items', [])
                    catalogue_ok = False
                elif not force and cache.get('accountId') == account['user_id'] and time.time()-cache.get('at', 0) < 3600:
                    catalogue = cache['items']
                    catalogue_ok = True
                else:
                    catalogue = await self.twitch.campaigns(account)
                    self.store.put('catalogueCache', {'accountId': account['user_id'], 'at': time.time(), 'items': catalogue})
                    self.store.put('catalogueFailedAt', 0)
                    catalogue_ok, catalogue_message = True, None
                enabled_games = {str(t['game'].get('id')) for t in tasks if t['enabled']}
                target_campaigns = {r['campaignId'] for t in tasks if t['enabled'] for r in t['goal']['rewards']}
                candidates = [c for c in catalogue if str((c.get('game') or {}).get('id')) in enabled_games
                              and (c.get('status') == 'ACTIVE' or str(c.get('id')) in target_campaigns)]
                candidates.sort(key=lambda c: date_seconds(campaigns.get(str(c['id']), {}).get('metadataFetchedAt')) or 0)
                for candidate in candidates[:24]:
                    existing = campaigns.get(str(candidate['id']), {})
                    if not force and time.time()-(date_seconds(existing.get('metadataFetchedAt')) or 0) < 21600:
                        continue
                    detailed = await self.twitch.details(account, str(candidate['id']))
                    if detailed:
                        normalized = merge_progress(clean_campaign(detailed, now()), campaigns.get(str(candidate['id']), {}))
                        normalized['metadataFetchedAt'] = now()
                        campaigns[str(candidate['id'])] = normalized
            except ServiceError as error:
                if error.code != 'capability_unavailable':
                    raise
                catalogue_ok, catalogue_message = False, str(error)
                self.store.put('catalogueFailedAt', time.time())
            if self.account is not account:
                return {'checked': False, 'accountChanged': True}
            self.store.put('campaigns', list(campaigns.values()))
            self.store.put('progress', {'status': 'available' if inventory_ok else 'unavailable',
                'sampledAt': sampled_at, 'catalogueAvailable': catalogue_ok,
                'catalogueMessage': catalogue_message, 'message': inventory_message,
                'failedAt': None if inventory_ok else (dt.datetime.fromtimestamp(self.store.get('inventoryFailedAt'), dt.timezone.utc).isoformat() if self.store.get('inventoryFailedAt') else old.get('failedAt'))})
            if self.current and self.account is account:
                try:
                    official = await self.twitch.current(account, self.current['channel'])
                    if isinstance(official, dict) and self.account is account and self.current:
                        drop_id, minutes, sampled = str(official.get('dropID', '')), number(official.get('currentMinutesWatched')), now()
                        self.store.put('currentProgress', {'dropId': drop_id,
                            'minutes': minutes, 'sampledAt': sampled, 'streamId': self.current['channel']['stream']['id'],
                            'accountId': account['user_id'], 'channelId': self.current['channel']['id']})
                        if minutes is not None:
                            fresh = self.store.get('campaigns', [])
                            for campaign in fresh:
                                for drop in campaign['drops']:
                                    if drop['id'] == drop_id:
                                        drop['minutes'], drop['sampledAt'] = max(drop.get('minutes') or 0, minutes), sampled
                            self.store.put('campaigns', fresh)
                except ServiceError as error:
                    if error.code != 'capability_unavailable':
                        raise
            self.capture_goals()
            self.finish_if_complete()
            self.store.put('lastSync', now())
            if before_goals != self.goal_states() or before_campaigns != self.selection_metadata():
                self.next_selection = 0
            self.event('完成一次资料检查；不可读取的字段保留未知')
            return {'checked': True, 'connected': True, 'progress': self.store.get('progress')['status']}

    def selection_metadata(self):
        return [{k: c.get(k) for k in ['id', 'gameId', 'startsAt', 'endsAt', 'linked', 'allowChannels']}
                for c in self.store.get('campaigns', [])]

    def active_campaigns(self, task):
        current = time.time()
        return [c for c in self.store.get('campaigns', []) if c['gameId'] == str(task['game'].get('id'))
            and (date_seconds(c['startsAt']) or 0) <= current < (date_seconds(c['endsAt']) or 0)]

    async def select_channel(self):
        async with self.selection_lock:
            revision, account = self.revision, self.account
            self.next_selection = time.time()+self.check_delay()
            tasks = sorted((t for t in self.store.get('tasks') if t['enabled']), key=lambda t: -t['priority'])
            reason = '暂无启用的游戏任务'
            for task in tasks:
                goal = evaluate(task, self.store.get('campaigns', []), self.store.get('progress', {}).get('status') == 'available')
                if goal['status'] == 'completed':
                    reason = '本轮目标已达标，正在检查其他游戏'
                    continue
                if goal['status'] == 'waiting_claim':
                    reason = '请先到 Twitch 库存领取前置奖励，领取后同步即可继续'
                    continue
                if goal['status'] == 'expired':
                    reason = '未完成目标的活动已结束，请调整目标；不会标记为完成'
                    continue
                campaigns = self.active_campaigns(task)
                if task['goal']['rewards']:
                    campaigns = [c for c in campaigns if c['id'] in goal['runnableCampaignIds']]
                    if not campaigns:
                        reason = '目标资料或前置状态无法确认，等待官方同步'
                        continue
                if campaigns and all(c['drops'] and all(d['claimed'] is True for d in c['drops']) for c in campaigns):
                    reason = '本轮奖励已领取，等待新活动'
                    continue
                if campaigns and any(c.get('linked') is False for c in campaigns):
                    reason = '请先在 Twitch 官方活动页关联此游戏账号'
                    continue
                if not campaigns and not task['watchOnly']:
                    reason = '尚未确认可参与活动；同步资料，或明确启用仅观看模式'
                    continue
                channels = list(task['channels'])
                allowed = {x['login'].lower() for c in campaigns for x in c['allowChannels'] if x['login']}
                channels = list(dict.fromkeys(list(allowed) + channels))
                if task['autoChannels']:
                    try:
                        channels += await self.twitch.directory(task['game'])
                    except ServiceError as error:
                        if error.code != 'capability_unavailable':
                            raise
                for channel in await self.twitch.channels(list(dict.fromkeys(channels))[:12]):
                    if not channel or not channel.get('stream'):
                        continue
                    login = channel['login']
                    game = channel['stream'].get('game') or {}
                    if str(game.get('id')) != str(task['game'].get('id')):
                        continue
                    eligible = login.lower() in allowed
                    if not eligible and campaigns:
                        try:
                            available = await self.twitch.available(self.account, channel)
                            eligible = isinstance(available, list) and any(str(c.get('id')) in {x['id'] for x in campaigns} for c in available)
                        except ServiceError as error:
                            if error.code != 'capability_unavailable':
                                raise
                    if not eligible and not task['watchOnly']:
                        reason = '直播已找到，但无法确认掉宝资格；可在官方页面核对后启用仅观看模式'
                        continue
                    if not self.store.get('running', False) or self.account is not account or revision != self.revision:
                        return
                    changed = not self.current or self.current['channel']['login'] != channel['login'] or self.current['taskId'] != task['id'] or self.current['channel']['stream']['id'] != channel['stream']['id']
                    self.current = {'taskId': task['id'], 'game': task['game'], 'channel': channel,
                                    'eligibility': 'confirmed' if eligible else 'unverified', 'selectedAt': now()}
                    if changed:
                        self.next_watch = time.time()+60
                        self.event('已选择直播频道，开始等待观看上报间隔')
                    self.state, self.reason = 'watching', '观看上报运行中；奖励进度以 Twitch 返回为准'
                    return
                reason = reason if reason.startswith('直播') else '等待符合游戏和活动要求的频道开播'
            self.current = None
            self.state, self.reason = 'waiting', reason

    async def engine(self):
        while True:
            await asyncio.sleep(2)
            try:
                if not self.store.get('running', False):
                    self.current = None
                    if self.store.get('queueCompletedAt'):
                        self.state, self.reason = 'completed', '本轮所有目标已达标，挂机已结束；可手动领取奖励或设置下一轮目标'
                        continue
                    if self.state not in ['error']:
                        self.state, self.reason = 'paused', '挂机已暂停'
                    continue
                if not self.account:
                    self.state, self.reason = 'login_required', '请先连接 Twitch 账号'
                    continue
                if self.finish_if_complete():
                    continue
                if self.net.blocked_until > time.time():
                    self.state, self.reason = 'cooldown', 'Twitch 限流或验证冷却中'
                    continue
                if time.time() >= self.next_selection:
                    await self.select_channel()
                if self.current and time.time() >= self.next_watch:
                    account, revision = self.account, self.revision
                    current = self.current
                    channel = await self.twitch.channel(current['channel']['login'])
                    self.next_watch = time.time()+60
                    if not channel or not channel.get('stream') or str((channel['stream'].get('game') or {}).get('id')) != str(current['game'].get('id')):
                        self.current = None
                        self.next_selection = 0
                        continue
                    if not self.store.get('running', False) or self.account is not account or revision != self.revision or self.current is not current:
                        continue
                    await self.twitch.watch(account, channel)
                    self.last_watch = now()
                    self.watch_reports += 1
                    self.store.put('watchReports', self.watch_reports)
                    self.channel_errors = 0
            except ServiceError as error:
                self.channel_errors += 1
                self.next_selection = time.time()+min(900, 30*2**min(5, self.channel_errors))
                self.next_watch = self.next_selection
                self.record_error(error)
            except Exception:
                self.next_selection = time.time()+300
                self.next_watch = self.next_selection
                self.state, self.reason = 'error', '任务暂不可用，已延长重试间隔'
                self.event('挂机检查失败，已延长重试间隔', 'error')


def validate_goal(value, old, game, campaigns):
    previous = (old or {}).get('goal') or default_goal()
    same_game = bool(old and str(old['game'].get('id')) == str(game.get('id')))
    if value is None:
        return previous if same_game else default_goal()
    if not isinstance(value, dict) or value.get('mode') not in ['all', 'selected']:
        raise ServiceError('奖励目标设置无效')
    if type(value.get('reset', False)) is not bool:
        raise ServiceError('重新选择目标的设置无效')
    if value['mode'] == 'all':
        return previous if same_game and previous['mode'] == 'all' and not value.get('reset') else default_goal()
    refs = value.get('rewards')
    if not isinstance(refs, list) or not 1 <= len(refs) <= 500:
        raise ServiceError('请选择 1–500 个奖励目标')
    known = {(c['id'], d['id']) for c in campaigns if c['gameId'] == str(game.get('id')) for d in c['drops']}
    if same_game:
        known |= {(r['campaignId'], r['dropId']) for r in previous['rewards']}
    selected = []
    seen = set()
    for ref in refs:
        if not isinstance(ref, dict) or not all(isinstance(ref.get(k), str) and 1 <= len(ref[k]) <= 200 for k in ['campaignId', 'dropId']):
            raise ServiceError('奖励标识无效')
        key = (ref['campaignId'], ref['dropId'])
        if key not in known:
            raise ServiceError('所选奖励不属于此游戏或尚未读取，请先同步官方资料')
        if key not in seen:
            selected.append({'campaignId': key[0], 'dropId': key[1]})
            seen.add(key)
    return {'mode': 'selected', 'rewards': selected, 'lockedAt': now()}


def validate_task(body):
    if not isinstance(body, dict):
        raise ServiceError('任务设置格式无效')
    name = body.get('gameName')
    if not isinstance(name, str) or not 1 <= len(name.strip()) <= 100:
        raise ServiceError('请填写 Twitch 游戏名称')
    channels = body.get('channels', [])
    if not isinstance(channels, list) or len(channels) > 20 or any(not isinstance(x, str) or not re.fullmatch(r'[A-Za-z0-9_]{1,25}', x) for x in channels):
        raise ServiceError('频道最多 20 个，只填写频道名，不填写网址')
    for key in ['enabled', 'watchOnly', 'autoChannels']:
        if not isinstance(body.get(key), bool):
            raise ServiceError('任务开关格式无效')
    priority = body.get('priority')
    if type(priority) is not int or not 0 <= priority <= 100:
        raise ServiceError('优先级需为 0–100 的整数')
    return name.strip(), list(dict.fromkeys(x.lower() for x in channels))


async def main():
    service = DropsService()

    @web.middleware
    async def errors(request, handler):
        try:
            result = await handler(request)
            if not request.path.startswith('/assets/'):
                result.headers['Cache-Control'] = 'no-store'
            return result
        except ServiceError as error:
            return web.json_response({'error': str(error), 'code': error.code}, status=error.status, headers={'Cache-Control': 'no-store'})
        except web.HTTPException:
            raise
        except Exception:
            return web.json_response({'error': '操作失败，原有数据已保留'}, status=500, headers={'Cache-Control': 'no-store'})

    app = web.Application(middlewares=[errors], client_max_size=32768)
    actor = lambda request: request.get('actor', request.headers.get('X-Drops-Actor', '')[:100])

    async def snapshot(request):
        return web.json_response(service.snapshot(actor(request)))

    async def auth_start(request):
        if not actor(request):
            raise ServiceError('缺少已验证的管理身份', 'unauthorized', 403)
        body = await request.json()
        return web.json_response(await service.begin_auth(actor(request), body.get('clientId') or DEVICE_CLIENT))

    async def auth_cancel(request):
        if service.pending and service.pending['actor'] != actor(request):
            raise ServiceError('只能取消自己发起的授权', 'forbidden', 403)
        await service.cancel_auth()
        return web.json_response(service.snapshot(actor(request)))

    async def disconnect(request):
        await service.disconnect()
        return web.json_response(service.snapshot(actor(request)))

    async def control(request):
        body = await request.json()
        if not isinstance(body.get('running'), bool):
            raise ServiceError('挂机开关格式无效')
        if body['running']:
            if not service.account:
                raise ServiceError('请先连接 Twitch 账号')
            enabled = {t['id'] for t in service.store.get('tasks') if t['enabled']}
            goals = [g for g in service.goal_states() if g['taskId'] in enabled]
            if goals and all(g['status'] == 'completed' for g in goals):
                raise ServiceError('本轮目标已全部完成，请先在任务设置中选择下一轮目标')
        service.store.put('queueCompletedAt', None)
        service.revision += 1
        service.store.put('running', body['running'])
        service.current = None
        service.next_selection = 0
        service.state, service.reason = ('waiting', '正在选择任务') if body['running'] else ('paused', '挂机已暂停')
        service.event('已开启挂机' if body['running'] else '已暂停挂机')
        return web.json_response(service.snapshot(actor(request)))

    async def settings(request):
        body = await request.json()
        if body.get('networkMode') not in ['auto', 'direct', 'proxy']:
            raise ServiceError('网络模式无效')
        if body['networkMode'] == 'proxy' and not service.net.proxy.url:
            raise ServiceError('请先通过 TDM_PROXY_URL 或 mihomo 环境变量配置代理')
        updated = {**service.store.get('settings'), **{k: body[k] for k in DEFAULT_SETTINGS if k in body}}
        minimum, maximum = updated['checkMinSeconds'], updated['checkMaxSeconds']
        if type(minimum) is not int or type(maximum) is not int or not 120 <= minimum <= maximum <= 10800:
            raise ServiceError('随机检查间隔需为 2–180 分钟，最短不能大于最长')
        service.store.put('settings', updated)
        service.next_selection = time.time()+service.check_delay()
        service.automation_changed.set()
        service.net.route = 'unknown'
        service.net.last_direct_probe = 0
        service.event('已更新连接设置；按低流量方式持续运行，不设流量额度')
        return web.json_response(service.snapshot(actor(request)))

    async def save_task(request):
        body = await request.json()
        name, channels = validate_task(body)
        tasks = service.store.get('tasks')
        task_id = request.match_info.get('task_id')
        old = next((t for t in tasks if t['id'] == task_id), None)
        if task_id and not old:
            raise ServiceError('游戏任务不存在', 'not_found', 404)
        if not old and len(tasks) >= 12:
            raise ServiceError('最多配置 12 个游戏任务')
        game = old['game'] if old and old['game']['name'] == name else await service.twitch.game(name)
        tasks = service.store.get('tasks')
        if task_id and not any(t['id'] == task_id for t in tasks):
            raise ServiceError('任务已被另一位管理员删除，请刷新', 'not_found', 409)
        if any(t['id'] != task_id and t['game']['name'].lower() == game['name'].lower() for t in tasks):
            raise ServiceError('该游戏已在任务列表中')
        goal = validate_goal(body.get('goal'), old, game, service.store.get('campaigns', []))
        updated = {'id': task_id or uuid.uuid4().hex, 'game': game, 'channels': channels, 'goal': goal,
                   **{key: body[key] for key in ['enabled', 'priority', 'watchOnly', 'autoChannels']}}
        service.store.put('tasks', [updated if t['id'] == task_id else t for t in tasks] if old else tasks+[updated])
        service.revision += 1
        service.current = None
        service.next_selection = 0
        service.capture_goals()
        service.store.put('queueCompletedAt', None)
        service.event('已保存游戏任务及本轮奖励目标')
        return web.json_response(service.snapshot(actor(request)))

    async def delete_task(request):
        task_id = request.match_info['task_id']
        tasks = service.store.get('tasks')
        service.revision += 1
        service.store.put('tasks', [t for t in tasks if t['id'] != task_id])
        service.current = None
        service.next_selection = 0
        service.event('已删除游戏任务')
        return web.json_response(service.snapshot(actor(request)))

    async def sync(request):
        if request.path == '/internal/scan':
            try:
                return web.json_response(await service.scan())
            except ServiceError as error:
                service.record_error(error)
                raise
        service.queue_scan()
        return web.json_response(service.snapshot(actor(request)), status=202)

    async def automation(request):
        external = os.environ.get('TDM_EXTERNAL_SCHEDULER', 'false').lower() == 'true'
        if request.method == 'PATCH':
            if external:
                raise ServiceError('请通过部署方的原生定时任务管理器控制此计划', 'external_scheduler', 409)
            body = await request.json()
            if type(body.get('enabled')) is not bool:
                raise ServiceError('enabled 必须是布尔值')
            service.store.put('automationEnabled', body['enabled'])
            service.store.put('automationNextAt', None)
            service.automation_changed.set()
        return web.json_response({'source': 'external' if external else 'service', 'intervalMinSeconds': int(os.environ.get('TDM_EXTERNAL_CHECK_MIN_SECONDS', '600')) if external else service.store.get('settings')['checkMinSeconds'],
            'intervalMaxSeconds': int(os.environ.get('TDM_EXTERNAL_CHECK_MAX_SECONDS', '1200')) if external else service.store.get('settings')['checkMaxSeconds'],
            'nextRunAt': None if external else service.store.get('automationNextAt'),
            'timeZone': 'UTC', 'enabled': None if external else service.store.get('automationEnabled', True),
            'lastStartedAt': service.store.get('automationLastStarted'), 'lastFinishedAt': service.store.get('automationLastFinished'),
            'lastStatus': service.store.get('automationLastStatus', 'unknown')})

    async def reward_images(request):
        return web.json_response(service.image_import(await request.json(), save=request.path.endswith('/import')))

    async def health(_request):
        return web.json_response({'ok': True, 'version': '1.1.0'})

    app.router.add_get('/health', health)
    app.router.add_get('/automation', automation)
    app.router.add_patch('/automation', automation)
    app.router.add_get('/snapshot', snapshot)
    app.router.add_post('/auth/start', auth_start)
    app.router.add_post('/auth/cancel', auth_cancel)
    app.router.add_post('/auth/disconnect', disconnect)
    app.router.add_post('/control', control)
    app.router.add_patch('/settings', settings)
    app.router.add_post('/tasks', save_task)
    app.router.add_put('/tasks/{task_id}', save_task)
    app.router.add_delete('/tasks/{task_id}', delete_task)
    app.router.add_post('/sync', sync)
    app.router.add_post('/reward-images/preview', reward_images)
    app.router.add_post('/reward-images/import', reward_images)
    app.router.add_post('/internal/scan', sync)
    http_port = int(os.environ.get('TDM_HTTP_PORT', '8080'))
    if http_port:
        WebAccess(os.environ.get('TDM_DATA_DIR', '/data')).install(app)
    runner = web.AppRunner(app, access_log=None)
    await runner.setup()
    socket = Path(os.environ.get('TDM_SOCKET', '/run/twitch-drops/service.sock'))
    socket.parent.mkdir(parents=True, exist_ok=True)
    if socket.exists():
        socket.unlink()
    await service.start()
    await web.UnixSite(runner, str(socket)).start()
    os.chmod(socket, 0o600)
    if http_port:
        await web.TCPSite(runner, os.environ.get('TDM_HTTP_HOST', '0.0.0.0'), http_port).start()
    if os.environ.get('TDM_EXTERNAL_SCHEDULER', 'false').lower() != 'true':
        service.background.append(asyncio.create_task(service.auto_sync()))
    done = asyncio.Event()
    for sig in [signal.SIGTERM, signal.SIGINT]:
        asyncio.get_running_loop().add_signal_handler(sig, done.set)
    try:
        await done.wait()
    finally:
        await runner.cleanup()
        await service.stop()
        socket.unlink(missing_ok=True)


if __name__ == '__main__':
    os.umask(0o077)
    try:
        asyncio.run(main())
    except Exception:
        # Parser failures can contain credential-file source lines. Never print
        # startup tracebacks, YAML snippets, exception values or upstream bodies.
        print('Twitch Drops startup failed; check private configuration and file permissions.', file=sys.stderr)
        sys.exit(1)
