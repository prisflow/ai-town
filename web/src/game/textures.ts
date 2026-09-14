// 程序化像素纹理生成：零外部素材依赖，启动时在内存里画出全部瓦片/资源/建筑/居民。
import { Texture } from 'pixi.js';

export const TILE = 16;

function mkCanvas(w: number, h: number): [HTMLCanvasElement, CanvasRenderingContext2D] {
  const c = document.createElement('canvas');
  c.width = w; c.height = h;
  const ctx = c.getContext('2d')!;
  return [c, ctx];
}

function mulberry32(seed: number) {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const texCache = new Map<string, Texture>();
function cacheTex(key: string, canvas: HTMLCanvasElement): Texture {
  let tex = texCache.get(key);
  if (!tex) {
    tex = Texture.from(canvas);
    tex.source.style.scaleMode = 'nearest';
    texCache.set(key, tex);
  }
  return tex;
}

// ---------- 地形瓦片 ----------

export const T_GRASS = 0, T_DIRT = 1, T_PATH = 2, T_WATER = 3, T_SAND = 4, T_FARM = 5;

function tileCanvas(kind: number, variant: number): HTMLCanvasElement {
  const rng = mulberry32(kind * 100 + variant * 7 + 3);
  const [c, ctx] = mkCanvas(TILE, TILE);
  const speck = (base: string, colors: string[], density: number) => {
    ctx.fillStyle = base; ctx.fillRect(0, 0, TILE, TILE);
    for (let y = 0; y < TILE; y++) for (let x = 0; x < TILE; x++) {
      if (rng() < density) {
        ctx.fillStyle = colors[Math.floor(rng() * colors.length)];
        ctx.fillRect(x, y, 1, 1);
      }
    }
  };
  switch (kind) {
    case T_GRASS:
      speck('#587d38', ['#4f7331', '#618841', '#547935'], 0.35);
      if (variant === 2) { // 小花
        ctx.fillStyle = '#d9c94e'; ctx.fillRect(3, 5, 1, 1); ctx.fillRect(11, 10, 1, 1);
        ctx.fillStyle = '#c96a6a'; ctx.fillRect(7, 12, 1, 1);
      }
      break;
    case T_DIRT:
      speck('#8a6f4d', ['#7a6142', '#96794f'], 0.3);
      break;
    case T_PATH:
      speck('#ab9468', ['#9c8760', '#b7a077', '#8f7b55'], 0.3);
      ctx.fillStyle = '#8f7b55'; ctx.fillRect(4, 3, 2, 1); ctx.fillRect(10, 9, 2, 1);
      break;
    case T_WATER: {
      speck('#3f6fa3', ['#3a67a0', '#4577ab'], 0.25);
      const off = variant * 3;
      ctx.fillStyle = '#5c8cbd';
      for (let i = 0; i < 4; i++) {
        const y = (2 + i * 4 + off) % TILE;
        ctx.fillRect((3 + i * 4) % TILE, y, 3, 1);
      }
      break;
    }
    case T_SAND:
      speck('#c9b579', ['#bfa96b', '#d2bf85'], 0.3);
      break;
    case T_FARM:
      ctx.fillStyle = '#6b4f33'; ctx.fillRect(0, 0, TILE, TILE);
      ctx.fillStyle = '#5d4429';
      for (let y = 1; y < TILE; y += 4) ctx.fillRect(0, y, TILE, 1);
      ctx.fillStyle = '#5d8f3f';
      for (let y = 3; y < TILE; y += 4) {
        for (let x = 1; x < TILE; x += 3) ctx.fillRect(x + (y % 2), y, 1, 2);
      }
      break;
  }
  return c;
}

export function tileTex(kind: number, variant: number): Texture {
  return cacheTex(`tile:${kind}:${variant}`, tileCanvas(kind, variant));
}

export function waterFrames(): Texture[] {
  return [tileTex(T_WATER, 0), tileTex(T_WATER, 1)];
}

// ---------- 资源 ----------

export interface ResourceTex { normal: Texture; depleted: Texture }

function treeCanvas(stump: boolean): HTMLCanvasElement {
  const [c, ctx] = mkCanvas(16, 24);
  if (stump) {
    ctx.fillStyle = '#6b4a2f'; ctx.fillRect(6, 19, 4, 3);
    ctx.fillStyle = '#8a6a45'; ctx.fillRect(6, 19, 4, 1);
    return c;
  }
  // 影
  ctx.fillStyle = 'rgba(0,0,0,0.25)'; ctx.fillRect(3, 22, 10, 2);
  // 树干
  ctx.fillStyle = '#6b4a2f'; ctx.fillRect(7, 15, 2, 7);
  ctx.fillStyle = '#57381f'; ctx.fillRect(8, 15, 1, 7);
  // 树冠三层
  const canopy = (cx: number, cy: number, rx: number, ry: number, col: string) => {
    ctx.fillStyle = col;
    for (let y = -ry; y <= ry; y++) {
      for (let x = -rx; x <= rx; x++) {
        if ((x * x) / (rx * rx) + (y * y) / (ry * ry) <= 1.05) ctx.fillRect(cx + x, cy + y, 1, 1);
      }
    }
  };
  canopy(8, 10, 6, 5, '#3c6127');
  canopy(8, 7, 5, 4, '#4a7a33');
  canopy(7, 5, 3, 2, '#5c9440');
  // 高光
  ctx.fillStyle = '#6fae4e';
  ctx.fillRect(5, 4, 1, 1); ctx.fillRect(6, 3, 1, 1); ctx.fillRect(10, 6, 1, 1);
  return c;
}

function berryCanvas(stump: boolean): HTMLCanvasElement {
  const [c, ctx] = mkCanvas(16, 12);
  if (stump) {
    ctx.fillStyle = '#3c5a28'; ctx.fillRect(4, 8, 8, 2);
    return c;
  }
  ctx.fillStyle = 'rgba(0,0,0,0.2)'; ctx.fillRect(3, 10, 10, 1);
  ctx.fillStyle = '#4a7a33';
  ctx.fillRect(3, 4, 10, 6); ctx.fillRect(5, 2, 6, 2);
  ctx.fillStyle = '#5c9440'; ctx.fillRect(5, 3, 4, 2);
  ctx.fillStyle = '#c23b3b';
  ctx.fillRect(4, 5, 1, 1); ctx.fillRect(7, 4, 1, 1); ctx.fillRect(10, 6, 1, 1); ctx.fillRect(6, 8, 1, 1); ctx.fillRect(11, 4, 1, 1);
  ctx.fillStyle = '#e05a5a'; ctx.fillRect(7, 5, 1, 1);
  return c;
}

function rockCanvas(stump: boolean): HTMLCanvasElement {
  const [c, ctx] = mkCanvas(14, 11);
  if (stump) {
    ctx.fillStyle = '#6f6f78'; ctx.fillRect(3, 7, 8, 2);
    return c;
  }
  ctx.fillStyle = 'rgba(0,0,0,0.25)'; ctx.fillRect(2, 9, 10, 1);
  ctx.fillStyle = '#84848e';
  ctx.fillRect(2, 4, 10, 5); ctx.fillRect(4, 2, 6, 2);
  ctx.fillStyle = '#6a6a74';
  ctx.fillRect(2, 7, 10, 2); ctx.fillRect(9, 4, 3, 2);
  ctx.fillStyle = '#a0a0aa';
  ctx.fillRect(4, 3, 3, 1); ctx.fillRect(3, 4, 2, 1);
  return c;
}

export function resourceTex(kind: string): ResourceTex {
  if (kind === 'tree') {
    return { normal: cacheTex('res:tree', treeCanvas(false)), depleted: cacheTex('res:tree:x', treeCanvas(true)) };
  }
  if (kind === 'berry') {
    return { normal: cacheTex('res:berry', berryCanvas(false)), depleted: cacheTex('res:berry:x', berryCanvas(true)) };
  }
  return { normal: cacheTex('res:rock', rockCanvas(false)), depleted: cacheTex('res:rock:x', rockCanvas(true)) };
}

// ---------- 建筑 ----------

function keepCanvas(): HTMLCanvasElement {
  const [c, ctx] = mkCanvas(48, 48);
  // 影
  ctx.fillStyle = 'rgba(0,0,0,0.3)'; ctx.fillRect(2, 44, 44, 3);
  // 主体石墙
  ctx.fillStyle = '#9aa0a8'; ctx.fillRect(4, 14, 40, 30);
  ctx.fillStyle = '#868c94';
  for (let y = 14; y < 44; y += 5) ctx.fillRect(4, y, 40, 1);
  for (let x = 4; x < 44; x += 8) ctx.fillRect(x, 14, 1, 30);
  // 城齿
  ctx.fillStyle = '#9aa0a8';
  for (let x = 4; x < 44; x += 8) ctx.fillRect(x, 10, 5, 5);
  ctx.fillStyle = '#73787f';
  for (let x = 4; x < 44; x += 8) ctx.fillRect(x, 10, 5, 1);
  // 大门
  ctx.fillStyle = '#3a2a1e'; ctx.fillRect(19, 30, 10, 14);
  ctx.fillStyle = '#57402c'; ctx.fillRect(19, 30, 10, 2);
  // 窗
  ctx.fillStyle = '#2a3540'; ctx.fillRect(10, 20, 4, 5); ctx.fillRect(34, 20, 4, 5);
  // 旗帜
  ctx.fillStyle = '#6b6f76'; ctx.fillRect(24, 2, 1, 9);
  ctx.fillStyle = '#a83232'; ctx.fillRect(25, 2, 8, 5);
  ctx.fillStyle = '#e0b35e'; ctx.fillRect(27, 4, 3, 1);
  return c;
}

function houseCanvas(): HTMLCanvasElement {
  const [c, ctx] = mkCanvas(32, 32);
  ctx.fillStyle = 'rgba(0,0,0,0.3)'; ctx.fillRect(2, 28, 28, 3);
  // 墙
  ctx.fillStyle = '#c9b28a'; ctx.fillRect(4, 14, 24, 14);
  ctx.fillStyle = '#6b4a2f';
  ctx.fillRect(4, 14, 24, 1); ctx.fillRect(4, 27, 24, 1); ctx.fillRect(4, 14, 1, 14); ctx.fillRect(27, 14, 1, 14);
  // 屋顶
  ctx.fillStyle = '#a5503c';
  for (let i = 0; i < 7; i++) ctx.fillRect(2 + i, 12 - i, 28 - i * 2, 1);
  ctx.fillStyle = '#8c4030';
  ctx.fillRect(2, 12, 28, 2);
  // 门 + 窗
  ctx.fillStyle = '#57402c'; ctx.fillRect(13, 19, 6, 9);
  ctx.fillStyle = '#2a3540'; ctx.fillRect(7, 18, 4, 4); ctx.fillRect(21, 18, 4, 4);
  ctx.fillStyle = '#e0b35e'; ctx.fillRect(8, 19, 1, 1); ctx.fillRect(22, 19, 1, 1);
  return c;
}

function granaryCanvas(): HTMLCanvasElement {
  const [c, ctx] = mkCanvas(32, 32);
  ctx.fillStyle = 'rgba(0,0,0,0.3)'; ctx.fillRect(2, 28, 28, 3);
  // 仓体木板
  ctx.fillStyle = '#b09a6e'; ctx.fillRect(5, 13, 22, 15);
  ctx.fillStyle = '#99855c';
  for (let y = 13; y < 28; y += 4) ctx.fillRect(5, y, 22, 1);
  // 锥顶
  ctx.fillStyle = '#8a6f4d';
  for (let i = 0; i < 6; i++) ctx.fillRect(4 + i, 11 - i, 24 - i * 2, 1);
  ctx.fillStyle = '#755c3c'; ctx.fillRect(4, 11, 24, 1);
  // 通风口与粮袋
  ctx.fillStyle = '#57402c'; ctx.fillRect(13, 17, 6, 4);
  ctx.fillStyle = '#d9c07a'; ctx.fillRect(7, 24, 4, 4); ctx.fillRect(21, 24, 4, 4);
  return c;
}

function millCanvas(): HTMLCanvasElement {
  const [c, ctx] = mkCanvas(32, 32);
  ctx.fillStyle = 'rgba(0,0,0,0.3)'; ctx.fillRect(2, 28, 28, 3);
  // 石砌塔身
  ctx.fillStyle = '#9aa0a8'; ctx.fillRect(8, 12, 16, 16);
  ctx.fillStyle = '#868c94';
  for (let y = 14; y < 28; y += 4) ctx.fillRect(8, y, 16, 1);
  // 尖顶
  ctx.fillStyle = '#8a6f4d';
  for (let i = 0; i < 5; i++) ctx.fillRect(9 + i, 9 - i, 14 - i * 2, 1);
  ctx.fillStyle = '#755c3c'; ctx.fillRect(9, 9, 14, 1);
  // 门与窗
  ctx.fillStyle = '#3a2a1e'; ctx.fillRect(13, 21, 6, 6);
  ctx.fillStyle = '#2a3540'; ctx.fillRect(10, 15, 3, 3);
  // 风车叶片（静态十字）
  ctx.fillStyle = '#c9b579';
  ctx.fillRect(15, 2, 2, 8); ctx.fillRect(15, 8, 2, 8);
  ctx.fillRect(9, 8, 6, 2); ctx.fillRect(17, 8, 6, 2);
  ctx.fillStyle = '#6b4a2f'; ctx.fillRect(15, 7, 2, 2);
  return c;
}

function farmCanvas(): HTMLCanvasElement {
  const [c, ctx] = mkCanvas(48, 32);
  ctx.fillStyle = '#6b4f33'; ctx.fillRect(0, 0, 48, 32);
  ctx.fillStyle = '#5d4429';
  for (let y = 3; y < 32; y += 7) ctx.fillRect(0, y, 48, 2);
  ctx.fillStyle = '#5d8f3f';
  for (let y = 0; y < 32; y += 7) {
    for (let x = 2; x < 48; x += 5) ctx.fillRect(x + (y % 3), y + 2, 2, 3);
  }
  ctx.fillStyle = '#6fae4e';
  for (let y = 0; y < 32; y += 7) {
    for (let x = 2; x < 48; x += 5) ctx.fillRect(x + (y % 3), y + 2, 1, 1);
  }
  return c;
}

export function buildingTex(kind: string): Texture {
  switch (kind) {
    case 'keep': return cacheTex('bld:keep', keepCanvas());
    case 'house': return cacheTex('bld:house', houseCanvas());
    case 'granary': return cacheTex('bld:granary', granaryCanvas());
    case 'mill': return cacheTex('bld:mill', millCanvas());
    case 'farm': return cacheTex('bld:farm', farmCanvas());
  }
  return cacheTex('bld:house', houseCanvas());
}

// ---------- 居民 ----------

export interface CharTex { frames: Texture[][] } // [dir 0下1上2左3右][step 0/1]

const ROLE_COLOR: Record<string, [string, string]> = {
  // [衣服, 帽子]
  woodcutter: ['#7a5230', '#4a3a2a'],
  farmer: ['#5d8f3f', '#c9b579'],
  builder: ['#c07a3a', '#8a4a2a'],
  forager: ['#8a5da5', '#d9c07a'],
  villager: ['#7a7a85', '#5a5a64'],
};

function charCanvas(color: string, hat: string, dir: number, step: number): HTMLCanvasElement {
  const [c, ctx] = mkCanvas(12, 16);
  // 影
  ctx.fillStyle = 'rgba(0,0,0,0.3)'; ctx.fillRect(2, 15, 8, 1);
  // 腿（走路帧交替）
  ctx.fillStyle = '#4a3a2a';
  if (step === 0) {
    ctx.fillRect(4, 12, 2, 3); ctx.fillRect(7, 12, 2, 3);
  } else {
    ctx.fillRect(3, 12, 2, 3); ctx.fillRect(8, 12, 2, 3);
  }
  // 身体
  ctx.fillStyle = color; ctx.fillRect(3, 7, 6, 5);
  ctx.fillStyle = 'rgba(0,0,0,0.18)'; ctx.fillRect(3, 11, 6, 1);
  // 手臂
  ctx.fillStyle = color; ctx.fillRect(2, 8, 1, 3); ctx.fillRect(9, 8, 1, 3);
  ctx.fillStyle = '#e8b98a'; ctx.fillRect(2, 11, 1, 1); ctx.fillRect(9, 11, 1, 1);
  // 头
  ctx.fillStyle = '#e8b98a'; ctx.fillRect(3, 1, 6, 5);
  // 头发/帽子
  ctx.fillStyle = hat; ctx.fillRect(3, 0, 6, 2);
  if (dir === 1) { ctx.fillRect(3, 0, 6, 4); } // 背面全发
  // 眼睛
  ctx.fillStyle = '#1a1a1a';
  if (dir === 0) { ctx.fillRect(4, 3, 1, 1); ctx.fillRect(7, 3, 1, 1); }
  else if (dir === 2) { ctx.fillRect(4, 3, 1, 1); }
  else if (dir === 3) { ctx.fillRect(7, 3, 1, 1); }
  return c;
}

export function characterTex(role: string): CharTex {
  const key = `char:${role}`;
  const cached = texCache.get(`sheet:${key}`);
  if (cached) return { frames: (cached as any).frames };
  const [cc, hat] = ROLE_COLOR[role] ?? ROLE_COLOR.villager;
  const frames: Texture[][] = [];
  for (let dir = 0; dir < 4; dir++) {
    const row: Texture[] = [];
    for (let step = 0; step < 2; step++) {
      row.push(cacheTex(`char:${role}:${dir}:${step}`, charCanvas(cc, hat, dir, step)));
    }
    frames.push(row);
  }
  return { frames };
}
