// PixiJS 渲染引擎：相机、静态世界层、资源/建筑/居民层与屏幕空间 UI 层（铭牌/气泡）。
// 设计：仿真是后端权威（250ms 快照流），前端只做平滑插值，不做任何权威判定。
import { Application, Container, Graphics, Sprite, Text } from 'pixi.js';
import { AgentJSON, AgentView, Snapshot, WorldJSON } from '../api';
import {
  CharTex, ResourceTex, buildingTex, characterTex, resourceTex, tileTex, waterFrames,
  T_DIRT, T_FARM, T_GRASS, T_PATH, T_SAND, T_WATER,
} from './textures';

const TILE = 16;

interface AgentSprite {
  spr: Sprite;
  char: CharTex;
  tx: number; ty: number;   // 目标（像素）
  moving: boolean;
  working: boolean;         // 劳作中：原地轻颠动画，让"干活"肉眼可见
  hidden: boolean;          // 已进屋（睡觉时在自家房内，隐藏精灵）
  facing: number;
}
interface ResSprite { spr: Sprite; tex: ResourceTex; depleted: boolean }
interface BldSprite { spr: Sprite; complete: boolean; progress: number }

/** 气泡：容器（背景 + 文本），到期自动销毁。 */
interface BubbleSprite { c: Container; until: number }

export class GameView {
  app = new Application();
  ready = false;
  world: WorldJSON | null = null;
  /** 点击居民回调（拖拽不算；由 App 绑定选中联动）。 */
  onSelect: ((id: string) => void) | null = null;

  private root = new Container();
  private tilesC = new Container();
  private resL = new Container();
  private bldL = new Container();
  private agentL = new Container();
  private uiC = new Container(); // 屏幕空间 UI 层（铭牌/气泡），不随相机变换

  private agents = new Map<string, AgentSprite>();
  private resMap = new Map<string, ResSprite>();
  private bldMap = new Map<string, BldSprite>();
  private labels = new Map<string, Container>();
  private bubbles = new Map<string, BubbleSprite[]>();
  private water: Sprite[] = [];
  private waterFrame = 0;
  private waterT = 0;

  private cam = { cx: 0, cy: 0, zoom: 2 };
  private drag: { x: number; y: number } | null = null;
  private time = 0;
  private posOut = new Map<string, [number, number]>();

  async init(canvas: HTMLCanvasElement): Promise<void> {
    await this.app.init({
      canvas,
      resizeTo: window,
      antialias: false,
      backgroundColor: 0x10161d,
      preference: 'webgl', // 集显友好；2D 精灵批渲染负载极低
    });
    this.app.stage.addChild(this.root);
    this.app.stage.addChild(this.uiC); // UI 层在相机层之上，屏幕坐标系
    this.root.addChild(this.tilesC, this.resL, this.bldL, this.agentL);
    this.app.ticker.maxFPS = 60;
    this.app.ticker.add((tk) => this.frame(Math.min(tk.deltaMS / 1000, 0.1)));
    this.bindInput(canvas);
    this.ready = true;
  }

  // ---------- 世界构建 ----------

  setWorld(w: WorldJSON): void {
    this.world = w;
    this.tilesC.removeChildren();
    this.resL.removeChildren();
    this.bldL.removeChildren();
    this.agentL.removeChildren();
    this.agents.clear();
    this.resMap.clear();
    this.bldMap.clear();
    this.water = [];

    for (let y = 0; y < w.h; y++) {
      for (let x = 0; x < w.w; x++) {
        const kind = w.tiles[y * w.w + x];
        let variant = (x * 7 + y * 13) % 3;
        let tex;
        switch (kind) {
          case T_DIRT: tex = tileTex(T_DIRT, 0); break;
          case T_PATH: tex = tileTex(T_PATH, variant % 2); break;
          case T_WATER: tex = tileTex(T_WATER, 0); break;
          case T_SAND: tex = tileTex(T_SAND, 0); break;
          case T_FARM: tex = tileTex(T_FARM, 0); break;
          default: tex = tileTex(T_GRASS, variant); break;
        }
        const s = new Sprite(tex);
        s.position.set(x * TILE, y * TILE);
        this.tilesC.addChild(s);
        if (kind === T_WATER) this.water.push(s);
      }
    }
    for (const r of w.resources) this.addResource(r);
    for (const b of w.buildings) this.addBuilding(b);
    for (const a of w.agents) this.addAgent(a);
    this.setLabels(w.agents);
    if (this.cam.cx === 0 && this.cam.cy === 0) {
      this.cam.cx = (w.w * TILE) / 2;
      this.cam.cy = (w.h * TILE) / 2;
    }
  }

