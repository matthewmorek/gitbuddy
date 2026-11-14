package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var oldBytes = []byte("original executable")
var newBytes = []byte("verified new executable")

func fixtureTarget() releaseTarget {
	if target := supportedTarget(runtime.GOOS, runtime.GOARCH); target != "" {
		return target
	}
	// Isolated filesystem regressions need no native executable. Keep them
	// runnable on other Unix hosts; only real execution tests require support.
	return darwinARM64
}

func installFixture(t *testing.T) (*Client, string) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("filesystem eligibility requires a non-root user")
	}
	var temp string
	if runtime.GOOS == "linux" {
		// /tmp is normally world-writable on Linux. Stage under the package
		// directory so the real ancestor safety checks remain enabled.
		workdir, err := os.Getwd()
		must(t, err)
		temp, err = os.MkdirTemp(workdir, ".gitbuddy-updater-test-")
		must(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(temp) })
	} else {
		temp = t.TempDir()
	}
	dir, err := filepath.EvalSymlinks(temp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "gitbuddy")
	if err := os.WriteFile(path, oldBytes, 0755); err != nil {
		t.Fatal(err)
	}
	baseline, err := inspect(path, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	c := New("0.1.0", false)
	c.reason, c.path, c.uid, c.baseline = "", path, os.Getuid(), baseline
	c.target = fixtureTarget()
	c.probe = func(context.Context, string, string) error { return nil }
	return c, dir
}

func serveInstall(t *testing.T, c *Client, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c.downloadBase = server.URL
}

func goodDownload(w http.ResponseWriter, r *http.Request) {
	asset := fixtureTarget().assetName("0.2.0")
	switch r.URL.Path {
	case "/v0.2.0/" + asset:
		w.Write(newBytes)
	case "/v0.2.0/SHA256SUMS":
		fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(newBytes), asset)
	default:
		http.NotFound(w, r)
	}
}

func assertBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("file %s = %q, %v; want %q", path, got, err, want)
	}
}

func assertNoStaging(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gitbuddy-update-") {
			t.Errorf("staging directory leaked: %s", entry.Name())
		}
	}
}

func TestEligibility(t *testing.T) {
	if New("0.1.0", false).InstallReason() == "" || New("dev", true).InstallReason() == "" {
		t.Fatal("source/dev build eligible")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"nonexecutable", func(t *testing.T, path string) { must(t, os.Chmod(path, 0644)) }},
		{"writable executable", func(t *testing.T, path string) { must(t, os.Chmod(path, 0775)) }},
		{"writable directory", func(t *testing.T, path string) { must(t, os.Chmod(filepath.Dir(path), 0777)) }},
		{"unwritable directory", func(t *testing.T, path string) {
			dir := filepath.Dir(path)
			must(t, os.Chmod(dir, 0500))
			t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
		}},
		{"hardlink", func(t *testing.T, path string) { must(t, os.Link(path, path+".link")) }},
		{"symlink target", func(t *testing.T, path string) {
			must(t, os.Rename(path, path+".real"))
			must(t, os.Symlink(path+".real", path))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := installFixture(t)
			tc.mutate(t, c.path)
			if _, err := inspect(c.path, c.uid); err == nil {
				t.Fatal("unsafe target accepted")
			}
		})
	}
	t.Run("symlink ancestor", func(t *testing.T) {
		c, dir := installFixture(t)
		link := filepath.Join(dir, "linked")
		must(t, os.Symlink(dir, link))
		if _, err := inspect(filepath.Join(link, "gitbuddy"), c.uid); err == nil {
			t.Fatal("symlink ancestor accepted")
		}
	})
	t.Run("wrong owner", func(t *testing.T) {
		c, _ := installFixture(t)
		if _, err := inspect(c.path, c.uid+1); err == nil {
			t.Fatal("other user's target accepted")
		}
	})
	t.Run("distribution filename", func(t *testing.T) {
		c, dir := installFixture(t)
		path := filepath.Join(dir, "gitbuddy-0.1.0-darwin-arm64")
		must(t, os.Rename(c.path, path))
		if _, err := inspect(path, c.uid); err == nil {
			t.Fatal("distribution filename accepted")
		}
	})
	t.Run("real running executable", func(t *testing.T) {
		// The test process is not named gitbuddy, even on a supported platform.
		if New("0.1.0", true).InstallReason() == "" {
			t.Fatal("test executable eligible")
		}
	})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestChecksumManifest(t *testing.T) {
	asset := "gitbuddy-0.2.0-darwin-arm64"
	hash := fmt.Sprintf("%x", sha256.Sum256(newBytes))
	for _, manifest := range []string{
		hash + "  " + asset + "\n",
		strings.ToUpper(hash) + " *" + asset,
		hash + "  other-platform\n" + hash + "  " + asset + "\n",
	} {
		if _, err := checksum(manifest, asset); err != nil {
			t.Fatalf("valid checksum: %v", err)
		}
	}
	for _, manifest := range []string{
		"", hash + "  wrong", hash + "  ../" + asset,
		hash + "  " + asset + "\n" + hash + "  " + asset,
		hash + "  " + asset + "\n" + hash + "  ../outside",
		strings.Repeat("z", 64) + "  " + asset,
		hash + " " + asset, hash + "  " + asset + "\r\n",
	} {
		if _, err := checksum(manifest, asset); err == nil {
			t.Errorf("unsafe manifest accepted: %q", manifest)
		}
	}
}

