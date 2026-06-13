// Package pet は外部 Pet バイナリ（別リポ）の設定・起動を管理するカーネル機能。
// Pet 本体は agentarium が公開する CLI/SSE 契約に従う独立バイナリ。
package pet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/server"
)

// 設定キー（secrets.Store。settings の kernel グループ規約 kernel.<field>）。
const (
	KeyBinary    = "kernel.pet_binary"
	KeySkin      = "kernel.pet_skin"
	KeyAutostart = "kernel.pet_autostart"
)

// Supervisor は Pet バイナリの設定・skin 取得・起動を担う。
type Supervisor struct {
	store    *secrets.Store
	subCount func() int // /terminal/events の購読者数（terminal.Service から注入）
	addr     string     // SetAddr で App.Run が設定する自サーバ addr
}

// New は store と「SSE 購読者数を返す関数」を束ねた Supervisor を返す。
func New(store *secrets.Store, subCount func() int) *Supervisor {
	return &Supervisor{store: store, subCount: subCount}
}

// SetAddr は起動時に Pet へ渡す自サーバ addr を設定する（App.Run が呼ぶ）。
func (s *Supervisor) SetAddr(addr string) { s.addr = addr }

func (s *Supervisor) binary() string { v, _ := s.store.Get(KeyBinary); return v }
func (s *Supervisor) skin() string   { v, _ := s.store.Get(KeySkin); return v }

// Autostart は kernel.pet_autostart が "1" のとき true。
func (s *Supervisor) Autostart() bool { v, _ := s.store.Get(KeyAutostart); return v == "1" }

// ListSkins は <binary> --list-skin の stdout を 1 行 1 skin として返す。
func (s *Supervisor) ListSkins() ([]string, error) {
	bin := s.binary()
	if bin == "" {
		return nil, errors.New("pet: binary not configured")
	}
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("pet: binary not found: %s", bin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--list-skin").Output()
	if err != nil {
		return nil, fmt.Errorf("pet: --list-skin failed: %w", err)
	}
	var skins []string
	for _, line := range strings.Split(string(out), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			skins = append(skins, t)
		}
	}
	return skins, nil
}

// Launch は <binary> --server <addr> [--skin <skin>] を起動する（fire-and-forget）。
func (s *Supervisor) Launch(addr string) (string, error) {
	bin := s.binary()
	if bin == "" {
		return "", errors.New("pet: binary not configured")
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("pet: binary not found: %s", bin)
	}
	args := []string{"--server", addr}
	if sk := s.skin(); sk != "" {
		args = append(args, "--skin", sk)
	}
	cmd := exec.Command(bin, args...)
	if lp := logPath(); lp != "" {
		_ = os.MkdirAll(filepath.Dir(lp), 0o700)
		if f, err := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			fmt.Fprintf(f, "\n=== pet launched %s (bin=%s addr=%s skin=%s) ===\n",
				time.Now().Format(time.RFC3339), bin, addr, s.skin())
			cmd.Stdout = f
			cmd.Stderr = f
		}
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("pet: launch failed: %w", err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return bin, nil
}

// MountOn は pet の制御 route を mux に登録する（server.Mountable）。
func (s *Supervisor) MountOn(mux *http.ServeMux) {
	mux.HandleFunc("GET /pet/config", s.handleConfigGet)
	mux.HandleFunc("POST /pet/config", s.handleConfigSet)
	mux.HandleFunc("GET /pet/skins", s.handleSkins)
	mux.HandleFunc("POST /pet/launch", s.handleLaunch)
	mux.HandleFunc("GET /pet/status", s.handleStatus)
}

func (s *Supervisor) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"binary": s.binary(), "skin": s.skin(), "autostart": s.Autostart()})
}

type configBody struct {
	Binary    string `json:"binary"`
	Skin      string `json:"skin"`
	Autostart bool   `json:"autostart"`
}

func (s *Supervisor) handleConfigSet(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin rejected", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var body configBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	auto := ""
	if body.Autostart {
		auto = "1"
	}
	for k, v := range map[string]string{KeyBinary: body.Binary, KeySkin: body.Skin, KeyAutostart: auto} {
		if err := s.store.Set(k, v); err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Supervisor) handleSkins(w http.ResponseWriter, r *http.Request) {
	skins, err := s.ListSkins()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"skins": skins})
}

func (s *Supervisor) handleLaunch(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin rejected", http.StatusForbidden)
		return
	}
	bin, err := s.Launch(s.addr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"launched": true, "binary": bin})
}

func (s *Supervisor) handleStatus(w http.ResponseWriter, r *http.Request) {
	n := 0
	if s.subCount != nil {
		n = s.subCount()
	}
	writeJSON(w, map[string]any{"subscriber_count": n, "connected": n >= 1})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// logPath は ~/.config/agentarium/pet.log（取れなければ ""）。
func logPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "agentarium", "pet.log")
}
