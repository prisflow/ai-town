// 与 Go 后端对应的 REST 客户端。
// 数据类型定义在 api.gen.ts（由 cmd/docgen 从后端 Go 结构体自动生成，
// SSE 帧的 t 字段已收窄为字面量，可直接做联合判别），
// 本文件只保留：类型别名/联合类型、常量与工具函数、REST 调用函数。
// 所有调用统一做 HTTP 状态检查：非 2xx 抛出带服务端 error 文案的 Error。
import type {
  BubbleMsg,
  ChatMsg,
  Config,
  EdictMsg,
  EventMsg,
  Snapshot,
  StateData,
  WorldJSON,
  WorldMsg,
} from './api.gen';
export type {
  AgentJSON,
  AgentView,
  BldJSON,
  BldState,
  BubbleMsg,
  ChatMsg,
  Config,
  EdictMsg,
  EventMsg,
  JobView,
  ProviderConfig,
  ResJSON,
  ResState,
  SaveMeta,
  Snapshot,
  Stats,
  WorldJSON,
  WorldMsg,
} from './api.gen';

export const TILE = 16;

/** SSE 单帧（按 t 字段判别）。 */
export type ServerMsg = Snapshot | WorldMsg | EventMsg | ChatMsg | BubbleMsg | EdictMsg;

/** GET /api/state 响应：StateData + 掩码后的配置。 */
export type StateResp = StateData & { config: Config };

/** GET /api/diag 响应：LLM 统计 + 最近日志 tail。 */
export interface DiagResp {
  stats: {
    llm_calls: number;
    llm_errors: number;
    tokens_in: number;
    tokens_out: number;
    last_error?: string;
    last_latency_ms: number;
  };
  debug: boolean;
  tail: string[];
}

/** 解析响应体；失败或非 2xx 时抛出带原因的 Error。 */
async function readBody<T>(r: Response): Promise<T> {
  let data: unknown = null;
  try {
    data = await r.json();
  } catch {
    /* 空体或非 JSON */
  }
  if (!r.ok) {
    const obj = data as { error?: string; message?: string } | null;
    throw new Error(obj?.error || obj?.message || `HTTP ${r.status}`);
  }
  return (data ?? {}) as T;
}

async function post<T = unknown>(url: string, body?: unknown): Promise<T> {
  return readBody<T>(
    await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body ?? {}),
    }),
  );
}

async function getJSON<T>(url: string): Promise<T> {
  return readBody<T>(await fetch(url));
}

export const api = {
  getState: () => getJSON<StateResp>('/api/state'),
  /** 创建世界：skipAI=true 时直接铺内置默认世界（不调用 LLM） */
  createWorld: (prompt: string, skipAI = false) => post('/api/world', { prompt, skip_ai: skipAI }),
  sendEdict: (text: string) => post('/api/edict', { text }),
  setPaused: (paused: boolean) => post('/api/pause', { paused }),
  saveNow: () => post<{ ok?: boolean }>('/api/save'),
  continueGame: () => post<{ ok?: boolean }>('/api/continue'),
  getConfig: () => getJSON<Config>('/api/config'),
  saveConfig: (cfg: Config) => post<Config>('/api/config', cfg),
  testProvider: (provider: string) => post<{ ok: boolean; latency_ms?: number; error?: string }>('/api/config/test', { provider }),
  getDiag: () => getJSON<DiagResp>('/api/diag'),
  appointOfficial: (id: string, appoint: boolean) => post<{ ok?: boolean }>('/api/official', { id, appoint }),
  exile: (id: string) => post<{ ok?: boolean }>('/api/exile', { id }),
};

export function fmtTime(hour: number): string {
  const h = Math.floor(hour);
  const m = Math.floor((hour - h) * 60);
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`;
}

export const ROLE_LABEL: Record<string, string> = {
  woodcutter: '伐木工', farmer: '农夫', builder: '工匠', forager: '采集者', villager: '居民',
};
/** 资源中文名（顶栏直接显示文字，不用难认的 emoji）。 */
export const RES_LABEL: Record<string, string> = {
  wood: '木材', food: '食物', stone: '石料', wheat: '小麦', flour: '面粉', bread: '面包',
};
