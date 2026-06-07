package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseUKIList_Valid(t *testing.T) {
	got, err := parseUKIList([]string{
		"linux/amd64=boot-amd64.efi",
		"linux/arm64=boot-arm64.efi",
		"linux/riscv64=boot-riscv64.efi",
		"linux/loong64=boot-loong64.efi",
		"linux/loongarch64=boot-loongarch64.efi",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d entries, want 5", len(got))
	}
	wantArches := []string{"amd64", "arm64", "riscv64", "loong64", "loongarch64"}
	wantPaths := []string{"boot-amd64.efi", "boot-arm64.efi", "boot-riscv64.efi", "boot-loong64.efi", "boot-loongarch64.efi"}
	for i, u := range got {
		if u.Arch != wantArches[i] {
			t.Errorf("[%d] Arch = %q, want %q", i, u.Arch, wantArches[i])
		}
		if u.Path != wantPaths[i] {
			t.Errorf("[%d] Path = %q, want %q", i, u.Path, wantPaths[i])
		}
	}
}

func TestParseUKIList_PreservesOrder(t *testing.T) {
	got, err := parseUKIList([]string{
		"linux/arm64=a.efi",
		"linux/amd64=b.efi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Arch != "arm64" || got[1].Arch != "amd64" {
		t.Errorf("order not preserved: %v", got)
	}
}

func TestParseUKIList_Errors(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{"no equals", "linux/amd64-foo.efi", "want linux/<arch>=<path>"},
		{"empty key", "=foo.efi", "want linux/<arch>=<path>"},
		{"empty path", "linux/amd64=", "want linux/<arch>=<path>"},
		{"no slash", "amd64=foo.efi", "want linux/<arch>"},
		{"empty arch", "linux/=foo.efi", "want linux/<arch>"},
		{"unknown arch", "linux/m68k=foo.efi", "unsupported arch"},
		{"sneaky arch", "linux/sparc64=foo.efi", "unsupported arch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseUKIList([]string{c.in})
			if err == nil {
				t.Fatalf("want error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err=%q, want substring %q", err.Error(), c.wantErr)
			}
		})
	}
}

func TestParseUKIList_EmptyList(t *testing.T) {
	got, err := parseUKIList(nil)
	if err != nil {
		t.Fatalf("nil input should produce empty list, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

// TestRootCmd_RejectsMissingUKIFlag wires up the cobra tree with no
// --uki flag and asserts the RunE produces the expected diagnostic
// (without actually invoking the ISO assembler, which would require
// mformat/xorriso on the host).
func TestRootCmd_RejectsMissingUKIFlag(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetOut(&stderr)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error, got nil; stderr=%q", stderr.String())
	}
	if !strings.Contains(err.Error(), "at least one --uki") {
		t.Errorf("err=%q, want 'at least one --uki' substring", err.Error())
	}
}

// TestRootCmd_PropagatesParseError checks that an invalid --uki value
// reaches the user via the cobra RunE return path.
func TestRootCmd_PropagatesParseError(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--uki", "linux/sparc64=foo.efi"})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetOut(&stderr)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported arch") {
		t.Errorf("err=%q, want 'unsupported arch'", err.Error())
	}
}
