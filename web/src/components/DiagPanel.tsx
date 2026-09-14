// 诊断面板（shadcn Dialog）：轮询 /api/diag，展示 LLM 统计与最近日志 tail（排障用）。
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { api, type DiagResp } from '../api';
import { useStore } from '../store';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';

const POLL_MS = 2000;

function levelOf(line: string): 'err' | 'warn' | 'dbg' | 'info' {
  if (line.includes('ERROR')) return 'err';
  if (line.includes('WARN')) return 'warn';
  if (line.includes('DEBUG')) return 'dbg';
  return 'info';
}

const LEVEL_COLOR = {
  err: 'text-rose-400',
  warn: 'text-amber-300',
  dbg: 'text-slate-600',
  info: 'text-slate-300',
} as const;

function fmtK(n: number): string {
  if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k';
  return String(n);
}

export default function DiagPanel() {
  const { s, set } = useStore();
  const [diag, setDiag] = useState<DiagResp | null>(null);
  const logRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!s.diagOpen) return;
    let alive = true;
    const tick = () =>
      api
        .getDiag()
        .then((d) => alive && setDiag(d))
        .catch(() => {});
    tick();
    const h = setInterval(tick, POLL_MS);
    return () => {
      alive = false;
      clearInterval(h);
    };
  }, [s.diagOpen]);

  // 新日志自动滚到底（用户往上翻时不打扰）
  useEffect(() => {
    const el = logRef.current;
    if (el && el.scrollHeight - el.scrollTop - el.clientHeight < 60) {
      el.scrollTop = el.scrollHeight;
    }
  }, [diag]);

  return (
    <Dialog open={s.diagOpen} onOpenChange={(v) => set((p) => ({ ...p, diagOpen: v }))}>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-[44rem]">
        <DialogHeader>
          <DialogTitle>🔧 诊断</DialogTitle>
        </DialogHeader>

        <div className="flex justify-end">
          <Button
            variant="outline"
            size="xs"
            onClick={() => {
              void navigator.clipboard
                .writeText(diag?.tail.join('\n') ?? '')
                .then(() => toast.success('日志已复制'))
                .catch(() => toast.error('复制失败'));
            }}
          >
            复制日志
          </Button>
        </div>

        {diag ? (
          <>
            <div className="mt-1 grid grid-cols-4 gap-2 text-center text-[13px]">
              <div className="rounded-lg bg-white/5 p-2">
                <div className="text-slate-500">调用</div>
                <div className="text-slate-200">{diag.stats.llm_calls}</div>
              </div>
              <div className="rounded-lg bg-white/5 p-2">
                <div className="text-slate-500">错误</div>
                <div className={diag.stats.llm_errors > 0 ? 'text-rose-400' : 'text-slate-200'}>
                  {diag.stats.llm_errors}
                </div>
              </div>
              <div className="rounded-lg bg-white/5 p-2">
                <div className="text-slate-500">tokens</div>
                <div className="text-slate-200">
                  ↑{fmtK(diag.stats.tokens_in)} ↓{fmtK(diag.stats.tokens_out)}
                </div>
              </div>
              <div className="rounded-lg bg-white/5 p-2">
                <div className="text-slate-500">DEBUG</div>
                <div className={diag.debug ? 'text-sky-400' : 'text-slate-500'}>{diag.debug ? '开' : '关'}</div>
              </div>
            </div>

            {diag.stats.last_error && (
              <div className="mt-2 rounded-lg border border-rose-400/30 bg-rose-950/40 px-3 py-1.5 text-xs leading-relaxed text-rose-200">
                最近错误：{diag.stats.last_error}
              </div>
            )}

            <div
              ref={logRef}
              className="thin-scroll mt-2 min-h-40 flex-1 overflow-y-auto rounded-lg bg-slate-950/80 p-2
                font-mono text-xs leading-relaxed"
            >
              {diag.tail.length === 0 ? (
                <div className="text-slate-600">暂无日志</div>
              ) : (
                diag.tail.map((line, i) => (
                  <div key={i} className={`${LEVEL_COLOR[levelOf(line)]} break-all whitespace-pre-wrap`}>
                    {line}
                  </div>
                ))
              )}
            </div>
          </>
        ) : (
          <div className="mt-6 text-sm text-slate-500">加载中…</div>
        )}
      </DialogContent>
    </Dialog>
  );
}
