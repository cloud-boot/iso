# cloud-boot/iso

Pure-Go assembler for **multi-arch bootable hybrid iso9660 + GPT ISOs**
that carry one or more PE32+/EFI applications under the UEFI
removable-media fallback paths, plus a **pure-Go QEMU/EDK2 boot
harness** for end-to-end boot tests across `amd64`, `arm64`,
`riscv64`, and `loong64`.

```text
boot.iso  ->  GPT partition 2 (FAT16/32 ESP)
                 \EFI\BOOT\BOOTX64.EFI         <- amd64 firmware reads this
                 \EFI\BOOT\BOOTAA64.EFI        <- arm64 firmware reads this
                 \EFI\BOOT\BOOTRISCV64.EFI     <- riscv64 firmware reads this
                 \EFI\BOOT\BOOTLOONGARCH64.EFI <- loong64 firmware reads this
```

Firmware on each CPU reads only its own arch's file, so the same ISO
is bootable on any of the supported CPUs.

This repo is **shape-agnostic**: the per-arch `.efi` inputs can be
anything the UEFI loader accepts at the removable-media fallback path.
Two typical producers in the cloud-boot org:

| Producer              | Shape of the per-arch `.efi`                               |
| --------------------- | ---------------------------------------------------------- |
| `cloud-boot/uki`      | UKI = Linux kernel + initrd + cmdline wrapped in systemd's EFI stub (`.linux` + `.initrd` + `.cmdline` PE sections). |
| `cloud-boot/tamago-uefi` | Standalone PE32+/EFI Go unikernel built with the TamaGo runtime (`GOOS=tamago`, `CGO_ENABLED=0`). No kernel, no initrd. |

This repo doesn't care which one you feed it. The library
(`pkg/iso.BuildMultiArchISO`) just packs the bytes into an ESP and
emits a hybrid ISO; the harness (`pkg/multiarchboot.Run`) just spawns
`qemu-system-<arch>` with the matching EDK2 firmware and looks for
operator-supplied banner substrings in the captured serial stream.

`cloud-boot/uki`'s historical `iso` subcommand + 4-arch boot test
infrastructure (commit `9a6b879`) moved here on 2026-06-07; the UKI
repo now focuses on its UKI-builder role.

## Layout

| Path                         | Role |
| ---------------------------- | ---- |
| `pkg/iso/`                   | Library — `BuildMultiArchISO([]ArchEFI, string)` assembles the hybrid ISO. Also exports `ArchEFIName` / `SupportedArches`. |
| `pkg/multiarchboot/`         | QEMU/EDK2 boot harness — `DefaultArches()`, `Run(ctx, iso, arches, timeout)`, `FormatReport([]Result)`, env-var-overridable firmware paths. |
| `cmd/cloud-boot-iso/`        | Thin cobra CLI wrapping `pkg/iso`. Parses repeated `--uki linux/<arch>=<path>` flags into `ArchEFI`s. |
| `Taskfile.yaml`              | `task build`, `task iso:multiarch`, `task test:multiarch:boot`. |

External tooling (consumed via host PATH; nothing is vendored):

| Tool       | Used for                                                      |
| ---------- | ------------------------------------------------------------- |
| `mformat`, `mmd`, `mcopy` | FAT ESP image assembly (mtools).                |
| `xorriso`  | Hybrid iso9660 + El Torito + GPT image emission.              |
| `qemu-system-<arch>` | Per-arch boot tests (test:multiarch:boot only).     |
| EDK2 OVMF firmware images | Per-arch boot tests (env-var-supplied paths).    |

`CGO_ENABLED=0`, no vendoring — every external is invoked via
`exec.LookPath`.

## Build

```sh
task build           # -> bin/cloud-boot-iso
```

## Assemble a multi-arch ISO

```sh
cloud-boot-iso \
    --uki linux/amd64=BOOTX64.EFI \
    --uki linux/arm64=BOOTAA64.EFI \
    --uki linux/riscv64=BOOTRISCV64.EFI \
    --uki linux/loong64=BOOTLOONGARCH64.EFI \
    --output boot.iso
```

