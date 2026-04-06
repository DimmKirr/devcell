#!/usr/bin/env node
// bot-detect-test.js — standalone Patchright test runner for bot detection sites.
// Usage: node test/bot-detect-test.js [--init-script path] [--only creepjs|sannysoft|incolumitas]
//
// Uses the same Chrome flags and init-script as patchright-mcp-cell wrapper.
// Outputs structured results for fast iteration without MCP reconnection.

const { chromium } = require('/opt/devcell/.local/state/nix/profiles/profile/lib/node_modules/nix-patchright-mcp-server/node_modules/patchright');
const fs = require('fs');
const path = require('path');

// Resolve init-script: CLI arg > bundled in nix profile > local dev copy
function findInitScript() {
  const argIdx = process.argv.indexOf('--init-script');
  if (argIdx !== -1 && process.argv[argIdx + 1]) return process.argv[argIdx + 1];

  // Try nix profile bundled
  try {
    const bin = fs.realpathSync('/opt/devcell/.local/state/nix/profiles/profile/bin/patchright-mcp-cell');
    const share = path.join(path.dirname(path.dirname(bin)), 'share', 'patchright');
    const f = path.join(share, 'stealth-init.js');
    if (fs.existsSync(f)) return f;
  } catch {}

  // Try local dev copy
  const local = path.join(__dirname, '..', 'stealth-init.dev.js');
  if (fs.existsSync(local)) return local;

  console.error('No init-script found. Pass --init-script <path>');
  process.exit(1);
}

// Chrome launch args — same as patchright-mcp-cell config
const CHROME_ARGS = [
  '--no-sandbox',
  '--use-gl=angle',
  '--use-angle=vulkan',
  '--ignore-gpu-blocklist',
  '--window-size=1920,1040',
  '--force-device-scale-factor=1',
  '--disable-features=AudioServiceSandbox',
  '--autoplay-policy=no-user-gesture-required',
  '--disable-blink-features=AutomationControlled',
];
// Append extra chrome flags from CLI: --chrome-args="--flag1 --flag2"
const extraIdx = process.argv.indexOf('--chrome-args');
if (extraIdx !== -1 && process.argv[extraIdx + 1]) {
  CHROME_ARGS.push(...process.argv[extraIdx + 1].split(/\s+/).filter(Boolean));
}