  // ---------- 屏幕空间 UI（铭牌/气泡，Pixi 渲染，与画面同一图层） ----------

  /** 重建居民铭牌（世界加载时自动调用）。 */
  setLabels(agents: AgentJSON[]): void {
    for (const lab of this.labels.values()) lab.destroy({ children: true });
    this.labels.clear();
    for (const a of agents) {
      const c = new Container();
      const txt = new Text({
        text: a.name,
        style: { fontFamily: 'sans-serif', fontSize: 11, fill: 0xe2e8f0 },
      });
      txt.position.set(0, 0);
      const bg = new Graphics()
        .roundRect(-3, -1.5, txt.width + 6, txt.height + 3, 3)
        .fill({ color: 0x0f172a, alpha: 0.72 });
      c.addChild(bg, txt);
      c.visible = false;
      this.uiC.addChild(c);
      this.labels.set(a.id, c);
    }
  }

  /** 居民头顶冒一句话气泡（每名最多 2 条，旧的让位）。 */
  bubble(actorID: string, text: string, secs = 4): void {
    const list = this.bubbles.get(actorID) ?? [];
    while (list.length >= 2) {
      const old = list.shift()!;
      old.c.destroy({ children: true });
    }
    const c = new Container();
    const txt = new Text({
      text,
      style: {
        fontFamily: 'sans-serif', fontSize: 12, fill: 0xf1f5f9,
        wordWrap: true, wordWrapWidth: 200, breakWords: true, lineHeight: 15,
      },
    });
    const bg = new Graphics()
      .roundRect(-5, -4, txt.width + 10, txt.height + 8, 5)
      .fill({ color: 0x0f172a, alpha: 0.94 })
      .roundRect(-5, -4, txt.width + 10, txt.height + 8, 5)
      .stroke({ color: 0xffffff, alpha: 0.12, width: 1 });
    c.addChild(bg, txt);
    this.uiC.addChild(c);
    list.push({ c, until: Date.now() + (secs > 0 ? secs : 4) * 1000 });
    this.bubbles.set(actorID, list);
  }

  private syncUI(): void {
    const now = Date.now();
    // 铭牌（进屋内睡觉的居民隐藏铭牌与气泡）
    for (const [id, lab] of this.labels) {
      const p = this.posOut.get(id);
      const hidden = this.agents.get(id)?.hidden ?? false;
      if (!p || hidden) { lab.visible = false; continue; }
      lab.visible = true;
      lab.pivot.set(lab.width / 2, 0);
      lab.position.set(p[0], p[1] + 12 * this.cam.zoom);
    }
    // 气泡（堆叠在铭牌上方，末期淡出）
    for (const [id, list] of this.bubbles) {
      const alive = list.filter((b) => b.until > now);
      if (alive.length !== list.length) {
        for (const b of list) if (b.until <= now) b.c.destroy({ children: true });
        this.bubbles.set(id, alive);
      }
      const p = this.posOut.get(id);
      const hidden = this.agents.get(id)?.hidden ?? false;
      alive.forEach((b, i) => {
        if (!p || hidden) { b.c.visible = false; return; }
        b.c.visible = true;
        b.c.pivot.set(b.c.width / 2, b.c.height);
        b.c.position.set(p[0], p[1] + 8 * this.cam.zoom - i * 36);
        const remain = b.until - now;
        b.c.alpha = remain < 400 ? remain / 400 : 1;
      });
    }
  }

  private addResource(r: { id: string; kind: string; x: number; y: number }): void {
    const tex = resourceTex(r.kind);
    const spr = new Sprite(tex.normal);
    spr.anchor.set(0.5, 1);
    spr.position.set(r.x * TILE + TILE / 2, r.y * TILE + TILE);
    this.resL.addChild(spr);
    this.resMap.set(r.id, { spr, tex, depleted: false });
  }

