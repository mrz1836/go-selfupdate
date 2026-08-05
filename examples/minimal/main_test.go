package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestRootCommandWiring pins that the example wires both halves of the
// library. A README snippet can drift silently; an example that is
// compiled and exercised cannot.
func TestRootCommandWiring(t *testing.T) {
	root := newRootCommand()

	want := map[string]bool{"version": false, "update": false}
	for _, cmd := range root.Commands() {
		if _, ok := want[cmd.Name()]; ok {
			want[cmd.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("the example registers no %q command", name)
		}
	}

	// AttachBanner installs a persistent pre-run hook on the root.
	if root.PersistentPreRunE == nil {
		t.Error("no persistent pre-run hook; the passive notice is not attached")
	}
}

// TestUpdateCommandFlags pins the flag shape a user of the example sees.
func TestUpdateCommandFlags(t *testing.T) {
	update := commandNamed(t, "update")

	for _, name := range []string{"force", "check", "verbose"} {
		if update.Flags().Lookup(name) == nil {
			t.Errorf("the update command does not register --%s", name)
		}
	}
	if len(update.Aliases) == 0 {
		t.Error("the update command has no alias; both spellings should work")
	}
}

// TestVersionCommandPrintsVersion runs the example end to end, with the
// update check switched off so the test touches no network.
func TestVersionCommandPrintsVersion(t *testing.T) {
	t.Setenv("NO_UPDATE_CHECK", "1")

	root := newRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute version: %v", err)
	}
	if !strings.Contains(out.String(), binaryName) || !strings.Contains(out.String(), version) {
		t.Errorf("output = %q, want the binary name and version", out.String())
	}
}

// commandNamed returns the example's subcommand with the given name.
func commandNamed(t *testing.T, name string) *cobra.Command {
	t.Helper()

	for _, cmd := range newRootCommand().Commands() {
		if cmd.Name() == name {
			return cmd
		}
	}
	t.Fatalf("no %q command registered", name)
	return nil
}
