// 顶栏：世界名/时间/暂停/库存/token 仪表/工作单看板/诊断/设置入口。
import { toast } from 'sonner';
import { api, fmtTime, RES_LABEL } from '../api';
import { useStore } from '../store';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

const SEASON_ICON: Record<string, string> = { '春': '🌸', '夏': '☀', '秋': '🍂', '冬': '❄' };

function fmtK(n: number): string {
  if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k';
  return String(n);
}

/** 图标按钮 + Tooltip 的固定包装。 */
function TipButton({
  tip,
  onClick,
  disabled,
  children,
}: {
  tip: string;
  onClick?: () => void;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="outline" size="icon-sm" onClick={onClick} disabled={disabled}>
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{tip}</TooltipContent>
    </Tooltip>
  );
}

export default function Topbar() {
  const { s, set } = useStore();

  const togglePause = async () => {
    if (!s.snap) return;
    const paused = !s.snap.paused;
    try {
      await api.setPaused(paused);
      set((p) => ({ ...p, snap: p.snap ? { ...p.snap, paused } : p.snap }));
    } catch (e) {
      toast.error('暂停失败：' + (e instanceof Error ? e.message : String(e)));
    }
  };

  const saveGame = async () => {
    try {
      await api.saveNow();
      toast.success('已存档');
    } catch (e) {
      toast.error('存档失败：' + (e instanceof Error ? e.message : String(e)));
    }
  };

  const d = s.snap?.domain;
  const st = s.snap?.stats;
  const openJobs = s.snap?.jobs.filter((j) => j.status === 'pending' || j.status === 'claimed').length ?? 0;

  return (
    <header className="pointer-events-auto absolute inset-x-0 top-0 z-30 flex h-11 items-center gap-3 border-b border-border bg-background/80 px-3 backdrop-blur">
      <span className="font-semibold tracking-wide text-foreground">
        🏰 {s.world?.name ?? '未命名领地'}
      </span>
      {s.snap && (
        <span className="text-sm text-muted-foreground">
          [{SEASON_ICON[s.snap.season] ?? s.snap.season} {s.snap.season}] 第 {s.snap.day} 天 {fmtTime(s.snap.hour)}
        </span>
      )}
      <Tooltip>
        <TooltipTrigger asChild>
          <span
            className={`inline-block h-2 w-2 cursor-default rounded-full ${s.connected ? 'bg-emerald-400' : 'bg-rose-500'}`}
          />
        </TooltipTrigger>
        <TooltipContent>{s.connected ? '实时连接正常' : '与后端断开，正在自动重连…'}</TooltipContent>
      </Tooltip>
      <TipButton tip={s.snap?.paused ? '继续时间' : '暂停时间'} onClick={() => void togglePause()} disabled={!s.snap}>
        {s.snap?.paused ? '▶' : '⏸'}
      </TipButton>
      <TipButton tip="手动存档（游戏每天与退出时也会自动存档）" onClick={() => void saveGame()} disabled={!s.world}>
        💾
      </TipButton>

      {d && (
        <span className="flex items-center gap-2.5 text-[13px]">
          {Object.entries(RES_LABEL).map(([k, label]) => (
            <span key={k}>
              <span className="text-slate-500">{label}</span>{' '}
              <span className="tabular-nums text-slate-200">{d[k as keyof typeof d] ?? 0}</span>
            </span>
          ))}
        </span>
      )}

      <span className="flex-1" />

      {s.generating && <span className="animate-pulse text-xs text-sky-400">世界生成中…</span>}

      {st && (
        <span className="text-xs text-muted-foreground">
          调用 {fmtK(st.llm_calls)} · 错
          <span
            className={st.llm_errors > 0 ? 'cursor-pointer font-medium text-rose-400' : ''}
            title={st.last_error ? `最近错误：${st.last_error}` : '无错误'}
            onClick={() => st.llm_errors > 0 && set((p) => ({ ...p, diagOpen: true }))}
          >
            {st.llm_errors}
          </span>{' '}
          · ↑{fmtK(st.tokens_in)} ↓{fmtK(st.tokens_out)}
          {st.last_latency_ms > 0 ? ` · ${st.last_latency_ms}ms` : ''}
        </span>
      )}

      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="outline" size="sm" onClick={() => set((p) => ({ ...p, jobsOpen: true }))}>
            📋 工作单
            {openJobs > 0 && (
              <Badge variant="secondary" className="px-1 text-[10px]">
                {openJobs}
              </Badge>
            )}
          </Button>
        </TooltipTrigger>
        <TooltipContent>查看公告栏：谁发布了什么、谁在干</TooltipContent>
      </Tooltip>
      <TipButton tip="诊断面板：LLM 统计与最近日志" onClick={() => set((p) => ({ ...p, diagOpen: true }))}>
        🔧
      </TipButton>
      <TipButton tip="设置：提供商/角色路由/额度/创意度/节奏" onClick={() => set((p) => ({ ...p, settingsOpen: true }))}>
        ⚙
      </TipButton>
    </header>
  );
}
