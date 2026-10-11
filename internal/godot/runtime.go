package godot

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aviorstudio/termcade/sdk"
)

const maxMessage = 1 << 20

type KeyEvent struct {
	Code int  `json:"code"`
	Down bool `json:"down"`
}

type Frame struct {
	Width        int              `json:"width"`
	Height       int              `json:"height"`
	Pixels       string           `json:"pixels"`
	Error        string           `json:"error"`
	RenderWidth  int              `json:"render_width"`
	RenderHeight int              `json:"render_height"`
	Timings      map[string]int64 `json:"timings"`
}

// Runtime owns one native engine child and its private loopback protocol. It
// is a developer runtime, not a security sandbox for marketplace downloads.
type Runtime struct {
	cmd            *exec.Cmd
	display        *virtualDisplay
	conn           net.Conn
	reader         *bufio.Reader
	log            *engineLog
	done           chan struct{}
	close          sync.Once
	temp           string
	LastRenderSize [2]int
	LastFrameTimes map[string]int64
	Title          string
	width          int
	height         int
}

func Start(ctx context.Context, bin, pack string) (_ *Runtime, err error) {
	if filepath.Ext(pack) != ".tgd" {
		return nil, fmt.Errorf("Godot terminal packages use .tgd")
	}
	info, err := os.Stat(pack)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 4 || info.Size() > maxPackageBytes {
		return nil, fmt.Errorf("expected a regular .tgd package of at most 256 MiB")
	}
	pack, err = filepath.Abs(pack)
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	deadline := time.Now().Add(10 * time.Second)
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	listener.SetDeadline(deadline)
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(secret[:])
	temp, err := os.MkdirTemp("", "termcade-godot-runtime-")
	if err != nil {
		return nil, err
	}
	r := &Runtime{log: &engineLog{}, done: make(chan struct{}), temp: temp}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	r.cmd = exec.CommandContext(ctx, bin, "--display-driver", "x11", "--rendering-method", "gl_compatibility",
		"--audio-driver", "Dummy", "--disable-render-loop", "--quiet", "--fixed-fps", "60",
		"--path", temp, "--main-pack", pack, "--script", "res://addons/termcade/runtime.gd", "--",
		fmt.Sprint(listener.Addr().(*net.TCPAddr).Port), token)
	r.cmd.WaitDelay = time.Second
	// Caller credentials and environment are not game inputs. This is still
	// ordinary native code: the project can access the host OS on its own.
	r.cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + temp, "XDG_DATA_HOME=" + temp, "GODOT_SILENCE_ROOT_WARNING=1"}
	r.cmd.Env = append(r.cmd.Env, "APPDATA="+temp, "LOCALAPPDATA="+temp, "USERPROFILE="+temp)
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		r.cmd.Env = append(r.cmd.Env, "SystemRoot="+systemRoot)
	}
	r.display, err = startDisplay(ctx, r.cmd.Env)
	if err != nil {
		return nil, err
	}
	r.cmd.Env = append(r.cmd.Env, "DISPLAY="+r.display.name, "LIBGL_ALWAYS_SOFTWARE=1")
	if os.Getenv("TERMCADE_GODOT_FULL_RES") == "1" {
		r.cmd.Env = append(r.cmd.Env, "TERMCADE_GODOT_FULL_RES=1")
	}
	r.cmd.Stdout, r.cmd.Stderr = r.log, r.log
	if err = r.cmd.Start(); err != nil {
		close(r.done)
		return nil, err
	}
	go func() {
		_ = r.cmd.Wait()
		close(r.done)
		listener.Close() // a child that fails before connecting must not cost 10s
	}()
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			output, _ := r.log.snapshot()
			return nil, fmt.Errorf("Godot terminal handshake failed: %w; %s", acceptErr, output)
		}
		conn.SetDeadline(deadline)
		reader := bufio.NewReaderSize(conn, maxMessage)
		line, readErr := boundedLine(reader)
		var hello struct {
			Protocol int    `json:"protocol"`
			Token    string `json:"token"`
			Title    string `json:"title"`
		}
		if readErr != nil || json.Unmarshal(line, &hello) != nil ||
			hello.Protocol != 2 || subtle.ConstantTimeCompare([]byte(hello.Token), []byte(token)) != 1 {
			conn.Close()
			continue
		}
		if len(hello.Title) > 200 {
			conn.Close()
			return nil, fmt.Errorf("Godot title exceeds 200 bytes")
		}
		r.conn, r.reader, r.Title = conn, reader, hello.Title
		break
	}
	return r, nil
}

func boundedLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return nil, err
	}
	return line, nil
}

// Reset chooses the framebuffer dimensions; every following frame must match.
func (r *Runtime) Reset(width, height int) ([]sdk.Color, error) {
	if width < 1 || width > 600 || height < 1 || height > 360 {
		return nil, fmt.Errorf("invalid Godot framebuffer %dx%d", width, height)
	}
	r.width, r.height = width, height
	return r.exchange(map[string]any{"op": "reset", "width": width, "height": height}, 30*time.Second)
}

func (r *Runtime) Step(keys []KeyEvent) ([]sdk.Color, error) {
	return r.StepCanvas(keys, r.width, r.height, 0.5)
}

// StepCanvas adjusts only capture dimensions; resizing never restarts gameplay.
func (r *Runtime) StepCanvas(keys []KeyEvent, width, height int, pixelAspect float64) ([]sdk.Color, error) {
	if width < 1 || width > 600 || height < 1 || height > 360 || pixelAspect <= 0 || pixelAspect > 2 {
		return nil, fmt.Errorf("invalid Godot framebuffer dimensions")
	}
	if len(keys) > 256 {
		return nil, fmt.Errorf("too many input events in one frame")
	}
	if keys == nil {
		keys = []KeyEvent{}
	}
	r.width, r.height = width, height
	return r.exchange(map[string]any{"op": "step", "keys": keys, "width": width, "height": height, "pixel_aspect": pixelAspect}, 2*time.Second)
}

func (r *Runtime) exchange(request any, budget time.Duration) ([]sdk.Color, error) {
	r.conn.SetDeadline(time.Now().Add(budget))
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(r.conn, strings.NewReader(string(data)+"\n")); err == nil {
		var line []byte
		line, err = boundedLine(r.reader)
		if err == nil {
			var frame Frame
			err = json.Unmarshal(line, &frame)
			if err == nil {
				r.LastFrameTimes = frame.Timings
				r.LastRenderSize = [2]int{frame.RenderWidth, frame.RenderHeight}
				pixels, decodeErr := decodeFrame(frame, r.width, r.height)
				_, failed := r.log.snapshot()
				if decodeErr == nil && !failed {
					return pixels, nil
				}
				err = decodeErr
				if failed {
					err = fmt.Errorf("Godot reported a script/runtime error")
				}
			}
		}
	}
	r.Close()
	output, _ := r.log.snapshot()
	return nil, fmt.Errorf("Godot terminal frame failed: %w; %s", err, output)
}

func decodeFrame(frame Frame, width, height int) ([]sdk.Color, error) {
	if frame.Error != "" {
		return nil, fmt.Errorf("%s", frame.Error)
	}
	if width < 1 || height < 1 || width > 600 || height > 360 || frame.Width != width || frame.Height != height {
		return nil, fmt.Errorf("Godot framebuffer dimensions changed")
	}
	want := width * height * 3
	if len(frame.Pixels) != base64.StdEncoding.EncodedLen(want) {
		return nil, fmt.Errorf("Godot framebuffer length does not match dimensions")
	}
	data, err := base64.StdEncoding.DecodeString(frame.Pixels)
	if err != nil || len(data) != want {
		return nil, fmt.Errorf("invalid Godot RGB framebuffer")
	}
	pixels := make([]sdk.Color, width*height)
	for index := range pixels {
		i := index * 3
		pixels[index] = sdk.Color(uint32(data[i])<<16 | uint32(data[i+1])<<8 | uint32(data[i+2]))
	}
	return pixels, nil
}

func (r *Runtime) Close() error {
	r.close.Do(func() {
		if r.conn != nil {
			r.conn.Close()
		}
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
			<-r.done
		}
		if r.display != nil {
			r.display.Close()
		}
		os.RemoveAll(r.temp)
	})
	return nil
}