Or via `task` (defaults point at `../tamago-uefi/`'s four outputs):

```sh
task iso:multiarch
# -> ./bin/cloud-boot-iso \
#      --uki linux/amd64=../tamago-uefi/BOOTX64.EFI \
#      --uki linux/arm64=../tamago-uefi/BOOTAA64.EFI \
#      --uki linux/loongarch64=../tamago-uefi/BOOTLOONGARCH64.EFI \
#      --uki linux/riscv64=../tamago-uefi/BOOTRISCV64.EFI \
#      --output boot-multi.iso
```

Override per-EFI vars (`EFI_AMD64=… EFI_ARM64=… EFI_LOONG64=…
EFI_RISCV64=…`) if the inputs live elsewhere. The output path
defaults to `boot-multi.iso`; override with `MULTI_ISO=…`.

The flag is named `--uki` for backwards compatibility with the
original cloud-boot CLI; the per-arch input may be any PE32+/EFI
binary — a UKI OR a standalone EFI app.

Inspect the result:

```sh
xorriso -indev boot-multi.iso -toc          # boot record + ESP partition
mdir -i <(dd if=boot-multi.iso bs=512 skip=140) -/ ::/EFI/BOOT
```

You should see all four `BOOTX64.EFI`, `BOOTAA64.EFI`,
`BOOTLOONGARCH64.EFI`, `BOOTRISCV64.EFI` files in the ESP.

## Boot-test each arch under QEMU + EDK2

```sh
task test:multiarch:boot
```

This wraps the pure-Go test driver in `pkg/multiarchboot`. For each
arch in `DefaultArches()` it spawns `qemu-system-<arch>` with the
matching EDK2 firmware and asserts the captured serial output
contains the operator-supplied banner substrings (defaults match the
`cloud-boot/tamago-uefi` banner):

```text
hello from cloud-boot tamago/<arch> UEFI board
runtime: go1.26.3 GOOS=tamago GOARCH=<arch>
goroutine sum: 499500
DONE - halting
```

Each arch is skip-gated on its OVMF firmware env var being set to
an existing file — the suite stays green on hosts that don't ship
one of the four firmwares. Defaults assume homebrew's qemu bottle
on macOS:

| Env var                       | Default                                                          | Required? |
| ----------------------------- | ---------------------------------------------------------------- | --------- |
| `CLOUDBOOT_OVMF_AMD64_CODE`   | `$QEMU_EDK2_DIR/edk2-x86_64-code.fd`                             | always    |
| `CLOUDBOOT_OVMF_AMD64_VARS`   | `$QEMU_EDK2_DIR/edk2-i386-vars.fd`                               | always    |
| `CLOUDBOOT_OVMF_ARM64_CODE`   | `$QEMU_EDK2_DIR/edk2-aarch64-code.fd`                            | always    |
| `CLOUDBOOT_OVMF_LOONG64_CODE` | `$QEMU_EDK2_DIR/edk2-loongarch64-code.fd`                        | always    |
| `CLOUDBOOT_OVMF_RISCV64_CODE` | `$QEMU_EDK2_DIR/edk2-riscv-code.fd`                              | always    |
| `CLOUDBOOT_OVMF_RISCV64_VARS` | `$QEMU_EDK2_DIR/edk2-riscv-vars.fd`                              | always    |
| `QEMU_EDK2_DIR`               | `/opt/homebrew/Cellar/qemu/10.2.2/share/qemu`                    | macOS     |
| `CLOUDBOOT_BOOT_TIMEOUT`      | `60s` (per arch)                                                 | optional  |
| `CLOUDBOOT_TEST_ISO`          | none — set to the path of `boot-multi.iso`                       | always    |

Override `QEMU_EDK2_DIR` to point at any other directory holding
the six `edk2-*.fd` images (Debian: `/usr/share/qemu/`; Arch:
`/usr/share/edk2/...`; Fedora: `/usr/share/edk2/...`).

### Where to get OVMF binaries

- **macOS** — `brew install qemu` ships `edk2-{x86_64,aarch64,
  loongarch64,riscv}-code.fd` and `edk2-{i386,arm,loongarch64,
  riscv}-vars.fd` under
  `/opt/homebrew/Cellar/qemu/<ver>/share/qemu/`.
- **Debian / Ubuntu** — `apt install ovmf qemu-efi-aarch64`. The
  aarch64 code lives at `/usr/share/AAVMF/AAVMF_CODE.fd`; amd64
  at `/usr/share/OVMF/OVMF_CODE.fd`. loongarch64 + riscv64 ship
  with `qemu-efi-loongarch64` / `qemu-efi-riscv64` in recent
  releases (Debian trixie / Ubuntu 24.04+); otherwise pull
  prebuilt `edk2-stable*` images or build EDK2 from source for
  the missing arches.
- **Arch / Fedora** — `pacman -S edk2-aarch64 edk2-x86_64 edk2-
  loongarch64 edk2-riscv64-virt` (or the Fedora `edk2-*` package
  set).

### Known per-arch status (QEMU 10.2.2 + EDK2 stable202408)

| Arch     | Firmware                            | Status                                                 |
| -------- | ----------------------------------- | ------------------------------------------------------ |
| amd64    | `edk2-x86_64-code.fd` + vars        | PASS — full banner over serial                         |
| arm64    | `edk2-aarch64-code.fd`              | PASS — full banner over serial                         |
| loong64  | `edk2-loongarch64-code.fd`          | PASS — full banner over serial                         |
| riscv64  | `edk2-riscv-code.fd` + vars         | PASS — banner shown                                    |

If the RISC-V firmware bug noted in tamago-uefi's README
(`SetUefiImageMemoryAttributes` fault under earlier
`edk2-stable202408` snapshots) reproduces on a different EDK2 build,
flip the `ExpectFail` field on the riscv64 entry in
`pkg/multiarchboot/multiarchboot.go` to keep the suite green.

## Library API

```go
import "github.com/cloud-boot/iso/pkg/iso"

err := iso.BuildMultiArchISO([]iso.ArchEFI{
    {Arch: "amd64",   Path: "BOOTX64.EFI"},
    {Arch: "arm64",   Path: "BOOTAA64.EFI"},
    {Arch: "riscv64", Path: "BOOTRISCV64.EFI"},
    {Arch: "loong64", Path: "BOOTLOONGARCH64.EFI"},
}, "boot.iso")
```

```go
import (
    "context"
    "time"

    "github.com/cloud-boot/iso/pkg/multiarchboot"
)

ctx := multiarchboot.WithLogf(context.Background(), multiarchboot.Logf(t.Logf))
results, err := multiarchboot.Run(ctx, "boot.iso", nil, 60*time.Second)
// results[i].Status is one of "pass" | "fail" | "skip" | "xfail" | "xpass".
// Use multiarchboot.FormatReport(results) for a human-readable table.
```

## License

[BSD 3-Clause](./LICENSE).
