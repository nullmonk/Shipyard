package builder

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"dagger.io/dagger"
)

const (
	shellMsgData   byte = 0
	shellMsgResize byte = 1

	ioctlTCGETS = 0x5401
	ioctlTCSETS = 0x5402
)

type termios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Line   uint8
	Cc     [32]uint8
	Ispeed uint32
	Ospeed uint32
}

type termWinsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func makeRaw(fd uintptr) (*termios, error) {
	var orig termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlTCGETS, uintptr(unsafe.Pointer(&orig))); errno != 0 {
		return nil, errno
	}
	raw := orig
	// cfmakeraw equivalents
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlTCSETS, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil, errno
	}
	return &orig, nil
}

func restoreTermios(fd uintptr, orig *termios) {
	if orig == nil {
		return
	}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlTCSETS, uintptr(unsafe.Pointer(orig)))
}

func getTermSize(fd uintptr) (uint16, uint16) {
	var ws termWinsize
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); errno == 0 && ws.Row > 0 && ws.Col > 0 {
		return ws.Row, ws.Col
	}
	return 24, 80
}

func writeShellFrame(mu *sync.Mutex, w io.Writer, msgType byte, payload []byte) error {
	mu.Lock()
	defer mu.Unlock()
	var hdr [5]byte
	hdr[0] = msgType
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func sendResizeFrame(mu *sync.Mutex, w io.Writer, fd uintptr) {
	rows, cols := getTermSize(fd)
	var payload [4]byte
	binary.BigEndian.PutUint16(payload[0:2], rows)
	binary.BigEndian.PutUint16(payload[2:4], cols)
	_ = writeShellFrame(mu, w, shellMsgResize, payload[:])
}

func fdSet(fd int, set *syscall.FdSet) {
	set.Bits[fd/64] |= 1 << (uint(fd) % 64)
}

func fdIsSet(fd int, set *syscall.FdSet) bool {
	return set.Bits[fd/64]&(1<<(uint(fd)%64)) != 0
}

// dropIntoShell opens an interactive PTY shell in ctr by forwarding a temporary
// host Unix socket into the container and running `shipyard shell`. This works
// directly in standard terminals without requiring the Dagger CLI TUI (`dagger run`).
func dropIntoShell(ctx context.Context, client *dagger.Client, w io.Writer, ctr *dagger.Container, sfDir ...string) {
	if ctr == nil {
		fmt.Fprintln(w, "[!] Cannot open interactive shell: container is nil")
		return
	}
	fmt.Fprintln(w, "[!] Dropping into interactive shell...")
	if client == nil {
		if _, err := ctr.Terminal(dagger.ContainerTerminalOpts{Cmd: []string{"/bin/bash"}}).Sync(ctx); err != nil {
			fmt.Fprintf(w, "[!] Could not open interactive shell: %v\n", err)
		}
		return
	}

	var dir string
	if len(sfDir) > 0 {
		dir = sfDir[0]
	}
	self, err := findContainerBinary(dir)
	if err != nil {
		fmt.Fprintf(w, "[!] Could not resolve shipyard binary for interactive shell: %v\n", err)
		return
	}
	selfFile := client.Host().File(self)

	tmpDir, err := os.MkdirTemp("", "sy-sh-*")
	if err != nil {
		fmt.Fprintf(w, "[!] Could not create shell socket dir: %v\n", err)
		return
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "s.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		fmt.Fprintf(w, "[!] Could not listen on shell socket: %v\n", err)
		return
	}
	defer ln.Close()

	stdinFd := os.Stdin.Fd()
	origTerm, _ := makeRaw(stdinFd)
	defer restoreTermios(stdinFd, origTerm)

	sessionDone := make(chan struct{})
	go func() {
		defer close(sessionDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var writeMu sync.Mutex
		sendResizeFrame(&writeMu, conn, stdinFd)

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGWINCH)
		defer signal.Stop(sigCh)

		stopResize := make(chan struct{})
		defer close(stopResize)
		go func() {
			for {
				select {
				case <-sigCh:
					sendResizeFrame(&writeMu, conn, stdinFd)
				case <-stopResize:
					return
				}
			}
		}()

		cancelR, cancelW, pipeErr := os.Pipe()
		if pipeErr == nil {
			defer cancelR.Close()
			defer cancelW.Close()
		}

		// Read from os.Stdin and forward to container until container closes connection.
		go func() {
			buf := make([]byte, 4096)
			inFd := int(stdinFd)
			for {
				if pipeErr == nil {
					cFd := int(cancelR.Fd())
					var rfds syscall.FdSet
					fdSet(inFd, &rfds)
					fdSet(cFd, &rfds)
					maxFd := inFd
					if cFd > maxFd {
						maxFd = cFd
					}
					n, selErr := syscall.Select(maxFd+1, &rfds, nil, nil, nil)
					if selErr != nil {
						if selErr == syscall.EINTR {
							continue
						}
						return
					}
					if n <= 0 || fdIsSet(cFd, &rfds) {
						return
					}
				}
				nr, rerr := os.Stdin.Read(buf)
				if nr > 0 {
					if werr := writeShellFrame(&writeMu, conn, shellMsgData, buf[:nr]); werr != nil {
						return
					}
				}
				if rerr != nil {
					return
				}
			}
		}()

		_, _ = io.Copy(os.Stdout, conn)
		if pipeErr == nil {
			_ = cancelW.Close()
		}
	}()

	term := os.Getenv("TERM")
	if term == "" {
		term = "xterm-256color"
	}
	hostSock := client.Host().UnixSocket(sockPath)
	containerSock := "/tmp/.shipyard-shell.sock"

	_, err = ctr.
		WithFile("/usr/local/bin/shipyard", selfFile, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithEnvVariable("SHIPYARD_IN_CONTAINER", "1").
		WithEnvVariable("TERM", term).
		WithVolatileVariable("SHIPYARD_SHELL_NONCE", fmt.Sprintf("%d", time.Now().UnixNano())).
		WithUnixSocket(containerSock, hostSock).
		WithExec([]string{"/usr/local/bin/shipyard", "shell", containerSock}).
		Sync(ctx)

	_ = ln.Close()
	<-sessionDone
	restoreTermios(stdinFd, origTerm)

	if err != nil {
		var execErr *dagger.ExecError
		if errors.As(err, &execErr) {
			if execErr.Stderr != "" {
				fmt.Fprint(w, execErr.Stderr)
			}
			if execErr.Stdout != "" {
				fmt.Fprint(w, execErr.Stdout)
			}
		}
		fmt.Fprintf(w, "\n[!] Interactive shell exited with error: %v\n", err)
	}
}
