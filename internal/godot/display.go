package godot

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A private X server keeps all game windows off the user's desktop. Its only
// listener is a local Unix socket, and the runtime owns and reaps the process.
type virtualDisplay struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
	name string
}

func startDisplay(ctx context.Context, environment []string) (_ *virtualDisplay, err error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("Godot framebuffer previews currently require Linux and Xvfb")
	}
	bin := os.Getenv("TERMCADE_XVFB_BIN")
	if bin == "" {
		bin = "Xvfb"
	}
	bin, err = exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("Godot framebuffer previews require Xvfb (install xorg-server-xvfb on Arch or xvfb on Ubuntu): %w", err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	defer writer.Close()
	d := &virtualDisplay{done: make(chan struct{})}
	d.cmd = exec.CommandContext(ctx, bin, "-displayfd", "3", "-screen", "0", "4096x4096x24", "-nolisten", "tcp", "-noreset")
	d.cmd.ExtraFiles = []*os.File{writer}
	d.cmd.Env = environment
	log := &engineLog{}
	d.cmd.Stdout, d.cmd.Stderr = log, log
	d.cmd.WaitDelay = time.Second
	if err = d.cmd.Start(); err != nil {
		return nil, err
	}
	writer.Close()
	go func() { _ = d.cmd.Wait(); close(d.done) }()
	defer func() {
		if err != nil {
			d.Close()
		}
	}()
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReaderSize(reader, 64).ReadSlice('\n')
		ready <- strings.TrimSpace(string(line))
	}()
	select {
	case line := <-ready:
		number, parseErr := strconv.Atoi(line)
		if parseErr != nil || number < 0 || number > 65535 {
			output, _ := log.snapshot()
			return nil, fmt.Errorf("Xvfb did not announce a display: %s", output)
		}
		d.name = ":" + line
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("Xvfb display startup timed out")
	}
	return d, nil
}

func (d *virtualDisplay) Close() {
	d.once.Do(func() {
		// SIGINT gives Xvfb time to remove its own socket and lock file.
		_ = d.cmd.Process.Signal(os.Interrupt)
		select {
		case <-d.done:
		case <-time.After(time.Second):
			_ = d.cmd.Process.Kill()
			<-d.done
		}
	})
}
