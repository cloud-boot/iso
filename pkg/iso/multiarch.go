// Package iso assembles a hybrid iso9660 + GPT bootable image whose EFI
// System Partition carries one or more PE32+/EFI applications under the
// UEFI removable-media fallback paths (\EFI\BOOT\BOOT<ARCH>.EFI).
//
// UEFI firmware on every CPU only ever reads the file under
// \EFI\BOOT\ that matches its own arch — amd64 firmware reads BOOTX64.EFI
// and ignores BOOTAA64.EFI sitting next to it, and vice-versa. So a
// single ESP can carry several per-arch PE32+ binaries at the canonical
// removable-media fallback paths, and the same ISO boots on any of the
// supported CPUs.
//
// The library is shape-agnostic: it works on any PE32+/EFI input. The
// caller is responsible for producing the per-arch .efi files —
// they may be UKIs (kernel + initrd + cmdline wrapped in systemd's
// EFI stub, as built by cloud-boot/uki) or standalone EFI apps with no
// kernel and no initrd (as built by cloud-boot/tamago-uefi).
package iso

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ArchEFI is one entry in a multi-arch ISO build: a per-arch PE32+/EFI
// application that's already been built (e.g. by running
// `cloud-boot build --arch <a> --uki-only` for each arch on the
// UKI side, or building the tamago-uefi board package on the
// standalone side).
type ArchEFI struct {
	// Arch is the GOARCH-style key — one of "amd64", "arm64",
	// "riscv64", "loong64". The library translates this into the
	// UEFI removable-media fallback filename via ArchEFIName.
	Arch string
	// Path is the path to the PE32+ .efi binary on disk.
	Path string
}

// ArchEFIName maps a GOARCH-style key onto the UEFI removable-media
// fallback filename firmware looks for under \EFI\BOOT\. Exposed so
// callers (CLIs, test drivers) can resolve the canonical name
// without re-implementing the mapping.
//
// Keep in sync with the UEFI spec table of well-known arch suffixes:
//
//	amd64       → BOOTX64.EFI         (x86_64)
//	arm64       → BOOTAA64.EFI        (aarch64)
//	riscv64     → BOOTRISCV64.EFI
//	loong64     → BOOTLOONGARCH64.EFI
//	loongarch64 → BOOTLOONGARCH64.EFI (alias)
var archEFINames = map[string]string{
	"amd64":       "BOOTX64.EFI",
	"arm64":       "BOOTAA64.EFI",
	"riscv64":     "BOOTRISCV64.EFI",
	"loong64":     "BOOTLOONGARCH64.EFI",
	"loongarch64": "BOOTLOONGARCH64.EFI",
}

// ArchEFIName returns the canonical \EFI\BOOT filename for the given
// GOARCH-style key. An error is returned for an unrecognised arch
// (so the caller can surface a meaningful diagnostic instead of
// silently producing a malformed ISO).
func ArchEFIName(arch string) (string, error) {
	name, ok := archEFINames[arch]
	if !ok {
		return "", fmt.Errorf("unsupported arch %q (allowed: amd64, arm64, riscv64, loong64/loongarch64)", arch)
	}
	return name, nil
}

// SupportedArches lists every GOARCH-style key ArchEFIName understands.
// Useful for CLIs that want to surface "allowed: …" in error messages.
func SupportedArches() []string {
	out := make([]string, 0, len(archEFINames))
	for k := range archEFINames {
		out = append(out, k)
	}
	return out
}

// Indirections used by tests to stand in for external binaries.
var (
	lookPath = exec.LookPath
	cmdRun   = func(name string, args ...string) error {
		c := exec.Command(name, args...)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		return c.Run()
	}
)