func TestInstallAtomicSuccessAndReceipt(t *testing.T) {
	c, dir := installFixture(t)
	preferences := filepath.Join(dir, "preferences.json")
	must(t, os.WriteFile(preferences, []byte("untouched"), 0600))
	serveInstall(t, c, goodDownload)
	c.probe = func(ctx context.Context, path, version string) error {
		assertBytes(t, path, newBytes)
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0755 || version != "0.2.0" {
			return errors.New("unexpected staged executable")
		}
		assertBytes(t, c.path, oldBytes)
		return nil
	}
	must(t, c.Install(context.Background(), Release{"0.2.0"}))
	assertBytes(t, c.path, newBytes)
	assertBytes(t, preferences, []byte("untouched"))
	assertNoStaging(t, dir)
	if err := c.Install(context.Background(), Release{"0.3.0"}); err == nil {
		t.Fatal("second installation before restart accepted")
	}
	called := false
	cwd, _ := os.Getwd()
	c.exec = func(path string, args, env []string) error {
		called = true
		current, _ := os.Getwd()
		if path != c.path || !equalStrings(args, os.Args) || !equalStrings(env, os.Environ()) || current != cwd {
			t.Error("restart did not preserve process context")
		}
		return nil
	}
	must(t, c.Restart())
	if !called {
		t.Fatal("validated receipt did not reach exec seam")
	}
	must(t, os.WriteFile(c.path, []byte("tampered"), 0755))
	called = false
	if err := c.Restart(); err == nil || called {
		t.Fatal("tampered receipt reached exec")
	}
}

func TestInstallSelectsExactTargetAsset(t *testing.T) {
	for _, asset := range []string{"gitbuddy-0.2.0-darwin-arm64", "gitbuddy-0.2.0-linux-amd64"} {
		t.Run(asset, func(t *testing.T) {
			c, dir := installFixture(t)
			c.target = darwinARM64
			if strings.HasSuffix(asset, "linux-amd64") {
				c.target = linuxAMD64
			}
			var requests []string
			var mu sync.Mutex
			serveInstall(t, c, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.URL.Path)
				mu.Unlock()
				switch r.URL.Path {
				case "/v0.2.0/" + asset:
					w.Write(newBytes)
				case "/v0.2.0/SHA256SUMS":
					// Another target's entry must not shadow the exact match.
					fmt.Fprintf(w, "%064d  other-target\n%x  %s\n", 0, sha256.Sum256(newBytes), asset)
				default:
					http.NotFound(w, r)
				}
			})
			must(t, c.Install(context.Background(), Release{"0.2.0"}))
			want := []string{"/v0.2.0/" + asset, "/v0.2.0/SHA256SUMS"}
			mu.Lock()
			defer mu.Unlock()
			if !equalStrings(requests, want) {
				t.Fatalf("requests = %v; want %v", requests, want)
			}
			assertBytes(t, c.path, newBytes)
			assertNoStaging(t, dir)
		})
	}
}

