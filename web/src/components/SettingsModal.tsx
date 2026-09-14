// 设置模态（shadcn Dialog + Tabs）：BYOK 提供商、角色路由、额度与创意度、节奏、诊断。
// 保存后立即生效（热更新）；密钥只存本地 data/config.json。
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { api, type Config, type ProviderConfig } from '../api';
import { useStore } from '../store';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';

const ROLE_LABEL: Record<string, string> = {
  worldgen: '世界初始化（建议强模型）',
  planner: '领主指令规划（建议强模型）',
  brain: '村民自主决策（高频小请求，便宜模型）',
  dialogue: '村民对话（可用便宜模型）',
  choice: '决策分支（便宜模型）',
  narrate: '旁白台词（便宜模型）',
};

// 各角色 temperature 的内置默认（留空 = 用它）
const TEMP_DEFAULT: Record<string, number> = {
  worldgen: 0.9, planner: 0.3, brain: 0.6, dialogue: 0.95, choice: 0.5, narrate: 0.9,
};

// 节奏参数默认（与后端 config.DefaultPacing 一致）
const DEFAULT_PACING = {
  think_cooldown_sec: 5, chat_cooldown_sec: 150, chat_rounds: 3, event_chance: 10,
  startup_officials: 2, night_start: 21.5, night_end: 6, day_break: 7,
};

interface TestState { status: 'idle' | 'testing' | 'ok' | 'bad'; msg: string }

/** 一行「标签 + 控件」的栅格条目。 */
function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[1fr_9.5rem] items-center gap-2 text-[13px]">
      <div>
        <Label className="text-slate-400">{label}</Label>
        {hint && <div className="text-[11px] leading-tight text-slate-600">{hint}</div>}
      </div>
      {children}
    </div>
  );
}

function SectionHint({ children }: { children: React.ReactNode }) {
  return <p className="text-xs leading-relaxed text-slate-600">{children}</p>;
}

