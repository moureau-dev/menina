// Copies the repository's llms.txt, docs/ and installer scripts into the
// site's public/ folder so they are served at /llms.txt, /docs/*,
// /install.sh and /install-client.*. Markdown links inside docs/ are rewritten
// to absolute https://menina.moureau.dev URLs. Run automatically before
// start/build.
import { cp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const siteDir = process.cwd(); // website/
const repoDir = resolve(siteDir, ".."); // repository root
const outDir = join(siteDir, "public");
const SITE = "https://menina.moureau.dev";
const SITE_ROOT = new URL("/", SITE);
const DOCS_ROOT = new URL("/docs/", SITE);

const externalRe = /^(?:[a-z][a-z0-9+.-]*:|\/\/|#)/i;

function absoluteLink(target: string): string {
  const trimmed = target.trim();
  if (!trimmed || externalRe.test(trimmed)) {
    return target;
  }

  // A link that already contains docs/ is resolved from the site root, so
  // both "docs/CLI.md" and "../docs/CLI.md" become /docs/CLI.md. Everything
  // else is resolved from /docs/, which also normalizes ./ and ../ segments.
  const docsAt = trimmed.indexOf("docs/");
  const url =
    docsAt >= 0
      ? new URL(trimmed.slice(docsAt), SITE_ROOT)
      : new URL(trimmed.replace(/^\/+/, ""), DOCS_ROOT);
  return url.href;
}

function rewriteLinks(markdown: string): string {
  return markdown.replace(/\]\(([^)]+)\)/g, (whole, inner: string) => {
    const match = inner.match(/^(\S+)(.*)$/s);
    if (!match) {
      return whole;
    }
    const [, target, rest] = match;
    return `](${absoluteLink(target)}${rest})`;
  });
}

async function copyDocs(srcDir: string, dstDir: string): Promise<void> {
  await mkdir(dstDir, { recursive: true });
  for (const entry of await readdir(srcDir, { withFileTypes: true })) {
    const src = join(srcDir, entry.name);
    const dst = join(dstDir, entry.name);
    if (entry.isDirectory()) {
      await copyDocs(src, dst);
    } else if (entry.name.endsWith(".md")) {
      await writeFile(dst, rewriteLinks(await readFile(src, "utf8")));
    } else {
      await cp(src, dst);
    }
  }
}

await mkdir(outDir, { recursive: true });
await rm(join(outDir, "docs"), { recursive: true, force: true });
await rm(join(outDir, "schema"), { recursive: true, force: true });

await cp(join(repoDir, "llms.txt"), join(outDir, "llms.txt"));
await copyDocs(join(repoDir, "docs"), join(outDir, "docs"));
await mkdir(join(outDir, "schema", "v1"), { recursive: true });
await cp(join(repoDir, "internal", "schema", "menina.schema.json"), join(outDir, "schema", "v1", "menina.schema.json"));
await cp(join(repoDir, "internal", "schema", "menina.schema.json"), join(outDir, "schema", "menina.schema.json"));
await cp(join(repoDir, "scripts", "install.sh"), join(outDir, "install.sh"));
await cp(join(repoDir, "scripts", "install-client.sh"), join(outDir, "install-client.sh"));
await cp(join(repoDir, "scripts", "install-client.ps1"), join(outDir, "install-client.ps1"));

console.log("sync-docs: copied llms.txt, docs/, schema/ and installer scripts into public/");
