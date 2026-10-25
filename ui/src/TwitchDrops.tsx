import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Checkbox, ConfigProvider, Drawer, Empty, Form, Input, InputNumber, Modal, Progress, Select, Spin, Table, Tag } from 'antd';
import { Check, Copy, ExternalLink, Gamepad2, ImageOff, ImagePlus, Network, Pause, Play, Plus, Radio as Twitch, RefreshCw, Settings2, Unplug } from 'lucide-react';
import './twitch-drops.css';
import { warThunderAssets, warThunderDropsTheme } from './war-thunder-style';
import type { MouseEventHandler } from 'react';

export type TwitchDropsProps = { apiBase?: string; taskRegistryHref?: string; onNavigateTasks?: MouseEventHandler<HTMLAnchorElement> };

type PageStyle = 'default' | 'war-thunder';
const pageStyleKey = 'twitch-drops-ui:style:v1';
function readPageStyle(): PageStyle {
 try { const saved = localStorage.getItem(pageStyleKey); if (saved === 'default' || saved === 'war-thunder') return saved; } catch { /* The page can still use a session-only selection. */ }
 return document.documentElement.dataset.theme === 'war-thunder' ? 'war-thunder' : 'default';
}
function WarIcon({ kind, className = '' }: { kind: keyof typeof warThunderAssets; className?: string }) {
 const [failed, setFailed] = useState(false);
 return failed ? null : <img className={`drops-war-icon ${className}`} src={warThunderAssets[kind]} alt="" aria-hidden="true" width={24} height={24} decoding="async" referrerPolicy="no-referrer" onError={() => setFailed(true)} />;
}

type Settings = { networkMode: 'auto' | 'direct' | 'proxy'; checkMinSeconds: number; checkMaxSeconds: number };
type SettingsForm = { networkMode: Settings['networkMode']; checkMinMinutes: number; checkMaxMinutes: number };
type Announcement = { id: string; gameName: string; name: string; startsAt: string; endsAt: string; sourceUrl: string; sourceLabel: string; verifiedAt: string; rewards: { name: string; label: string; requiredMinutes: number; imageUrl?: string | null }[] };
type TaskQueryPlan = { taskId: string; state: string; known: boolean; active: boolean; startsAt: string | null; nextWakeAt: number | null; sourceUrl?: string; label?: string };
type QuerySchedule = { mode: 'auto' | 'always' | 'custom'; sessions: { startsAt: string }[] };
type Schedule = { queryPlan?: { active: boolean; state: string; nextWakeAt: number | null; tasks: TaskQueryPlan[] };  metadataSource: 'external' | 'service'; metadataMinSeconds: number; metadataMaxSeconds: number; nextMetadataAt: number | null; nextChannelAt: number | null };
type GoalRef = { campaignId: string; dropId: string };
type Goal = { mode: 'all' | 'selected'; rewards: GoalRef[]; lockedAt: string | null };
type GoalState = { taskId: string; status: string; total: number; completed: number; claimPending: number; blocked: number; rewards: (GoalRef & { name: string; campaignName: string; status: string; completed: boolean })[] };
type Task = { querySchedule?: QuerySchedule; id: string; game: { id: string | null; name: string; slug: string }; enabled: boolean; priority: number; watchOnly: boolean; autoChannels: boolean; channels: string[]; goal: Goal };
type Drop = { id: string; name: string; benefits: string[]; images?: { name: string; url: string }[]; minutes: number | null; requiredMinutes: number | null; claimed: boolean | null; claimable: boolean; sampledAt: string };
type Campaign = { id: string; name: string; gameId: string; gameName: string; startsAt: string | null; endsAt: string | null; sampledAt: string; linked: boolean | null; drops: Drop[] };
type ImageScope = { id: string; name: string; gameName: string; rewardCount: number };
type ImagePreview = { scope: string; scopeName: string; matches: { rewardKey: string; name: string; imageUrl: string; previousImageUrl: string | null; matchedBy: string }[]; unmatched: { name: string; reason: string }[]; invalid: { index: number; reason: string }[]; conflicts: { name: string; reason: string }[]; canApply: boolean; updated?: number };
type Snapshot = {
 account: { login: string; user_id: string; validatedAt: string; expiresAt: number } | null;
 authorization: { pending: boolean; reconnectRequired?: boolean; device: { userCode: string; verificationUri: string; expiresAt: string } | null; error: string | null };
 announcements: Announcement[]; schedule: Schedule; rewardImageScopes?: ImageScope[];
 settings: Settings; running: boolean; state: string; reason: string;
 current: { taskId: string; game: Task['game']; eligibility: string; channel: { login: string; displayName: string }; selectedAt: string } | null;
 goals: GoalState[]; queueCompletedAt: string | null;
 tasks: Task[]; campaigns: Campaign[]; progress: { status: string; sampledAt: string | null; message: string; failedAt?: string; catalogueAvailable?: boolean; catalogueMessage?: string | null };
 currentProgress: { dropId: string; minutes: number | null; sampledAt: string } | null;
 syncing: boolean; lastSync: string | null; lastWatchAt: string | null; watchReports: number; sampledAt: string;
 network: { route: string; proxy: string; lastSuccessAt: string | null; lastError: string | null; cooldownUntil: string | null };
 traffic: { hourBytes: number; dayBytes: number; proxyDayBytes: number; directDayBytes: number };
 events: { id: number; at: string; level: string; message: string }[];
};
type TaskForm = { queryMode: QuerySchedule['mode']; sessionsText: string; gameName: string; priority: number; enabled: boolean; watchOnly: boolean; autoChannels: boolean; channelsText: string; goalMode: 'all' | 'selected'; rewardKeys: string[]; resetGoals: boolean };
type RunState = { kind: 'running' | 'pending' | 'paused' | 'disabled' | 'completed' | 'attention' | 'cooldown'; label: string; detail?: string };
function queueRunState(data: Snapshot, controlPending: boolean): RunState {
 if (controlPending) return { kind: 'pending', label: data.running ? '正在暂停' : '正在启动' };
 if (data.queueCompletedAt) return { kind: 'completed', label: '本轮已完成' };
 if (data.authorization.reconnectRequired || data.state === 'login_required') return { kind: 'attention', label: '需要授权' };
 if (!data.running) return { kind: data.state === 'error' ? 'attention' : 'paused', label: data.state === 'error' ? '需要处理' : '已暂停' };
 if (data.state === 'cooldown') return { kind: 'cooldown', label: '已启动 · 冷却中' };
 if (data.state === 'error') return { kind: 'attention', label: '已启动 · 需要处理' };
 return { kind: 'running', label: data.state === 'watching' ? '运行中 · 观看上报' : data.state === 'scheduled_wait' ? '运行中 · 等待计划时间' : '运行中 · 自动值守' };
}
function taskRunState(task: Task, data: Snapshot): RunState {
 if (!task.enabled) return { kind: 'disabled', label: '已停用', detail: '不参与本轮调度' };
 const goal = data.goals.find(item => item.taskId === task.id);
 if (goal?.status === 'completed') return { kind: 'completed', label: '目标已达标', detail: '奖励仍需手动领取' };
 if (data.authorization.reconnectRequired || data.state === 'login_required') return { kind: 'attention', label: '等待授权', detail: '连接账号后可继续' };
 if (!data.running) return { kind: data.state === 'error' ? 'attention' : 'paused', label: data.state === 'error' ? '需要处理' : '已暂停', detail: '当前没有执行挂机' };
 if (data.state === 'cooldown') return { kind: 'cooldown', label: '冷却中', detail: '到时自动检查' };
 if (data.state === 'error') return { kind: 'attention', label: '需要处理', detail: '请查看上方运行说明' };
 if (goal?.status === 'waiting_claim') return { kind: 'attention', label: '等待手动领取', detail: '领取前置奖励后继续' };
 if (goal?.status === 'expired') return { kind: 'attention', label: '活动已结束', detail: '请重新设置奖励目标' };
 if (data.current?.taskId === task.id) return { kind: 'running', label: '观看上报中', detail: '掉宝进度以 Twitch 为准' };
 if (data.current) return { kind: 'pending', label: '排队中', detail: '当前正在执行其他游戏' };
 const plan = data.schedule.queryPlan?.tasks.find(item => item.taskId === task.id);
 if (plan?.state === 'scheduled') return { kind: 'running', label: '计划值守 · 等待开播', detail: `${date(plan.startsAt)} 开播，提前 5 分钟查询` };
 if (plan?.state === 'schedule_finished') return { kind: 'attention', label: '等待后续场次', detail: '可补充直播时间或切换不限时间查询' };
 if (goal?.status === 'waiting_targets' && !task.watchOnly) return { kind: 'pending', label: '已启动 · 等待资料', detail: '取得活动资料后自动调度' };
 return { kind: 'running', label: '挂机中 · 等待开播', detail: '发现符合条件直播后执行' };
}
function RunMarker({ state, pulse = false }: { state: RunState; pulse?: boolean }) {
 return <span className="drops-run-marker" data-state={state.kind} data-pulse={pulse && state.kind === 'running' || undefined}><i aria-hidden="true" /><span>{state.label}</span></span>;
}
const goalLabels: Record<string, string> = { waiting_targets: '等待官方目标资料', ready: '等待直播／执行中', unknown: '官方进度无法确认', waiting_claim: '等待手动领取前置奖励', completed: '本轮目标已达标', expired: '活动结束，目标未完成' };
const rewardKey = (ref: GoalRef) => JSON.stringify([ref.campaignId, ref.dropId]);
const date = (value?: string | null) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' }) : '尚无记录';
function parseSessions(value: string) {
 const lines = value.split('\n').map(line => line.trim()).filter(Boolean);
 if (!lines.length || lines.length > 100) throw new Error('请填写 1–100 个开播时间');
 return lines.map(line => {
  if (!/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/.test(line)) throw new Error('请每行按 YYYY-MM-DD HH:mm 填写北京时间');
  const dateValue = new Date(line.replace(' ', 'T') + ':00+08:00');
  if (!Number.isFinite(dateValue.getTime()) || new Date(dateValue.getTime() + 8 * 3600000).toISOString().slice(0,16) !== line.replace(' ', 'T')) throw new Error('开播日期或时间无效');
  return { startsAt: dateValue.toISOString() };
 });
}
const bytes = (value: number) => value < 1024 ? `${value} B` : value < 1048576 ? `${(value / 1024).toFixed(1)} KiB` : `${(value / 1048576).toFixed(2)} MiB`;
const percent = (value: number, limit: number) => Math.min(100, Math.max(0, value * 100 / Math.max(1, limit)));
const imageExtractor = `copy(JSON.stringify([...new Map([...document.querySelectorAll('img[src*="/twitch-quests-assets/REWARD/"]')].slice(0,200).map(i => {
 const contexts=[]; let p=i.parentElement;
 for(let depth=0;p&&depth<4&&!['BODY','MAIN'].includes(p.tagName);depth++,p=p.parentElement){const t=(p.innerText||'').trim();if(t&&t.length<=400&&!contexts.includes(t))contexts.push(t);}
 return [i.currentSrc||i.src,{name:i.alt.trim(),imageUrl:i.currentSrc||i.src,contexts}];
})).values()],null,2))`;

