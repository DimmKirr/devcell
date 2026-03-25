#!/usr/bin/env node
// headless-signals-diag.js — Checks each CreepJS "like headless" signal individually.
// Usage: node test/headless-signals-diag.js [--init-script path]

const { chromium } = require('/opt/npm-tools/node_modules/patchright');
const fs = require('fs');
const path = require('path');

function findInitScript() {
  const argIdx = process.argv.indexOf('--init-script');
  if (argIdx !== -1 && process.argv[argIdx + 1]) return process.argv[argIdx + 1];
  const local = path.join(__dirname, '..', 'stealth-init.dev.js');
  if (fs.existsSync(local)) return local;
  console.error('No init-script found'); process.exit(1);
}

const CHROME_ARGS = [
  '--no-sandbox',
  '--use-gl=angle',
  '--use-angle=swiftshader',
  '--enable-unsafe-swiftshader',
  '--ozone-platform=headless',
  '--ozone-override-screen-size=1920,1080',
  '--window-size=1920,1040',
  '--force-device-scale-factor=1',
  '--disable-features=AudioServiceSandbox',
  '--autoplay-policy=no-user-gesture-required',
  '--disable-blink-features=AutomationControlled',
  '--force-dark-mode',
  '--blink-settings=preferredColorScheme=1',
];

async function main() {
  const initScript = findInitScript();
  console.log('Init-script:', initScript);
  console.log('Chrome args:', CHROME_ARGS.length, 'flags');

  const browser = await chromium.launch({ headless: false, args: CHROME_ARGS });
  const context = await browser.newContext({ viewport: null });
  const script = fs.readFileSync(initScript, 'utf8');
  await context.addInitScript(script);

  const page = await context.newPage();
  await page.goto('data:text/html,<html><body></body></html>', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(500);

  // Use exposeFunction FIRST, before any addScriptTag
  let diagResult = null;
  await page.exposeFunction('__reportDiag', (data) => {
    diagResult = JSON.parse(data);
  });

  // Single addScriptTag with complete diagnostic
  await page.addScriptTag({ content: `
    (async () => {
      const IS_BLINK = ('chrome' in window && 'CSS' in window && CSS.supports('accent-color: initial'));
      const signals = {};

      signals.noChrome = IS_BLINK && !('chrome' in window);

      try {
        if (IS_BLINK && 'permissions' in navigator) {
          const res = await navigator.permissions.query({ name: 'notifications' });
          signals.hasPermissionsBug = (
            res.state === 'prompt' &&
            'Notification' in window &&
            Notification.permission === 'denied'
          );
          signals._permState = res.state;
          signals._notifPerm = 'Notification' in window ? Notification.permission : 'no Notification';
        } else {
          signals.hasPermissionsBug = false;
        }
      } catch(e) { signals.hasPermissionsBug = 'error: ' + e.message; }

      signals.noPlugins = IS_BLINK && navigator.plugins.length === 0;
      signals._pluginsLen = navigator.plugins.length;
      signals.noMimeTypes = IS_BLINK && navigator.mimeTypes.length === 0;
      signals._mimeTypesLen = navigator.mimeTypes.length;

      signals.notificationIsDenied = (IS_BLINK && 'Notification' in window && Notification.permission === 'denied');
      signals._notifPermission = typeof Notification !== 'undefined' ? Notification.permission : 'no Notification';

      try {
        const div = document.createElement('div');
        document.body.appendChild(div);
        div.setAttribute('style', 'background-color: ActiveText');
        const cs = getComputedStyle(div);
        const activeText = cs.backgroundColor;
        document.body.removeChild(div);
        signals.hasKnownBgColor = activeText === 'rgb(255, 0, 0)';
        signals._activeTextColor = activeText;
      } catch(e) { signals.hasKnownBgColor = 'error: ' + e.message; }

      signals.prefersLightColor = matchMedia('(prefers-color-scheme: light)').matches;
      signals._prefersDark = matchMedia('(prefers-color-scheme: dark)').matches;

      if ('userAgentData' in navigator) {
        signals.uaDataIsBlank = navigator.userAgentData?.platform === '';
        signals._uaDataPlatform = navigator.userAgentData?.platform;
      } else {
        signals.uaDataIsBlank = false;
      }

      signals.pdfIsDisabled = 'pdfViewerEnabled' in navigator && navigator.pdfViewerEnabled === false;
      signals._pdfViewerEnabled = navigator.pdfViewerEnabled;

      signals.noTaskbar = screen.height === screen.availHeight && screen.width === screen.availWidth;
      signals._screen = screen.width + 'x' + screen.height;
      signals._avail = screen.availWidth + 'x' + screen.availHeight;

      signals.hasVvpScreenRes = (
        (innerWidth === screen.width && outerHeight === screen.height) || (
          'visualViewport' in window &&
          (visualViewport.width === screen.width && visualViewport.height === screen.height)
        )
      );
      signals._dims = {
        innerW: innerWidth, outerH: outerHeight,
        vvW: window.visualViewport?.width, vvH: window.visualViewport?.height,
        scrW: screen.width, scrH: screen.height
      };

      signals.hasSwiftShader = 'WORKER_CHECK';
      signals.noWebShare = IS_BLINK && CSS.supports('accent-color: initial') && (
        !('share' in navigator) || !('canShare' in navigator)
      );
      signals.noContentIndex = !('ContentIndex' in window);
      signals.noContactsManager = !('ContactsManager' in window);
      signals.noDownlinkMax = navigator.connection?.downlinkMax === undefined;
      signals._downlinkMax = navigator.connection?.downlinkMax;

      const triggering = Object.entries(signals)
        .filter(([k, v]) => !k.startsWith('_') && v === true)
        .map(([k]) => k);
      signals._summary = { total: 16, triggering: triggering.length, signals: triggering };

      window.__reportDiag(JSON.stringify(signals));
    })();
  `});

  await page.waitForTimeout(3000);

  if (!diagResult) {
    console.error('Failed to get diagnostic results');
    await browser.close();
    process.exit(1);
  }

  console.log('\\n=== HEADLESS SIGNAL DIAGNOSTIC ===\\n');
  const signalNames = [
    'noChrome', 'hasPermissionsBug', 'noPlugins', 'noMimeTypes',
    'notificationIsDenied', 'hasKnownBgColor', 'prefersLightColor',
    'uaDataIsBlank', 'pdfIsDisabled', 'noTaskbar', 'hasVvpScreenRes',
    'hasSwiftShader', 'noWebShare', 'noContentIndex', 'noContactsManager',
    'noDownlinkMax'
  ];
  for (const name of signalNames) {
    const val = diagResult[name];
    const icon = val === true ? 'FAIL' : val === false ? ' ok ' : '????';
    console.log(`[${icon}] ${name}: ${JSON.stringify(val)}`);
  }
  console.log('\\n=== SUMMARY ===');
  console.log(JSON.stringify(diagResult._summary, null, 2));
  console.log('\\n=== DEBUG ===');
  for (const [k, v] of Object.entries(diagResult)) {
    if (k.startsWith('_') && k !== '_summary') console.log(`  ${k}: ${JSON.stringify(v)}`);
  }

  await browser.close();
}

main().catch(err => { console.error(err); process.exit(1); });
