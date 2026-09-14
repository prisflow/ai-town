// 共享状态（React Context + useState 单一大对象）。
// SSE 与 REST 更新都通过 set() 进入；组件用 useStore() 订阅。
// 提示反馈统一走 sonner（直接 import { toast }），本文件不再持有 toasts。
import { createContext, useContext, useState, type Dispatch, type ReactNode, type SetStateAction } from 'react';
import type { AgentJSON, ChatMsg, EventMsg, SaveMeta, Snapshot, WorldJSON } from './api';

export interface LogLine { day: number; hour: number; cat: string; text: string }

export interface Store {
  /** 世界静态数据（瓦片/资源/建筑/居民）；null = 尚无世界 */
  world: WorldJSON | null;
  /** 250ms 快照（时间/库存/居民动态/工作单/统计） */
  snap: Snapshot | null;
  /** 居民静态元数据索引（id → AgentJSON） */
  agentsMeta: Map<string, AgentJSON>;
  /** 当前选中的居民 ID（左侧面板详情 / 画布点选） */
  selected: string;
  /** SSE 连接状态 */
  connected: boolean;
  /** 初始状态加载中（首屏"正在连接后端"） */
  loading: boolean;
  /** 初始加载失败原因（非空时展示重试界面） */
  loadError: string;
  /** 世界 AI 生成中（生成覆盖层用） */
  generating: boolean;
  /** 存档概况（启动"继续 / 新世界"选择页用；无档为 null） */
  save: SaveMeta | null;
  /** 事件日志（控制台"事件"页） */
  logs: LogLine[];
  /** 对话记录（控制台"对话"页） */
  chats: LogLine[];
  settingsOpen: boolean;
  newWorldOpen: boolean;
  diagOpen: boolean;
  jobsOpen: boolean;
}

interface StoreCtx {
  s: Store;
  set: Dispatch<SetStateAction<Store>>;
}

const Ctx = createContext<StoreCtx | null>(null);

export function StoreProvider({ children }: { children: ReactNode }) {
  const [s, set] = useState<Store>({
    world: null,
    snap: null,
    agentsMeta: new Map(),
    selected: '',
    connected: false,
    loading: true,
    loadError: '',
    generating: false,
    save: null,
    logs: [],
    chats: [],
    settingsOpen: false,
    newWorldOpen: false,
    diagOpen: false,
    jobsOpen: false,
  });
  return <Ctx.Provider value={{ s, set }}>{children}</Ctx.Provider>;
}

export function useStore(): StoreCtx {
  const v = useContext(Ctx);
  if (!v) throw new Error('StoreProvider missing');
  return v;
}

const MAX_LOGS = 250;

/** 追加一条事件日志（保留最近 MAX_LOGS 条）。 */
export function appendLog(set: Dispatch<SetStateAction<Store>>, line: LogLine): void {
  set((p) => ({ ...p, logs: [...p.logs, line].slice(-MAX_LOGS) }));
}

/** 追加一条对话记录（保留最近 MAX_LOGS 条）。 */
export function appendChat(set: Dispatch<SetStateAction<Store>>, line: LogLine): void {
  set((p) => ({ ...p, chats: [...p.chats, line].slice(-MAX_LOGS) }));
}

/** event 帧转日志。 */
export function eventToLog(e: EventMsg): LogLine {
  return { day: e.day, hour: e.hour, cat: e.cat, text: e.text };
}

/** chat 帧转日志。 */
export function chatToLog(e: ChatMsg): LogLine {
  return { day: e.day, hour: e.hour, cat: 'chat', text: `${e.from_name} → ${e.to_name}：${e.text}` };
}

/** 世界就绪：写入 world 与居民元数据表。 */
export function worldMetas(w: WorldJSON): Map<string, AgentJSON> {
  const m = new Map<string, AgentJSON>();
  for (const a of w.agents) m.set(a.id, a);
  return m;
}
