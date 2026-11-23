//go:build windows

package katago

import (
	"os"
	"os/exec"
	"time"
)

func configureProcessTermination(command *exec.Cmd) {
	command.WaitDelay = 2 * time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return command.Process.Kill()
	}
}