// BuildMultiArchISO produces one hybrid iso9660 + El Torito + GPT image
// whose ESP carries every binary in `arches` under
// \EFI\BOOT\BOOT<ARCH>.EFI. UEFI firmware on each CPU reads only its
// own arch's file, so the same ISO is bootable on amd64, arm64,
// riscv64 and loong64 hosts (subject to the per-arch firmware actually
// being available at boot time — that's the firmware's problem, not
// ours).
//
// The required external tools are mformat, mmd, mcopy and xorriso.
// They're looked up via the host PATH; nothing is vendored.
func BuildMultiArchISO(arches []ArchEFI, output string) error {
	if len(arches) == 0 {
		return fmt.Errorf("no EFI inputs supplied")
	}
	for _, tool := range []string{"mformat", "mmd", "mcopy", "xorriso"} {
		if _, err := lookPath(tool); err != nil {
			return fmt.Errorf("required tool %q not in PATH", tool)
		}
	}
	// Resolve every entry to (efiName, path) and check for duplicates
	// up-front so the error path is "before we touched any disk".
	resolved := make([]struct {
		EFIName string
		Path    string
		Arch    string
	}, 0, len(arches))
	seen := make(map[string]string, len(arches))
	for _, a := range arches {
		efiName, err := ArchEFIName(a.Arch)
		if err != nil {
			return err
		}
		if prev, ok := seen[efiName]; ok {
			return fmt.Errorf("duplicate EFI input for %s: %q and %q", efiName, prev, a.Path)
		}
		if _, err := os.Stat(a.Path); err != nil {
			return fmt.Errorf("efi %s: %w", a.Path, err)
		}
		seen[efiName] = a.Path
		resolved = append(resolved, struct {
			EFIName string
			Path    string
			Arch    string
		}{efiName, a.Path, a.Arch})
	}

	wd, err := os.MkdirTemp("", "cloud-boot-iso-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(wd)
	log.Printf("multi-arch ISO workdir: %s", wd)

	esp := filepath.Join(wd, "efiboot.img")
	if err := buildESP(resolved, esp); err != nil {
		return err
	}
	if err := buildISO(esp, output); err != nil {
		return err
	}
	names := make([]string, 0, len(resolved))
	for _, r := range resolved {
		names = append(names, r.Arch)
	}
	log.Printf("built multi-arch %s (%s)", output, strings.Join(names, ", "))
	return nil
}

// buildESP drops every per-arch .efi into a single FAT image at the
// corresponding \EFI\BOOT\<EFIName> path. The image size is computed
// from the total .efi byte total with 50% headroom plus an 8 MiB FAT
// floor, then rounded up to the next 16 MiB so mformat picks a
// sensible cluster geometry.
//
// The FAT type is left to mformat (no -F): for a small ESP (~16 MiB,
// the common single-/dual-arch case) forcing FAT32 yields fewer than
// the 65525 clusters the FAT32 spec requires, so mformat emits a
// sub-spec FAT32 that UEFI firmware (e.g. OVMF) refuses to mount —
// the image then "fails to load / Not Found" at boot. Letting mformat
// auto-select gives a spec-valid FAT16 for small ESPs (which UEFI
// supports on removable/ISO media) and FAT32 once the volume is large
// enough. Verified bootable under QEMU/OVMF.
func buildESP(entries []struct {
	EFIName string
	Path    string
	Arch    string
}, out string) error {
	const (
		floor  = 8 * 1024 * 1024
		round  = 16 * 1024 * 1024
		hdroom = 3 // multiplied / 2 → 1.5x total efi bytes
	)
	var total int64
	for _, e := range entries {
		st, err := os.Stat(e.Path)
		if err != nil {
			return err
		}
		total += st.Size()
	}
	size := total*int64(hdroom)/2 + floor
	if size%round != 0 {
		size = ((size / round) + 1) * round
	}
	log.Printf("creating ESP image -> %s (%d MiB, %d EFIs)", out, size/(1024*1024), len(entries))
	if err := truncate(out, size); err != nil {
		return err
	}
	if err := run("mformat", "-i", out, "::"); err != nil {
		return err
	}
	if err := run("mmd", "-i", out, "::/EFI"); err != nil {
		return err
	}
	if err := run("mmd", "-i", out, "::/EFI/BOOT"); err != nil {
		return err
	}
	for _, e := range entries {
		if err := run("mcopy", "-i", out, e.Path, "::/EFI/BOOT/"+e.EFIName); err != nil {
			return fmt.Errorf("mcopy %s: %w", e.EFIName, err)
		}
		log.Printf("  + /EFI/BOOT/%-22s (%s)", e.EFIName, e.Path)
	}
	return nil
}

// buildISO produces a hybrid iso9660 + El Torito + GPT image
// (xorriso's -append_partition recipe). The FAT image `esp` is
// appended at the tail and exposed as GPT partition 2 with the
// EFI System Partition type GUID; El Torito's boot catalog
// references the same byte range via
// `--interval:appended_partition_2:all::`. UEFI firmware boots
// `\EFI\BOOT\BOOT<ARCH>.EFI` from the FAT either way (El Torito
// path or GPT-ESP path).
//
// Why hybrid (not pure iso9660): Apple Virtualization.framework
// rejects every storage device whose first sector lacks the MBR
// signature 0x55 0xAA at offset 510 ("Invalid virtual machine
// configuration. The storage device attachment is invalid.").
// Pure-iso9660 images have zeros there and get refused at vfkit
// launch. The hybrid layout writes a protective MBR + GPT, which
// contains the signature and satisfies VZ.
func buildISO(esp, out string) error {
	log.Printf("creating ISO -> %s (hybrid iso9660 + El Torito + GPT)", out)
	if _, err := os.Stat(esp); err != nil {
		return fmt.Errorf("esp image: %w", err)
	}
	stage, err := os.MkdirTemp("", "iso-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	readme := filepath.Join(stage, "README.txt")
	if err := os.WriteFile(readme, []byte("cloud-boot multi-arch bootable image — boot files live in GPT partition 2.\n"), 0o644); err != nil {
		return err
	}
	return run("xorriso",
		"-as", "mkisofs",
		"-V", "CLOUDBOOT",
		"-o", out,
		"-no-emul-boot",
		"-e", "--interval:appended_partition_2:all::",
		"-append_partition", "2", "C12A7328-F81F-11D2-BA4B-00A0C93EC93B", esp,
		"-appended_part_as_gpt",
		stage,
	)
}

// truncate creates `path` (overwriting if present) and resizes it to
// exactly `size` bytes — the FAT mformat consumes next.
func truncate(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(size)
}

func run(name string, args ...string) error {
	return cmdRun(name, args...)
}
