// Copies the repository's llms.txt, docs/ and scripts/install.sh into the
// site's public/ folder so they are served at /llms.txt, /docs/* and
// /install.sh. Run automatically before start/build.
import { cp, mkdir, rm } from "node:fs/promises";
import { join, resolve } from "node:path";

const siteDir = process.cwd(); // website/
const repoDir = resolve(siteDir, ".."); // repository root
const outDir = join(siteDir, "public");

await mkdir(outDir, { recursive: true });
await rm(join(outDir, "docs"), { recursive: true, force: true });

await cp(join(repoDir, "llms.txt"), join(outDir, "llms.txt"));
await cp(join(repoDir, "docs"), join(outDir, "docs"), { recursive: true });
await cp(join(repoDir, "scripts", "install.sh"), join(outDir, "install.sh"));

console.log("sync-docs: copied llms.txt, docs/ and install.sh into public/");
