#!/usr/bin/env node
// bot-detect-test.js — standalone Patchright test runner for bot detection sites.
//
// Usage:
//   node test/js/bot-detect-test.js [--assert] [--only creepjs|sannysoft|direct] [--init-script path]
//
// --assert   Exit 1 on any failed assertion (for TDD / CI).
//            Without --assert, prints results and always exits 0 (investigation mode).
//
// Uses the same Chrome flags, stealth plugin, and init-script as patchright-mcp-cell.

const MCP_PKG = '/opt/devcell/.local/state/nix/profiles/profile/lib/node_modules/nix-patchright-mcp-server/node_modules';

// Load the stealth plugin setup BEFORE importing patchright — this is what
// patchright-mcp-cell does at runtime (via playwright-extra-setup.js).
// Without this, the test uses vanilla patchright and misses the conflict
// between puppeteer-extra-plugin-stealth's Proxy-based WebGL patch and
// our Object.defineProperty-based patch in stealth-init.js.
// --setup-script <path> overrides for testing patched versions.
// Load stealth plugin with conflicting evasions disabled.
// patchright-core no-ops addInitScript (intentional anti-detection), so the
// stealth plugin's JS-injection evasions silently fail anyway. But we still
// disable webgl.vendor and user-agent-override explicitly because they
// interfere with our extension-injected stealth-init.js when patchright
// eventually restores addInitScript or uses a different injection path.
const setupIdx = process.argv.indexOf('--setup-script');
if (setupIdx !== -1 && process.argv[setupIdx + 1]) {
  require(process.argv[setupIdx + 1]);
} else {
  const { addExtra } = require(MCP_PKG + '/playwright-extra');
  const StealthPlugin = require(MCP_PKG + '/puppeteer-extra-plugin-stealth');
  const patchright = require(MCP_PKG + '/patchright-core');
  const stealth = StealthPlugin();
  ['webgl.vendor', 'user-agent-override'].forEach(e => stealth.enabledEvasions.delete(e));
  const extra = addExtra(patchright.chromium);
  extra.use(stealth);
  patchright.chromium = extra;
  const cacheEntry = require.cache[require.resolve(MCP_PKG + '/patchright-core')];
  if (cacheEntry) cacheEntry.exports = patchright;
}
const { chromium } = require(MCP_PKG + '/patchright-core');

const fs = require('fs');
const os = require('os');
const path = require('path');

// ── CLI parsing ──────────────────────────────────────────────────────────────

const ASSERT_MODE = process.argv.includes('--assert');

function findInitScript() {
  const argIdx = process.argv.indexOf('--init-script');
  if (argIdx !== -1 && process.argv[argIdx + 1]) return process.argv[argIdx + 1];
  try {
    const bin = fs.realpathSync('/opt/devcell/.local/state/nix/profiles/profile/bin/patchright-mcp-cell');
    const share = path.join(path.dirname(path.dirname(bin)), 'share', 'patchright');
    const f = path.join(share, 'stealth-init.js');
    if (fs.existsSync(f)) return f;
  } catch {}
  const local = path.join(__dirname, '..', 'stealth-init.dev.js');
  if (fs.existsSync(local)) return local;
  console.error('No init-script found. Pass --init-script <path>');
  process.exit(1);
}

// Resolve stealth extension directory: CLI arg > bundled in nix profile
function findStealthExt() {
  const argIdx = process.argv.indexOf('--stealth-ext');
  if (argIdx !== -1 && process.argv[argIdx + 1]) return process.argv[argIdx + 1];
  try {
    const bin = fs.realpathSync('/opt/devcell/.local/state/nix/profiles/profile/bin/patchright-mcp-cell');
    const share = path.join(path.dirname(path.dirname(bin)), 'share', 'patchright');
    const ext = path.join(share, 'stealth-extension');
    if (fs.existsSync(path.join(ext, 'manifest.json'))) return ext;
  } catch {}
  return null;
}

// Chrome launch args — same as patchright-mcp-cell config.json
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
const extraIdx = process.argv.indexOf('--chrome-args');
if (extraIdx !== -1 && process.argv[extraIdx + 1]) {
  CHROME_ARGS.push(...process.argv[extraIdx + 1].split(/\s+/).filter(Boolean));
}

// ── Assertion tracker ────────────────────────────────────────────────────────

const failures = [];

function assert(name, actual, expected, cmp) {
  const op = cmp || '===';
  let pass;
  if (op === '===') pass = actual === expected;
  else if (op === '!==') pass = actual !== expected;
  else if (op === 'includes') pass = typeof actual === 'string' && actual.includes(expected);
  else if (op === '!includes') pass = typeof actual !== 'string' || !actual.includes(expected);
  else if (op === '<') pass = actual < expected;
  else if (op === '>=') pass = actual >= expected;
  else pass = actual === expected;

  const status = pass ? 'PASS' : 'FAIL';
  const line = `  [${status}] ${name}: got ${JSON.stringify(actual)}` +
    (pass ? '' : `, want ${op} ${JSON.stringify(expected)}`);
  console.log(line);
  if (!pass) failures.push(name);
}

