// Copies the repository's llms.txt, docs/ and installer scripts into the
// site's public/ folder so they are served at /llms.txt, /docs/*,
// /install.sh and /install-client.*. Run automatically before start/build.
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
await cp(join(repoDir, "scripts", "install-client.sh"), join(outDir, "install-client.sh"));
await cp(join(repoDir, "scripts", "install-client.ps1"), join(outDir, "install-client.ps1"));

console.log("sync-docs: copied llms.txt, docs/ and installer scripts into public/");
