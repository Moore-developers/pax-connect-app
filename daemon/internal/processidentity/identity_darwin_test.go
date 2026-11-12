package processidentity

import (
	"os"
	"testing"
)

func TestKernelIdentity(t *testing.T) {
	i, err := Read(os.Getpid())
	if err != nil || int(i.UID) != os.Getuid() || i.Seconds == 0 || !Alive(i) {
		t.Fatalf("%+v %v", i, err)
	}
	i.Micros++
	if Alive(i) {
		t.Fatal("birth time mismatch accepted")
	}
	if _, err = Read(-1); err == nil {
		t.Fatal("invalid PID accepted")
	}
}
