//go:build !windows

package runner

import (
	"errors"
	"os/exec"
	"syscall"
)

// configureProcess startet den Runner in einer eigenen Prozessgruppe, damit sich die ganze
// Gruppe (Runner, CLI und deren Kindprozesse) beenden lässt.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup sendet sig an die Prozessgruppe des gestarteten Prozesses (Gruppen-ID = PID).
func signalGroup(cmd *exec.Cmd, kill bool) {
	if cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		// Gruppe nicht erreichbar: wenigstens den Prozess selbst beenden.
		_ = cmd.Process.Signal(sig)
	}
}
