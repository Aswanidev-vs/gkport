package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// TestMain re-execs the test binary as a throwaway TCP listener child process
// when GKPORT_TEST_LISTENER is set; otherwise it runs the tests normally.
func TestMain(m *testing.M) {
	if port := os.Getenv("GKPORT_TEST_LISTENER"); port != "" {
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			fmt.Fprintln(os.Stderr, "listener child failed:", err)
			os.Exit(1)
		}
		defer ln.Close()
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(m.Run())
}

// Regression test for the "(unknown)" bug: Windows releases a killed process's
// sockets asynchronously, so a rescan issued the instant the kill returns
// would still find the dead owner's LISTEN entry (empty name -> "(unknown)").
func TestKillPortByNumberWaitsForRelease(t *testing.T) {
	const port = 47911
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "GKPORT_TEST_LISTENER="+strconv.Itoa(port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start listener child: %v", err)
	}
	defer cmd.Process.Kill()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if found, _ := findListeningPort(port); found != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Skipf("listener on port %d never appeared (sandboxed environment?)", port)
		}
		time.Sleep(50 * time.Millisecond)
	}

	info, err := killPortByNumber(port)
	if err != nil {
		t.Fatalf("killPortByNumber(%d) failed: %v", port, err)
	}
	if info.Port != port {
		t.Fatalf("killed port %d, want %d", info.Port, port)
	}

	// The port must be gone by the time killPortByNumber returns, otherwise
	// the TUI's immediate rescan renders the dead PID as "(unknown)".
	if found, _ := findListeningPort(port); found != nil {
		t.Errorf("port %d still LISTENING after killPortByNumber returned", port)
	}
}
