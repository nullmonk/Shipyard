package incontainer

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"github.com/spf13/cobra"
)

const (
	shellMsgData   byte = 0
	shellMsgResize byte = 1

	ioctlTIOCGPTN   = 0x80045430
	ioctlTIOCSPTLCK = 0x40045431
)

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func newShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "shell <unix-socket>",
		Short:  "Internal helper to attach an interactive PTY shell over a forwarded Unix socket",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   runShell,
	}
}

func openPTY() (*os.File, *os.File, error) {
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	// Unlock PTY slave
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), ioctlTIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		ptmx.Close()
		return nil, nil, fmt.Errorf("TIOCSPTLCK: %w", errno)
	}

	// Get PTY slave number
	var ptn uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), ioctlTIOCGPTN, uintptr(unsafe.Pointer(&ptn))); errno != 0 {
		ptmx.Close()
		return nil, nil, fmt.Errorf("TIOCGPTN: %w", errno)
	}

	slavePath := fmt.Sprintf("/dev/pts/%d", ptn)
	tty, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		ptmx.Close()
		return nil, nil, fmt.Errorf("open %s: %w", slavePath, err)
	}

	return ptmx, tty, nil
}

func setPTYSize(ptmx *os.File, rows, cols uint16) {
	ws := winsize{Row: rows, Col: cols}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
}

func runShell(cmd *cobra.Command, args []string) error {
	sockPath := args[0]
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect to host shell socket %s: %w", sockPath, err)
	}
	defer conn.Close()

	ptmx, tty, err := openPTY()
	if err != nil {
		return err
	}
	defer ptmx.Close()

	shellBin := "/bin/bash"
	if _, err := os.Stat(shellBin); err != nil {
		shellBin = "/bin/sh"
	}

	sh := exec.Command(shellBin, "-i")
	sh.Stdin = tty
	sh.Stdout = tty
	sh.Stderr = tty
	sh.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}

	if err := sh.Start(); err != nil {
		tty.Close()
		return fmt.Errorf("start %s: %w", shellBin, err)
	}
	// Close slave fd in parent so ptmx receives EOF/EIO as soon as the shell exits.
	tty.Close()

	// Forward host -> PTY (framed messages: data or resize)
	go func() {
		hdr := make([]byte, 5)
		for {
			if _, err := io.ReadFull(conn, hdr); err != nil {
				_ = sh.Process.Signal(syscall.SIGHUP)
				return
			}
			msgType := hdr[0]
			length := binary.BigEndian.Uint32(hdr[1:5])
			if length == 0 {
				continue
			}
			payload := make([]byte, length)
			if _, err := io.ReadFull(conn, payload); err != nil {
				_ = sh.Process.Signal(syscall.SIGHUP)
				return
			}
			switch msgType {
			case shellMsgData:
				_, _ = ptmx.Write(payload)
			case shellMsgResize:
				if len(payload) >= 4 {
					rows := binary.BigEndian.Uint16(payload[0:2])
					cols := binary.BigEndian.Uint16(payload[2:4])
					setPTYSize(ptmx, rows, cols)
				}
			}
		}
	}()

	// Forward PTY -> host
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(conn, ptmx)
		if uc, ok := conn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
	}()

	_ = sh.Wait()
	_ = ptmx.Close()
	wg.Wait()
	return nil
}