function RewardImage({ src, name }: { src?: string | null; name: string }) {
 const [failedUrl, setFailedUrl] = useState('');
 let url = '';
 try {
  const parsed = new URL(src || '');
  if (parsed.protocol === 'https:' && !parsed.username && !parsed.password && ['static-cdn.jtvnw.net', 'images.twitch.tv', 'static.twitchcdn.net', 'staticfiles.warthunder.com', 'patchnotes.cdn.gaijin.net'].includes(parsed.hostname)) url = parsed.href;
 } catch { /* Missing or invalid metadata keeps a labeled placeholder. */ }
 return <div className="drops-reward-image">{url && failedUrl !== url ? <img src={url} alt={name} width={256} height={256} loading="lazy" decoding="async" referrerPolicy="no-referrer" onError={() => setFailedUrl(url)} /> : <div className="drops-image-placeholder" role="img" aria-label={`${name}：${url ? '图片加载失败' : '暂无图片'}`}><ImageOff size={24} aria-hidden="true" /><small>{url ? '图片暂不可用' : '暂无奖励图片'}</small></div>}</div>;
}

async function api<T = Snapshot>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal, base = '/api'): Promise<T> {
 const response = await fetch(base + path, { method, credentials: 'same-origin', signal: signal ?? AbortSignal.timeout(30000),
  ...(body === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }) });
 const data = await response.json();
 if (!response.ok) throw new Error(data.error || data.message || '掉宝服务暂不可用');
 return data;
}

