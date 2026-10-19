//go:build unix

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// The first signal ends the worker's context; a second one ends the
// process at once, even while a handler does not return. The test runs
// itself as a child process (signalHelper) that listens, then hangs.
func TestASecondSignalEndsTheProcess(t *testing.T) {
	if os.Getenv("GEMINUS_SIGNAL_HELPER") == "1" {
		signalHelper()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestASecondSignalEndsTheProcess$")
	cmd.Env = append(os.Environ(), "GEMINUS_SIGNAL_HELPER=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(out)
	for _, want := range []string{"listening", "stopping"} {
		if !lines.Scan() || lines.Text() != want {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatalf("the helper did not say %q (got %q)", want, lines.Text())
		}
		if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("the helper ended with %v", err)
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("the second signal did not end the process: %v", err)
	}
}

// signalHelper listens as main does, then hangs as a handler that does not
// return would.
func signalHelper() {
	ctx, stop := listen()
	defer stop()
	fmt.Println("listening")
	<-ctx.Done()
	fmt.Println("stopping")
	time.Sleep(time.Minute)
	fmt.Println("still running")
	os.Exit(0)
}