const TESTS = {
  creepjs: {
    url: 'https://abrahamjuliot.github.io/creepjs/',
    waitSec: 22,
    extract: async (page) => {
      const r = await page.evaluate(() => {
        const r = {};
        const text = document.body.innerText;

        // Headless scores
        const likeMatch = text.match(/(\d+)% like headless/);
        const headlessMatch = text.match(/(\d+)% headless:/);
        const stealthMatch = text.match(/(\d+)% stealth:/);
        r.likeHeadless = likeMatch ? parseInt(likeMatch[1]) : null;
        r.headless = headlessMatch ? parseInt(headlessMatch[1]) : null;
        r.stealth = stealthMatch ? parseInt(stealthMatch[1]) : null;
        r.undetection = r.likeHeadless != null ? (100 - r.likeHeadless) + '%' : 'unknown';

        // Screen
        const screenMatch = text.match(/screen query:\s*(\d+)\s*x\s*(\d+)/);
        r.screenQuery = screenMatch ? `${screenMatch[1]}x${screenMatch[2]}` : 'unknown';

        // Viewport line
        const vpSection = document.querySelector('[class*="screen"]') || document.body;
        const vpText = text.match(/viewport:[\s\S]*?(\d+).*?(\d+).*?(\d+).*?(\d+).*?(\d+).*?(\d+).*?(browser|mobile).*?(portrait|landscape)/);

        // @media vs matchMedia
        const mediaMatch = text.match(/@media:\s*([a-f0-9]+)/);
        const matchMediaMatch = text.match(/matchMedia:\s*([a-f0-9]+)/);
        r.mediaHash = mediaMatch ? mediaMatch[1] : 'unknown';
        r.matchMediaHash = matchMediaMatch ? matchMediaMatch[1] : 'unknown';
        r.mediaConsistent = r.mediaHash === r.matchMediaHash;

        // WebGL GPU
        const gpuLines = [...document.querySelectorAll('*')].filter(el =>
          el.textContent.includes('ANGLE') || el.textContent.includes('Intel')
        );
        // Get the actual displayed GPU text from the WebGL section
        const webglSection = text.match(/WebGL[\s\S]*?gpu:[\s\S]*?((?:Intel|Google|ANGLE)[\s\S]*?)(?=\n\s*\n|\nimages)/);
        r.webglGpu = webglSection ? webglSection[1].trim().substring(0, 120) : 'unknown';

        // Worker
        const workerArch = text.match(/Linux\s+(arm_64|x86_64)\s*$/m);
        r.workerArch = workerArch ? workerArch[1] : 'unknown';

        // Worker GPU
        const workerGpuMatch = text.match(/Worker[\s\S]*?gpu:[\s\S]*?((?:Intel|Google|ANGLE|unsupported)[\s\S]*?)(?=\n\s*userAgent)/);
        r.workerGpu = workerGpuMatch ? workerGpuMatch[1].trim().substring(0, 120) : 'unknown';

        // Capture the Headless section which contains individual signal details
        const headlessIdx = text.indexOf('Headless');
        r.headlessSection = headlessIdx > -1 ? text.substring(headlessIdx, headlessIdx + 800).trim() : 'not found';

        // Capture the WebGL section
        const webglIdx = text.indexOf('WebGL');
        r.webglSection = webglIdx > -1 ? text.substring(webglIdx, webglIdx + 600).trim() : 'not found';

        // Capture the Lies section (detects prototype tampering)
        const liesIdx = text.indexOf('Lies');
        r.liesSection = liesIdx > -1 ? text.substring(liesIdx, liesIdx + 400).trim() : 'not found';

        return r;
      });

      // Direct WebGL check — run in main world (isolatedContext: false)
      // Patchright runs page.evaluate() in an isolated world by default,
      // which bypasses init-script prototype patches. isolatedContext: false
      // verifies that init-script patches work in the page's own world.
      try {
        const direct = await page.evaluate(() => {
          const c = document.createElement('canvas');
          const gl = c.getContext('webgl');
          if (!gl) return { error: 'no webgl' };
          const ext = gl.getExtension('WEBGL_debug_renderer_info');
          return {
            vendor: ext ? gl.getParameter(ext.UNMASKED_VENDOR_WEBGL) : 'no ext',
            renderer: ext ? gl.getParameter(ext.UNMASKED_RENDERER_WEBGL) : 'no ext',
            maxTexSize: gl.getParameter(gl.MAX_TEXTURE_SIZE),
            maxSamples: gl.getParameter(36183),
          };
        }, undefined, false);
        r.directGlVendor = direct.vendor;
        r.directGlRenderer = direct.renderer;
        r.directMaxTexSize = direct.maxTexSize;
        r.directMaxSamples = direct.maxSamples;
      } catch(e) { r.directGlError = e.message; }

      return r;
    }
  },
  sannysoft: {
    url: 'https://bot.sannysoft.com/',
    waitSec: 8,
    extract: async (page) => {
      return page.evaluate(() => {
        const rows = [...document.querySelectorAll('table tr')];
        const results = {};
        for (const row of rows) {
          const cells = row.querySelectorAll('td');
          if (cells.length >= 2) {
            const key = cells[0].textContent.trim();
            const val = cells[cells.length - 1].textContent.trim();
            const cls = cells[cells.length - 1].className || '';
            results[key] = { value: val, pass: cls.includes('passed') || !cls.includes('failed') };
          }
        }
        // Count passes/fails
        const entries = Object.values(results);
        const passed = entries.filter(e => e.pass).length;
        const failed = entries.filter(e => !e.pass).length;
        return { passed, failed, total: entries.length, details: results };
      });
    }
  },
  incolumitas: {
    url: 'https://bot.incolumitas.com/',
    waitSec: 15,
    extract: async (page) => {
      return page.evaluate(() => {
        const text = document.body.innerText;
        const botScore = text.match(/Bot Score[:\s]*([0-9.]+)/i);
        const humanScore = text.match(/Human Score[:\s]*([0-9.]+)/i);
        // Try to get the detection results table
        const results = {};
        const items = document.querySelectorAll('.test-result, [class*="result"]');
        items.forEach(el => {
          const name = el.querySelector('.test-name, .name')?.textContent?.trim();
          const val = el.querySelector('.test-value, .value')?.textContent?.trim();
          if (name) results[name] = val;
        });
        return {
          botScore: botScore ? parseFloat(botScore[1]) : null,
          humanScore: humanScore ? parseFloat(humanScore[1]) : null,
          text: text.substring(0, 2000),
          details: results
        };
      });
    }
  }
};

async function main() {
  const initScript = findInitScript();
  console.log(`Init-script: ${initScript}`);

  // Which tests to run
  const onlyIdx = process.argv.indexOf('--only');
  const onlyTests = onlyIdx !== -1 ? process.argv[onlyIdx + 1].split(',') : Object.keys(TESTS);

  console.log(`Tests: ${onlyTests.join(', ')}`);
  console.log(`Chrome args: ${CHROME_ARGS.length} flags`);
  console.log('---');

  const browser = await chromium.launch({
    headless: false, // Patchright stealth mode (not Chromium headless)
    args: CHROME_ARGS,
  });

  const context = await browser.newContext({
    viewport: null, // Use window-size from args
  });

  // Inject init-script
  const script = fs.readFileSync(initScript, 'utf8');
  await context.addInitScript(script);

  for (const testName of onlyTests) {
    const test = TESTS[testName];
    if (!test) { console.log(`Unknown test: ${testName}`); continue; }

    console.log(`\n=== ${testName.toUpperCase()} ===`);
    console.log(`URL: ${test.url}`);

    const page = await context.newPage();
    try {
      await page.goto(test.url, { timeout: 30000, waitUntil: 'domcontentloaded' });
      console.log(`Waiting ${test.waitSec}s for results...`);
      await page.waitForTimeout(test.waitSec * 1000);

      const results = await test.extract(page);
      console.log(JSON.stringify(results, null, 2));
    } catch (err) {
      console.log(`ERROR: ${err.message}`);
    } finally {
      await page.close();
    }
  }

  await browser.close();
  console.log('\n--- done ---');
}

main().catch(err => { console.error(err); process.exit(1); });
