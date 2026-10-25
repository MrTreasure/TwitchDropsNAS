import { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Alert, Button, ConfigProvider, Form, Input, Spin } from 'antd';
import TwitchDrops from './TwitchDrops';
import './shell.css';

function App() {
 const [authenticated, setAuthenticated] = useState<boolean | null>(null), [error, setError] = useState(''), [busy, setBusy] = useState(false);
 useEffect(() => { const controller = new AbortController(); void fetch('/api/session', { signal: controller.signal }).then(r => setAuthenticated(r.ok)).catch(() => { if (!controller.signal.aborted) { setError('无法连接后端服务'); setAuthenticated(false); } }); return () => controller.abort(); }, []);
 return <ConfigProvider theme={{ token: { colorPrimary: '#526e5b', borderRadius: 8, fontFamily: 'system-ui, sans-serif' } }}><main className="standalone-shell"><header><div><h1>TwitchDropsNAS</h1><p>低流量挂机 · 多游戏目标 · 手动领取</p></div>{authenticated && <Button onClick={async () => { const r = await fetch('/api/session', { method: 'DELETE' }); if (r.ok) setAuthenticated(false); else setError('退出失败，请重试'); }}>退出管理界面</Button>}</header>{error && <Alert type="error" title={error} showIcon />}{authenticated === null ? <Spin /> : authenticated ? <TwitchDrops /> : <section className="login-card"><h2>登录管理界面</h2><p>输入部署时设置的管理令牌。Twitch 账号在登录后单独授权。</p><Form layout="vertical" onFinish={async ({ token }) => { setBusy(true); setError(''); try { const r = await fetch('/api/session', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token }) }); if (!r.ok) throw new Error('管理令牌无效'); setAuthenticated(true); } catch (e) { setError(e instanceof Error ? e.message : '登录失败'); } finally { setBusy(false); } }}><Form.Item name="token" label="管理令牌" rules={[{ required: true }]}><Input.Password autoComplete="current-password" /></Form.Item><Button type="primary" htmlType="submit" loading={busy}>登录</Button></Form><p>未设置 TDM_ADMIN_TOKEN 时，首次启动会在持久化数据目录生成 admin-token 文件。</p></section>}</main></ConfigProvider>;
}
createRoot(document.getElementById('root')!).render(<App />);
