import { afterEach, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { chmod, lstat, mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { temporaryRoot } from "./temp";

const installer = resolve(import.meta.dir, "../scripts/install.sh");
const repository = "https://github.com/matthewmorek/gitbuddy";
const owned: string[] = [];
afterEach(async () => { await Promise.all(owned.splice(0).map((path) => rm(path, { recursive: true, force: true }))); });

function binary(version: string) {
  return `#!/bin/bash\n[ "$#" = 1 ] && [ "$1" = --version ] || exit 2\nprintf '%s\\n' 'gitbuddy ${version}'\n`;
}

// Only the platform detector and network boundary are fixtures. Bash, permissions,
// hashing, staging, executable validation, signals, and rename are the real tools.
async function fixture(options: { version?: string; binaryVersion?: string; sums?: string } = {}) {
  const root = await mkdtemp(join(temporaryRoot, "gitbuddy-installer-"));
  owned.push(root);
  const home = join(root, "home");
  const tools = join(root, "tools");
  const installDir = join(home, ".local", "bin");
  const target = join(installDir, "gitbuddy");
  const preferences = join(home, ".config", "gitbuddy", "config.json");
  await mkdir(tools);
  await mkdir(join(home, ".config", "gitbuddy"), { recursive: true });
  await writeFile(preferences, '{"theme":"preserve-me"}\n');
  await writeFile(join(home, ".curlrc"), "insecure\nurl=http://must-not-be-used.invalid\n");
  const version = options.version ?? "0.2.0";
  const asset = `gitbuddy-${version}-darwin-arm64`;
  const payload = binary(options.binaryVersion ?? version);
  await writeFile(join(root, "binary"), payload);
  const checksum = createHash("sha256").update(payload).digest("hex");
  await writeFile(join(root, "sums"), options.sums ?? `${checksum}  ${asset}\n`);
  await writeFile(join(tools, "uname"), `#!/bin/bash\ncase $1 in -s) echo "\${FIXTURE_OS:-Darwin}";; -m) echo "\${FIXTURE_ARCH:-arm64}";; *) exit 1;; esac\n`);
  await writeFile(join(tools, "curl"), `#!/bin/bash
set -eu
[ "$1" = -q ] || exit 90
shift
output= write_out=false proto=false redir=false bounded=false
while [ "$#" -gt 0 ]; do
  case $1 in
    --output) output=$2; shift 2;;
    --write-out) [ "$2" = '%{url_effective}' ] || exit 91; write_out=true; shift 2;;
    --proto) [ "$2" = '=https' ] || exit 92; proto=true; shift 2;;
    --proto-redir) [ "$2" = '=https' ] || exit 93; redir=true; shift 2;;
    --max-time) bounded=true; shift 2;;
    --max-redirs|--connect-timeout|--max-filesize) shift 2;;
    --fail|--silent|--show-error|--head|--location) shift;;
    https://*) url=$1; shift;;
    *) exit 94;;
  esac
done
[ "$proto" = true ] && [ "$redir" = true ] && [ "$bounded" = true ] || exit 95
printf '%s\\n' "$url" >> "$FIXTURE_ROOT/requests"
case $url in
  */releases/latest)
    [ "$write_out" = true ] || exit 96
    [ "\${FIXTURE_FAILURE:-}" != latest ] || exit 22
    printf '%s' "$FIXTURE_LATEST";;
  */SHA256SUMS)
    [ "\${FIXTURE_FAILURE:-}" != sums ] || exit 22
    /bin/cp "$FIXTURE_ROOT/sums" "$output"
    if [ "\${FIXTURE_FAILURE:-}" = interrupt ]; then kill -TERM "$PPID"; exit 143; fi
    if [ "\${FIXTURE_FAILURE:-}" = concurrent ]; then
      /bin/cp "$FIXTURE_ROOT/concurrent" "$FIXTURE_TARGET"
      /bin/chmod 755 "$FIXTURE_TARGET"
    fi;;
  */gitbuddy-*)
    [ "\${FIXTURE_FAILURE:-}" != binary ] || exit 22
    /bin/cp "$FIXTURE_ROOT/binary" "$output";;
  *) exit 97;;
esac
`);
  await chmod(join(tools, "uname"), 0o755);
  await chmod(join(tools, "curl"), 0o755);
  async function existing(current = "0.1.0") {
    await mkdir(installDir, { recursive: true });
    await writeFile(target, binary(current));
    await chmod(target, 0o755);
    return readFile(target, "utf8");
  }
  async function run(args: string[] = [], env: Record<string, string> = {}) {
    const process = Bun.spawn(["/bin/bash", installer, ...args], {
      cwd: root,
      env: {
        HOME: home, PATH: `${tools}:/usr/bin:/bin`,
        FIXTURE_ROOT: root, FIXTURE_TARGET: target,
        FIXTURE_LATEST: `${repository}/releases/tag/v${version}`, ...env,
      },
      stdout: "pipe", stderr: "pipe",
    });
    const [code, stdout, stderr] = await Promise.all([process.exited, new Response(process.stdout).text(), new Response(process.stderr).text()]);
    return { code, stdout, stderr };
  }
  async function requests() {
    try { return (await readFile(join(root, "requests"), "utf8")).trim().split("\n"); }
    catch (error) { if ((error as NodeJS.ErrnoException).code === "ENOENT") return []; throw error; }
  }
  async function preserved(prior: string) {
    expect(await readFile(target, "utf8")).toBe(prior);
    expect(await readFile(preferences, "utf8")).toBe('{"theme":"preserve-me"}\n');
    expect((await readdir(installDir)).filter((name) => name.startsWith(".gitbuddy-install."))).toEqual([]);
  }
  return { root, home, tools, installDir, target, version, asset, checksum, preferences, existing, run, requests, preserved };
}

test("fresh default install resolves once and pins both assets to that stable release", async () => {
  const f = await fixture();
  const result = await f.run();
  expect(result.code).toBe(0);
  expect(result.stdout).toContain(f.target);
  expect(result.stdout).toContain("PATH");
  expect(await readFile(f.target, "utf8")).toBe(binary(f.version));
  expect((await lstat(f.target)).mode & 0o777).toBe(0o755);
  expect(await f.requests()).toEqual([
    `${repository}/releases/latest`,
    `${repository}/releases/download/v${f.version}/${f.asset}`,
    `${repository}/releases/download/v${f.version}/SHA256SUMS`,
  ]);
  await f.preserved(binary(f.version));
});

test("rerunning upgrades atomically, current release is a no-op without asset downloads", async () => {
  const f = await fixture();
  await f.existing();
  expect((await f.run()).code).toBe(0);
  const before = await lstat(f.target);
  const current = await f.run();
  expect(current.code).toBe(0);
  expect(current.stdout).toContain("already installed");
  expect((await lstat(f.target)).ino).toBe(before.ino);
  expect(await f.requests()).toHaveLength(4);
  await f.preserved(binary(f.version));
});

test("latest refuses downgrade but explicit version permits rollback without latest resolution", async () => {
  const f = await fixture({ version: "0.1.0" });
  const prior = await f.existing("0.2.0");
  const refused = await f.run();
  expect(refused.code).toBe(1);
  expect(refused.stderr).toContain("explicit rollback");
  await f.preserved(prior);
  expect((await f.run(["--version", "0.1.0"])).code).toBe(0);
  expect(await f.requests()).toEqual([
    `${repository}/releases/latest`,
    `${repository}/releases/download/v0.1.0/${f.asset}`,
    `${repository}/releases/download/v0.1.0/SHA256SUMS`,
  ]);
  await f.preserved(binary("0.1.0"));
});

test("version ordering compares numeric components, not lexically or with integer overflow", async () => {
  const f = await fixture({ version: "0.10.0" });
  await f.existing("0.9.0");
  expect((await f.run()).code).toBe(0);
  await f.existing("999999999999999999999999999.0.0");
  const result = await f.run();
  expect(result.code).toBe(1);
  expect(result.stderr).toContain("older than installed");
});

test("absolute override treats whitespace and metacharacters literally and never edits profiles", async () => {
  const f = await fixture();
  const directory = join(f.home, "bin space ' $() ; * \\ newline\n");
  const result = await f.run(["--install-dir", `${directory}/`, "--version", f.version]);
  expect(result.code).toBe(0);
  expect(await readFile(join(directory, "gitbuddy"), "utf8")).toBe(binary(f.version));
  expect((await readdir(f.home)).sort()).toEqual([".config", ".curlrc", "bin space ' $() ; * \\ newline\n"]);
});

test("help and malformed arguments do not download or install", async () => {
  const f = await fixture();
  expect((await f.run(["--help"])).code).toBe(0);
  for (const args of [
    ["--version", "01.2.3"], ["--version", "1.02.3"], ["--version", "1.2.03"],
    ["--version", "v1.2.3"], ["--version", "1.2.3-dev.0"], ["--version", "1.2.3+build"],
    ["--version", "1.2"], ["--version", "1.2.3;touch nope"], ["--version"],
    ["--install-dir", "relative"], ["--install-dir"], ["--unknown"],
  ]) expect((await f.run(args)).code).toBe(1);
  expect(await f.requests()).toEqual([]);
  expect(await readdir(f.home)).not.toContain(".local");
});

test("unsupported OS or architecture fails before network or installation", async () => {
  const f = await fixture();
  const platforms: Record<string, string>[] = [{ FIXTURE_OS: "Linux" }, { FIXTURE_ARCH: "x86_64" }];
  for (const env of platforms) {
    const result = await f.run([], env);
    expect(result.code).toBe(1);
    expect(result.stderr).toContain("unsupported");
  }
  expect(await f.requests()).toEqual([]);
  expect(await readdir(f.home)).not.toContain(".local");
});

test("latest redirect must be exactly this repository's stable tag URL", async () => {
  const f = await fixture();
  const prior = await f.existing();
  for (const url of [
    "https://github.com/other/gitbuddy/releases/tag/v0.2.0",
    `${repository}/releases/tag/v0.2.0-dev.1`, `${repository}/releases/tag/v00.2.0`,
    `${repository}/releases/tag/v0.2.0?extra=1`, `${repository}/releases/tag/v0.2.0/`,
    `http://github.com/matthewmorek/gitbuddy/releases/tag/v0.2.0`,
  ]) {
    expect((await f.run([], { FIXTURE_LATEST: url })).code).toBe(1);
    await f.preserved(prior);
  }
  expect((await f.requests()).every((url) => url.endsWith("/releases/latest"))).toBe(true);
});

test("network failure at resolution or either download preserves binary and preferences", async () => {
  const f = await fixture();
  const prior = await f.existing();
  for (const failure of ["latest", "binary", "sums"]) {
    expect((await f.run([], { FIXTURE_FAILURE: failure })).code).toBe(1);
    await f.preserved(prior);
  }
});

test("corrupt, missing, path-prefixed, or duplicate checksum entries cannot replace binary", async () => {
  for (const kind of ["corrupt", "missing", "path", "duplicate"]) {
    const f = await fixture();
    const entry = `${f.checksum}  ${f.asset}\n`;
    const sums = kind === "corrupt" ? `${"0".repeat(64)}  ${f.asset}\n`
      : kind === "missing" ? `${f.checksum}  unrelated\n`
      : kind === "path" ? `${f.checksum}  ../${f.asset}\n` : entry + entry;
    await writeFile(join(f.root, "sums"), sums);
    const prior = await f.existing();
    expect((await f.run()).code).toBe(1);
    await f.preserved(prior);
  }
});

test("a checksummed binary reporting another version is rejected", async () => {
  const f = await fixture({ binaryVersion: "9.9.9" });
  const prior = await f.existing();
  const result = await f.run();
  expect(result.code).toBe(1);
  expect(result.stderr).toContain("version does not match");
  await f.preserved(prior);
});

test("a checksummed binary that cannot execute preserves the existing release", async () => {
  const f = await fixture();
  const payload = "#!/bin/bash\nexit 126\n";
  await writeFile(join(f.root, "binary"), payload);
  await writeFile(join(f.root, "sums"), `${createHash("sha256").update(payload).digest("hex")}  ${f.asset}\n`);
  const prior = await f.existing();
  const result = await f.run();
  expect(result.code).toBe(1);
  expect(result.stderr).toContain("could not run --version");
  await f.preserved(prior);
});

test("an unusable Git prerequisite fails before download or installation", async () => {
  const f = await fixture();
  await writeFile(join(f.tools, "git"), "#!/bin/bash\nexit 1\n");
  await chmod(join(f.tools, "git"), 0o755);
  const result = await f.run();
  expect(result.code).toBe(1);
  expect(result.stderr).toContain("Git must be installed");
  expect(await f.requests()).toEqual([]);
  expect(await readdir(f.home)).not.toContain(".local");
});

test("symlinks, directories, unrelated executables, and unsafe permissions are never overwritten", async () => {
  const f = await fixture();
  await mkdir(f.installDir, { recursive: true });
  const other = join(f.root, "other");
  await writeFile(other, "untouched");
  await symlink(other, f.target);
  expect((await f.run()).code).toBe(1);
  expect((await lstat(f.target)).isSymbolicLink()).toBe(true);
  expect(await readFile(other, "utf8")).toBe("untouched");
  await rm(f.target);
  await mkdir(f.target);
  expect((await f.run()).code).toBe(1);
  expect((await lstat(f.target)).isDirectory()).toBe(true);
  await rm(f.target, { recursive: true });
  await writeFile(f.target, "#!/bin/bash\necho unrelated\n");
  await chmod(f.target, 0o755);
  expect((await f.run()).code).toBe(1);
  expect(await readFile(f.target, "utf8")).toContain("echo unrelated");
  await f.existing();
  await chmod(f.target, 0o777);
  expect((await f.run()).code).toBe(1);
  await chmod(f.target, 0o755);
  await chmod(f.installDir, 0o777);
  expect((await f.run()).code).toBe(1);
  await chmod(f.installDir, 0o755);
  expect(await f.requests()).toEqual([]);
});

test("installation directory symlinks are refused without writing through them", async () => {
  const f = await fixture();
  const directory = join(f.root, "redirect");
  const destination = join(f.root, "destination");
  await mkdir(destination);
  await symlink(destination, directory);
  expect((await f.run(["--install-dir", directory])).code).toBe(1);
  expect(await readdir(destination)).toEqual([]);
  expect(await f.requests()).toEqual([]);
});

test("TERM before replacement cleans staging and preserves the prior executable and preferences", async () => {
  const f = await fixture();
  const prior = await f.existing();
  expect((await f.run([], { FIXTURE_FAILURE: "interrupt" })).code).toBe(143);
  await f.preserved(prior);
});

test("concurrent target modification is retained rather than overwritten by the installer", async () => {
  const f = await fixture();
  await f.existing();
  const concurrent = binary("0.3.0");
  await writeFile(join(f.root, "concurrent"), concurrent);
  const result = await f.run([], { FIXTURE_FAILURE: "concurrent" });
  expect(result.code).toBe(1);
  expect(result.stderr).toContain("target changed");
  await f.preserved(concurrent);
});

test("a target appearing during a fresh install is retained", async () => {
  const f = await fixture();
  const concurrent = binary("0.3.0");
  await writeFile(join(f.root, "concurrent"), concurrent);
  const result = await f.run([], { FIXTURE_FAILURE: "concurrent" });
  expect(result.code).toBe(1);
  expect(result.stderr).toContain("target appeared");
  await f.preserved(concurrent);
});