func equalStrings(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func TestInstallFailuresPreserveExecutable(t *testing.T) {
	for _, kind := range []string{"checksum", "missing manifest", "version", "partial", "oversized binary", "oversized manifest", "canceled download", "canceled probe"} {
		t.Run(kind, func(t *testing.T) {
			c, dir := installFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probed := false
			c.probe = func(context.Context, string, string) error {
				probed = true
				if kind == "canceled probe" {
					cancel()
					return nil
				}
				return errors.New("wrong version")
			}
			serveInstall(t, c, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
					switch kind {
					case "checksum":
						fmt.Fprintf(w, "%064d  %s\n", 0, c.target.assetName("0.2.0"))
					case "missing manifest":
						http.NotFound(w, r)
					case "oversized manifest":
						fmt.Fprint(w, strings.Repeat("x", maxManifest+1))
					default:
						goodDownload(w, r)
					}
					return
				}
				switch kind {
				case "oversized binary":
					// Exercise streaming enforcement, not only Content-Length.
					w.(http.Flusher).Flush()
					chunk := bytes.Repeat([]byte("x"), 1<<20)
					for i := 0; i < 65; i++ {
						if _, err := w.Write(chunk); err != nil {
							return
						}
					}
				case "partial":
					w.Header().Set("Content-Length", "500")
					w.Write(newBytes)
				case "canceled download":
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					cancel()
					<-r.Context().Done()
				default:
					goodDownload(w, r)
				}
			})
			if err := c.Install(ctx, Release{"0.2.0"}); err == nil {
				t.Fatal("failed update reported success")
			}
			assertBytes(t, c.path, oldBytes)
			assertNoStaging(t, dir)
			if c.receipt != nil {
				t.Error("failed update recorded a receipt")
			}
			if kind != "version" && kind != "canceled probe" && probed {
				t.Error("unverified bytes were executed")
			}
		})
	}
}

func TestInstallRejectsExternalChanges(t *testing.T) {
	for _, kind := range []string{"before install", "replace file", "identical replacement", "edit content", "replace directory", "symlink directory"} {
		t.Run(kind, func(t *testing.T) {
			c, dir := installFixture(t)
			serveInstall(t, c, goodDownload)
			outside := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-outside")
			must(t, os.Mkdir(outside, 0700))
			t.Cleanup(func() { _ = os.RemoveAll(outside) })
			must(t, os.WriteFile(filepath.Join(outside, "gitbuddy"), []byte("outside"), 0755))
			mutated := false
			mutate := func() {
				mutated = true
				switch kind {
				case "before install", "replace file", "identical replacement":
					must(t, os.Rename(c.path, c.path+".old"))
					replacement := []byte("external replacement")
					if kind == "identical replacement" {
						replacement = oldBytes
					}
					must(t, os.WriteFile(c.path, replacement, 0755))
				case "edit content":
					must(t, os.WriteFile(c.path, []byte("external replacement"), 0755))
				default:
					moved := dir + "-moved"
					must(t, os.Rename(dir, moved))
					t.Cleanup(func() { _ = os.RemoveAll(moved) })
					if kind == "replace directory" {
						must(t, os.Mkdir(dir, 0700))
						must(t, os.WriteFile(c.path, []byte("external replacement"), 0755))
					} else {
						must(t, os.Symlink(outside, dir))
					}
				}
			}
			if kind == "before install" {
				mutate()
			} else {
				c.probe = func(context.Context, string, string) error { mutate(); return nil }
			}
			if err := c.Install(context.Background(), Release{"0.2.0"}); err == nil {
				t.Fatal("overwrote external replacement")
			}
			if !mutated {
				t.Fatal("fixture failed to mutate")
			}
			assertBytes(t, filepath.Join(outside, "gitbuddy"), []byte("outside"))
			if kind == "replace directory" || kind == "symlink directory" {
				assertBytes(t, filepath.Join(dir+"-moved", "gitbuddy"), oldBytes)
				assertNoStaging(t, dir+"-moved")
			} else if kind == "identical replacement" {
				assertBytes(t, c.path, oldBytes)
				assertNoStaging(t, dir)
			} else {
				assertBytes(t, c.path, []byte("external replacement"))
				assertNoStaging(t, dir)
			}
		})
	}
}

