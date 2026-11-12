import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { spawn } from "node:child_process";

import { temporaryRoot } from "./temp";

/** All Git writes and cleanup are confined to this test's uniquely owned directory. */
export async function fixture() {
  const root = await mkdtemp(join(temporaryRoot, "gitbuddy-test-"));
  const env = {
    ...process.env, GIT_CONFIG_GLOBAL: "/dev/null", GIT_CONFIG_SYSTEM: "/dev/null",
    GIT_AUTHOR_NAME: "Fixture Author", GIT_AUTHOR_EMAIL: "author@example.invalid",
    GIT_COMMITTER_NAME: "Fixture Committer", GIT_COMMITTER_EMAIL: "committer@example.invalid",
    GIT_TERMINAL_PROMPT: "0", GIT_EDITOR: "true", GIT_SEQUENCE_EDITOR: "true",
  };
  for (const key of ["GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"] as const) delete (env as NodeJS.ProcessEnv)[key];
  async function git(cwd: string, ...args: string[]): Promise<string> {
    if (cwd !== root && !cwd.startsWith(root + "/")) throw new Error("Fixture Git command escaped its owner.");
    return new Promise((resolve, reject) => {
      const child = spawn("git", ["-c", "commit.gpgSign=false", "-c", "tag.gpgSign=false", ...args], { cwd, env, stdio: ["ignore", "pipe", "pipe"] });
      let out = "";
      let err = "";
      child.stdout.on("data", (chunk) => { out += chunk; });
      child.stderr.on("data", (chunk) => { err += chunk; });
      child.on("error", reject);
      child.on("close", (code) => code === 0 ? resolve(out.replace(/\n$/, "")) : reject(new Error(`fixture git ${args.join(" ")} (${code}): ${err}`)));
    });
  }
  async function repo(name = "repo with spaces", bare = false) {
    const path = join(root, name);
    await mkdir(path);
    await git(path, "init", "--initial-branch=main", ...(bare ? ["--bare"] : []));
    if (!bare) {
      await git(path, "config", "user.name", "Fixture Author");
      await git(path, "config", "user.email", "author@example.invalid");
      await git(path, "config", "commit.gpgSign", "false");
      await git(path, "config", "core.hooksPath", "/dev/null");
    }
    return path;
  }
  async function commit(path: string, name: string, content = name, message = name) {
    await writeFile(join(path, name), content);
    await git(path, "add", "--", name);
    await git(path, "commit", "-m", message);
    return git(path, "rev-parse", "HEAD");
  }
  async function paired() {
    const remote = await repo("remote.git", true);
    const writer = await repo("writer");
    await commit(writer, "base");
    await git(writer, "remote", "add", "origin", remote);
    await git(writer, "push", "--set-upstream", "origin", "main");
    const local = join(root, "local");
    await git(root, "clone", remote, local);
    await git(local, "config", "user.name", "Fixture Author");
    await git(local, "config", "user.email", "author@example.invalid");
    await git(local, "config", "commit.gpgSign", "false");
    await git(local, "config", "core.hooksPath", "/dev/null");
    return { remote, writer, local };
  }
  return { root, git, repo, commit, paired, cleanup: () => rm(root, { recursive: true, force: true }) };
}
