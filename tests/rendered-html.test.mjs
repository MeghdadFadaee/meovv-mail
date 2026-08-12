import assert from "node:assert/strict";
import { access, readFile, readdir } from "node:fs/promises";
import test from "node:test";

test("builds the standalone MEOVV application shell", async () => {
  const html = await readFile(new URL("../web-dist/index.html", import.meta.url), "utf8");
  assert.match(html, /<title>MEOVV Mail — Private email, beautifully run<\/title>/i);
  assert.match(html, /<div id="root"><\/div>/);
  assert.match(html, /meovv-mail-social\.png/);
  assert.doesNotMatch(html, /cloudflare|vinext-starter/i);
  const assets = await readdir(new URL("../web-dist/assets", import.meta.url));
  assert.ok(assets.some((file) => file.endsWith(".js")));
  assert.ok(assets.some((file) => file.endsWith(".css")));
});

test("includes production auth, JMAP, sanitization, and responsive UI paths", async () => {
  const [page, css, openapi, standalone] = await Promise.all([
    readFile(new URL("../app/page.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/globals.css", import.meta.url), "utf8"),
    readFile(new URL("../api/openapi.yaml", import.meta.url), "utf8"),
    readFile(new URL("../web-dist/index.html", import.meta.url), "utf8"),
  ]);
  assert.match(page, /crypto\.subtle\.digest\("SHA-256"/);
  assert.match(page, /fetch\("\/api\/auth"/);
  assert.match(page, /fetch\("\/api\/mail\/jmap"/);
  assert.match(page, /new EventSource\("\/api\/mail\/events"\)/);
  assert.match(page, /DOMPurify\.sanitize/);
  assert.match(page, /Remote images are blocked/);
  assert.match(css, /@media \(max-width: 720px\)/);
  assert.match(css, /prefers-reduced-motion: reduce/);
  assert.doesNotMatch(css, /linear-gradient|radial-gradient/);
  assert.match(openapi, /openapi: 3\.1\.0/);
  assert.match(openapi, /Idempotency-Key/);
  assert.match(standalone, /<div id="root"><\/div>/);
  await access(new URL("../web-dist/assets", import.meta.url));
});