func TestInstallSerializationAndInput(t *testing.T) {
	c, dir := installFixture(t)
	for _, version := range []string{"0.1.0", "0.0.9", "0.2.0-rc", "../outside"} {
		if err := c.Install(context.Background(), Release{version}); err == nil {
			t.Errorf("accepted %q", version)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Install(ctx, Release{"0.2.0"}); err == nil {
		t.Fatal("pre-canceled update accepted")
	}
	if err := c.Restart(); err == nil {
		t.Fatal("restart without receipt accepted")
	}
	entered, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	serveInstall(t, c, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered); <-finish })
		goodDownload(w, r)
	})
	result := make(chan error, 1)
	go func() { result <- c.Install(context.Background(), Release{"0.2.0"}) }()
	<-entered
	// Cached eligibility must not wait for the network/install lock.
	if c.InstallReason() != "" {
		t.Error("unexpected reason")
	}
	if err := c.Install(context.Background(), Release{"0.2.0"}); err == nil {
		t.Error("duplicate install accepted")
	}
	if err := c.Restart(); err == nil {
		t.Error("restart during installation accepted")
	}
	close(finish)
	must(t, <-result)
	assertNoStaging(t, dir)
}

// The real probe executes only this locally built test binary. No release
// downloads or actual process replacement occur in the test suite.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--updater-eligibility" {
		if reason := New("0.2.0", true).InstallReason(); reason != "" {
			fmt.Fprintln(os.Stderr, reason)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		if os.Getenv("DYLD_INSERT_LIBRARIES") != "" || os.Getenv("LD_PRELOAD") != "" ||
			os.Getenv("GITHUB_TOKEN") != "" {
			os.Exit(9)
		}
		switch filepath.Base(os.Args[0]) {
		case "verbose":
			fmt.Print(strings.Repeat("x", 2048))
		case "stderr":
			fmt.Fprint(os.Stderr, "unexpected diagnostic")
		case "trailing":
			fmt.Print("gitbuddy 0.2.0\nextra\n")
		case "slow":
			time.Sleep(10 * time.Second)
		default:
			fmt.Print("gitbuddy 0.2.0\n")
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func realProbeBinary(t *testing.T) []byte {
	t.Helper()
	if supportedTarget(runtime.GOOS, runtime.GOARCH) == "" {
		t.Skip("real executable probe requires a supported native platform")
	}
	source, err := os.Executable()
	must(t, err)
	if runtime.GOOS == "linux" {
		// The test driver may use cgo (net/http); releases do not. Build the
		// same local test fixture with the official static, non-PIE policy.
		source = filepath.Join(t.TempDir(), "probe.test")
		cmd := exec.Command("go", "test", "-c", "-buildmode=exe", "-o", source, ".")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build static probe fixture: %v\n%s", err, output)
		}
	}
	data, err := os.ReadFile(source)
	must(t, err)
	return data
}

func TestRealVersionProbe(t *testing.T) {
	data := realProbeBinary(t)
	dir := t.TempDir()
	t.Setenv("GITHUB_TOKEN", "must-not-reach-probe")
	t.Setenv("LD_PRELOAD", "must-not-reach-probe")
	t.Setenv("DYLD_INSERT_LIBRARIES", "must-not-reach-probe")
	for _, tc := range []struct {
		name, version string
		fail          bool
	}{
		{"good", "0.2.0", false},
		{"wrong", "0.3.0", true},
		{"verbose", "0.2.0", true},
		{"stderr", "0.2.0", true},
		{"trailing", "0.2.0", true},
		{"slow", "0.2.0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			must(t, os.WriteFile(path, data, 0755))
			timeout := 5 * time.Second
			if tc.name == "slow" {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			if err := probeVersion(ctx, path, tc.version); (err != nil) != tc.fail {
				t.Fatalf("probe error = %v, want failure=%v", err, tc.fail)
			}
		})
	}
	path := filepath.Join(dir, "script")
	must(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755))
	if err := probeVersion(context.Background(), path, "0.2.0"); err == nil {
		t.Fatal("script passed executable-format validation")
	}
}

