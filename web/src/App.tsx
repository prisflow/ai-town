// 根组件：装配 Pixi 画布、SSE、REST 初始状态、加载/断线/无世界兜底界面与各 UI 面板。
// 注意 StrictMode 下 effect 会执行两次：GameView/EventSource 都在清理函数中释放，可安全重挂。
import { useCallback, useEffect, useRef, useState } from 'react';
import { toast, Toaster } from 'sonner';
import { api, type ServerMsg } from './api';
import { GameView } from './game/engine';
import { appendChat, appendLog, chatToLog, eventToLog, useStore, worldMetas } from './store';
import { Button } from '@/components/ui/button';
import { TooltipProvider } from '@/components/ui/tooltip';
import Topbar from './components/Topbar';
import AgentPanel from './components/AgentPanel';
import LordConsole from './components/Console';
import SettingsModal from './components/SettingsModal';
import NewWorldModal from './components/NewWorldModal';
import DiagPanel from './components/DiagPanel';
import JobPanel from './components/JobPanel';

export default function App() {
  const { s, set } = useStore();
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const gameRef = useRef<GameView | null>(null);
  // 夜间快进转场：游戏天数跳变时黑屏淡入淡出，掩盖时间跳跃
  const [blackout, setBlackout] = useState(false);
  const lastDay = useRef(0);
  // 启动选择页："继续上次"载入中
  const [resuming, setResuming] = useState(false);
  // 快捷键回调需要读取最新 store（handler 只注册一次）
  const sRef = useRef(s);
  sRef.current = s;

  // ---------- 引擎启动（一次） ----------
  useEffect(() => {
    let disposed = false;
    const canvas = canvasRef.current!;
    const game = new GameView();
    void game.init(canvas).then(() => {
      if (!disposed) gameRef.current = game;
    });
    return () => {
      disposed = true;
      gameRef.current = null;
    };
  }, []);

  // ---------- 初始状态：有世界就恢复，没有就弹建向导 ----------
  const loadState = useCallback(async () => {
    set((p) => ({ ...p, loading: true, loadError: '' }));
    try {
      const state = await api.getState();
      if (state.world) {
        const metas = worldMetas(state.world);
        set((p) => ({
          ...p,
          loading: false,
          world: state.world,
          agentsMeta: metas,
          snap: state.snapshot ?? null,
          generating: !!state.generating,
          save: state.save ?? null,
        }));
        gameRef.current?.setWorld(state.world);
        if (state.snapshot) gameRef.current?.applySnapshot(state.snapshot);
      } else if (state.save?.exists) {
        // 有存档：展示"继续 / 新世界"选择页（不自动弹建世界向导）
        set((p) => ({ ...p, loading: false, save: state.save ?? null }));
      } else {
        set((p) => ({ ...p, loading: false, save: null, newWorldOpen: true }));
      }
    } catch (e) {
      set((p) => ({ ...p, loading: false, loadError: e instanceof Error ? e.message : String(e) }));
    }
  }, [set]);

  useEffect(() => {
    void loadState();
  }, [loadState]);

  // ---------- SSE ----------
  useEffect(() => {
    const es = new EventSource('/api/events');
    es.onopen = () => set((p) => ({ ...p, connected: true }));
    es.onerror = () => set((p) => ({ ...p, connected: false })); // EventSource 会自动重连
    es.onmessage = (ev) => {
      let msg: ServerMsg;
      try {
        msg = JSON.parse(ev.data) as ServerMsg;
      } catch {
        return;
      }
      switch (msg.t) {
        case 'snapshot': {
          // 夜间快进检测：游戏天数跳变 → 黑屏转场（约 0.9s）
          if (lastDay.current > 0 && msg.day > lastDay.current) {
            setBlackout(true);
            window.setTimeout(() => setBlackout(false), 900);
          }
          lastDay.current = msg.day;
          set((p) => ({ ...p, snap: msg }));
          gameRef.current?.applySnapshot(msg);
          break;
        }
        case 'world': {
          const metas = worldMetas(msg.world);
          set((p) => ({ ...p, world: msg.world, agentsMeta: metas, generating: false, newWorldOpen: false }));
          gameRef.current?.setWorld(msg.world); // setWorld 内部自动重建铭牌
          break;
        }
        case 'event':
          appendLog(set, eventToLog(msg));
          break;
        case 'chat':
          appendChat(set, chatToLog(msg));
          break;
        case 'bubble':
          gameRef.current?.bubble(msg.actor, msg.text, msg.secs);
          break;
        // edict 帧：专用于未来的指令 UI；日志由 event 帧（cat=edict）负责
      }
    };
    return () => es.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ---------- 画布点选村民 ----------
  useEffect(() => {
    const game = gameRef.current;
    if (!game) return;
    game.onSelect = (id) => set((p) => ({ ...p, selected: id }));
  }, [s.world]);

  // ---------- 快捷键：空格暂停（输入框聚焦/面板打开时忽略） ----------
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.code !== 'Space' || e.repeat) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable)) return;
      const cur = sRef.current;
      if (!cur.snap || cur.settingsOpen || cur.newWorldOpen || cur.diagOpen || cur.jobsOpen) return;
      e.preventDefault();
      const paused = !cur.snap.paused;
      api
        .setPaused(paused)
        .then(() => set((p) => ({ ...p, snap: p.snap ? { ...p.snap, paused } : p.snap })))
        .catch(() => void 0);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [set]);

  const retry = () => void loadState();

  // 启动选择页："继续上次"（POST /api/continue 后世界帧经 SSE 到达；再拉一次状态兜底）
  const resume = async () => {
    if (resuming) return;
    setResuming(true);
    try {
      await api.continueGame();
      await loadState();
    } catch (e) {
      toast.error('继续失败：' + (e instanceof Error ? e.message : String(e)));
      void loadState(); // 存档可能已失效：刷新选择页数据
    } finally {
      setResuming(false);
    }
  };

  const save = s.save;
  const showSavePicker = !s.loading && !s.loadError && !s.world && !s.newWorldOpen && !!save?.exists;

  return (
    <TooltipProvider delayDuration={300}>
      <div className="fixed inset-0">
        <canvas ref={canvasRef} className="absolute inset-0 h-full w-full" />
        <Topbar />
        <AgentPanel />
        <LordConsole />
        {s.settingsOpen && <SettingsModal />}
        {s.newWorldOpen && <NewWorldModal />}
        {s.diagOpen && <DiagPanel />}
        {s.jobsOpen && <JobPanel />}

        {/* 断线提示条 */}
        {s.world && !s.connected && (
          <div className="pointer-events-none absolute inset-x-0 top-11 z-30 bg-amber-950/80 py-0.5 text-center text-xs text-amber-300">
            与后端的实时连接断开，正在自动重连…（界面数据可能暂停更新）
          </div>
        )}

        {/* 首屏加载 / 加载失败 / 存档选择页 / 无世界兜底 */}
        {s.loading && <CenterOverlay title="正在连接后端…" desc="首次启动可能需要几秒" />}
        {!s.loading && s.loadError && (
          <CenterOverlay title="无法连接后端" desc={s.loadError}>
            <Button onClick={retry}>重试</Button>
          </CenterOverlay>
        )}
        {showSavePicker && (
          <CenterOverlay
            title="欢迎回来"
            desc={`上次进度：${save!.world_name} · 第 ${save!.day} 天${
              save!.saved_at ? ` · ${new Date(save!.saved_at).toLocaleString()}` : ''
            }（开新世界会覆盖这份存档）`}
          >
            <Button onClick={() => void resume()} disabled={resuming}>
              {resuming ? '载入中…' : '▶ 继续上次'}
            </Button>
            <Button variant="outline" disabled={resuming} onClick={() => set((p) => ({ ...p, newWorldOpen: true }))}>
              🏰 开新世界
            </Button>
          </CenterOverlay>
        )}
        {!s.loading && !s.loadError && !s.world && !s.newWorldOpen && !save?.exists && (
          <CenterOverlay title="还没有领地" desc="创建一片属于你的领地，开始观察村民的生活">
            <Button onClick={() => set((p) => ({ ...p, newWorldOpen: true }))}>🏰 创建世界</Button>
          </CenterOverlay>
        )}

        {/* 夜间快进转场：黑屏淡入淡出 */}
        <div
          className={`pointer-events-none absolute inset-0 z-50 bg-black transition-opacity duration-700 ${
            blackout ? 'opacity-100' : 'opacity-0'
          }`}
        />

        <Toaster theme="dark" position="top-center" richColors closeButton />
      </div>
    </TooltipProvider>
  );
}

/** 居中覆盖层：加载 / 失败重试 / 无世界引导。 */
function CenterOverlay({ title, desc, children }: { title: string; desc?: string; children?: React.ReactNode }) {
  return (
    <div className="absolute inset-0 z-40 flex items-center justify-center bg-background/80 backdrop-blur-sm">
      <div className="flex w-[26rem] max-w-full flex-col items-center gap-2 rounded-2xl border border-border bg-card p-6 text-center shadow-xl">
        <div className="text-lg font-semibold text-foreground">{title}</div>
        {desc && <div className="text-xs leading-relaxed text-muted-foreground">{desc}</div>}
        {children && <div className="mt-2 flex gap-2">{children}</div>}
      </div>
    </div>
  );
}
