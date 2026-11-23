//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package katago

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureProcessTermination(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
