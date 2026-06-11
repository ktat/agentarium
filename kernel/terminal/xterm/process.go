// Package xterm は xterm.js クライアント描画向けの PTY バックエンドを実装する。
// PTY の生バイトをそのまま WS で配信し、描画はブラウザ側 xterm.js が担う想定。
package xterm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"

	"github.com/ktat/agentarium/kernel/terminal"
)

// replayBufferSize はリロード時に再現する PTY 出力の最大サイズ。
const replayBufferSize = 1 << 20 // 1 MiB

// Process は PTY 上で起動した任意のコマンド（Agent）を管理する。
type Process struct {
	cmd      *exec.Cmd
	ptmx     *os.File
	mu       sync.Mutex
	writers  []io.Writer
	workDir  string
	command  string
	args     []string
	ringBuf  *terminal.RingBuffer
	onExit   func() // PTY 終了（readLoop 抜け）時に 1 回だけ呼ばれる
	onInput  func([]byte)
	onOutput func([]byte)
}

// NewProcess は workDir / command / args を指定して未起動の Process を返す。
func NewProcess(workDir, command string, args ...string) *Process {
	return &Process{
		workDir: workDir,
		command: command,
		args:    args,
		ringBuf: terminal.NewRingBuffer(replayBufferSize),
	}
}

// Start は PTY 内でコマンドを起動する。すでに稼働中なら no-op。
func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		return nil
	}
	cmd := exec.Command(p.command, p.args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if p.workDir != "" {
		cmd.Dir = p.workDir
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("pty start: %w", err)
	}
	p.cmd = cmd
	p.ptmx = ptmx
	go p.readLoop()
	return nil
}

func (p *Process) readLoop() {
	buf := make([]byte, 4096)
	for {
		ptmx := p.getPTMX()
		if ptmx == nil {
			return
		}
		n, err := ptmx.Read(buf)
		if err != nil {
			p.cleanup()
			return
		}
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			p.broadcast(data)
		}
	}
}

func (p *Process) getPTMX() *os.File {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ptmx
}

// SetOnExit は PTY EOF 検出時に 1 回だけ呼ばれるコールバックを登録する。
func (p *Process) SetOnExit(fn func()) {
	p.mu.Lock()
	p.onExit = fn
	p.mu.Unlock()
}

// SetOnInput は Write 呼び出しごとに渡されたバイト列で呼ばれる callback を登録する。
func (p *Process) SetOnInput(fn func([]byte)) {
	p.mu.Lock()
	p.onInput = fn
	p.mu.Unlock()
}

// SetOnOutput は PTY から読み取って writers に配信するデータごとに呼ばれる callback を登録する。
func (p *Process) SetOnOutput(fn func([]byte)) {
	p.mu.Lock()
	p.onOutput = fn
	p.mu.Unlock()
}

func (p *Process) cleanup() {
	p.mu.Lock()
	p.cmd = nil
	p.ptmx = nil
	fn := p.onExit
	p.onExit = nil // 二重発火防止
	p.mu.Unlock()
	if fn != nil {
		go fn() // Registry.Remove は mu を取るので別 goroutine で
	}
}

func (p *Process) broadcast(data []byte) {
	_, _ = p.ringBuf.Write(data)
	p.mu.Lock()
	writers := make([]io.Writer, len(p.writers))
	copy(writers, p.writers)
	onOutput := p.onOutput
	p.mu.Unlock()
	for _, w := range writers {
		_, _ = w.Write(data)
	}
	if onOutput != nil {
		onOutput(data)
	}
}

// ReplayBuffer は過去出力（リングバッファ）のスナップショットを返す。
func (p *Process) ReplayBuffer() []byte {
	return p.ringBuf.Bytes()
}

// Write は stdin（= PTY）に書き込む。
func (p *Process) Write(data []byte) error {
	p.mu.Lock()
	ptmx := p.ptmx
	onInput := p.onInput
	p.mu.Unlock()
	if ptmx == nil {
		return errors.New("process not running")
	}
	if _, err := ptmx.Write(data); err != nil {
		return err
	}
	if onInput != nil {
		onInput(data)
	}
	return nil
}

// Resize は PTY のウィンドウサイズを変更する。
func (p *Process) Resize(rows, cols uint16) error {
	p.mu.Lock()
	ptmx := p.ptmx
	p.mu.Unlock()
	if ptmx == nil {
		return nil
	}
	return pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

// Stop は SIGINT で穏便に終了させ、3 秒で応答なければ Kill する。
func (p *Process) Stop() error {
	p.mu.Lock()
	ptmx := p.ptmx
	cmd := p.cmd
	p.mu.Unlock()
	if ptmx != nil {
		_ = ptmx.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
	p.cleanup()
	return nil
}

// Running は PTY プロセスが稼働中かを返す。
func (p *Process) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd != nil
}

// AddWriter は PTY 出力の配信先を追加する（WS 接続ごと）。
func (p *Process) AddWriter(w io.Writer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writers = append(p.writers, w)
}

// RemoveWriter は配信先を取り除く。
func (p *Process) RemoveWriter(w io.Writer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, x := range p.writers {
		if x == w {
			p.writers = append(p.writers[:i], p.writers[i+1:]...)
			return
		}
	}
}
