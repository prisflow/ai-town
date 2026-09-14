// 领主控制台：指令输入 + 快捷指令 + 事件/对话 双页日志（过滤/清空/智能滚动）。
import { useEffect, useMemo, useRef, useState } from 'react';
import { toast } from 'sonner';
import { api, fmtTime } from '../api';
import { useStore } from '../store';
import { Button } from '@/components/ui/button';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';

const CHIPS = ['多储备一些木材', '囤积食物准备过冬', '建造一座新民居', '建一座粮仓', '开垦一片农田'];

const CAT_COLOR: Record<string, string> = {
  system: 'text-slate-500',
  world: 'text-emerald-400',
  edict: 'text-amber-300',
  chat: 'text-sky-300',
  plan: 'text-violet-400',
  job: 'text-lime-400',
  build: 'text-orange-300',
  social: 'text-pink-300',
};

const CAT_OPTIONS: Array<[string, string]> = [
  ['all', '全部'],
  ['edict', '领主指令'],
  ['job', '工作'],
  ['build', '建设'],
  ['world', '世界'],
  ['social', '社交'],
  ['system', '系统'],
];

export default function LordConsole() {
  const { s, set } = useStore();
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const [filter, setFilter] = useState('all');
  const [tab, setTab] = useState('events');
  const logRef = useRef<HTMLDivElement>(null);
  const stick = useRef(true); // 是否吸附底部（用户往上翻时不再打扰）

  const lines = useMemo(() => {
    if (tab === 'chats') return s.chats;
    return filter === 'all' ? s.logs : s.logs.filter((l) => l.cat === filter);
  }, [tab, filter, s.logs, s.chats]);

  // 智能滚动：仅当用户本来就在底部时才跟随新日志
  useEffect(() => {
    const el = logRef.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [lines]);

  const onScroll = () => {
    const el = logRef.current;
    if (!el) return;
    stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60;
  };

  const send = async (raw?: string) => {
    const t = (raw ?? text).trim();
    if (!t) return;
    if (!s.world) {
      toast.error('请先创建世界');
      return;
    }
    setSending(true);
    try {
      await api.sendEdict(t);
      setText('');
    } catch (err) {
      toast.error('指令发送失败：' + (err instanceof Error ? err.message : String(err)));
    } finally {
      setSending(false);
    }
  };

  const clearCurrent = () => {
    if (tab === 'chats') set((p) => ({ ...p, chats: [] }));
    else set((p) => ({ ...p, logs: [] }));
    stick.current = true;
  };

  return (
    <section
      className="pointer-events-auto absolute bottom-3 right-3 z-30 flex w-[26rem] max-w-[calc(100vw-17rem)]
        flex-col gap-2 rounded-xl border border-border bg-background/85 p-3 backdrop-blur"
    >
      <div className="flex items-center gap-2">
        <div className="text-sm font-semibold text-foreground">领主控制台</div>
        <span className="flex-1" />
        {tab === 'events' && (
          <select
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="rounded-md border border-input bg-transparent px-1.5 py-0.5 text-xs text-muted-foreground outline-none"
          >
            {CAT_OPTIONS.map(([v, label]) => (
              <option key={v} value={v} className="bg-background">
                {label}
              </option>
            ))}
          </select>
        )}
        <Button variant="ghost" size="xs" onClick={clearCurrent}>
          清空
        </Button>
      </div>

      <div className="flex items-end gap-2">
        <Textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault();
              void send();
            }
          }}
          rows={2}
          placeholder="向全领地下达指令，例如：多储备木材与食物，准备过冬…"
          className="resize-none text-[13px]"
        />
        <Button disabled={sending} onClick={() => void send()}>
          {sending ? '…' : '下达'}
        </Button>
      </div>

      <div className="flex flex-wrap gap-1">
        {CHIPS.map((c) => (
          <button
            key={c}
            type="button"
            onClick={() => void send(c)}
            className="rounded-full border border-white/10 px-2 py-0.5 text-xs text-slate-400 hover:bg-white/10 hover:text-slate-200"
          >
            {c}
          </button>
        ))}
      </div>

      <Tabs value={tab} onValueChange={(v) => { setTab(v); stick.current = true; }} className="gap-1">
        <TabsList className="h-7 w-full">
          <TabsTrigger value="events" className="text-xs">
            事件
          </TabsTrigger>
          <TabsTrigger value="chats" className="text-xs">
            对话
          </TabsTrigger>
        </TabsList>
        <TabsContent value="events" className="m-0">
          <LogView lines={lines} logRef={logRef} onScroll={onScroll} empty="暂无事件" />
        </TabsContent>
        <TabsContent value="chats" className="m-0">
          <LogView lines={lines} logRef={logRef} onScroll={onScroll} empty="还没有人聊过天" />
        </TabsContent>
      </Tabs>
    </section>
  );
}

function LogView({
  lines,
  logRef,
  onScroll,
  empty,
}: {
  lines: { day: number; hour: number; cat: string; text: string }[];
  logRef: React.RefObject<HTMLDivElement | null>;
  onScroll: () => void;
  empty: string;
}) {
  return (
    <div ref={logRef} onScroll={onScroll} className="thin-scroll h-44 overflow-y-auto pr-1 text-[13px] leading-relaxed">
      {lines.length === 0 && <div className="text-slate-600">{empty}</div>}
      {lines.map((line, i) => (
        <div key={i} className="flex gap-2">
          <span className="shrink-0 tabular-nums text-slate-600">
            第{line.day}天 {fmtTime(line.hour)}
          </span>
          <span className={CAT_COLOR[line.cat] ?? 'text-slate-300'}>{line.text}</span>
        </div>
      ))}
    </div>
  );
}
