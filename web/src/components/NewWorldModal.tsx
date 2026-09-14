// 新建世界模态（shadcn Dialog）：
// 两条路径——"AI 生成"（LLM 生成世界规格）与"默认世界"（零 LLM 直接铺设）。
// 世界就绪（world 帧）时由 App 关闭本模态。
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { api } from '../api';
import { useStore } from '../store';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Textarea } from '@/components/ui/textarea';

const PRESETS = [
  '一座靠山临水的渔村，村民朴实勤劳，村后有一片黑松林',
  '边境线上的屯垦小村，常受狼群袭扰，村民警觉而团结',
  '河谷沃野上的农业村落，正准备迎接第一个丰收节',
];

type Busy = '' | 'ai' | 'default';

export default function NewWorldModal() {
  const { s, set } = useStore();
  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState<Busy>('');
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const [elapsed, setElapsed] = useState(0);

  // 生成期：把系统/世界日志同步到状态栏
  useEffect(() => {
    if (!busy) return;
    const last = s.logs[s.logs.length - 1];
    if (last && (last.cat === 'system' || last.cat === 'world')) setStatus(last.text);
  }, [s.logs, busy]);

  // 生成计时（AI 路径可能等几十秒）
  useEffect(() => {
    if (busy !== 'ai') return;
    setElapsed(0);
    const h = window.setInterval(() => setElapsed((e) => e + 1), 1000);
    return () => window.clearInterval(h);
  }, [busy]);

  const start = async (useAI: boolean) => {
    if (busy) return;
    setError('');
    setBusy(useAI ? 'ai' : 'default');
    setStatus(useAI ? '正在构思土地与居民…（LLM 不可用会自动回退默认世界）' : '正在铺设默认世界…');
    if (useAI) set((p) => ({ ...p, generating: true }));
    try {
      await api.createWorld(useAI ? prompt.trim() : '', !useAI);
      // 成功路径：默认世界 world 帧几乎立即到达；AI 路径由 App 按 world 帧关闭模态
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setError(msg);
      setStatus('');
      setBusy('');
      set((p) => ({ ...p, generating: false }));
      toast.error('创建世界失败：' + msg);
    }
  };

  const cancel = () => {
    if (busy) return;
    set((p) => ({ ...p, newWorldOpen: false }));
  };

  return (
    <Dialog open={s.newWorldOpen} onOpenChange={(v) => !v && cancel()}>
      <DialogContent
        className="sm:max-w-[34rem]"
        onInteractOutside={(e) => busy && e.preventDefault()}
        onEscapeKeyDown={(e) => busy && e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>🏰 创建你的领地</DialogTitle>
          <DialogDescription className="leading-relaxed">
            用一句话描述你想要的世界：地理、气候、人情…… 也可以直接铺一片内置世界，不消耗任何 LLM 调用。
          </DialogDescription>
        </DialogHeader>

        <Textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          rows={3}
          disabled={!!busy}
          placeholder="例如：云雾山间的小村，村口有老槐树，猎户多，传闻山里有白鹿…"
          className="resize-none text-[13px]"
        />

        <div className="flex flex-wrap gap-1">
          {PRESETS.map((p) => (
            <button
              key={p}
              type="button"
              title={p}
              disabled={!!busy}
              onClick={() => setPrompt(p)}
              className="rounded-full border border-white/10 px-2 py-0.5 text-xs text-slate-400 hover:bg-white/10 hover:text-slate-200 disabled:opacity-50"
            >
              {p.slice(0, 12)}…
            </button>
          ))}
        </div>

        <div className="min-h-5 text-xs leading-relaxed">
          {error ? <span className="text-rose-400">{error}</span> : <span className="text-slate-500">{status}</span>}
        </div>

        <DialogFooter className="gap-2">
          {busy ? (
            <Button variant="outline" onClick={() => set((p) => ({ ...p, newWorldOpen: false }))}>
              后台继续，先关闭
            </Button>
          ) : (
            <Button variant="outline" onClick={cancel}>
              取消
            </Button>
          )}
          <Button variant="secondary" disabled={!!busy} onClick={() => void start(false)}>
            {busy === 'default' ? '铺设中…' : '跳过 AI（默认世界）'}
          </Button>
          <Button disabled={!!busy} onClick={() => void start(true)}>
            {busy === 'ai' ? `生成中 ${elapsed}s…` : '✨ AI 生成世界'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
