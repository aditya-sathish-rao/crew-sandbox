package lima

import (
	"errors"
	"testing"
)

// A mount requested for an instance that exists without it must be refused, not
// silently dropped. Lima fixes mounts at creation time, so starting an existing
// instance cannot add one, and running the command without the workspace would
// be a quiet lie about what the guest can see.
func TestStartRefusesMountsOnExistingInstance(t *testing.T) {
	for _, status := range []Status{StatusRunning, StatusStopped} {
		g := Guest{
			Name:   "crew-test",
			Mounts: []Mount{{Location: "/tmp/repo", MountPoint: "/tmp/repo"}},
			// A limactl that reports the instance as existing with no mounts.
			Limactl: fakeLimactl(t, string(status)),
		}
		err := g.Start()
		if !errors.Is(err, ErrMountsNotApplied) {
			t.Fatalf("status %q: got %v, want ErrMountsNotApplied", status, err)
		}
	}
}

// A second run of the same workspace must work. The instance already carries the
// mount, so refusing would make every run after the first fail.
func TestStartAcceptsAlreadyAppliedMount(t *testing.T) {
	m := Mount{Location: "/tmp/repo", MountPoint: "/tmp/repo"}
	g := Guest{
		Name:    "crew-test",
		Mounts:  []Mount{m},
		Limactl: fakeLimactl(t, string(StatusRunning), m),
	}
	if err := g.Start(); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

// A read-only request is not satisfied by a writable mount: the guest would have
// more access than the operator asked for.
func TestStartRefusesWritableMountWhenReadOnlyRequested(t *testing.T) {
	g := Guest{
		Name:   "crew-test",
		Mounts: []Mount{{Location: "/tmp/repo", MountPoint: "/tmp/repo"}},
		Limactl: fakeLimactl(t, string(StatusRunning), Mount{
			Location: "/tmp/repo", MountPoint: "/tmp/repo", Writable: true,
		}),
	}
	if err := g.Start(); !errors.Is(err, ErrMountsNotApplied) {
		t.Fatalf("got %v, want ErrMountsNotApplied", err)
	}
}

// Without mounts, starting an existing instance is a no-op and must not error.
func TestStartWithoutMountsIsIdempotent(t *testing.T) {
	g := Guest{
		Name:    "crew-test",
		Limactl: fakeLimactl(t, string(StatusRunning)),
	}
	if err := g.Start(); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}
