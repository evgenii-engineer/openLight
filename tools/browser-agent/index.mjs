import fs from "node:fs/promises";
import path from "node:path";
import { chromium } from "playwright";

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) {
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString("utf8");
}

function truncate(text, maxChars) {
  const value = String(text || "").trim();
  if (value.length <= maxChars) {
    return value;
  }
  return `${value.slice(0, maxChars).trim()}...`;
}

async function main() {
  let browser;
  try {
    const raw = await readStdin();
    const request = JSON.parse(raw || "{}");
    const timeoutMs = Math.max(1000, Number(request.timeoutSeconds || 20) * 1000);

    browser = await chromium.launch({ headless: true });
    const page = await browser.newPage();
    await page.goto(request.url, { waitUntil: "domcontentloaded", timeout: timeoutMs });
    try {
      await page.waitForLoadState("networkidle", { timeout: Math.min(timeoutMs, 5000) });
    } catch {
      // Network-idle is a best-effort wait only.
    }

    const title = await page.title();
    const textPreview = truncate(
      await page.locator("body").innerText().catch(() => ""),
      4000,
    );

    let screenshotPath = "";
    if (request.action === "screenshot" && request.screenshotPath) {
      screenshotPath = request.screenshotPath;
      await fs.mkdir(path.dirname(screenshotPath), { recursive: true });
      await page.screenshot({ path: screenshotPath, fullPage: true });
    }

    const containsText =
      request.action === "check" && request.expectedText
        ? textPreview.includes(String(request.expectedText))
        : false;

    process.stdout.write(
      JSON.stringify({
        ok: true,
        title,
        textPreview,
        screenshotPath,
        containsText,
      }),
    );
  } catch (error) {
    process.stdout.write(
      JSON.stringify({
        ok: false,
        error: error instanceof Error ? error.message : "browser request failed",
      }),
    );
    process.exitCode = 1;
  } finally {
    if (browser) {
      await browser.close();
    }
  }
}

await main();
