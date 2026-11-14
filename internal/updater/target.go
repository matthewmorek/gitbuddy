package updater

import (
	"debug/elf"
	"debug/macho"
	"errors"
)

type releaseTarget string

const (
	darwinARM64 releaseTarget = "darwin-arm64"
	linuxAMD64  releaseTarget = "linux-amd64"

	unsupportedTargetReason = "self-update requires macOS arm64 or Linux x86_64"
)

// supportedTarget is the shared policy for eligibility, asset names and the
// executable format checked before the downloaded release may run.
func supportedTarget(goos, goarch string) releaseTarget {
	switch {
	case goos == "darwin" && goarch == "arm64":
		return darwinARM64
	case goos == "linux" && goarch == "amd64":
		return linuxAMD64
	default:
		return ""
	}
}

func (target releaseTarget) assetName(version string) string {
	return "gitbuddy-" + version + "-" + string(target)
}

func (target releaseTarget) validateExecutable(path string) error {
	switch target {
	case darwinARM64:
		format, err := macho.Open(path)
		if err != nil {
			return errors.New("release is not a macOS executable")
		}
		defer format.Close()
		if format.Cpu != macho.CpuArm64 || format.Type != macho.TypeExec {
			return errors.New("release is not a macOS arm64 executable")
		}
	case linuxAMD64:
		format, err := elf.Open(path)
		if err != nil {
			return errors.New("release is not a Linux ELF executable")
		}
		defer format.Close()
		if format.Class != elf.ELFCLASS64 || format.Data != elf.ELFDATA2LSB ||
			format.Machine != elf.EM_X86_64 || format.Type != elf.ET_EXEC {
			return errors.New("release is not a Linux 64-bit little-endian x86_64 executable")
		}
		// Official CGO_ENABLED=0, non-PIE releases need neither a dynamic
		// interpreter nor dynamic linking metadata.
		for _, program := range format.Progs {
			if program.Type == elf.PT_INTERP || program.Type == elf.PT_DYNAMIC {
				return errors.New("release is not a statically linked Linux executable")
			}
		}
	default:
		return errors.New(unsupportedTargetReason)
	}
	return nil
}
