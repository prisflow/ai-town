package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"aitown/internal/config"
	"aitown/internal/game"
	"aitown/internal/llm"
	"aitown/internal/xlog"
)

// Server API 与静态资源。
type Server struct {
	eng     *game.Engine
	gw      *llm.Gateway
	hub     *Hub
	cfg     *config.Config
	cfgPath string
	web     fs.FS
}

// New 构造 Server。
func New(eng *game.Engine, gw *llm.Gateway, cfg *config.Config, cfgPath string, web fs.FS, hub *Hub) *Server {
	eng.SetConvCap(cfg.Limits.ConvCap) // 对话场数上限：启动时从配置注入（设置页可改）
	eng.SetPacing(cfg.Pacing)          // 节奏参数：启动时注入（设置页可改）
	return &Server{eng: eng, gw: gw, cfg: cfg, cfgPath: cfgPath, web: web, hub: hub}
}

// Handler 组装路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/world", s.handleCreateWorld)
	mux.HandleFunc("POST /api/edict", s.handleEdict)
	mux.HandleFunc("POST /api/pause", s.handlePause)
	mux.HandleFunc("POST /api/save", s.handleSave)
	mux.HandleFunc("POST /api/continue", s.handleContinue)
	mux.HandleFunc("POST /api/official", s.handleOfficial)
	mux.HandleFunc("POST /api/exile", s.handleExile)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/config", s.handleSetConfig)
	mux.HandleFunc("POST /api/config/test", s.handleTestConfig)
	mux.HandleFunc("GET /api/diag", s.handleDiag)
	mux.HandleFunc("GET /api/events", s.hub.ServeSSE)
	mux.Handle("/", s.spaHandler())
	return cors(mux)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	st := s.eng.State()
	writeJSON(w, 200, map[string]any{
		"has_world":  st.HasWorld,
		"world":      st.World,
		"snapshot":   st.Snapshot,
		"generating": st.Generating,
		"save":       st.Save,
		"config":     s.cfg.Masked(),
	})
}

func (s *Server) handleCreateWorld(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt string `json:"prompt"`
		SkipAI bool   `json:"skip_ai"` // true = 直接铺内置默认世界（不调用 LLM）
	}
	if err := readJSON(w, r, &body); err != nil {
		return
	}
	var err error
	if body.SkipAI {
		err = s.eng.CreateDefaultWorld()
	} else {
		err = s.eng.CreateWorld(body.Prompt)
	}
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 202, map[string]any{"ok": true})
}

func (s *Server) handleEdict(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := readJSON(w, r, &body); err != nil {
		return
	}
	if err := s.eng.Edict(body.Text); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 202, map[string]any{"ok": true})
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paused bool `json:"paused"`
	}
	if err := readJSON(w, r, &body); err != nil {
		return
	}
	s.eng.SetPaused(body.Paused)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleSave 手动存档（同步落盘，返回后存档已在磁盘上）。
func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.SaveNow(); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleContinue 继续上次存档（启动选择页）：加载成功后世界帧/快照经 SSE 广播。
func (s *Server) handleContinue(w http.ResponseWriter, r *http.Request) {
	loaded, err := s.eng.LoadSave()
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "读档失败：" + err.Error()})
		return
	}
	if !loaded {
		writeJSON(w, 400, map[string]any{"error": "没有可继续的存档"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleOfficial 领主任命/罢免官员。
func (s *Server) handleOfficial(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Appoint bool   `json:"appoint"`
	}
	if err := readJSON(w, r, &body); err != nil {
		return
	}
	cap := 0
	if s.cfg != nil {
		cap = s.cfg.Limits.OfficialCap
	}
	if err := s.eng.AppointOfficial(body.ID, body.Appoint, cap); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleExile 领主放逐村民。
func (s *Server) handleExile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		return
	}
	if err := s.eng.Exile(body.ID); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.cfg.Masked())
}

func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var cfg config.Config
	if err := readJSON(w, r, &cfg); err != nil {
		return
	}
	// 密钥为掩码时保留旧值
	for name, p := range cfg.Providers {
		if strings.HasPrefix(p.APIKey, "****") {
			if old := s.cfg.Providers[name]; old != nil {
				p.APIKey = old.APIKey
			} else {
				p.APIKey = ""
			}
		}
	}
	if err := cfg.Save(s.cfgPath); err != nil {
		writeJSON(w, 500, map[string]any{"error": "保存配置失败: " + err.Error()})
		return
	}
	s.cfg = &cfg
	s.gw.UpdateConfig(&cfg)
	s.eng.SetConvCap(cfg.Limits.ConvCap) // 对话场数上限热生效
	s.eng.SetPacing(cfg.Pacing)          // 节奏参数热生效
	xlog.SetDebug(cfg.Debug)             // 设置页 debug 开关热生效
	writeJSON(w, 200, s.cfg.Masked())
}

// handleDiag 诊断快照：LLM 统计 + 最近日志 tail（环形缓冲，不用读文件）。
func (s *Server) handleDiag(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"stats": s.gw.Stats(),
		"debug": xlog.DebugEnabled(),
		"tail":  xlog.Tail(200),
	})
}

func (s *Server) handleTestConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
	}
	if err := readJSON(w, r, &body); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	lat, err := s.gw.TestProvider(ctx, body.Provider)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "latency_ms": lat})
}

// spaHandler 静态资源 + SPA 回退。
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.web, p); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeJSON(w, 400, map[string]any{"error": "请求体不是合法 JSON"})
		return err
	}
	return nil
}
