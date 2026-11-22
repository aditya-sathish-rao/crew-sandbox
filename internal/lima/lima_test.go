package lima

import (
	"strings"
	"testing"
)

func TestMountOverrideReadOnly(t *testing.T) {
	got := mountOverride(Mount{Location: "/tmp/repo", MountPoint: "/tmp/repo"})
	want := `.mounts += [{"location": "/tmp/repo", "mountPoint": "/tmp/repo", "writable": false}]`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMountOverrideWritable(t *testing.T) {
	got := mountOverride(Mount{Location: "/tmp/repo", MountPoint: "/tmp/repo", Writable: true})
	if !strings.Contains(got, `"writable": true`) {
		t.Fatalf("writable mount not rendered as writable: %s", got)
	}
}

func TestMountOverrideEscapesQuotes(t *testing.T) {
	// A quote in a path must be escaped, or the yq expression would end the
	// string early and the mount would be silently dropped or misparsed.
	got := mountOverride(Mount{Location: `/tmp/a"b`, MountPoint: `/tmp/a"b`})
	if !strings.Contains(got, `a\"b`) {
		t.Fatalf("quote in path was not escaped: %s", got)
	}
}

func TestEmbeddedDefinitionHasNoMounts(t *testing.T) {
	// The boundary depends on the default definition mounting nothing. A mount
	// added to the embedded file would silently widen every guest.
	if !strings.Contains(string(definition), "mounts: []") {
		t.Fatal("embedded lima.yaml no longer declares `mounts: []`")
	}
}

func TestEmbeddedDefinitionHasNoPortForwards(t *testing.T) {
	// The definition must keep the ignore rule that closes every forwarded port.
	if !strings.Contains(string(definition), "ignore: true") {
		t.Fatal("embedded lima.yaml no longer ignores port forwards")
	}
}