export default function TwitchDrops({ apiBase = '/api', taskRegistryHref, onNavigateTasks }: TwitchDropsProps = {}) {
 const [pageStyle, setPageStyle] = useState<PageStyle>(readPageStyle);
 const warStyle = pageStyle === 'war-thunder';
 const chooseStyle = (style: PageStyle) => { setPageStyle(style); try { localStorage.setItem(pageStyleKey, style); } catch { /* Preserve the session selection when storage is unavailable. */ } };
 useEffect(() => { const sync = (event: StorageEvent) => { if (event.key === pageStyleKey || event.key === null) setPageStyle(readPageStyle()); }; window.addEventListener('storage', sync); return () => window.removeEventListener('storage', sync); }, []);
 const [data, setData] = useState<Snapshot | null>(null), [error, setError] = useState(''), [busy, setBusy] = useState('');
 const [settingsOpen, setSettingsOpen] = useState(false), [editor, setEditor] = useState<Task | 'new' | null>(null), [disconnectOpen, setDisconnectOpen] = useState(false);
 const [imageScriptCopied, setImageScriptCopied] = useState(false), [authorizationHelpOpen, setAuthorizationHelpOpen] = useState(false);
 const accountSection = useRef<HTMLElement | null>(null);
 const [imagesOpen, setImagesOpen] = useState(false), [imageScope, setImageScope] = useState(''), [imageInput, setImageInput] = useState('');
 const [imagePreview, setImagePreview] = useState<ImagePreview | null>(null), [imageError, setImageError] = useState(''), [imageNotice, setImageNotice] = useState('');
 const [clientId, setClientId] = useState(''), [copied, setCopied] = useState(false), [clock, setClock] = useState(Date.now());
 const [settingsForm] = Form.useForm<SettingsForm>(), [taskForm] = Form.useForm<TaskForm>();
 const goalMode = Form.useWatch('goalMode', taskForm);
 const editingGame = Form.useWatch('gameName', taskForm);
 const queryMode = Form.useWatch('queryMode', taskForm);
 const sequence = useRef(0), current = useRef(data), mounted = useRef(true), mutation = useRef(false);
 current.current = data;
 const load = useCallback(async (signal?: AbortSignal) => {
  if (mutation.current) return;
  const id = ++sequence.current;
  const result = await api('/snapshot', 'GET', undefined, signal, apiBase);
  if (!signal?.aborted && mounted.current && id === sequence.current) { setData(result); setError(''); }
 }, [apiBase]);
 useEffect(() => {
  mounted.current = true;
  let timer: number | undefined; let controller: AbortController | null = null;
  const poll = async () => {
   if (document.hidden) return;
   controller?.abort(); controller = new AbortController(); const ownController = controller;
   try { await load(ownController.signal); } catch (e) { if (!ownController.signal.aborted && mounted.current) setError(e instanceof Error ? e.message : '状态读取失败'); }
   if (controller === ownController && mounted.current && !document.hidden) timer = window.setTimeout(poll, current.current?.authorization.pending ? 3000 : current.current?.running || current.current?.syncing ? 10000 : 30000);
  };
  const visible = () => { window.clearTimeout(timer); if (document.hidden) controller?.abort(); else void poll(); };
  const changed = () => { window.clearTimeout(timer); void poll(); };
  void poll(); document.addEventListener('visibilitychange', visible); window.addEventListener('orbit-api-change', changed);
  return () => { mounted.current = false; controller?.abort(); window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible); window.removeEventListener('orbit-api-change', changed); };
 }, [load]);
 const pending = data?.authorization.device;
 useEffect(() => { if (!pending) return; setClock(Date.now()); const timer = window.setInterval(() => setClock(Date.now()), 1000); return () => window.clearInterval(timer); }, [pending?.expiresAt]);
 async function action(key: string, path: string, method = 'POST', body: unknown = {}) {
  if (mutation.current) return false;
  mutation.current = true;
  setBusy(key); setError(''); const id = ++sequence.current;
  try { const result = await api(path, method, body, undefined, apiBase); if (mounted.current && id === sequence.current) setData(result); return true; }
  catch (e) { if (mounted.current) setError(e instanceof Error ? e.message : '操作失败'); return false; }
  finally { mutation.current = false; if (mounted.current) setBusy(''); }
 }
 function openSettings() { if (data) settingsForm.setFieldsValue({ networkMode: data.settings.networkMode, checkMinMinutes: data.settings.checkMinSeconds / 60, checkMaxMinutes: data.settings.checkMaxSeconds / 60 }); setSettingsOpen(true); }
 function openImages() { setImageScriptCopied(false); setImageScope(data?.rewardImageScopes?.[0]?.id || ''); setImageInput(''); setImagePreview(null); setImageError(''); setImagesOpen(true); }
 async function inspectImages(save = false) {
  if (mutation.current) return;
  if (!imageScope || !imageInput.trim()) { setImageError('请选择活动并粘贴图片资料'); return; }
  if (new TextEncoder().encode(imageInput).length > 16000) { setImageError('图片资料最多 16 KiB，请只提取目标活动'); return; }
  mutation.current = true; const id = ++sequence.current; setBusy(save ? 'image-import' : 'image-preview'); setImageError('');
  try {
   const result = await api<ImagePreview>(`/reward-images/${save ? 'import' : 'preview'}`, 'POST', { scope: imageScope, input: imageInput }, undefined, apiBase);
   if (!mounted.current) return;
   if (save) {
    const snapshot = await api('/snapshot', 'GET', undefined, undefined, apiBase);
    if (mounted.current && id === sequence.current) setData(snapshot);
    setImageNotice(`已保存 ${result.updated ?? 0} 个奖励图片地址，重启后仍会保留。`); setImagesOpen(false); setImageInput(''); setImagePreview(null);
   } else setImagePreview(result);
  } catch (e) { if (mounted.current) setImageError(e instanceof Error ? e.message : '图片资料处理失败'); }
  finally { mutation.current = false; if (mounted.current) setBusy(''); }
 }
 function edit(task: Task | 'new') {
  taskForm.setFieldsValue(task === 'new' ? { queryMode: 'auto', sessionsText: '', gameName: '', priority: 50, enabled: true, watchOnly: false, autoChannels: true, channelsText: '', goalMode: 'all', rewardKeys: [], resetGoals: false }
   : { queryMode: task.querySchedule?.mode ?? 'auto', sessionsText: (task.querySchedule?.sessions ?? []).map(item => new Date(Date.parse(item.startsAt) + 8 * 3600000).toISOString().slice(0,16).replace('T',' ')).join('\n'), gameName: task.game.name, priority: task.priority, enabled: task.enabled, watchOnly: task.watchOnly, autoChannels: task.autoChannels, channelsText: task.channels.join('\n'), goalMode: task.goal.mode, rewardKeys: task.goal.rewards.map(rewardKey), resetGoals: false });
  setEditor(task);
 }
 async function copyCode() {
  if (!pending) return;
  try { await navigator.clipboard.writeText(pending.userCode); setCopied(true); window.setTimeout(() => setCopied(false), 2000); }
  catch { setError('复制失败，可直接选中授权码复制'); }
 }
 const secondsLeft = pending ? Math.max(0, Math.ceil((Date.parse(pending.expiresAt) - clock) / 1000)) : 0;
 const editingTask = editor && editor !== 'new' ? editor : null;
 const editingCampaigns = data?.campaigns.filter(c => c.gameName.toLowerCase() === editingGame?.trim().toLowerCase()) ?? [];
 const rewardOptions = editingCampaigns.map(c => ({ label: c.name, options: c.drops.map(d => ({ value: rewardKey({ campaignId: c.id, dropId: d.id }), label: d.benefits.join(' / ') || d.name })) }));
 const availableKeys = new Set(rewardOptions.flatMap(c => c.options.map(o => o.value)));
 const savedOptions = data?.goals.find(g => g.taskId === editingTask?.id)?.rewards.filter(r => !availableKeys.has(rewardKey(r))).map(r => ({ value: rewardKey(r), label: `${r.campaignName} · ${r.name}` })) ?? [];
 if (savedOptions.length && editingGame === editingTask?.game.name) rewardOptions.push({ label: '已保存目标（等待资料）', options: savedOptions });
 const selectedRewards = new Set(data?.tasks.flatMap(t => t.enabled ? t.goal.rewards.map(rewardKey) : []) ?? []);
 const reconnectRequired = !!data?.authorization.reconnectRequired || data?.state === 'login_required';
 const known = data?.progress.status === 'available' && !reconnectRequired;
 function showAccountConnection() { accountSection.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }); }
 async function connectTwitch() { showAccountConnection(); if (!data?.authorization.pending) await action('auth', '/auth/start', 'POST', { clientId: clientId.trim() || undefined }); }

 return <ConfigProvider theme={warStyle ? warThunderDropsTheme : undefined}><div className="drops-page" data-drops-style={pageStyle} aria-busy={!!busy}>
  <header className="drops-page-heading"><div className="drops-page-identity">{warStyle && <a className="drops-war-brand" href="https://warthunder.com/" target="_blank" rel="noopener noreferrer" aria-label="War Thunder 官方网站"><WarIcon kind="logo" /></a>}<div><span className="drops-page-kicker">{warStyle ? 'WAR THUNDER · TWITCH DROPS' : 'TWITCH DROPS'}</span><h1>挂机掉宝</h1><p>直播自动发现 · 多游戏目标 · 奖励手动领取</p></div></div><div className="drops-style-switch" role="group" aria-label="挂机掉宝页面风格"><span>页面风格</span><Button size="small" type="text" aria-pressed={!warStyle} icon={<Gamepad2 size={15} />} onClick={() => chooseStyle('default')}>默认</Button><Button size="small" type="text" aria-pressed={warStyle} icon={<WarIcon kind="icon" />} onClick={() => chooseStyle('war-thunder')}>战雷</Button></div></header>
  {error && <Alert type="error" showIcon title={error} closable onClose={() => setError('')} />}
 {imageNotice && <Alert type="success" showIcon title={imageNotice} closable onClose={() => setImageNotice('')} />}
  <div className="drops-toolbar"><div className="drops-status" role="status" aria-live="polite">{data ? <RunMarker state={queueRunState(data, busy === 'control')} pulse /> : <Tag>读取状态</Tag>}<span>{data?.reason ?? '正在连接掉宝服务'}</span></div><div className="drops-actions">
   <Button icon={<ImagePlus size={15} />} disabled={!data?.rewardImageScopes?.length || !!busy} title={!data?.rewardImageScopes?.length ? '取得活动资料后可补充图片' : undefined} onClick={openImages}>奖励图片</Button>
   <Button icon={<Settings2 size={15} />} disabled={!data || !!busy} onClick={openSettings}>连接设置</Button>
   <Button icon={<RefreshCw size={15} />} loading={busy === 'sync' || data?.syncing} disabled={!data?.account || !!busy || data?.syncing} onClick={() => void action('sync', '/sync')}>同步官方进度</Button>
   <Button type={data?.running ? 'default' : 'primary'} icon={data?.running ? <Pause size={15} /> : <Play size={15} />} disabled={!data?.account || !!busy} loading={busy === 'control'} onClick={() => void action('control', '/control', 'POST', { running: !data?.running })}>{data?.running ? '暂停挂机' : '开始挂机'}</Button>
  </div></div>
  {!data && !error && <Spin />}
  {data && <>
   {data.queueCompletedAt && <Alert type="success" showIcon title="本轮所有目标已达标，挂机已自动结束" description={<>完成于 {date(data.queueCompletedAt)}。未领取奖励请到 Twitch 手动领取；下一轮请在游戏任务设置中重新选择目标。</>} />}
   <div className="drops-summary">
    <section className="wt-admin-panel drops-metric"><span><Twitch size={16} /> Twitch 账号</span><strong>{data.account?.login ?? '未连接'}</strong><small>{data.account ? '登录校验 ' + date(data.account.validatedAt) : '在官方页面完成设备授权'}</small></section>
    <section className="wt-admin-panel drops-metric"><span><Gamepad2 size={16} /> 当前任务</span><strong>{data.current?.game.name ?? (data.running ? data.state === 'scheduled_wait' ? '按赛程待命' : '后台值守中' : '已暂停')}</strong><small>{data.current ? <a href={`https://www.twitch.tv/${encodeURIComponent(data.current.channel.login)}`} target="_blank" rel="noreferrer">{data.current.channel.displayName} ↗</a> : data.running ? data.state === 'scheduled_wait' ? '已知直播在开播前 5 分钟开始查询' : '自动检查频道，等待符合条件的直播' : '启动后将自动检查频道'}</small></section>
    <section className="wt-admin-panel drops-metric"><span><Network size={16} /> 当前线路</span><strong>{({ direct: '直连', proxy: data.network.proxy, unknown: '等待连接' } as Record<string, string>)[data.network.route] ?? '未知'}</strong><small>{data.network.route === 'proxy' ? '仅使用部署方配置的代理' : data.settings.networkMode === 'auto' ? '直连优先，失败后使用配置的代理' : '按手动线路设置运行'}</small></section>
    <section className="wt-admin-panel drops-metric"><span>今日代理流量</span><strong>{bytes(data.traffic.proxyDayBytes)}</strong><small>今日全部线路 {bytes(data.traffic.dayBytes)}</small></section>
   </div>

   <div className="drops-workspace"><div className="drops-main">
   <section className="wt-admin-panel">
    <div className="wt-admin-panel-title"><div><h2>{warStyle && <WarIcon kind="research" />}游戏任务</h2><p className="wt-admin-help">按优先级执行；全部启用目标达标后结束本轮。</p></div><Button icon={<Plus size={15} />} disabled={!!busy || data.tasks.length >= 12} onClick={() => edit('new')}>添加游戏</Button></div>
    <Table<Task> className="drops-task-table" size="small" rowKey="id" tableLayout="fixed" rowClassName={task => { const state = taskRunState(task, data); return state.kind === 'running' ? 'drops-task-running' : ''; }} dataSource={data.tasks} pagination={false} scroll={{ x: 1080 }} locale={{ emptyText: <Empty description="还没有游戏任务" /> }} columns={[
     { title: '游戏', width: 210, render: (_, task) => <div className="drops-game">{warStyle && (task.game.slug === 'war-thunder' || /^war\s*thunder$/i.test(task.game.name)) && <WarIcon kind="icon" />}<strong>{task.game.name}</strong></div> },
     { title: '运行状态', width: 196, render: (_, task) => { const state = taskRunState(task, data); return <div className="drops-task-status"><RunMarker state={state} /><small>{state.detail}</small></div>; } },
     { title: '优先级', dataIndex: 'priority', width: 80, sorter: (a, b) => a.priority - b.priority, defaultSortOrder: 'descend' },
     { title: '频道', width: 210, render: (_, task) => <div className="drops-channel-policy"><span>{task.channels.length ? `${task.channels.length} 个指定频道` : '未指定频道'}{task.autoChannels ? ' · 自动发现' : ''}</span><small>{task.watchOnly ? '仅观看 · 资格需人工核对' : '确认资格后执行'}</small></div> },
     { title: '本轮目标', width: 250, render: (_, task) => { const goal = data.goals.find(g => g.taskId === task.id); return <div className="drops-goal-summary"><strong>{task.goal.mode === 'all' ? '本轮全部奖励' : '指定奖励'}{goal?.total ? ` · ${goal.completed} / ${goal.total}` : ''}</strong><span>{goalLabels[goal?.status ?? 'waiting_targets']}</span>{!!goal?.claimPending && <small>{goal.claimPending} 个目标已达标，待手动领取</small>}{!!goal?.blocked && <small>{goal.blocked} 个目标等待领取前置奖励，其他目标可继续</small>}</div>; } },
     { title: '', width: 132, render: (_, task) => <div className="drops-actions"><Button size="small" disabled={!!busy} onClick={() => edit(task)}>设置</Button><Button size="small" disabled={!!busy} onClick={() => void action('task-toggle', `/tasks/${task.id}`, 'PUT', { queryMode: task.querySchedule?.mode ?? 'auto', sessionsText: (task.querySchedule?.sessions ?? []).map(item => new Date(Date.parse(item.startsAt) + 8 * 3600000).toISOString().slice(0,16).replace('T',' ')).join('\n'), gameName: task.game.name, priority: task.priority, watchOnly: task.watchOnly, autoChannels: task.autoChannels, channels: task.channels, enabled: !task.enabled })}>{task.enabled ? '停用' : '启用'}</Button></div> },
    ]} />
   </section>

   {!!data.announcements.length && <section className="wt-admin-panel drops-announcements"><div className="wt-admin-panel-title"><div><h2>{warStyle && <WarIcon kind="talisman" />}活动与奖励说明</h2><p className="wt-admin-help">官方公告中的奖励与观看要求，个人进度见下方。</p></div><Tag>公开活动说明</Tag></div>{data.announcements.map(announcement => <article key={announcement.id}><div className="drops-announcement-title"><h3>{announcement.name}</h3><span>{date(announcement.startsAt)} — {date(announcement.endsAt)}</span></div><div className="drops-public-rewards">{announcement.rewards.map(reward => <div key={reward.name}><RewardImage src={reward.imageUrl} name={reward.label} /><div className="drops-public-reward-body"><strong title={reward.name}>{reward.label}</strong><b>{reward.requiredMinutes < 60 ? `${reward.requiredMinutes} 分钟` : `${reward.requiredMinutes / 60} 小时`}</b></div></div>)}</div><p className="wt-admin-help"><a href={announcement.sourceUrl} target="_blank" rel="noreferrer">{announcement.sourceLabel} ↗</a> · 核对于 {date(announcement.verifiedAt)} · 活动要求，不代表个人进度</p></article>)}</section>}

   <section className="wt-admin-panel drops-progress">
    <div className="wt-admin-panel-title"><div><h2>{warStyle && <WarIcon kind="research" />}官方掉宝进度</h2><p className="wt-admin-help">最近确认：{date(data.progress.sampledAt)}（北京时间）</p></div><Tag color={known ? 'success' : data.account ? 'warning' : 'default'}>{known ? '官方库存可读' : reconnectRequired ? '需要重新授权' : data.account ? '账号已连接 · 进度未知' : '尚未连接账号'}</Tag></div>
    {(!known || data.progress.catalogueAvailable === false) && <Alert type={data.account ? 'warning' : 'info'} showIcon
     title={!data.account ? (data.authorization.pending ? '等待完成 Twitch 官方授权' : '先连接你的 Twitch 账号') : reconnectRequired ? 'Twitch 登录已失效，请重新授权' : !known ? 'Twitch 账号已连接，个人掉宝进度暂不可读取' : '账号进度可读，活动目录暂未返回'}
     description={<div className="drops-progress-guidance"><p>{!data.account ? '点击连接获取授权码，在 Twitch 官方页面登录并批准；此服务不接收邮箱密码。' : reconnectRequired ? '重新获取授权码并在 Twitch 官方页面批准，完成后再同步进度。' : '账号连接成功不代表进度接口可用。请先到官方页面核对活动和游戏账号关联；进度未知时无法自动判定目标达标。'}</p><div className="drops-actions">
      {(!data.account || reconnectRequired) && <Button size="small" type="primary" loading={busy === 'auth'} disabled={!!busy || data.authorization.pending && !pending} onClick={() => void connectTwitch()}>{data.authorization.pending ? '查看授权码与入口' : reconnectRequired ? '重新授权 Twitch' : '连接并授权 Twitch'}</Button>}
      <a href="https://www.twitch.tv/drops/inventory" target="_blank" rel="noreferrer">打开官方库存 ↗</a>
      <a href="https://www.twitch.tv/drops/campaigns" target="_blank" rel="noreferrer">活动与账号关联 ↗</a>
      <Button type="link" size="small" onClick={() => setAuthorizationHelpOpen(true)}>授权与进度说明</Button>
     </div></div>} />}
    {data.current?.eligibility === 'unverified' && <Alert type="info" title="当前使用仅观看模式：频道掉宝资格尚未经服务确认，请在 Twitch 活动页面核对。" />}
    {data.currentProgress && <p className="wt-admin-help">当前会话由 Twitch 确认：{data.currentProgress.minutes ?? '未知'} 分钟 · {date(data.currentProgress.sampledAt)}</p>}
    {data.campaigns.length ? <div className="drops-campaigns">{data.campaigns.map(campaign => <details key={campaign.id} open><summary><span>{campaign.name}</span><Tag>{campaign.gameName}</Tag>{campaign.linked === false && <Tag color="warning"><a href="https://www.twitch.tv/drops/campaigns" target="_blank" rel="noreferrer">去关联游戏账号 ↗</a></Tag>}<small>截止 {date(campaign.endsAt)}</small></summary><div className="drops-rewards">{campaign.drops.map(drop => <div className="drops-reward" key={drop.id}>
     <div className="drops-reward-identity"><div className="drops-reward-thumbs">{drop.images?.length ? drop.images.map(image => <RewardImage key={image.name + image.url} src={image.url} name={image.name} />) : <RewardImage name={drop.name} />}</div><div><strong>{drop.benefits.join(' / ') || drop.name}</strong>{selectedRewards.has(rewardKey({ campaignId: campaign.id, dropId: drop.id })) && <Tag color="blue">本轮目标</Tag>}<small>确认于 {date(drop.sampledAt)}</small></div></div><div>{drop.minutes === null || drop.requiredMinutes === null ? <span>进度未知</span> : <><Progress percent={percent(drop.minutes, drop.requiredMinutes)} showInfo={false} size="small" /><small>{drop.minutes} / {drop.requiredMinutes} 分钟</small></>}</div><Tag color={drop.claimed ? 'success' : drop.claimable ? 'warning' : 'default'}>{drop.claimed === null ? '领取状态未知' : drop.claimed ? '已领取' : drop.claimable ? '待手动领取' : '尚未完成'}</Tag>
    </div>)}</div></details>)}</div> : known ? <p className="drops-compact-empty">当前未取得可跟踪的活动与奖励资料</p> : null}
   </section>

   </div><aside className="drops-sidebar" aria-label="账号与运行状态">
   <section ref={accountSection} className="wt-admin-panel drops-account">
    <div className="wt-admin-panel-title"><div><h2><Twitch size={17} /> Twitch 账号连接</h2><p className="wt-admin-help">通过 Twitch 官方页面授权；运行环境在线时，后台可持续运行。</p></div><div className="drops-actions">
     <a className="drops-link" href="https://www.twitch.tv/drops/inventory" target="_blank" rel="noreferrer">手动领取奖励 <ExternalLink size={13} /></a>
     {data.account && <Button icon={<Unplug size={14} />} disabled={!!busy} onClick={() => setDisconnectOpen(true)}>解除连接</Button>}
     {!data.authorization.pending && <Button type={data.account ? 'default' : 'primary'} loading={busy === 'auth'} disabled={!!busy} onClick={() => void action('auth', '/auth/start', 'POST', { clientId: clientId.trim() || undefined })}>{data.account ? '重新授权' : '连接 Twitch'}</Button>}
    </div></div>
    {data.authorization.error && <Alert type="warning" showIcon title={data.authorization.error} />}
    {pending && <div className="drops-device"><div><span>在 Twitch 激活页输入授权码</span><div className="drops-device-code"><code>{pending.userCode}</code><Button aria-label="复制 Twitch 授权码" size="small" icon={copied ? <Check size={14} /> : <Copy size={14} />} onClick={() => void copyCode()}>{copied ? '已复制' : '复制'}</Button></div><small>有效期剩余 {Math.floor(secondsLeft / 60)}:{String(secondsLeft % 60).padStart(2, '0')} · 等待官方授权结果</small></div><div className="drops-actions"><Button type="primary" href={pending.verificationUri} target="_blank" rel="noreferrer" icon={<ExternalLink size={14} />}>打开 Twitch 授权页</Button><Button disabled={!!busy} onClick={() => void action('cancel', '/auth/cancel')}>取消授权</Button></div></div>}
    {data.authorization.pending && !pending && <Alert type="info" title="另一位管理员正在完成 Twitch 授权。" />}
    {!data.authorization.pending && <details className="drops-advanced"><summary>高级授权设置</summary><label htmlFor="drops-client-id">自有 Twitch 公共应用 Client ID（可选）</label><div><Input id="drops-client-id" value={clientId} maxLength={64} onChange={event => setClientId(event.target.value)} placeholder="留空使用默认设备授权方式" /><a href="https://dev.twitch.tv/console/apps" target="_blank" rel="noreferrer">应用管理 ↗</a></div><p>仅在需要使用自己的应用时填写；更换 Client ID 不会自动获得掉宝读取能力。无需邮箱密码或 Client Secret，授权码与登录令牌不保存在浏览器存储中。</p></details>}
   </section>

    <section className="wt-admin-panel"><div className="wt-admin-panel-title"><h2>连接与调度</h2><Tag>低流量</Tag></div><p className="wt-admin-help">自动查询按直播时间门控；原生任务可本地唤醒，时段外跳过对外请求。</p><dl className="drops-facts"><dt>直播计划查询</dt><dd>{data.schedule.queryPlan?.state === 'scheduled' && data.schedule.queryPlan.nextWakeAt ? date(new Date(data.schedule.queryPlan.nextWakeAt * 1000).toISOString()) : data.schedule.queryPlan?.active ? '当前处于查询时段' : data.schedule.queryPlan?.state === 'paused' ? '队列已暂停' : '等待后续场次'}</dd><dt>已知场次规则</dt><dd>提前 5 分钟查询；非时段不对外轮询</dd><dt>频道检查</dt><dd>{data.settings.checkMinSeconds / 60}～{data.settings.checkMaxSeconds / 60} 分钟，范围内随机</dd><dt>下次频道检查</dt><dd>{data.schedule.nextChannelAt ? date(new Date(data.schedule.nextChannelAt * 1000).toISOString()) : '尚未排定'}</dd><dt>官方资料同步</dt><dd>{data.schedule.metadataSource === 'external' ? `原生任务 · 约 ${data.schedule.metadataMinSeconds / 60}～${data.schedule.metadataMaxSeconds / 60} 分钟，以原生计划为准` : `${data.schedule.metadataMinSeconds / 60}～${data.schedule.metadataMaxSeconds / 60} 分钟，范围内随机`}</dd></dl><details className="drops-stream-plan"><summary>直播场次与来源（北京时间）</summary>{data.schedule.queryPlan?.tasks.filter(plan => plan.known).map(plan => <p key={plan.taskId}><strong>{plan.label ?? '已公布场次'}</strong><span>{plan.startsAt ? date(plan.startsAt) : '已公布场次已结束'}</span>{plan.nextWakeAt && <small>开始查询：{date(new Date(plan.nextWakeAt * 1000).toISOString())}</small>}{plan.sourceUrl?.startsWith('https://') && <a href={plan.sourceUrl} target="_blank" rel="noreferrer">查看官方时间 ↗</a>}</p>) ?? <p>此服务版本尚未提供场次资料。</p>}</details><details className="drops-diagnostics"><summary>流量与运行明细</summary><dl className="drops-facts"><dt>最近一小时</dt><dd>{bytes(data.traffic.hourBytes)}</dd><dt>今日全部线路</dt><dd>{bytes(data.traffic.dayBytes)}</dd><dt>最近网络成功</dt><dd>{date(data.network.lastSuccessAt)}</dd><dt>最近观看上报</dt><dd>{date(data.lastWatchAt)}</dd><dt>已接收上报次数</dt><dd>{data.watchReports}（不等于掉宝分钟数）</dd><dt>最近资料检查</dt><dd>{date(data.lastSync)}</dd></dl></details>{data.network.lastError && <Alert type="warning" title={data.network.lastError} />}</section>
    <section className="wt-admin-panel"><div className="wt-admin-panel-title"><h2>{warStyle && <WarIcon kind="icon" />}运行记录</h2>{taskRegistryHref && <a href={taskRegistryHref} onClick={onNavigateTasks}>定时任务 ↗</a>}</div><div className="drops-log" aria-label="脱敏运行记录">{data.events.length ? data.events.map(event => <div key={event.id}><time dateTime={event.at} title={date(event.at)}>{new Date(event.at).toLocaleTimeString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' })}</time><span data-level={event.level}>{event.message}</span></div>) : <p>暂无运行记录</p>}</div></section>
   </aside></div>
  </>}

  <Modal rootClassName={warStyle ? 'drops-war-thunder-overlay' : undefined} title="Twitch 授权与掉宝进度" width={620} open={authorizationHelpOpen} onCancel={() => setAuthorizationHelpOpen(false)} footer={<Button onClick={() => setAuthorizationHelpOpen(false)}>知道了</Button>}>
   <ol className="drops-auth-steps">
    <li><strong>连接 Twitch 账号</strong><p>在右侧「Twitch 账号连接」点击连接，获取一次性授权码，再打开 Twitch 官方授权页批准。{data?.account ? '当前账号已连接。' : '当前尚未连接账号。'}</p><Button size="small" onClick={() => { setAuthorizationHelpOpen(false); showAccountConnection(); }}>前往账号连接</Button></li>
    <li><strong>关联游戏账号并核对活动资格</strong><p>打开 Twitch 活动页，展开对应活动，通过活动提供的「连接」入口关联自己的游戏账号。已经关联的账号无需重复操作。</p><a href="https://www.twitch.tv/drops/campaigns" target="_blank" rel="noreferrer">打开活动与关联入口 ↗</a></li>
    <li><strong>查看官方进度，再同步到服务</strong><p>在官方库存确认活动和观看进度，返回后点击「同步官方进度」。当前没有已确认可提供给普通观众的额外“进度读取授权”入口；已连接但接口不可读时，重复授权或填写自有 Client ID 不保证恢复。服务保留未知状态，不用本地计时推算奖励。</p><a href="https://www.twitch.tv/drops/inventory" target="_blank" rel="noreferrer">打开官方库存 ↗</a></li>
   </ol>
  </Modal>

  <Modal rootClassName={warStyle ? 'drops-war-thunder-overlay' : undefined} title="导入奖励图片" width={940} open={imagesOpen} onCancel={() => { if (!busy) setImagesOpen(false); }} destroyOnHidden footer={[
   <Button key="close" disabled={!!busy} onClick={() => setImagesOpen(false)}>关闭</Button>,
   <Button key="preview" loading={busy === 'image-preview'} disabled={!!busy || !imageInput.trim() || !imageScope} onClick={() => void inspectImages()}>预览匹配</Button>,
   <Button key="save" type="primary" loading={busy === 'image-import'} disabled={!!busy || !imagePreview?.canApply} onClick={() => void inspectImages(true)}>确认保存 {imagePreview?.matches.length || 0} 张</Button>,
  ]}>
   <div className="drops-image-import">
    <Alert type="info" showIcon title="补充 CDN 图片资料，不改变账号进度或奖励目标" />
    <label htmlFor="drops-image-scope">图片所属活动</label>
    <Select id="drops-image-scope" value={imageScope || undefined} disabled={!!busy} options={(data?.rewardImageScopes || []).map(scope => ({ value: scope.id, label: `${scope.gameName} · ${scope.name} · ${scope.rewardCount} 个奖励` }))} onChange={value => { setImageScope(value); setImagePreview(null); setImageError(''); }} />
    <details className="drops-image-extractor"><summary>如何从 Twitch 页面批量提取</summary><ol><li>在 Twitch 活动页展开目标活动。</li><li>复制下方脚本，在该页的开发者工具控制台运行。</li><li>结果已复制到剪贴板；显示 undefined 属正常返回，在下方粘贴即可。</li></ol><Button icon={<Copy size={14} />} onClick={async () => { try { await navigator.clipboard.writeText(imageExtractor); setImageScriptCopied(true); } catch { setImageError('复制失败，可直接选中下方脚本复制'); } }}>{imageScriptCopied ? '提取脚本已复制' : '复制提取脚本'}</Button><pre>{imageExtractor}</pre></details>
    {imagePreview ? <Button className="drops-image-edit-input" onClick={() => setImagePreview(null)}>修改原始图片资料</Button> : <>
    <label htmlFor="drops-image-input">图片 JSON 或 HTML</label>
    <Input.TextArea id="drops-image-input" value={imageInput} disabled={!!busy} rows={4} placeholder='[{"name":"奖励名称","imageUrl":"https://static-cdn.jtvnw.net/...png"}]' onChange={event => { setImageInput(event.target.value); setImagePreview(null); setImageError(''); }} />
    <p className="wt-admin-help">名称、已登记别名或图片附近文字用于匹配。其他游戏的图片会列为未匹配；同一奖励出现多个地址时需先处理冲突。</p>
    </>}
    {imageError && <Alert type="error" showIcon title={imageError} />}
    {imagePreview && <>
     <Alert type={imagePreview.canApply ? 'success' : 'warning'} showIcon title={`匹配 ${imagePreview.matches.length} 张 · 未匹配 ${imagePreview.unmatched.length} 张 · 无效 ${imagePreview.invalid.length} 项 · 冲突 ${imagePreview.conflicts.length} 项`} description={imagePreview.canApply ? '检查下面的奖励对应关系后确认保存，未匹配图片不会写入。' : '请补充准确名称或处理无效地址和冲突后重新预览。'} />
     <div className="drops-image-preview">{imagePreview.matches.map(match => <div key={match.rewardKey}><RewardImage src={match.imageUrl} name={match.name} /><div><strong>{match.name}</strong><small>{match.matchedBy === 'context' ? '附近文字匹配' : '名称或别名匹配'}</small><a href={match.imageUrl} target="_blank" rel="noreferrer">查看 CDN 原图 ↗</a></div></div>)}</div>
     {!!(imagePreview.unmatched.length + imagePreview.invalid.length + imagePreview.conflicts.length) && <details><summary>查看未匹配及冲突项目</summary><ul>{imagePreview.unmatched.map((item, index) => <li key={`u${index}`}>{item.name}：{item.reason}</li>)}{imagePreview.invalid.map(item => <li key={`i${item.index}`}>第 {item.index} 项：{item.reason}</li>)}{imagePreview.conflicts.map((item, index) => <li key={`c${index}`}>{item.name}：{item.reason}</li>)}</ul></details>}
    </>}
   </div>
  </Modal>

  <Drawer rootClassName={warStyle ? 'drops-war-thunder-overlay' : undefined} title="连接设置" width={460} open={settingsOpen} onClose={() => setSettingsOpen(false)} destroyOnHidden><Form form={settingsForm} layout="vertical" onFinish={async values => { if (await action('settings', '/settings', 'PATCH', { networkMode: values.networkMode, checkMinSeconds: Math.round(values.checkMinMinutes * 60), checkMaxSeconds: Math.round(values.checkMaxMinutes * 60) })) setSettingsOpen(false); }}><Form.Item name="networkMode" label="连接方式" rules={[{ required: true }]}><Select options={[{ value: 'auto', label: '直连优先，失败后使用配置的代理' }, { value: 'proxy', label: '仅使用配置的代理' }, { value: 'direct', label: '仅直连' }]} /></Form.Item><p className="wt-admin-help">空闲频道检查间隔（分钟）：在范围内重新随机选择。延长间隔会降低请求量，也会延迟发现开播；随机间隔不保证避免平台识别。</p><div className="drops-interval-grid"><Form.Item name="checkMinMinutes" label="最短间隔" rules={[{ required: true, type: 'number', min: 2, max: 180 }]}><InputNumber min={2} max={180} precision={0} /></Form.Item><Form.Item name="checkMaxMinutes" label="最长间隔" dependencies={['checkMinMinutes']} rules={[{ required: true, type: 'number', min: 2, max: 180 }, ({ getFieldValue }) => ({ validator: (_, value: number) => value >= Number(getFieldValue('checkMinMinutes')) ? Promise.resolve() : Promise.reject(new Error('最长间隔不能小于最短间隔')) })]}><InputNumber min={2} max={180} precision={0} /></Form.Item></div><p className="wt-admin-help">{data?.schedule.metadataSource === 'external' ? `官方资料同步由原生定时任务控制，当前相邻检查约 ${data.schedule.metadataMinSeconds / 60}～${data.schedule.metadataMaxSeconds / 60} 分钟。这里的设置只控制查询时段内的空闲频道检查。` : '独立模式下，官方资料同步也采用此随机区间。'}已知场次在开播前 5 分钟进入查询，非查询时段自动跳过对外请求。观看中的心跳继续按协议节奏运行。</p><Alert type="info" showIcon title="代理由部署环境配置" description="不切换运行环境的系统代理或自动选择其他节点。网络错误逐步延长重试；遇限流或验证暂停，不通过换线路重试。" /><Button className="drops-form-save" type="primary" htmlType="submit" loading={busy === 'settings'}>保存设置</Button></Form></Drawer>
  <Drawer rootClassName={warStyle ? 'drops-war-thunder-overlay' : undefined} title={editor === 'new' ? '添加游戏任务' : '游戏任务与奖励目标'} width={620} open={!!editor} onClose={() => setEditor(null)} destroyOnHidden><Form form={taskForm} layout="vertical" onFinish={async values => { const channels = values.channelsText.split(/[\s,，]+/).map(x => x.trim()).filter(Boolean); const goal = { mode: values.goalMode, reset: values.resetGoals, rewards: values.goalMode === 'selected' ? values.rewardKeys.map(key => { const [campaignId, dropId] = JSON.parse(key) as [string, string]; return { campaignId, dropId }; }) : [] }; const querySchedule = { mode: values.queryMode, sessions: values.queryMode === 'custom' ? parseSessions(values.sessionsText) : [] }; const body = { ...values, channels, goal, querySchedule }; const path = editor === 'new' ? '/tasks' : `/tasks/${(editor as Task).id}`; if (await action('task', path, editor === 'new' ? 'POST' : 'PUT', body)) setEditor(null); }}><Form.Item name="gameName" label="Twitch 游戏名称" rules={[{ required: true, max: 100 }]}><Input placeholder="例如 War Thunder" /></Form.Item><Form.Item name="goalMode" label="本轮奖励目标" rules={[{ required: true }]}><Select options={[{ value: 'all', label: '本轮全部奖励（锁定当前活动）' }, { value: 'selected', label: '只完成我选择的奖励' }]} /></Form.Item>{goalMode === 'selected' ? <Form.Item name="rewardKeys" label="选择活动与奖励" rules={[{ required: true, type: 'array', min: 1, message: '请至少选择一个奖励；没有资料时先同步官方进度' }]}><Select mode="multiple" options={rewardOptions} optionFilterProp="label" maxTagCount={3} placeholder="按活动选择奖励，可跨同一游戏的多个活动" notFoundContent="暂无官方奖励资料，请先同步" /></Form.Item> : <div className="drops-goal-help"><p>保存后锁定本轮可见活动的全部奖励。暂无资料时，首次获取完整活动资料后锁定；后续新活动不会自动加入。</p>{!!editingTask?.goal.rewards.length && <><p>已锁定 {editingTask.goal.rewards.length} 个奖励，保存普通设置会保留本轮目标。</p><Form.Item name="resetGoals" valuePropName="checked"><Checkbox>重新选择本轮全部奖励（替换已锁定目标）</Checkbox></Form.Item></>}</div>}<Alert type="info" showIcon title="官方确认达标即停止该目标，奖励仍由你手动领取" description="前置奖励需要领取时自动等待，其他可进行的目标继续执行；领取后点击同步或等待下次检查。官方进度未知或活动过期不会被判定为完成。" /><Form.Item name="priority" label="优先级（数值越大越先执行）" rules={[{ required: true }]}><InputNumber min={0} max={100} precision={0} /></Form.Item><Form.Item name="queryMode" label="直播查询时间" rules={[{ required: true }]}><Select options={[{value:'auto',label:'按已公布场次，开播前 5 分钟查询'}, {value:'custom',label:'自定义直播时间'}, {value:'always',label:'不限时间，按随机间隔查询'}]}/></Form.Item>{queryMode === 'custom' && <Form.Item name="sessionsText" label="开播时间（北京时间，每行一个）" rules={[{validator:async (_, value) => { parseSessions(value ?? ''); }}]}><Input.TextArea rows={4} maxLength={4000} placeholder="2026-10-25 21:50"/></Form.Item>}<p className="drops-goal-help">已知场次在开播前 5 分钟开始查询；预计开播后 1 小时仍未找到直播则等待下一场，已选中的直播会继续。自动模式没有场次资料时保留低频查询；临时加播可切换不限时间。</p><Form.Item name="channelsText" label="指定频道（每行一个频道名，最多 20 个）"><Input.TextArea rows={5} maxLength={600} placeholder="warthunder_esports" /></Form.Item><Form.Item name="enabled" valuePropName="checked"><Checkbox>启用此游戏任务</Checkbox></Form.Item><Form.Item name="autoChannels" valuePropName="checked"><Checkbox>自动寻找带掉宝标记的频道</Checkbox></Form.Item><Form.Item name="watchOnly" valuePropName="checked"><Checkbox>允许仅观看模式，由我在 Twitch 核对活动及频道资格</Checkbox></Form.Item><Alert type="warning" showIcon title="仅观看模式可能无法读取掉宝进度" description="请先确认游戏账号已关联、频道参与活动。服务只累计观看上报，不保证奖励获得；前置奖励可能需要先手动领取。" /><div className="drops-editor-actions"><Button type="primary" htmlType="submit" loading={busy === 'task'}>保存任务</Button>{editor && editor !== 'new' && <Button danger disabled={!!busy} onClick={async () => { if (await action('delete-task', `/tasks/${editor.id}`, 'DELETE')) setEditor(null); }}>删除任务</Button>}</div></Form></Drawer>
  <Modal rootClassName={warStyle ? 'drops-war-thunder-overlay' : undefined} title="解除 Twitch 连接" open={disconnectOpen} onCancel={() => setDisconnectOpen(false)} confirmLoading={busy === 'disconnect'} okText="解除连接" okButtonProps={{ danger: true }} onOk={async () => { if (await action('disconnect', '/auth/disconnect')) setDisconnectOpen(false); }}><p>停止所有挂机任务，清除当前部署保存的 Twitch 登录，并尝试撤销远端授权。游戏任务设置会保留。</p></Modal>
 </div></ConfigProvider>;
}