export default function SettingsModal() {
  const { s, set } = useStore();
  const [providers, setProviders] = useState<Record<string, ProviderConfig>>({});
  const [roles, setRoles] = useState<Record<string, string>>({});
  const [limits, setLimits] = useState({ max_concurrent: 3, rpm: 60, official_cap: 2, conv_cap: 2, llm_timeout_sec: 120 });
  const [temperatures, setTemperatures] = useState<Record<string, number>>({});
  const [pacing, setPacing] = useState({ ...DEFAULT_PACING });
  const [debug, setDebug] = useState(false);
  const [tests, setTests] = useState<Record<string, TestState>>({});
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!s.settingsOpen) return;
    void (async () => {
      try {
        const cfg = await api.getConfig();
        setProviders(cfg.providers ?? {});
        setRoles(cfg.roles ?? {});
        setLimits(cfg.limits);
        setTemperatures(cfg.limits?.temperatures ?? {});
        setPacing({ ...DEFAULT_PACING, ...(cfg.pacing ?? {}) });
        setDebug(cfg.debug ?? false);
      } catch (e) {
        toast.error('读取配置失败：' + (e instanceof Error ? e.message : String(e)));
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s.settingsOpen]);

  const patchProvider = (name: string, patch: Partial<ProviderConfig>) =>
    setProviders((p) => ({ ...p, [name]: { ...p[name], ...patch } }));

  const setTemp = (role: string, v: string) => {
    setTemperatures((t) => {
      const n = Number(v);
      const next = { ...t };
      if (!v.trim() || !isFinite(n) || n <= 0) delete next[role];
      else next[role] = Math.min(2, n);
      return next;
    });
  };

  const collect = (): Config => ({
    providers,
    roles: Object.fromEntries(Object.keys(ROLE_LABEL).map((r) => [r, roles[r] || 'default'])),
    limits: {
      max_concurrent: Math.max(1, limits.max_concurrent || 3),
      rpm: Math.max(1, limits.rpm || 60),
      official_cap: Math.max(1, limits.official_cap || 2),
      conv_cap: Math.max(1, limits.conv_cap || 2),
      llm_timeout_sec: Math.max(10, limits.llm_timeout_sec || 120),
      temperatures,
    },
    pacing: {
      ...pacing,
      think_cooldown_sec: Math.max(1, pacing.think_cooldown_sec || 5),
      chat_cooldown_sec: Math.max(10, pacing.chat_cooldown_sec || 150),
      chat_rounds: Math.min(4, Math.max(1, pacing.chat_rounds || 3)),
      event_chance: Math.min(100, Math.max(0, pacing.event_chance)),
      startup_officials: Math.min(8, Math.max(0, pacing.startup_officials)),
    },
    debug,
  });

  const save = async (silent = false): Promise<boolean> => {
    setSaving(true);
    try {
      await api.saveConfig(collect());
      if (!silent) {
        toast.success('配置已保存并生效');
        set((p) => ({ ...p, settingsOpen: false }));
      }
      return true;
    } catch (e) {
      toast.error('保存失败：' + (e instanceof Error ? e.message : String(e)));
      return false;
    } finally {
      setSaving(false);
    }
  };

  const testProvider = async (name: string) => {
    setTests((t) => ({ ...t, [name]: { status: 'testing', msg: '测试中…' } }));
    await save(true); // 先保存当前表单再测试
    try {
      const r = await api.testProvider(name);
      setTests((t) => ({
        ...t,
        [name]: r.ok
          ? { status: 'ok', msg: `✓ ${r.latency_ms}ms` }
          : { status: 'bad', msg: `✗ ${r.error ?? '失败'}` },
      }));
    } catch (e) {
      setTests((t) => ({ ...t, [name]: { status: 'bad', msg: `✗ ${e instanceof Error ? e.message : String(e)}` } }));
    }
  };

  const addProvider = () => {
    let i = 1;
    while (providers[`provider${i}`]) i++;
    setProviders((p) => ({ ...p, [`provider${i}`]: { type: 'openai', base_url: '', api_key: '', model: '' } }));
  };

  const providerNames = Object.keys(providers);

  return (
    <Dialog open={s.settingsOpen} onOpenChange={(v) => set((p) => ({ ...p, settingsOpen: v }))}>
      <DialogContent className="flex max-h-[90vh] flex-col sm:max-w-[40rem]">
        <DialogHeader>
          <DialogTitle>设置 · BYOK 接入</DialogTitle>
          <DialogDescription className="leading-relaxed">
            所有请求从本机直接发给你配置的提供商，密钥只保存在本地 data/config.json。
            OpenAI 兼容可填 DeepSeek / Moonshot / Qwen / 智谱 / OpenRouter / Ollama 等。修改保存后立即生效。
          </DialogDescription>
        </DialogHeader>

        <Tabs defaultValue="providers" className="min-h-0 flex-1 gap-2">
          <TabsList className="w-full">
            <TabsTrigger value="providers" className="text-xs">模型接入</TabsTrigger>
            <TabsTrigger value="routing" className="text-xs">路由与额度</TabsTrigger>
            <TabsTrigger value="pacing" className="text-xs">节奏</TabsTrigger>
            <TabsTrigger value="debug" className="text-xs">诊断</TabsTrigger>
          </TabsList>

          <div className="thin-scroll min-h-0 flex-1 overflow-y-auto pr-1">
            {/* ---------- 模型接入 ---------- */}
            <TabsContent value="providers" className="flex flex-col gap-3">
              {Object.entries(providers).map(([name, p]) => {
                const t = tests[name] ?? { status: 'idle' as const, msg: '' };
                return (
                  <div key={name} className="rounded-xl border border-border bg-white/5 p-3">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium text-slate-200">提供商：{name}</span>
                      <span className="flex-1" />
                      <Button variant="outline" size="xs" onClick={() => void testProvider(name)}>
                        测试连接
                      </Button>
                      <span
                        className={
                          t.status === 'ok'
                            ? 'text-xs text-emerald-400'
                            : t.status === 'bad'
                              ? 'text-xs text-rose-400'
                              : 'text-xs text-slate-500'
                        }
                      >
                        {t.msg}
                      </span>
                      <Button
                        variant="ghost"
                        size="xs"
                        className="text-rose-300"
                        onClick={() =>
                          setProviders((prev) => {
                            const next = { ...prev };
                            delete next[name];
                            return next;
                          })
                        }
                      >
                        删除
                      </Button>
                    </div>
                    <div className="mt-2 flex flex-col gap-2">
                      <Field label="类型" hint="openai＝任意兼容接口；anthropic＝Claude 原生；mock＝离线演示">
                        <Select value={p.type ?? 'openai'} onValueChange={(v) => patchProvider(name, { type: v })}>
                          <SelectTrigger className="w-full">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {['openai', 'anthropic', 'mock'].map((v) => (
                              <SelectItem key={v} value={v}>
                                {v}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Field>
                      <Field label="Base URL" hint="如 https://api.deepseek.com/v1">
                        <Input
                          value={p.base_url ?? ''}
                          onChange={(e) => patchProvider(name, { base_url: e.target.value })}
                          placeholder="https://api.deepseek.com/v1"
                        />
                      </Field>
                      <Field label="API Key" hint="只存本地，不回传任何第三方">
                        <Input
                          type="password"
                          value={p.api_key ?? ''}
                          onChange={(e) => patchProvider(name, { api_key: e.target.value })}
                          placeholder="sk-..."
                        />
                      </Field>
                      <Field label="模型" hint="如 deepseek-chat / claude-sonnet-4-5">
                        <Input
                          value={p.model ?? ''}
                          onChange={(e) => patchProvider(name, { model: e.target.value })}
                          placeholder="deepseek-chat"
                        />
                      </Field>
                    </div>
                  </div>
                );
              })}
              <Button variant="outline" size="sm" className="self-start border-dashed" onClick={addProvider}>
                + 添加提供商
              </Button>
            </TabsContent>

            {/* ---------- 路由与额度 ---------- */}
            <TabsContent value="routing" className="flex flex-col gap-3">
              <SectionHint>不同环节可用不同模型：贵的干重活，便宜的跑龙套。</SectionHint>
              {Object.entries(ROLE_LABEL).map(([role, label]) => (
                <Field key={role} label={label}>
                  <Select value={roles[role] ?? 'default'} onValueChange={(v) => setRoles((r) => ({ ...r, [role]: v }))}>
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {(providerNames.length ? providerNames : ['default']).map((n) => (
                        <SelectItem key={n} value={n}>
                          {n}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
              ))}

              <SectionHint>限额保护你的钱包；官员拥有完整 LLM 大脑，平民纯机械劳作零消耗。</SectionHint>
              <Field label="同时在途请求" hint="真实提供商的并发上限（背压即省钱）">
                <Input
                  type="number"
                  value={limits.max_concurrent}
                  onChange={(e) => setLimits((l) => ({ ...l, max_concurrent: Number(e.target.value) }))}
                />
              </Field>
              <Field label="每分钟请求数" hint="RPM 滑动窗口">
                <Input
                  type="number"
                  value={limits.rpm}
                  onChange={(e) => setLimits((l) => ({ ...l, rpm: Number(e.target.value) }))}
                />
              </Field>
              <Field label="官员人数上限" hint="官员数量决定 token 消耗规模">
                <Input
                  type="number"
                  min={1}
                  value={limits.official_cap}
                  onChange={(e) => setLimits((l) => ({ ...l, official_cap: Number(e.target.value) }))}
                />
              </Field>
              <Field label="同时对话场数上限" hint="每场对话按句消耗 dialogue token">
                <Input
                  type="number"
                  min={1}
                  value={limits.conv_cap}
                  onChange={(e) => setLimits((l) => ({ ...l, conv_cap: Number(e.target.value) }))}
                />
              </Field>
              <Field label="LLM 请求超时（秒）" hint="本地慢模型（如 Ollama 大模型）可调大">
                <Input
                  type="number"
                  min={10}
                  value={limits.llm_timeout_sec}
                  onChange={(e) => setLimits((l) => ({ ...l, llm_timeout_sec: Number(e.target.value) }))}
                />
              </Field>

              <SectionHint>
                创意度（temperature）：越高越发散（更有惊喜也更容易跑偏），越低越稳定保守。
                留空 = 内置默认（世界初始化 0.9 / 规划 0.3 / 决策 0.6 / 对话 0.95 / 分支 0.5 / 旁白 0.9）。
              </SectionHint>
              {Object.entries(ROLE_LABEL).map(([role, label]) => (
                <Field key={`temp-${role}`} label={label.split('（')[0]} hint={`默认 ${TEMP_DEFAULT[role] ?? 0.7}`}>
                  <Input
                    type="number"
                    step="0.05"
                    min={0}
                    max={2}
                    placeholder={String(TEMP_DEFAULT[role] ?? 0.7)}
                    value={temperatures[role] ?? ''}
                    onChange={(e) => setTemp(role, e.target.value)}
                  />
                </Field>
              ))}
            </TabsContent>

            {/* ---------- 节奏 ---------- */}
            <TabsContent value="pacing" className="flex flex-col gap-3">
              <SectionHint>
                冷却与事件频率保存后立即生效；入夜/天亮/日界在创建新世界时注入（对当前世界不追溯）。
                事件频率调 0 可关掉暴雨/野狗/商队等随机事件。
              </SectionHint>
              <Field label="官员思考冷却（秒）" hint="省 token 的总闸：调大省钱、调小吃戏">
                <Input
                  type="number"
                  min={1}
                  value={pacing.think_cooldown_sec}
                  onChange={(e) => setPacing((p) => ({ ...p, think_cooldown_sec: Number(e.target.value) }))}
                />
              </Field>
              <Field label="社交冷却（秒）" hint="一场对话后多久才会再主动找伴">
                <Input
                  type="number"
                  min={10}
                  value={pacing.chat_cooldown_sec}
                  onChange={(e) => setPacing((p) => ({ ...p, chat_cooldown_sec: Number(e.target.value) }))}
                />
              </Field>
              <Field label="对话轮数（1-4）" hint="每人台词数 = 轮数 × 2">
                <Input
                  type="number"
                  min={1}
                  max={4}
                  value={pacing.chat_rounds}
                  onChange={(e) => setPacing((p) => ({ ...p, chat_rounds: Number(e.target.value) }))}
                />
              </Field>
              <Field label="事件频率（%/游戏小时）" hint="0 = 关闭随机事件（纯沙盒）">
                <Input
                  type="number"
                  min={0}
                  max={100}
                  value={pacing.event_chance}
                  onChange={(e) => setPacing((p) => ({ ...p, event_chance: Number(e.target.value) }))}
                />
              </Field>
              <Field label="开局官员数（0-8）" hint="对话至少需要两名官员">
                <Input
                  type="number"
                  min={0}
                  max={8}
                  value={pacing.startup_officials}
                  onChange={(e) => setPacing((p) => ({ ...p, startup_officials: Number(e.target.value) }))}
                />
              </Field>
              <Field label="入夜时刻（24h）" hint="如 21.5 = 21:30">
                <Input
                  type="number"
                  step="0.5"
                  min={0}
                  max={24}
                  value={pacing.night_start}
                  onChange={(e) => setPacing((p) => ({ ...p, night_start: Number(e.target.value) }))}
                />
              </Field>
              <Field label="天亮时刻" hint="如 6">
                <Input
                  type="number"
                  step="0.5"
                  min={0}
                  max={24}
                  value={pacing.night_end}
                  onChange={(e) => setPacing((p) => ({ ...p, night_end: Number(e.target.value) }))}
                />
              </Field>
              <Field label="日界时刻" hint="跳夜目标与每日节拍，如 7">
                <Input
                  type="number"
                  step="0.5"
                  min={0}
                  max={24}
                  value={pacing.day_break}
                  onChange={(e) => setPacing((p) => ({ ...p, day_break: Number(e.target.value) }))}
                />
              </Field>
            </TabsContent>

            {/* ---------- 诊断 ---------- */}
            <TabsContent value="debug" className="flex flex-col gap-3">
              <div className="flex items-center justify-between rounded-xl border border-border bg-white/5 p-3">
                <div>
                  <Label className="text-slate-300">DEBUG 日志</Label>
                  <div className="mt-0.5 text-[11px] leading-relaxed text-slate-600">
                    记录完整 prompt 与原始 LLM 响应，写入 data/logs/aitown.log，排障用（文件会增长更快）
                  </div>
                </div>
                <Switch checked={debug} onCheckedChange={setDebug} />
              </div>
            </TabsContent>
          </div>
        </Tabs>

        <div className="flex justify-end gap-2 border-t border-border pt-3">
          <Button variant="outline" onClick={() => set((p) => ({ ...p, settingsOpen: false }))}>
            取消
          </Button>
          <Button disabled={saving} onClick={() => void save()}>
            {saving ? '保存中…' : '保存并生效'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
