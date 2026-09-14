// 工作单看板（只读）：公告栏视图——谁发布的、什么状态、谁在干。
// 数据来自 250ms 快照的 jobs（含 issued_by_name 署名）。
import { useStore } from '../store';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Badge } from '@/components/ui/badge';
import { Separator } from '@/components/ui/separator';

const STATUS_META: Record<string, { label: string; cls: string }> = {
  pending: { label: '待认领', cls: 'bg-amber-500/15 text-amber-300 border-amber-400/20' },
  claimed: { label: '进行中', cls: 'bg-sky-500/15 text-sky-300 border-sky-400/20' },
  failed: { label: '已撤销', cls: 'bg-rose-500/15 text-rose-300 border-rose-400/20' },
  done: { label: '已完成', cls: 'bg-emerald-500/15 text-emerald-300 border-emerald-400/20' },
};

const ORDER: Record<string, number> = { claimed: 0, pending: 1, failed: 2, done: 3 };

export default function JobPanel() {
  const { s, set } = useStore();
  const jobs = [...(s.snap?.jobs ?? [])].sort((a, b) => (ORDER[a.status] ?? 9) - (ORDER[b.status] ?? 9));

  return (
    <Dialog open={s.jobsOpen} onOpenChange={(v) => set((p) => ({ ...p, jobsOpen: v }))}>
      <DialogContent className="sm:max-w-[38rem]">
        <DialogHeader>
          <DialogTitle>📋 领地公告栏</DialogTitle>
          <DialogDescription>
            官员与领主发布的工作单：谁发的、谁在干，一目了然（官员会在思考时自行安排领地上的活计）。
          </DialogDescription>
        </DialogHeader>

        <div className="thin-scroll -mx-1 max-h-[60vh] overflow-y-auto px-1">
          {jobs.length === 0 && <div className="py-8 text-center text-sm text-muted-foreground">公告栏空空如也</div>}
          {jobs.map((j, i) => {
            const meta = STATUS_META[j.status] ?? { label: j.status, cls: '' };
            const claimer = j.claimed_by ? s.agentsMeta.get(j.claimed_by)?.name ?? j.claimed_by : '';
            return (
              <div key={j.id}>
                {i > 0 && <Separator className="my-2 opacity-50" />}
                <div className="flex items-center gap-2">
                  <span className="text-sm text-foreground">{j.title}</span>
                  {j.priority >= 4 && <span className="text-xs text-rose-400">紧急</span>}
                  <span className="flex-1" />
                  <Badge className={meta.cls}>{meta.label}</Badge>
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  发布：{j.issued_by_name || '领地'}
                  {claimer && <> · 认领：{claimer}</>}
                  {j.note && <> · {j.note}</>}
                </div>
              </div>
            );
          })}
        </div>
      </DialogContent>
    </Dialog>
  );
}
