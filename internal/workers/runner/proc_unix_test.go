//go:build !windows

package runner

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

func TestContextCancelKillsTheProcessGroupAfterTheGracePeriod(t *testing.T) {
	h := newHarness(t, "ignore-stop") // ignoriert stop und SIGTERM, hat einen Kindprozess in derselben Gruppe
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := h.r.Start(ctx, testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Runner und Kindprozess laufen", func() bool { return h.exists("main.pid") && h.exists("child.pid") })
	mainPID := pidOf(t, h.read("main.pid"))
	childPID := pidOf(t, h.read("child.pid"))
	if !pidAlive(mainPID) || !pidAlive(childPID) {
		t.Fatal("Vorbedingung: beide Prozesse laufen")
	}
	began := time.Now()
	cancel()
	exec := waitExec(t, p, 15*time.Second)
	if time.Since(began) > 10*time.Second {
		t.Errorf("Beenden dauerte %v", time.Since(began))
	}
	if !exec.StopSent || !exec.ForcedKill {
		t.Errorf("StopSent %v, ForcedKill %v", exec.StopSent, exec.ForcedKill)
	}
	if !exec.Crashed || exec.State != domain.ExecCrashed {
		t.Errorf("ohne Ergebnis beendet: State %s, Crashed %v", exec.State, exec.Crashed)
	}
	if exec.ExitCode != -1 {
		t.Errorf("ExitCode = %d (Signal erwartet)", exec.ExitCode)
	}
	if got := h.read("commands.txt"); !strings.Contains(got, "stop") {
		t.Errorf("stop wurde vor dem Beenden nicht gesendet: %q", got)
	}
	waitFor(t, "keine verwaisten Prozesse", func() bool { return !pidAlive(mainPID) && !pidAlive(childPID) })
}

func pidOf(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStopDoesNotKillTheGroupBeforeTheGracePeriod(t *testing.T) {
	h := newHarness(t, "wait-stop", func(c *Config) { c.StopGrace = time.Minute })
	p, err := h.r.Start(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Zustand running", func() bool { return p.State() == domain.ExecRunning })
	p.Stop(domain.ReasonUserRequest)
	exec := waitExec(t, p, 10*time.Second)
	if exec.ForcedKill || exec.State != domain.ExecCancelled {
		t.Errorf("ForcedKill %v, State %s", exec.ForcedKill, exec.State)
	}
	_ = os.Stdout
}
