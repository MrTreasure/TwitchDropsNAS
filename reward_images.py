"""Public reward image metadata: parse, scope, preview and persist original URLs."""
import hashlib
import json
import re
import unicodedata
from html.parser import HTMLParser
from urllib.parse import urlsplit

from transport import ServiceError

CDN_HOSTS = {'static-cdn.jtvnw.net', 'images.twitch.tv', 'static.twitchcdn.net',
             'staticfiles.warthunder.com', 'patchnotes.cdn.gaijin.net'}


def reward_image_url(value):
    if not isinstance(value, str) or len(value) > 2048 or any(ord(c) < 33 for c in value):
        return None
    try:
        url = urlsplit(value)
        if url.scheme == 'https' and url.hostname in CDN_HOSTS and not url.username and not url.password and url.port in (None, 443) and not url.fragment:
            return value
    except ValueError:
        pass
    return None


def normalized(value):
    text = unicodedata.normalize('NFKC', str(value or ''))
    return re.sub(r'\s+', ' ', text.translate(str.maketrans({'“': '"', '”': '"', '‘': "'", '’': "'"}))).strip().casefold()


def image_key(name):
    return hashlib.sha256(normalized(name).encode()).hexdigest()[:24]


class ImageHTML(HTMLParser):
    """Read image attributes and nearby labels; never render or execute HTML."""
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.entries, self.frames = [], []
        self.ignored_depth = 0

    def handle_starttag(self, tag, attrs):
        if tag in ('script', 'style'):
            self.ignored_depth += 1
        if self.ignored_depth:
            return
        if tag == 'img':
            attrs = dict(attrs)
            if len(self.entries) >= 200:
                raise ServiceError('一次最多导入 200 张图片')
            self.entries.append({'name': attrs.get('alt') or '', 'imageUrl': attrs.get('src') or attrs.get('data-src'), 'contexts': []})
            for frame in self.frames[-6:]:
                frame['images'].append(len(self.entries)-1)
        elif tag not in ('br', 'hr', 'input', 'meta', 'link', 'source', 'wbr'):
            if len(self.frames) >= 64:
                raise ServiceError('HTML 嵌套过深，请只复制奖励区域或使用 JSON')
            self.frames.append({'tag': tag, 'text': '', 'images': []})

    def handle_startendtag(self, tag, attrs):
        self.handle_starttag(tag, attrs)
        if tag != 'img':
            self.handle_endtag(tag)

    def handle_data(self, text):
        if not self.ignored_depth:
            for frame in self.frames:
                if len(frame['text']) < 800:
                    frame['text'] += ' ' + text[:800]

    def handle_endtag(self, tag):
        if tag in ('script', 'style') and self.ignored_depth:
            self.ignored_depth -= 1
            return
        if self.ignored_depth:
            return
        found = next((i for i in range(len(self.frames)-1, -1, -1) if self.frames[i]['tag'] == tag), None)
        if found is None:
            return
        for frame in reversed(self.frames[found:]):
            text = re.sub(r'\s+', ' ', frame['text']).strip()
            if text and len(text) <= 800:
                for index in frame['images']:
                    contexts = self.entries[index]['contexts']
                    if text not in contexts and len(contexts) < 6:
                        contexts.append(text)
        del self.frames[found:]


def parse_images(raw):
    if isinstance(raw, str):
        if len(raw.encode()) > 16000:
            raise ServiceError('图片资料最多 16 KiB，请只展开目标活动后重新提取')
        if raw.lstrip().startswith('<'):
            parser = ImageHTML()
            parser.feed(raw)
            parser.close()
            entries = parser.entries
        else:
            try:
                entries = json.loads(raw)
            except ValueError:
                raise ServiceError('请粘贴图片 JSON 或奖励区域 HTML') from None
    else:
        entries = raw
    if not isinstance(entries, list) or not 1 <= len(entries) <= 200:
        raise ServiceError('图片资料需为 1–200 个项目的 JSON 数组或含图片的 HTML')
    if len(json.dumps(entries, ensure_ascii=False).encode()) > 24000:
        raise ServiceError('图片资料过大，请只提取目标活动')
    return entries


