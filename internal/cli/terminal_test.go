package cli

import (
	"io"
	"os"
	"runtime"
	"testing"
)

func TestRedirectedCharacterDeviceIsNotTerminal(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// The name must not affect detection: os.Stdout is named /dev/stdout
	// even when its descriptor is redirected to a non-terminal device.
	stdout := os.NewFile(f.Fd(), "/dev/stdout")
	// f owns this borrowed descriptor and closes it above.
	runtime.SetFinalizer(stdout, nil)
	if New("test", stdout, io.Discard).isTTY {
		t.Fatal("redirected character device classified as a terminal")
	}
}

func TestPipeIsNotTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if New("test", w, io.Discard).isTTY {
		t.Fatal("pipe classified as a terminal")
	}
}