func machoFixture(t *testing.T, cpu macho.Cpu, kind macho.Type) []byte {
	t.Helper()
	var out bytes.Buffer
	header := struct {
		macho.FileHeader
		Reserved uint32
	}{FileHeader: macho.FileHeader{Magic: macho.Magic64, Cpu: cpu, Type: kind}}
	must(t, binary.Write(&out, binary.LittleEndian, header))
	return out.Bytes()
}

func elfFixture(t *testing.T, class elf.Class, data elf.Data, machine elf.Machine, kind elf.Type, program elf.ProgType) []byte {
	t.Helper()
	var order binary.ByteOrder = binary.LittleEndian
	if data == elf.ELFDATA2MSB {
		order = binary.BigEndian
	}
	var ident [elf.EI_NIDENT]byte
	copy(ident[:], elf.ELFMAG)
	ident[elf.EI_CLASS], ident[elf.EI_DATA], ident[elf.EI_VERSION] = byte(class), byte(data), byte(elf.EV_CURRENT)
	var out bytes.Buffer
	if class == elf.ELFCLASS32 {
		header := elf.Header32{
			Ident: ident, Type: uint16(kind), Machine: uint16(machine), Version: uint32(elf.EV_CURRENT),
			Ehsize: 52, Phentsize: 32, Shentsize: 40,
		}
		must(t, binary.Write(&out, order, header))
	} else {
		header := elf.Header64{
			Ident: ident, Type: uint16(kind), Machine: uint16(machine), Version: uint32(elf.EV_CURRENT),
			Ehsize: 64, Phentsize: 56, Shentsize: 64,
		}
		if program != elf.PT_NULL {
			header.Phoff, header.Phnum = 64, 1
		}
		must(t, binary.Write(&out, order, header))
		if program != elf.PT_NULL {
			must(t, binary.Write(&out, order, elf.Prog64{Type: uint32(program)}))
		}
	}
	return out.Bytes()
}

