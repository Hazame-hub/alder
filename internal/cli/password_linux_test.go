package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPTY opens a pseudo-terminal: the side a person types on (master) and
// the side the program reads (tty).
func openPTY(t *testing.T) (master, tty *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("cannot unlock the pseudo-terminal: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Skipf("cannot name the pseudo-terminal: %v", err)
	}
	tty, err = os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("cannot open the pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = tty.Close() })
	return master, tty
}

func echoing(t *testing.T, tty *os.File) bool {
	t.Helper()
	tios, err := unix.IoctlGetTermios(int(tty.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	return tios.Lflag&unix.ECHO != 0
}

// waitFor reads what the terminal shows until it holds want.
func waitFor(t *testing.T, master *os.File, want string) string {
	t.Helper()
	var seen bytes.Buffer
	deadline := time.Now().Add(5 * time.Second)
	_ = master.SetReadDeadline(deadline)
	buf := make([]byte, 256)
	for !strings.Contains(seen.String(), want) {
		n, err := master.Read(buf)
		seen.Write(buf[:n])
		if err != nil {
			t.Fatalf("the terminal showed %q, never %q: %v", seen.String(), want, err)
		}
	}
	return seen.String()
}

func TestTheTerminalDoesNotShowThePasswordAndGetsItsEchoBack(t *testing.T) {
	master, tty := openPTY(t)
	if !echoing(t, tty) {
		t.Fatal("a new terminal should echo")
	}
	var out bytes.Buffer
	done := make(chan string, 1)
	go func() {
		got, err := askTerminal(context.Background(), tty, &out, "Password for cn=admin: ")
		if err != nil {
			t.Error(err)
		}
		done <- got
	}()
	// The question goes to out; the terminal itself shows only what is
	// echoed, so wait for echo to be off before typing.
	for deadline := time.Now().Add(5 * time.Second); echoing(t, tty); {
		if time.Now().After(deadline) {
			t.Fatal("echo was never turned off")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := master.WriteString(testSecret + "\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got != testSecret {
			t.Errorf("read %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the password was never read")
	}
	if out.String() != "Password for cn=admin: \n" {
		t.Errorf("wrote %q", out.String())
	}
	if !echoing(t, tty) {
		t.Fatal("echo was not restored")
	}
	// What is typed now is echoed, and nothing before it was.
	if _, err := master.WriteString("after\n"); err != nil {
		t.Fatal(err)
	}
	if shown := waitFor(t, master, "after"); strings.Contains(shown, testSecret) {
		t.Errorf("the terminal showed the password: %q", shown)
	}
}

// Ctrl+C cancels the command's context rather than ending the process, so an
// interrupted question has to give the terminal its echo back itself.
func TestAnInterruptedQuestionGivesTheEchoBack(t *testing.T) {
	master, tty := openPTY(t)
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := askTerminal(ctx, tty, &out, "Password for cn=admin: ")
		done <- err
	}()
	for deadline := time.Now().Add(5 * time.Second); echoing(t, tty); {
		if time.Now().After(deadline) {
			t.Fatal("echo was never turned off")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		var exit *ExitError
		if !errors.As(err, &exit) || exit.Local != "interrupted" {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the question did not end when the context did")
	}
	if !echoing(t, tty) {
		t.Fatal("echo was not restored after the interrupt")
	}
	// Let the abandoned read finish.
	_, _ = master.WriteString("\n")
}

func TestAPipeIsNotATerminalToAsk(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	var out bytes.Buffer
	if _, err := askTerminal(context.Background(), r, &out, "Password: "); !errors.Is(err, errCannotAsk) {
		t.Errorf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("asked anyway: %q", out.String())
	}
}
