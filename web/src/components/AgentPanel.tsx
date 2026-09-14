// 左侧居民面板：列表 + 详情（官员徽章 / 任命 / 放逐确认）。
import { useState } from 'react';
import { toast } from 'sonner';
import { api, ROLE_LABEL } from '../api';
import { useStore } from '../store';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog';

const STATE_LABEL: Record<string, string> = {
  idle: '空闲', moving: '移动中', working: '劳作', talking: '交谈', sleeping: '睡觉',
};

export default function AgentPanel() {
  const { s, set } = useStore();
  const [pending, setPending] = useState(false);
  const selId = s.selected;
  const selMeta = s.agentsMeta.get(selId);
  const selView = s.snap?.agents.find((a) => a.id === selId);

  const toggleOfficial = async () => {
    if (!selMeta || pending) return;
    setPending(true);
    const appoint = selMeta.tier !== 1;
    try {
      await api.appointOfficial(selMeta.id, appoint);
      toast.success(`${selMeta.name} ${appoint ? '已任命为官员' : '已被罢免'}`);
    } catch (e) {
      toast.error('操作失败：' + (e instanceof Error ? e.message : String(e)));
    } finally {
      setPending(false);
    }
  };

  const doExile = async () => {
    if (!selMeta || pending) return;
    setPending(true);
    try {
      await api.exile(selMeta.id);
      toast.success(`${selMeta.name} 已被放逐`);
      set((p) => ({ ...p, selected: '' }));
    } catch (e) {
      toast.error('放逐失败：' + (e instanceof Error ? e.message : String(e)));
    } finally {
      setPending(false);
    }
  };

  return (
    <aside className="pointer-events-auto absolute left-3 top-14 z-30 flex max-h-[calc(100%-8.5rem)] w-60 flex-col gap-2
      rounded-xl border border-border bg-background/85 p-3 backdrop-blur">
      <h3 className="text-sm font-semibold text-foreground">居民</h3>

      <div className="thin-scroll flex flex-col gap-1 overflow-y-auto">
        {[...s.agentsMeta.values()].map((meta) => {
          const view = s.snap?.agents.find((a) => a.id === meta.id);
          const selected = meta.id === selId;
          // 行动文本优先于物理状态：workflow 间隙物理空闲但确实在"干活流程中"
          const stateTxt = view ? view.action || STATE_LABEL[view.state] || view.state || '空闲' : '…';
          const carry = view && view.carrying > 0 ? ` · 背包${view.carrying}` : '';
          return (
            <button
              key={meta.id}
              type="button"
              onClick={() => set((p) => ({ ...p, selected: p.selected === meta.id ? '' : meta.id }))}
              className={`rounded-lg px-2 py-1.5 text-left transition-colors ${
                selected ? 'bg-sky-500/20 ring-1 ring-sky-400/60' : 'hover:bg-white/5'
              }`}
            >
              <div className="flex items-center gap-1 text-[13px] text-slate-200">
                <span className="truncate">
                  {meta.name} · {ROLE_LABEL[meta.role] ?? meta.role}
                </span>
                {meta.tier === 1 && <Badge className="bg-amber-500/20 px-1 text-[10px] text-amber-300">官</Badge>}
              </div>
              <div className="truncate text-xs text-slate-500">
                {view ? `${stateTxt}${carry}` : '…'}
              </div>
            </button>
          );
        })}
      </div>

      <div className="thin-scroll mt-1 overflow-y-auto rounded-lg bg-white/5 p-2 text-[13px] leading-relaxed text-slate-400">
        {!selMeta ? (
          '点击居民查看详情（也可以直接点地图上的村民）'
        ) : (
          <>
            <div className="font-medium text-slate-200">
              {selMeta.name}（{ROLE_LABEL[selMeta.role] ?? selMeta.role}）
              {selMeta.tier === 1 && (
                <span className="ml-1 rounded bg-amber-500/20 px-1 text-xs text-amber-300">官员</span>
              )}
            </div>
            <div className="mt-1">{selMeta.personality}</div>
            {selMeta.traits && selMeta.traits.length > 0 && (
              <div className="mt-1 text-sky-400">特质：{selMeta.traits.join('、')}</div>
            )}
            {selView && (
              <div className="mt-1">
                正在：{selView.action || STATE_LABEL[selView.state] || '空闲'}
                {selView.carrying > 0 ? `（背包 ${selView.carrying}）` : ''}
              </div>
            )}
            <div className="mt-2 flex gap-1">
              <Button variant="outline" size="xs" disabled={pending} onClick={() => void toggleOfficial()}>
                {selMeta.tier === 1 ? '罢免' : '任命官员'}
              </Button>
              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <Button variant="destructive" size="xs" disabled={pending}>
                    放逐
                  </Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>放逐 {selMeta.name}？</AlertDialogTitle>
                    <AlertDialogDescription>
                      被放逐的村民会收拾行李永久离开领地，此操作不可撤销。
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>取消</AlertDialogCancel>
                    <AlertDialogAction onClick={() => void doExile()}>确认放逐</AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            </div>
          </>
        )}
      </div>
    </aside>
  );
}
