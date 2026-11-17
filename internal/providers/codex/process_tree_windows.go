//go:build windows

package codex

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func configureProcessTree(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		killer := exec.Command("taskkill.exe", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		if output, err := killer.CombinedOutput(); err != nil {
			treeErr := fmt.Errorf("taskkill Codex process tree: %w: %s", err, output)
			killErr := cmd.Process.Kill()
			if errors.Is(killErr, os.ErrProcessDone) {
				return treeErr
			}
			if killErr != nil {
				return errors.Join(treeErr, killErr)
			}
			// Killing the parent process does not prove its descendants exited.
			return treeErr
		}
		return nil
	}
}