  private addBuilding(b: { id: string; kind: string; x: number; y: number; w: number; progress?: number; complete: boolean }): void {
    const spr = new Sprite(buildingTex(b.kind));
    spr.position.set(b.x * TILE, b.y * TILE);
    const progress = b.progress ?? (b.complete ? 100 : 0);
    const rec: BldSprite = { spr, complete: b.complete, progress };
    this.bldL.addChild(spr);
    this.bldMap.set(b.id, rec);
    this.applyBldLook(rec);
  }

  /** 工地视觉：半透明蓝色随施工进度渐变（0.35→0.9），落成即实色。 */
  private applyBldLook(rec: BldSprite): void {
    if (rec.complete) {
      rec.spr.alpha = 1;
      rec.spr.tint = 0xffffff;
    } else {
      rec.spr.alpha = 0.35 + 0.55 * Math.min(100, Math.max(0, rec.progress)) / 100;
      rec.spr.tint = 0x9db8e8;
    }
  }

  private addAgent(a: AgentJSON): void {
    const char = characterTex(a.role);
    const spr = new Sprite(char.frames[0][0]);
    spr.anchor.set(0.5, 1);
    spr.position.set(a.x * TILE, a.y * TILE);
    this.agentL.addChild(spr);
    this.agents.set(a.id, { spr, char, tx: a.x * TILE, ty: a.y * TILE, moving: false, working: false, hidden: false, facing: 0 });
  }

  // ---------- 快照应用 ----------

  applySnapshot(snap: Snapshot): void {
    if (!this.world) return;
    const byId = new Map<string, AgentView>(snap.agents.map((v): [string, AgentView] => [v.id, v]));
    for (const [id, ag] of this.agents) {
      const v = byId.get(id);
      if (!v) continue;
      ag.tx = v.x * TILE;
      ag.ty = v.y * TILE;
      ag.moving = v.state === 'moving';
      ag.working = v.state === 'working';
      ag.hidden = !!v.hidden;
      ag.facing = v.facing;
    }
    for (const r of snap.resources) {
      // 快照是精简态（无 kind/坐标），新资源由 WorldMsg 全量重建覆盖
      const rs = this.resMap.get(r.id);
      if (!rs || rs.depleted === r.depleted) continue;
      rs.depleted = r.depleted;
      rs.spr.texture = r.depleted ? rs.tex.depleted : rs.tex.normal;
    }
    for (const b of snap.buildings) {
      // 快照精简态只含 id/progress/complete：更新已有建筑（工地 alpha 随进度渐变的主通道）；
      // 新建筑由 WorldMsg 全量重建覆盖
      const bs = this.bldMap.get(b.id);
      if (!bs) continue;
      if (bs.complete === b.complete && bs.complete) continue;
      bs.complete = b.complete;
      bs.progress = b.progress;
      this.applyBldLook(bs);
    }
  }

  // ---------- 帧循环 ----------

  private frame(dt: number): void {
    this.time += dt;
    // 水面动画
    this.waterT += dt;
    if (this.waterT > 0.6) {
      this.waterT = 0;
      this.waterFrame = 1 - this.waterFrame;
      const frames = waterFrames();
      for (const s of this.water) s.texture = frames[this.waterFrame];
    }
    // 居民插值 + 行走动画
    for (const ag of this.agents.values()) {
      const s = ag.spr;
      const k = Math.min(1, dt * 8);
      const dx = ag.tx - s.x;
      const dy = ag.ty - s.y;
      if (Math.abs(dx) > 0.5 || Math.abs(dy) > 0.5) {
        s.x += dx * k;
        s.y += dy * k;
      } else {
        s.x = ag.tx; s.y = ag.ty;
      }
      if (ag.moving) {
        const step = Math.floor(this.time * 6) % 2;
        s.texture = ag.char.frames[ag.facing][step];
      } else {
        s.texture = ag.char.frames[ag.facing][0];
      }
      // 劳作微动画：原地上下轻颠（不移动位置，只动精灵）
      if (ag.working && !ag.moving) {
        s.y = ag.ty - Math.abs(Math.sin(this.time * 6)) * 2.5;
      }
      // 进屋睡觉：隐藏精灵（铭牌与气泡由 syncUI 同步隐藏）
      s.visible = !ag.hidden;
    }
    // 相机
    const W = this.app.renderer.width / this.app.renderer.resolution;
    const H = this.app.renderer.height / this.app.renderer.resolution;
    this.root.position.set(W / 2 - this.cam.cx * this.cam.zoom, H / 2 - this.cam.cy * this.cam.zoom);
    this.root.scale.set(this.cam.zoom);
    // 屏幕坐标（供 UI 层同步）
    this.posOut.clear();
    for (const [id, ag] of this.agents) {
      const sx = ag.spr.x * this.cam.zoom + W / 2 - this.cam.cx * this.cam.zoom;
      const sy = ag.spr.y * this.cam.zoom + H / 2 - this.cam.cy * this.cam.zoom;
      this.posOut.set(id, [sx, sy - 16 * this.cam.zoom]);
    }
    this.syncUI();
  }

