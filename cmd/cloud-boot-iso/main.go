// cloud-boot-iso is a standalone cobra CLI that assembles a hybrid
// iso9660 + GPT bootable image carrying one or more per-arch
// PE32+/EFI applications under \EFI\BOOT\BOOT<ARCH>.EFI.
//
//	cloud-boot-iso \
//	    --uki linux/amd64=BOOTX64.EFI \
//	    --uki linux/arm64=BOOTAA64.EFI \
//	    --uki linux/riscv64=BOOTRISCV64.EFI \
//	    --uki linux/loong64=BOOTLOONGARCH64.EFI \
//	    --output boot.iso
//
// The flag name (--uki) is retained for compatibility with the
// historical cloud-boot/uki invocation; the input does NOT need to be
// a UKI — any PE32+/EFI binary works. The library underneath
// (cloud-boot/iso/pkg/iso) ships the actual assembly logic; this
// command is a thin parser + glue layer.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cloud-boot/iso/pkg/iso"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "cloud-boot-iso:", err)
		os.Exit(1)
	}
}

// newRootCmd builds the cobra tree fresh on every call so tests can
// drive it without leaking state between cases.
func newRootCmd() *cobra.Command {
	var (
		ukiFlags []string
		out      string
	)
	c := &cobra.Command{
		Use:   "cloud-boot-iso",
		Short: "Assemble a multi-arch hybrid ISO from per-arch PE32+/EFI binaries",
		Long: `Pack one or more already-built PE32+/EFI applications into a single
hybrid iso9660 + El Torito + GPT image.

Each input is dropped into the ESP at the UEFI removable-media
fallback path for its architecture (\EFI\BOOT\BOOTX64.EFI for amd64,
\EFI\BOOT\BOOTAA64.EFI for arm64, \EFI\BOOT\BOOTRISCV64.EFI for
riscv64, \EFI\BOOT\BOOTLOONGARCH64.EFI for loong64/loongarch64).
Firmware on each CPU reads only its own arch's file, so the same
ISO boots on any of the supported CPUs.

Example:

  cloud-boot-iso \
    --uki linux/amd64=boot-amd64.efi \
    --uki linux/arm64=boot-arm64.efi \
    --uki linux/riscv64=boot-riscv64.efi \
    --output boot.iso

The flag is named --uki for backwards compatibility with cloud-boot's
historical CLI; the per-arch input may be any PE32+/EFI binary —
a UKI (kernel + initrd + cmdline wrapped in systemd's EFI stub) OR a
standalone EFI app (no kernel, no initrd, e.g. cloud-boot/tamago-uefi
output). The library does not care about the inner shape.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(ukiFlags) == 0 {
				return fmt.Errorf("at least one --uki linux/<arch>=<path> is required")
			}
			arches, err := parseUKIList(ukiFlags)
			if err != nil {
				return err
			}
			return iso.BuildMultiArchISO(arches, out)
		},
	}
	f := c.Flags()
	f.StringArrayVar(&ukiFlags, "uki", nil, "per-arch EFI binary as linux/<arch>=<path>; repeat for each arch (amd64, arm64, riscv64, loong64/loongarch64)")
	f.StringVarP(&out, "output", "o", "boot.iso", "output ISO path")
	return c
}

// parseUKIList turns the repeated --uki flag values into ArchEFIs.
// Each entry is "linux/<arch>=<path>"; the arch is validated against
// iso.ArchEFIName for early diagnostics (so an unknown arch fails at
// CLI-parse time, not after the ISO assembly tools have been
// touched).
func parseUKIList(raw []string) ([]iso.ArchEFI, error) {
	out := make([]iso.ArchEFI, 0, len(raw))
	for _, entry := range raw {
		key, path, ok := strings.Cut(entry, "=")
		if !ok || key == "" || path == "" {
			return nil, fmt.Errorf("invalid --uki %q (want linux/<arch>=<path>)", entry)
		}
		_, arch, ok := strings.Cut(key, "/")
		if !ok || arch == "" {
			return nil, fmt.Errorf("invalid --uki platform %q (want linux/<arch>)", key)
		}
		if _, err := iso.ArchEFIName(arch); err != nil {
			return nil, err
		}
		out = append(out, iso.ArchEFI{Arch: arch, Path: path})
	}
	return out, nil
}
