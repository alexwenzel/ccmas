//go:build windows

package runner

import "os/exec"

// configureProcess: Unter Windows gibt es keine Prozessgruppen im Sinne von Unix; der Adapter
// beendet nur den gestarteten Prozess. Der Runner-Betrieb ist in diesem Stand für macOS und
// Linux vorgesehen (Windows baut, ist aber nicht belegt).
func configureProcess(cmd *exec.Cmd) {}

func signalGroup(cmd *exec.Cmd, kill bool) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