def preview_import(scope, raw):
    rewards = scope['rewards']
    names = {}
    for reward in rewards:
        for alias in [reward['name'], reward.get('label'), *reward.get('aliases', [])]:
            if normalized(alias):
                names.setdefault(normalized(alias), set()).add(reward['key'])
    by_key = {r['key']: r for r in rewards}
    candidates, unmatched, invalid = {}, [], []
    for index, entry in enumerate(parse_images(raw)):
        if not isinstance(entry, dict):
            invalid.append({'index': index+1, 'reason': '图片项目必须为对象'})
            continue
        source_name = str(entry.get('name') or '')[:300]
        url = reward_image_url(entry.get('imageUrl') or entry.get('imageAssetURL') or entry.get('url') or entry.get('src'))
        if not url:
            invalid.append({'index': index+1, 'reason': '只接受已允许的官方 HTTPS CDN 图片地址'})
            continue
        key = entry.get('rewardKey')
        if key is not None and (not isinstance(key, str) or key not in by_key):
            invalid.append({'index': index+1, 'reason': '奖励对应关系已失效，请重新预览'})
            continue
        found = {key} if isinstance(key, str) and key in by_key else names.get(normalized(source_name), set())
        method = 'name'
        if not found:
            contexts = entry.get('contexts', [entry.get('context', '')])
            if not isinstance(contexts, list):
                contexts = []
            for text in contexts[:6]:
                text = normalized(str(text)[:800])
                possible = set().union(*(keys for alias, keys in names.items() if re.search(r'(?<!\w)'+re.escape(alias)+r'(?!\w)', text)))
                if possible:
                    found, method = possible, 'context'
                    break
        if len(found) != 1:
            unmatched.append({'name': source_name or '未命名图片', 'reason': '对应多个奖励，请补充准确名称' if found else '不属于当前活动或缺少准确名称'})
            continue
        key = next(iter(found))
        candidates.setdefault(key, {})[url] = method
    matches, conflicts = [], []
    for key, urls in candidates.items():
        reward = by_key[key]
        if len(urls) != 1:
            conflicts.append({'name': reward['label'], 'reason': '同一奖励对应多个不同地址，请保留一个'})
            continue
        url = next(iter(urls))
        matches.append({'rewardKey': key, 'name': reward['label'], 'imageUrl': url, 'previousImageUrl': reward.get('imageUrl'), 'matchedBy': urls[url]})
    return {'scope': scope['id'], 'scopeName': scope['name'], 'matches': matches, 'unmatched': unmatched,
            'invalid': invalid, 'conflicts': conflicts, 'canApply': bool(matches) and not conflicts and not invalid}


def scope_definitions(announcements, campaigns):
    result = []
    for item in announcements:
        result.append({'id': 'announcement:'+item['id'], 'name': item['name'], 'gameName': item['gameName'], 'rewards': [
            {'key': image_key(r['name']), 'name': r['name'], 'label': r.get('label') or r['name'],
             'aliases': r.get('aliases') or [], 'imageUrl': r.get('imageUrl')} for r in item['rewards']]})
    for campaign in campaigns:
        rewards = []
        for drop in campaign['drops']:
            benefits = list(dict.fromkeys(drop.get('benefits') or [drop['name']]))
            for name in benefits:
                rewards.append({'key': str(drop['id'])+':'+image_key(name), 'name': name, 'label': name,
                                'aliases': [drop['name']] if len(benefits) == 1 else [],
                                'imageUrl': next((i['url'] for i in drop.get('images', []) if i['name'] == name), None)})
        result.append({'id': 'campaign:'+campaign['id'], 'name': campaign['name'], 'gameName': campaign['gameName'], 'rewards': rewards})
    return result


def apply_import(store, plan, timestamp):
    if not plan['canApply']:
        raise ServiceError('请先处理预览中的无效地址或冲突，并确保至少匹配一个奖励', 'image_import_conflict', 409)
    registry = store.get('rewardImageRegistry', {})
    if plan['scope'] not in registry and len(registry) >= 500:
        raise ServiceError('图片活动记录已达上限，请联系部署管理员归档旧图片资料')
    scope = registry.setdefault(plan['scope'], {})
    for match in plan['matches']:
        scope[match['rewardKey']] = {'url': match['imageUrl'], 'importedAt': timestamp, 'source': 'manual-page-import'}
    store.put('rewardImageRegistry', registry)
    return len(plan['matches'])


def resolve_images(announcements, campaigns, registry):
    # Decode copies so picture imports cannot mutate progress, goals or source metadata.
    announcements, campaigns = json.loads(json.dumps([announcements, campaigns]))
    for item in announcements:
        images = registry.get('announcement:'+item['id'], {})
        for reward in item['rewards']:
            saved = images.get(image_key(reward['name']), {})
            if url := reward_image_url(saved.get('url')):
                reward.update(imageUrl=url, imageSource=saved['source'], imageImportedAt=saved['importedAt'])
    for campaign in campaigns:
        images = registry.get('campaign:'+campaign['id'], {})
        for drop in campaign['drops']:
            # Fresh official images always take precedence over imported fallbacks.
            native = {i['name']: i for i in drop.get('images', []) if reward_image_url(i.get('url'))}
            for name in drop.get('benefits') or [drop['name']]:
                saved = images.get(str(drop['id'])+':'+image_key(name), {})
                if name not in native and (url := reward_image_url(saved.get('url'))):
                    native[name] = {'name': name, 'url': url, 'source': saved['source']}
            drop['images'] = list(native.values())
    return announcements, campaigns
