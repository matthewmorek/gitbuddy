import { existsSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// Honor the session's existing scratch directory without baking in a user's path.
const sessionScratch = join(tmpdir(), "opencode");
export const temporaryRoot = realpathSync(process.env.GITBUDDY_TEST_TMPDIR || (existsSync(sessionScratch) ? sessionScratch : tmpdir()));