func TestExecutableValidationBeforeProbe(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	mach := machoFixture(t, macho.CpuArm64, macho.TypeExec)
	linux := elfFixture(t, elf.ELFCLASS64, elf.ELFDATA2LSB, elf.EM_X86_64, elf.ET_EXEC, elf.PT_NULL)
	for _, tc := range []struct {
		name   string
		target releaseTarget
		data   []byte
		want   string
	}{
		{"macOS arm64", darwinARM64, mach, ""},
		{"Linux x86_64", linuxAMD64, linux, ""},
		{"truncated Mach-O", darwinARM64, mach[:16], "release is not a macOS executable"},
		{"truncated ELF", linuxAMD64, linux[:16], "release is not a Linux ELF executable"},
		{"ELF on macOS", darwinARM64, linux, "release is not a macOS executable"},
		{"Mach-O on Linux", linuxAMD64, mach, "release is not a Linux ELF executable"},
		{"script on macOS", darwinARM64, []byte("#!/bin/sh\ntouch '" + marker + "'\n"), "release is not a macOS executable"},
		{"script on Linux", linuxAMD64, []byte("#!/bin/sh\ntouch '" + marker + "'\n"), "release is not a Linux ELF executable"},
		{"Mach-O amd64", darwinARM64, machoFixture(t, macho.CpuAmd64, macho.TypeExec), "release is not a macOS arm64 executable"},
		{"Mach-O dylib", darwinARM64, machoFixture(t, macho.CpuArm64, macho.TypeDylib), "release is not a macOS arm64 executable"},
		{"ELF 32-bit", linuxAMD64, elfFixture(t, elf.ELFCLASS32, elf.ELFDATA2LSB, elf.EM_X86_64, elf.ET_EXEC, elf.PT_NULL), "release is not a Linux 64-bit little-endian x86_64 executable"},
		{"ELF big-endian", linuxAMD64, elfFixture(t, elf.ELFCLASS64, elf.ELFDATA2MSB, elf.EM_X86_64, elf.ET_EXEC, elf.PT_NULL), "release is not a Linux 64-bit little-endian x86_64 executable"},
		{"ELF arm64", linuxAMD64, elfFixture(t, elf.ELFCLASS64, elf.ELFDATA2LSB, elf.EM_AARCH64, elf.ET_EXEC, elf.PT_NULL), "release is not a Linux 64-bit little-endian x86_64 executable"},
		{"ELF PIE", linuxAMD64, elfFixture(t, elf.ELFCLASS64, elf.ELFDATA2LSB, elf.EM_X86_64, elf.ET_DYN, elf.PT_NULL), "release is not a Linux 64-bit little-endian x86_64 executable"},
		{"ELF interpreter", linuxAMD64, elfFixture(t, elf.ELFCLASS64, elf.ELFDATA2LSB, elf.EM_X86_64, elf.ET_EXEC, elf.PT_INTERP), "release is not a statically linked Linux executable"},
		{"ELF dynamic linking", linuxAMD64, elfFixture(t, elf.ELFCLASS64, elf.ELFDATA2LSB, elf.EM_X86_64, elf.ET_EXEC, elf.PT_DYNAMIC), "release is not a statically linked Linux executable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "candidate")
			must(t, os.WriteFile(path, tc.data, 0755))
			err := tc.target.validateExecutable(path)
			if tc.want == "" {
				must(t, err)
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("validation = %v; want %q", err, tc.want)
			}
			if tc.target == supportedTarget(runtime.GOOS, runtime.GOARCH) {
				// The exact format diagnostic proves rejection happened before
				// exec, rather than relying on an OS loader failure.
				err := probeVersion(context.Background(), path, "0.2.0")
				if err == nil || err.Error() != tc.want {
					t.Fatalf("probe = %v; want pre-execution rejection %q", err, tc.want)
				}
			}
		})
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid candidate was executed: %v", err)
	}
}

func TestInstallRealVerifiedBinary(t *testing.T) {
	data := realProbeBinary(t)
	c, dir := installFixture(t)
	asset := c.target.assetName("0.2.0")
	serveInstall(t, c, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.2.0/SHA256SUMS":
			fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(data), asset)
		case "/v0.2.0/" + asset:
			w.Write(data)
		default:
			http.NotFound(w, r)
		}
	})
	c.probe = probeVersion
	must(t, c.Install(context.Background(), Release{"0.2.0"}))
	assertBytes(t, c.path, data)
	assertNoStaging(t, dir)
	if c.receipt == nil {
		t.Fatal("real verified installation did not record a restart receipt")
	}
	// Unlike the isolated filesystem fixtures, this child goes through New
	// with its real executable identity and the native target eligibility gate.
	if output, err := exec.Command(c.path, "--updater-eligibility").CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatalf("installed native release is not eligible: %v\n%s", err, output)
	}
}

func TestDownloadSizeHeaderAndContext(t *testing.T) {
	c := New("0.1.0", false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
	}))
	defer server.Close()
	if err := c.download(context.Background(), server.URL, io.Discard, 10); err == nil {
		t.Fatal("oversized Content-Length accepted")
	}
}

func TestVersionOutputBound(t *testing.T) {
	var output limitedOutput
	_, err := io.Copy(&output, strings.NewReader(strings.Repeat("x", 2048)))
	if err == nil || output.buffer.Len() > 1024 {
		t.Fatal("version output was not bounded through io.Copy")
	}
}
