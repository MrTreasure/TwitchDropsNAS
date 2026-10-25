"""Metadata and watch-only adapter. No claim mutation or video fallback exists."""
import base64
import datetime as dt
import json

from transport import ServiceError

WEB_CLIENT = 'kimne78kx3ncx6brgo4mv6wki5h1ko'
DEVICE_CLIENT = 'ue6666qo983tsx6so1t0vnawi233wa'
# Operation names/hashes follow the MIT-licensed upstreams in NOTICE.md.
OPERATIONS = {
    'Inventory': ('Inventory', '8337eb8541b314040b0edde0c09c5c7a2783ba1960aa9edfbf3bac16d0fec404'),
    'Campaigns': ('ViewerDropsDashboard', '5a4da2ab3d5b47c9f9ce864e727b2cb346af1e3ea8b897fe8f704a97ff017619'),
    'Details': ('DropCampaignDetails', '039277bf98f3130929262cc7c6efd9c141ca3749cb6dca442fc8ead9a53f77c1'),
    'Available': ('DropsHighlightService_AvailableDrops', '782dad0f032942260171d2d80a654f88bdd0c5a9dddc392e9bc92218a0f42d20'),
    'Current': ('DropCurrentSessionContext', '4d06b702d25d652afb9ef835d2a550031f1cf762b193523a92166f40ea3d142b'),
}