  // ---------- 相机输入 ----------

  private bindInput(canvas: HTMLCanvasElement): void {
    let downX = 0;
    let downY = 0;
    canvas.addEventListener('pointerdown', (e) => {
      if (e.button === 0) {
        this.drag = { x: e.clientX, y: e.clientY };
        downX = e.clientX;
        downY = e.clientY;
      }
    });
    window.addEventListener('pointermove', (e) => {
      if (!this.drag) return;
      const dx = e.clientX - this.drag.x;
      const dy = e.clientY - this.drag.y;
      this.drag = { x: e.clientX, y: e.clientY };
      this.cam.cx -= dx / this.cam.zoom;
      this.cam.cy -= dy / this.cam.zoom;
      this.clampCam();
    });
    window.addEventListener('pointerup', (e) => {
      const wasDragging = this.drag !== null;
      this.drag = null;
      // 未拖拽的点击：命中居民 → 选中（App 联动左侧详情）
      if (wasDragging && Math.abs(e.clientX - downX) < 6 && Math.abs(e.clientY - downY) < 6) {
        const id = this.hitAgent(e.clientX, e.clientY);
        if (id && this.onSelect) this.onSelect(id);
      }
    });
    canvas.addEventListener('wheel', (e) => {
      e.preventDefault();
      const old = this.cam.zoom;
      const zoom = Math.min(4, Math.max(0.6, old * (e.deltaY < 0 ? 1.12 : 1 / 1.12)));
      if (zoom === old) return;
      const W = this.app.renderer.width / this.app.renderer.resolution;
      const H = this.app.renderer.height / this.app.renderer.resolution;
      const wx = (e.clientX - W / 2) / old + this.cam.cx;
      const wy = (e.clientY - H / 2) / old + this.cam.cy;
      this.cam.zoom = zoom;
      this.cam.cx = wx - (e.clientX - W / 2) / zoom;
      this.cam.cy = wy - (e.clientY - H / 2) / zoom;
      this.clampCam();
    }, { passive: false });
  }

  private clampCam(): void {
    const w = this.world;
    if (!w) return;
    const W = this.app.renderer.width / this.app.renderer.resolution;
    const H = this.app.renderer.height / this.app.renderer.resolution;
    const m = 80;
    this.cam.cx = Math.min(Math.max(this.cam.cx, -m), w.w * TILE + m);
    this.cam.cy = Math.min(Math.max(this.cam.cy, -m), w.h * TILE + m);
    void W; void H;
  }

  /** 屏幕坐标命中测试：返回最近的可见居民 ID（供点选联动）。 */
  private hitAgent(clientX: number, clientY: number): string | null {
    const W = this.app.renderer.width / this.app.renderer.resolution;
    const H = this.app.renderer.height / this.app.renderer.resolution;
    const wx = (clientX - W / 2) / this.cam.zoom + this.cam.cx;
    const wy = (clientY - H / 2) / this.cam.zoom + this.cam.cy;
    let best: string | null = null;
    let bestDx = 9; // 命中横向半径（像素，世界坐标）
    for (const [id, ag] of this.agents) {
      if (ag.hidden) continue;
      const dx = Math.abs(wx - ag.spr.x);
      const dy = wy - ag.spr.y; // 精灵锚点在脚下（y = 底部）
      if (dx < bestDx && dy > -26 && dy < 4) {
        best = id;
        bestDx = dx;
      }
    }
    return best;
  }
}