// ── Test definitions ─────────────────────────────────────────────────────────

const TESTS = {
  // Fast local checks — no external network. Served via tiny HTTP server
  // because both the Chrome extension (world: MAIN) and patchright's
  // route-based addInitScript require HTTP responses to inject into.
  direct: {
    url: '__HTTP_PROBE__',
    waitSec: 1,
    extract: async (page) => {
      // Read results from DOM — the probe HTML runs checks in the MAIN world
      // and writes JSON to #results. We read via evaluate (utility world) which
      // shares the DOM but not JavaScript globals.
      const text = await page.evaluate(() => document.getElementById('results').textContent);
      return JSON.parse(text);
    },
    assertions: (r) => {
      // WebGL spoof
      assert('webgl-vendor', r.webglVendor, 'Intel Inc.');
      assert('webgl-renderer', r.webglRenderer, 'Intel Iris OpenGL Engine');
      assert('webgl-maxCubeMap', r.maxCubeMap, 16384);
      assert('webgl-maxFragUniforms', r.maxFragUniforms, 1024);

      // Navigator spoofs
      assert('webdriver', r.webdriver, true, '!==');
      assert('plugins-gte-3', r.pluginsLength, 3, '>=');
      assert('pdfViewerEnabled', r.pdfViewerEnabled, true);

      // Architecture — must not leak real arm64
      assert('hea-architecture', r.heaArchitecture, 'x86');

      // Battery — must be present and spoofed (not VM defaults charging:true/level:1)
      assert('battery-available', r.batteryError, undefined);
      assert('battery-not-charging', r.batteryCharging, false);
      assert('battery-level-not-full', r.batteryLevel, 1, '!==');

      // Network info — must show spoofed values
      assert('net-rtt', r.netRtt, 50);
      assert('net-downlink', r.netDownlink, 10.5);

      // Screen
      assert('screen-width', r.screenWidth, 1920);
      assert('screen-height', r.screenHeight, 1080);

      // Speech synthesis — must return fake voices in container (DIMM-297)
      assert('speech-voices-gte-1', r.speechVoices, 1, '>=');
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
        const entries = Object.values(results);
        const passed = entries.filter(e => e.pass).length;
        const failed = entries.filter(e => !e.pass).length;
        return { passed, failed, total: entries.length, details: results };
      });
    },
    assertions: (r) => {
      assert('sannysoft-webgl-vendor', r.details?.['WebGL Vendor']?.value, 'Intel Inc.');
      assert('sannysoft-webgl-renderer', r.details?.['WebGL Renderer']?.value, 'Intel Iris OpenGL Engine');
      assert('sannysoft-webdriver', r.details?.['WebDriver (New)']?.value, 'missing (passed)');
      assert('sannysoft-chrome-present', r.details?.['Chrome (New)']?.value, 'present (passed)');
      assert('sannysoft-no-failures', r.failed, 0);
    }
  },

  creepjs: {
    url: 'https://abrahamjuliot.github.io/creepjs/',
    waitSec: 22,
    extract: async (page) => {
      const r = await page.evaluate(() => {
        const r = {};
        const text = document.body.innerText;

        const likeMatch = text.match(/(\d+)% like headless/);
        const headlessMatch = text.match(/(\d+)% headless:/);
        const stealthMatch = text.match(/(\d+)% stealth:/);
        r.likeHeadless = likeMatch ? parseInt(likeMatch[1]) : null;
        r.headless = headlessMatch ? parseInt(headlessMatch[1]) : null;
        r.stealth = stealthMatch ? parseInt(stealthMatch[1]) : null;

        // Worker arch leak
        const workerArch = text.match(/Linux\s+\S+\s+(arm_64|x86_64)/m);
        r.workerArch = workerArch ? workerArch[1] : 'unknown';

        // WebGL GPU from the WebGL section
        const webglSection = text.match(/WebGL[\s\S]*?gpu:[\s\S]*?((?:Intel|Google|ANGLE)[\s\S]*?)(?=\n\s*\n|\nimages)/);
        r.webglGpu = webglSection ? webglSection[1].trim().substring(0, 120) : 'unknown';

        // Worker GPU
        const workerGpuMatch = text.match(/Worker[\s\S]*?gpu:[\s\S]*?((?:Intel|Google|ANGLE|unsupported)[\s\S]*?)(?=\n\s*userAgent)/);
        r.workerGpu = workerGpuMatch ? workerGpuMatch[1].trim().substring(0, 120) : 'unknown';

        return r;
      });
      return r;
    },
    assertions: (r) => {
      assert('creepjs-like-headless', r.likeHeadless || 0, 50, '<');
      assert('creepjs-stealth', r.stealth || 0, 10, '<');
      assert('creepjs-worker-arch-no-arm', r.workerArch, 'arm_64', '!==');
      assert('creepjs-webgl-not-mesa', r.webglGpu, 'Mesa', '!includes');
      assert('creepjs-worker-gpu-not-mesa', r.workerGpu, 'Mesa', '!includes');
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

// ── Main ─────────────────────────────────────────────────────────────────────

async function main() {
  const initScript = findInitScript();
  const stealthExt = findStealthExt();
  console.log(`Init-script: ${initScript}`);
  console.log(`Stealth ext: ${stealthExt || 'none'}`);
  console.log(`Assert mode: ${ASSERT_MODE}`);

  const onlyIdx = process.argv.indexOf('--only');
  const onlyTests = onlyIdx !== -1 ? process.argv[onlyIdx + 1].split(',') : ['direct', 'sannysoft', 'creepjs'];

  console.log(`Tests: ${onlyTests.join(', ')}`);
  console.log(`Chrome args: ${CHROME_ARGS.length} flags`);
  console.log('---');

  // Test the PRODUCTION injection path: addInitScript (route-based in patchright).
  // Extension (--stealth-ext) is an optional override for extension-based injection testing.
  const launchArgs = [...CHROME_ARGS];
  if (stealthExt) {
    console.log('WARNING: using extension override — not testing production addInitScript path');
    launchArgs.push(`--load-extension=${stealthExt}`);
    launchArgs.push(`--disable-extensions-except=${stealthExt}`);
  }

  // Tiny HTTP server for the local probe page (extensions + route-based
  // addInitScript need HTTP, not file://).
  const http = require('http');
  const probeHtml = fs.readFileSync(path.join(__dirname, 'stealth-probe.html'), 'utf8');
  const probeServer = http.createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/html' });
    res.end(probeHtml);
  });
  await new Promise(r => probeServer.listen(0, '127.0.0.1', r));
  const probeUrl = `http://127.0.0.1:${probeServer.address().port}`;

  const profileDir = process.env.PROFILE_DIR || path.join(os.tmpdir(), `devcell-bot-test-${process.pid}`);
  // Match MCP's patchright-mcp-cell launch config exactly:
  //   - serviceWorkers: block (from config.json contextOptions)
  //   - all 3 init scripts (stealth, keep-alive, network-capture)
  const context = await chromium.launchPersistentContext(profileDir, {
    headless: false,
    args: launchArgs,
    viewport: null,
    serviceWorkers: 'block',
  });

  // Load all init scripts matching MCP's patchright-mcp-cell wrapper.
  const shareDir = path.dirname(initScript);
  const initScripts = [
    initScript,                                             // stealth-init.js
    path.join(shareDir, 'keep-alive-init.js'),
    path.join(shareDir, 'network-capture-init.js'),
  ];
  for (const scriptPath of initScripts) {
    if (fs.existsSync(scriptPath)) {
      await context.addInitScript(fs.readFileSync(scriptPath, 'utf8'));
    }
  }

  for (const testName of onlyTests) {
    const test = TESTS[testName];
    if (!test) { console.log(`Unknown test: ${testName}`); continue; }

    console.log(`\n=== ${testName.toUpperCase()} ===`);
    const url = test.url === '__HTTP_PROBE__' ? probeUrl : test.url;
    console.log(`URL: ${url}`);

    const page = await context.newPage();
    try {
      await page.goto(url, { timeout: 30000, waitUntil: 'domcontentloaded' });
      if (test.waitSec > 0) {
        console.log(`Waiting ${test.waitSec}s for results...`);
        await page.waitForTimeout(test.waitSec * 1000);
      }

      const results = await test.extract(page);
      console.log(JSON.stringify(results, null, 2));

      if (test.assertions) {
        console.log('\nAssertions:');
        test.assertions(results);
      }
    } catch (err) {
      console.log(`ERROR: ${err.message}`);
      if (ASSERT_MODE) failures.push(`${testName}-error`);
    } finally {
      await page.close();
    }
  }

  await context.close();
  probeServer.close();

  // Summary
  console.log(`\n--- ${failures.length === 0 ? 'ALL PASSED' : failures.length + ' FAILED'} ---`);
  if (failures.length > 0) {
    console.log('Failures:');
    for (const f of failures) console.log(`  - ${f}`);
  }

  if (ASSERT_MODE && failures.length > 0) process.exit(1);
}

main().catch(err => { console.error(err); process.exit(1); });