class Twitch:
    def __init__(self, transport):
        self.net = transport

    async def validate(self, token):
        status, result = await self.net.request('GET', 'https://id.twitch.tv/oauth2/validate',
            headers={'Authorization': f'OAuth {token}'}, cap=8192)
        if status != 200 or not result.get('user_id') or not result.get('client_id'):
            raise ServiceError('Twitch 登录已失效，请重新授权', 'login_required', 401)
        return result

    async def gql(self, account=None, *, query=None, variables=None, operation=None):
        client = account['client_id'] if account else WEB_CLIENT
        headers = {'Client-ID': client}
        if account:
            headers['Authorization'] = 'OAuth ' + account['access_token']
        if operation:
            name, digest = OPERATIONS[operation]
            payload = {'operationName': name, 'variables': variables or {},
                       'extensions': {'persistedQuery': {'version': 1, 'sha256Hash': digest}}}
        else:
            payload = {'query': query, 'variables': variables or {}}
        status, result = await self.net.request('POST', 'https://gql.twitch.tv/gql', headers=headers, payload=payload)
        if status in (401, 403):
            raise ServiceError('Twitch 拒绝此接口访问，请在官方库存核对进度', 'capability_unavailable', 409)
        if not isinstance(result, dict):
            raise ServiceError('Twitch 响应格式已变化，保留上次结果', 'capability_unavailable', 409)
        if status != 200 or result.get('errors'):
            errors = str(result.get('errors', '')).lower()
            if any(x in errors for x in ['integrity', 'unauthorized', 'authenticated']):
                if 'integrity' in errors:
                    raise ServiceError('Twitch 要求完整性验证，此接口已停止自动请求；请在 Twitch 官方页面查看进度', 'integrity_required', 409)
                raise ServiceError('Twitch 未开放此授权的掉宝读取能力', 'capability_unavailable', 409)
            raise ServiceError('Twitch 接口暂不可用或已变更，保留上次确认结果', 'capability_unavailable', 409)
        return result.get('data') or {}

    async def game(self, name):
        result = await self.gql(query='query($name:String!){game(name:$name){id name slug}}', variables={'name': name})
        game = result.get('game')
        if not game:
            raise ServiceError('没有找到游戏，请填写 Twitch 上的完整游戏名称', 'game_not_found')
        return game

    async def channel(self, login):
        result = await self.gql(query='query($login:String!){user(login:$login){id login displayName stream{id game{id name slug}}}}', variables={'login': login})
        return result.get('user')

    async def channels(self, logins):
        if not logins:
            return []
        logins = logins[:12]
        variables = {f'login{i}': login for i, login in enumerate(logins)}
        arguments = ','.join(f'$login{i}:String!' for i in range(len(logins)))
        fields = ' '.join(f'channel{i}:user(login:$login{i}){{id login displayName stream{{id game{{id name slug}}}}}}' for i in range(len(logins)))
        result = await self.gql(query=f'query({arguments}){{{fields}}}', variables=variables)
        return [result.get(f'channel{i}') for i in range(len(logins))]

    async def inventory(self, account):
        data = await self.gql(account, operation='Inventory', variables={'fetchRewardCampaigns': False})
        user = data.get('currentUser')
        if not user or not isinstance(user.get('inventory'), dict):
            raise ServiceError('Twitch 暂未返回此账号的掉宝库存', 'capability_unavailable', 409)
        if not isinstance(user['inventory'].get('dropCampaignsInProgress'), list):
            raise ServiceError('Twitch 未返回账号的掉宝进度；不能视为没有活动或零进度', 'capability_unavailable', 409)
        return user['inventory']

    async def campaigns(self, account):
        data = await self.gql(account, operation='Campaigns', variables={'fetchRewardCampaigns': False})
        user = data.get('currentUser') or {}
        if not isinstance(user.get('dropCampaigns'), list):
            raise ServiceError('Twitch 暂未返回掉宝活动目录', 'capability_unavailable', 409)
        return user['dropCampaigns']

    async def details(self, account, campaign_id):
        data = await self.gql(account, operation='Details', variables={'channelLogin': account['login'], 'dropID': campaign_id})
        return (data.get('user') or {}).get('dropCampaign')

    async def current(self, account, channel):
        data = await self.gql(account, operation='Current', variables={'channelID': str(channel['id']), 'channelLogin': ''})
        return (data.get('currentUser') or {}).get('dropCurrentSession')

    async def available(self, account, channel):
        data = await self.gql(account, operation='Available', variables={'channelID': str(channel['id'])})
        channel_data = data.get('channel') or {}
        campaigns = channel_data.get('viewerDropCampaigns')
        return campaigns if isinstance(campaigns, list) else None

    async def directory(self, game):
        query = '''query($slug:String!){game(slug:$slug){streams(first:10,options:{systemFilters:[DROPS_ENABLED],sort:VIEWER_COUNT}){edges{node{broadcaster{id login displayName}}}}}}'''
        data = await self.gql(query=query, variables={'slug': game['slug']})
        edges = ((data.get('game') or {}).get('streams') or {}).get('edges', [])
        return [edge['node']['broadcaster']['login'] for edge in edges]

    async def watch(self, account, channel):
        stream = channel.get('stream')
        if not stream:
            raise ServiceError('当前频道已离线', 'channel_offline', 409)
        game = stream.get('game') or {}
        events = [{'event': 'minute-watched', 'properties': {
            'broadcast_id': str(stream['id']), 'channel_id': str(channel['id']),
            'channel': channel['login'], 'client_time': dt.datetime.now(dt.timezone.utc).isoformat(),
            'game': game.get('name', ''), 'game_id': str(game.get('id', '')),
            'hidden': False, 'is_live': True, 'live': True, 'logged_in': True,
            'minutes_logged': 1, 'muted': False, 'user_id': account['user_id'],
        }}]
        encoded = base64.b64encode(json.dumps(events, separators=(',', ':')).encode()).decode()
        # Twitch's current settings.js advertises this collection path. A 204
        # from the host root does not establish that a watch event was collected.
        status, _ = await self.net.request('POST', 'https://spade.twitch.tv/track',
            headers={'Client-ID': account['client_id'], 'Authorization': 'OAuth ' + account['access_token']},
            data={'data': encoded}, cap=8192)
        if status != 204:
            raise ServiceError('观看上报未被接收，暂停并等待重新核对', 'watch_rejected', 409)
