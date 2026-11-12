import { chmod, mkdir, readFile, readdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import solidPlugin from "@opentui/solid/bun-plugin";
import manifest from "../package.json";

const root = fileURLToPath(new URL("../", import.meta.url));
const args = process.argv.slice(2);
if (args.some(arg => arg !== "--release")) throw new Error("Usage: bun run build [--release]");
if (process.platform !== "darwin" || process.arch !== "arm64") {
  throw new Error("Only native macOS arm64 builds are supported. Other architectures require executed binary verification first.");
}
if (Bun.version !== "1.4.4") throw new Error("Build with the pinned Bun 1.4.4 runtime.");
const stable = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
if (!stable.test(manifest.version) && !/^0\.1\.0-dev\.\d+$/.test(manifest.version)) {
  throw new Error("Use a stable SemVer version or the initial 0.1.0-dev.N development series.");
}
if (args.includes("--release") && !stable.test(manifest.version)) {
  throw new Error("Release builds require a stable package.json version; the first release will be 0.1.0.");
}

// Carry installed license texts with the standalone executable. Including build-only
// packages is conservative; this inventory is not a claim of legal clearance.
async function dependencyNotices(directory: string): Promise<string[]> {
  const notices: string[] = [];
  for (const entry of (await readdir(directory, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
    if (entry.name.startsWith(".") || !entry.isDirectory()) continue;
    const path = join(directory, entry.name);
    if (entry.name.startsWith("@")) {
      notices.push(...await dependencyNotices(path));
      continue;
    }
    const pkg = JSON.parse(await readFile(join(path, "package.json"), "utf8"));
    notices.push(`\n## ${pkg.name}@${pkg.version}\nDeclared license: ${pkg.license ?? "See upstream"}\n`);
    for (const file of (await readdir(path)).sort()) {
      if (/^(licen[cs]e|copying|notice|authors|patents)([._-]|$)/i.test(file) || file === "FFMPEG-SOURCE.md") {
        const text = await readFile(join(path, file), "utf8");
        notices.push(`\n### ${pkg.name}/${file}\n${text}\n`);
      }
    }
    try { notices.push(...await dependencyNotices(join(path, "node_modules"))); }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error; }
  }
  return notices;
}

const notices = [await readFile(join(root, "THIRD_PARTY_NOTICES.md"), "utf8"), ...await dependencyNotices(join(root, "node_modules"))].join("\n");
const outdir = join(root, "dist");
await mkdir(outdir, { recursive: true });
const filename = `gitbuddy-${manifest.version}-darwin-arm64`;
const outfile = join(outdir, filename);
const result = await Bun.build({
  entrypoints: [join(root, "src/main.tsx")],
  target: "bun",
  plugins: [solidPlugin],
  minify: true,
  define: { GITBUDDY_BUNDLED_NOTICES: JSON.stringify(notices) },
  compile: {
    target: "bun-darwin-arm64",
    outfile,
    // A repository being browsed must not supply executable Bun preload/config.
    autoloadBunfig: false,
    autoloadDotenv: false,
    autoloadPackageJson: false,
    autoloadTsconfig: false,
  },
});
if (!result.success) throw new AggregateError(result.logs, "Standalone build failed");
await chmod(outfile, 0o755);
const version = Bun.spawnSync([outfile, "--version"], { cwd: outdir });
if (version.exitCode !== 0 || version.stdout.toString().trim() !== `gitbuddy ${manifest.version}`) {
  throw new Error("Compiled executable version does not match package.json");
}
await Bun.write(join(outdir, "THIRD_PARTY_NOTICES.txt"), notices);
await Bun.write(join(outdir, "install.sh"), Bun.file(join(root, "scripts/install.sh")));
const checksums: string[] = [];
for (const asset of [filename, "THIRD_PARTY_NOTICES.txt", "install.sh"]) {
  const hash = new Bun.CryptoHasher("sha256").update(await Bun.file(join(outdir, asset)).arrayBuffer()).digest("hex");
  checksums.push(`${hash}  ${asset}`);
}
await Bun.write(join(outdir, "SHA256SUMS"), `${checksums.join("\n")}\n`);
console.log(`Built ${resolve(outfile)}\n${checksums[0]}\nRun the binary PTY smoke test before advertising this artifact.`);
