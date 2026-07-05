# scraping/default.nix -- Patchright MCP server + stealth Chromium automation
# Self-contained module: buildNpmPackage, playwright-driver browsers, stealth
# init script, config JSON, and wrapper script. No dependency on desktop/.
#
# Interactive browsing: nix chromium wrapper (--no-sandbox, per-app profile).
# Automation: Patchright's bundled Chromium (stealth -- no webdriver leak).
# Do NOT set PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH -- it overrides the patched binary.
{pkgs, lib, config, ...}:
let
  cfg = config.devcell.modules.scraping;
  mcpCfg = config.devcell.managedMcp;

  # Third-party Chromium extensions (CELL-37). Options + registry live in
  # ./extensions.nix; this just reads the resolved store paths and folds them
  # into Chromium's --load-extension / --disable-extensions-except flags.
  # Empty list (the default) -> flags are omitted entirely so an empty arg
  # doesn't sneak in and confuse chromium's flag parser.
  extensionPaths = config.devcell.scraping.extensionPaths;
  extensionPathsCsv = lib.concatStringsSep "," (map toString extensionPaths);
  extensionArgs = lib.optionals (extensionPaths != []) [
    "--load-extension=${extensionPathsCsv}"
    "--disable-extensions-except=${extensionPathsCsv}"
  ];


  # Chromium browser from playwright-driver -- chromium only, no firefox/webkit/ffmpeg.
  # patchright-core reads browsers.json for expected revision (e.g. 1208) but nixpkgs
  # playwright-driver may ship a different revision (e.g. 1194). Bridge with symlinks.
  patchrightChromiumRevision = "1223";
  baseBrowsers = pkgs.playwright-driver.browsers.override {
    withFirefox = false;
    withWebkit = false;
    withFfmpeg = false;
  };
  browsers = pkgs.runCommandLocal "patchright-browsers" {} ''
    mkdir -p $out
    for entry in ${baseBrowsers}/*; do
      ln -s "$(readlink "$entry")" "$out/$(basename "$entry")"
    done
    # Add symlinks for expected patchright revision if not already present
    if [ ! -e "$out/chromium-${patchrightChromiumRevision}" ]; then
      actual=$(ls -d ${baseBrowsers}/chromium-[0-9]* | head -1)
      [ -n "$actual" ] && ln -s "$(readlink "$actual")" "$out/chromium-${patchrightChromiumRevision}"
    fi
    if [ ! -e "$out/chromium_headless_shell-${patchrightChromiumRevision}" ]; then
      actual=$(ls -d ${baseBrowsers}/chromium_headless_shell-[0-9]* 2>/dev/null | head -1)
      [ -n "$actual" ] && ln -s "$(readlink "$actual")" "$out/chromium_headless_shell-${patchrightChromiumRevision}"
    fi
  '';

  # buildNpmPackage derivation for patchright MCP server
  patchrightMcp = pkgs.buildNpmPackage {
    pname = "mcp-server-patchright";
    version = "0.0.68-pr1.60.2";
    src = pkgs.runCommandLocal "patchright-mcp-src" {} ''
      mkdir -p $out
      cp ${./patchright-mcp-package.json} $out/package.json
      cp ${./patchright-mcp-package-lock.json} $out/package-lock.json
    '';
    npmDepsHash = "sha256-mteUzPzn90hUwmKXcOsYH/n1nlvpPU3Hgc0zsAlUYe0=";
    npmPackFlags = [ "--ignore-scripts" ];
    npmFlags = [ "--ignore-scripts" ];
    dontNpmBuild = true;
    nativeBuildInputs = [ pkgs.makeWrapper ];

    postInstall = ''
      MCP="$out/lib/node_modules/nix-patchright-mcp-server/node_modules/patchright-mcp"

      # --- Fix patchright 1.60 import path migration ---
      # patchright 1.60 moved MCP code from patchright/lib/mcp/* to patchright-core/lib/coreBundle.
      # Patch cli.js: decorateCommand → decorateMCPCommand from coreBundle.tools
      ${pkgs.gnused}/bin/sed -i \
        "s|const { decorateCommand } = require('patchright/lib/mcp/program');|const { tools: _mcpTools } = require('patchright-core/lib/coreBundle');|" \
        "$MCP/cli.js"
      ${pkgs.gnused}/bin/sed -i \
        "s|decorateCommand(p, packageJSON.version)|_mcpTools.decorateMCPCommand(p, packageJSON.version)|" \
        "$MCP/cli.js"
      echo "Patched cli.js: patchright/lib/mcp/program → patchright-core/lib/coreBundle"

      # Patch index.js: createConnection from coreBundle.tools
      ${pkgs.gnused}/bin/sed -i \
        "s|const { createConnection } = require('patchright/lib/mcp/index');|const { tools: _mcpTools } = require('patchright-core/lib/coreBundle'); const { createConnection } = _mcpTools;|" \
        "$MCP/index.js"
      echo "Patched index.js: patchright/lib/mcp/index → patchright-core/lib/coreBundle"

      # --- Inject human mouse movement into browser_click/browser_drag tools ---
      # In patchright 1.60+, snapshot.js is bundled in coreBundle. Instead of sed-patching
      # a minified bundle, we write a JS shim that monkey-patches the tool handlers at startup.
      SHIM="$out/lib/node_modules/nix-patchright-mcp-server/node_modules/patchright-mcp/devcell-mouse-shim.js"
      cat > "$SHIM" << 'SHIMEOF'
// devcell human mouse movement shim — wraps browser_click tool handler
// with Bezier cursor trajectory before each click.
const { tools } = require('patchright-core/lib/coreBundle');
let __hmLastX = 960, __hmLastY = 540;
async function __hmMove(page, tx, ty) {
  const sx = __hmLastX, sy = __hmLastY, dist = Math.hypot(tx-sx, ty-sy);
  if (dist < 2) { __hmLastX = tx; __hmLastY = ty; return; }
  if (dist < 50) {
    const st = 5 + ~~(Math.random()*5);
    for (let i = 1; i <= st; i++) {
      const t = i/st, e = t*t*(3-2*t);
      await page.mouse.move(sx+(tx-sx)*e+(Math.random()-.5)*2, sy+(ty-sy)*e+(Math.random()-.5)*2);
      await new Promise(r => setTimeout(r, 5+Math.random()*10));
    }
    await page.mouse.move(tx, ty);
    __hmLastX = tx; __hmLastY = ty; return;
  }
  const steps = Math.max(30, ~~(dist/5)+~~(Math.random()*20));
  const dur = 200 + dist*1.0 + Math.random()*250;
  const ang = Math.atan2(ty-sy, tx-sx), perp = ang + Math.PI/2;
  const arcMag = dist*(0.08+Math.random()*0.15)*(Math.random()>.5?1:-1);
  const cp1t = 0.2+Math.random()*0.15, cp2t = 0.65+Math.random()*0.15;
  const cx1 = sx+(tx-sx)*cp1t+Math.cos(perp)*arcMag, cy1 = sy+(ty-sy)*cp1t+Math.sin(perp)*arcMag;
  const cx2 = sx+(tx-sx)*cp2t+Math.cos(perp)*arcMag*0.6, cy2 = sy+(ty-sy)*cp2t+Math.sin(perp)*arcMag*0.6;
  const ov = 4+(dist/200)*5+Math.random()*4;
  const ox = tx+Math.cos(ang)*ov, oy = ty+Math.sin(ang)*ov;
  for (let i = 0; i <= steps; i++) {
    const t = i/steps;
    let e2; if (t<.5) e2=16*t*t*t*t*t; else { const f=-2*t+2; e2=1-f*f*f*f*f/2; }
    let x, y;
    if (t < .88) {
      const b = Math.min(e2/.88,1), u = 1-b;
      x = u*u*u*sx+3*u*u*b*cx1+3*u*b*b*cx2+b*b*b*ox;
      y = u*u*u*sy+3*u*u*b*cy1+3*u*b*b*cy2+b*b*b*oy;
    } else {
      const c = (t-.88)/.12, ce = c*c*(3-2*c);
      x = ox+(tx-ox)*ce; y = oy+(ty-oy)*ce;
    }
    const tr = 0.5+(1-Math.sin(t*Math.PI))*1.5;
    x += (Math.random()-.5)*tr; y += (Math.random()-.5)*tr;
    await page.mouse.move(x, y);
    const spd = 0.3+Math.sin(t*Math.PI)*1.0;
    await new Promise(r => setTimeout(r, (dur/steps)/spd+Math.random()*3));
  }
  await page.mouse.move(tx, ty);
  __hmLastX = tx; __hmLastY = ty;
}

// Wrap browser_click tool
const clickTool = tools.browserTools.find(t => t.schema && t.schema.name === 'browser_click');
if (clickTool) {
  const origHandle = clickTool.handle;
  clickTool.handle = async function(context, params) {
    // Move mouse to element before clicking
    try {
      const tab = context.currentTab || context._currentTab;
      if (tab && tab.page) {
        // Try to resolve the element position from the ref parameter
        const snapshot = tab.lastSnapshot;
        if (snapshot && params.ref) {
          const locator = snapshot.refLocator(params.ref);
          if (locator) {
            const box = await locator.boundingBox().catch(() => null);
            if (box) {
              const tx = box.x + box.width * (0.35 + Math.random() * 0.3);
              const ty = box.y + box.height * (0.35 + Math.random() * 0.3);
              await __hmMove(tab.page, tx, ty);
              await new Promise(r => setTimeout(r, 30 + Math.random() * 120));
            }
          }
        }
      }
    } catch (e) { /* non-critical — proceed with click */ }
    return origHandle.call(this, context, params);
  };
}

// Wrap browser_drag tool
const dragTool = tools.browserTools.find(t => t.schema && t.schema.name === 'browser_drag');
if (dragTool) {
  const origDragHandle = dragTool.handle;
  dragTool.handle = async function(context, params) {
    try {
      const tab = context.currentTab || context._currentTab;
      if (tab && tab.page && params.startRef) {
        const snapshot = tab.lastSnapshot;
        if (snapshot) {
          const locator = snapshot.refLocator(params.startRef);
          if (locator) {
            const box = await locator.boundingBox().catch(() => null);
            if (box) {
              const tx = box.x + box.width * 0.5;
              const ty = box.y + box.height * 0.5;
              await __hmMove(tab.page, tx, ty);
              await new Promise(r => setTimeout(r, 30 + Math.random() * 80));
            }
          }
        }
      }
    } catch (e) { /* non-critical */ }
    return origDragHandle.call(this, context, params);
  };
}
SHIMEOF
      # Inject the shim require at the top of cli.js (after the shebang)
      ${pkgs.gnused}/bin/sed -i '2i require("./devcell-mouse-shim");' "$MCP/cli.js"
      echo "Injected human mouse movement shim into cli.js"

      # --- Fix patchright-core calculateUserAgentMetadata: recognize aarch64 as arm ---
      # patchright-core hardcodes architecture="x86" and only checks ua.includes("ARM")
      # (uppercase). Our UA contains "aarch64" which isn't matched, so Sec-CH-UA-Arch
      # HTTP header reports "x86" on arm hosts. Also respects userAgentMetadata from
      # context options if explicitly provided (patchright ignores it).
      # Proof: patchright-core/lib/coreBundle.js calculateUserAgentMetadata() line ~37091
      # https://github.com/nicenemo/patchright/blob/main/packages/playwright-core/src/server/chromium/crPage.ts
      CORE="$out/lib/node_modules/nix-patchright-mcp-server/node_modules/patchright-core/lib/coreBundle.js"
      if [ -f "$CORE" ]; then
        ${pkgs.gnused}/bin/sed -i 's#if (ua.includes("ARM"))#if (ua.includes("ARM") || ua.includes("aarch64") || ua.includes("arm64"))#' "$CORE"
        echo "Patched coreBundle.js: calculateUserAgentMetadata recognizes aarch64/arm64 as arm"

        # CELL-150: patchright-core's calculateUserAgentMetadata omits the `bitness`
        # field entirely, producing empty Sec-CH-UA-Bitness HTTP header / JS
        # getHighEntropyValues().bitness on real Chrome (which always sends "64").
        # Wrap the call site so the result includes bitness:"64" plus any
        # user-supplied userAgentMetadata overrides from contextOptions.
        ${pkgs.gnused}/bin/sed -i 's#userAgentMetadata: calculateUserAgentMetadata(options2)#userAgentMetadata: Object.assign({ bitness: "64" }, calculateUserAgentMetadata(options2), options2.userAgentMetadata || {})#' "$CORE"
        if ${pkgs.gnugrep}/bin/grep -q 'bitness: "64"' "$CORE"; then
          echo "Patched coreBundle.js: _updateUserAgent injects bitness=64 (CELL-150)"
        else
          echo "ERROR: CELL-150 bitness patch did NOT apply — coreBundle.js layout changed"
          exit 1
        fi

        # CELL-69: real Chrome's Sec-CH-UA includes "Google Chrome" brand
        # alongside "Chromium" and a GREASE entry; patchright/Chromium ships
        # only Chromium + Not:A-Brand. Google's identifier-step classifier
        # rejects sign-in when "Google Chrome" is absent ("This browser or
        # app may not be secure"). Extend the CELL-150 Object.assign to also
        # inject brands + fullVersionList with all three brand entries.
        # Chrome major hardcoded to 141 (matches bundled chromium at time of
        # patch); grep guard fails the build if the assign signature drifts.
        ${pkgs.gnused}/bin/sed -i 's#Object.assign({ bitness: "64" }, calculateUserAgentMetadata(options2)#Object.assign({ bitness: "64", brands: [{brand:"Google Chrome",version:"141"},{brand:"Chromium",version:"141"},{brand:"Not?A_Brand",version:"8"}], fullVersionList: [{brand:"Google Chrome",version:"141.0.7390.108"},{brand:"Chromium",version:"141.0.7390.108"},{brand:"Not?A_Brand",version:"8.0.0.0"}] }, calculateUserAgentMetadata(options2)#' "$CORE"
        if ${pkgs.gnugrep}/bin/grep -q 'brand:"Google Chrome"' "$CORE"; then
          echo "Patched coreBundle.js: _updateUserAgent injects Google Chrome brand (CELL-69)"
        else
          echo "ERROR: CELL-69 brand patch did NOT apply — CELL-150 patch signature may have changed"
          exit 1
        fi
      else
        echo "WARNING: coreBundle.js not found at $CORE"
      fi

      # --- Disable stealth-plugin evasions that conflict with our stealth-init.js ---
      SETUP="$MCP/playwright-extra-setup.js"
      if [ -f "$SETUP" ]; then
        ${pkgs.gnused}/bin/sed -i "s|extra.use(StealthPlugin());|var _s = StealthPlugin(); ['webgl.vendor','user-agent-override'].forEach(function(e){_s.enabledEvasions.delete(e);}); extra.use(_s);|" "$SETUP"
        echo "Patched playwright-extra-setup.js: disabled webgl.vendor + user-agent-override evasions"
      else
        echo "WARNING: playwright-extra-setup.js not found at $SETUP"
      fi

      bin="$out/lib/node_modules/nix-patchright-mcp-server/node_modules/.bin"
      makeWrapper "$bin/mcp-server-patchright" "$out/bin/mcp-server-patchright" \
        --chdir "$bin" \
        --set PLAYWRIGHT_BROWSERS_PATH "${browsers}" \
        --set PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD "1"
    '';
  };

  # Static LD_LIBRARY_PATH fallback for the patchright-mcp-cell wrapper.
  # This wrapper is a nix derivation baked at eval time -- can't source files at runtime.
  # All other contexts (entrypoint services, interactive shells) use the merged
  # /opt/devcell/.nix-ld-libs/ directory via NIX_LD_LIBRARY_PATH.
  runtimeLibs = with pkgs; [
    glib
    nspr
    nss
    atk
    at-spi2-atk
    dbus
    cups
    libxkbcommon
    at-spi2-core
    xorg.libX11       # libX11 + libX11-xcb -- core X11 client lib (Electron SIGTRAP without it)
    xorg.libXcomposite
    xorg.libXcursor
    xorg.libXdamage
    xorg.libXext
    xorg.libXfixes
    xorg.libXi
    xorg.libXrandr
    xorg.libXtst
    xorg.libxkbfile
    libgbm        # GBM buffer manager -- mesa itself does NOT provide libgbm.so
    mesa          # Mesa 3D -- llvmpipe software rasterizer
    cairo
    pango
    alsa-lib
    pulseaudio    # PulseAudio client lib
    gcc.cc.lib    # libgomp (OpenMP runtime)
    gtk3          # GTK 3 -- needed by Electron/Chromium-based GUI apps
  ];
  runtimeLibPath = pkgs.lib.makeLibraryPath runtimeLibs;

  # Patchright MCP config -- Chromium launch args for X11 display.
  # No --ozone-platform needed (auto-detects X11 from DISPLAY).
  # WebGL via Mesa Lavapipe: ANGLE -> Vulkan -> lvp (CPU software renderer).
  # --ignore-gpu-blocklist prevents Chromium from disabling WebGL on software renderers.
  patchrightConfig = pkgs.writeTextFile {
    name = "patchright-mcp-config.json";
    text = builtins.toJSON {
      browser.launchOptions.args = [
        "--use-gl=angle"
        "--use-angle=vulkan"
        "--ignore-gpu-blocklist"
        "--window-size=1920,1040"
        "--force-device-scale-factor=1"
        "--disable-features=AudioServiceSandbox"
        "--autoplay-policy=no-user-gesture-required"
        "--disable-blink-features=AutomationControlled"
      ] ++ extensionArgs;
      # Block ServiceWorkers -- they run in a separate scope unreachable by init-script.
      # Forces detection scripts to fall back to SharedWorker, which we CAN intercept.
      browser.contextOptions.serviceWorkers = "block";
    };
  };

  stealthInitScript = pkgs.writeTextFile {
    name = "stealth-init.js";
    text = ''
      // Patch navigator.webdriver on the PROTOTYPE (instance-level patch doesn't stick
      // because Chromium defines it on Navigator.prototype, not the instance)
      Object.defineProperty(Navigator.prototype, 'webdriver', {
        get: () => undefined,
        configurable: true
      });

      // Ensure chrome.runtime exists -- Chromium may already provide window.chrome
      // as a non-configurable property (patchright 1.60+, headed mode). Extend
      // the existing object instead of replacing it.
      if (typeof window.chrome === 'undefined') {
        try {
          Object.defineProperty(window, 'chrome', {
            value: {},
            writable: true,
            configurable: true,
            enumerable: true
          });
        } catch (e) {
          window.chrome = {};
        }
      }
      if (!window.chrome.runtime) {
        window.chrome.runtime = { connect: function(){}, sendMessage: function(){} };
      }
      if (!window.chrome.loadTimes) {
        window.chrome.loadTimes = function() { return {}; };
      }
      if (!window.chrome.csi) {
        window.chrome.csi = function() { return {}; };
      }

      // --- Fix toString leaks (must be early -- WebGL patching uses _nativeFnNames) ---
      const origToString = Function.prototype.toString;
      const _nativeFnNames = new WeakMap();
      Function.prototype.toString = function() {
        const name = _nativeFnNames.get(this);
        if (name !== undefined) return 'function ' + name + '() { [native code] }';
        return origToString.call(this);
      };
      _nativeFnNames.set(Function.prototype.toString, 'toString');
      // Register webdriver getter
      const wdDesc = Object.getOwnPropertyDescriptor(Navigator.prototype, 'webdriver');
      if (wdDesc && wdDesc.get) _nativeFnNames.set(wdDesc.get, 'get webdriver');
      // Register chrome.runtime functions
      if (window.chrome && window.chrome.runtime) {
        if (window.chrome.runtime.connect) _nativeFnNames.set(window.chrome.runtime.connect, 'connect');
        if (window.chrome.runtime.sendMessage) _nativeFnNames.set(window.chrome.runtime.sendMessage, 'sendMessage');
        if (window.chrome.loadTimes) _nativeFnNames.set(window.chrome.loadTimes, 'loadTimes');
        if (window.chrome.csi) _nativeFnNames.set(window.chrome.csi, 'csi');
      }

      // Fix plugins + mimeTypes -- headless Chrome may have empty arrays.
      if (navigator.plugins.length === 0) {
        const pdfMime = { type: 'application/pdf', suffixes: 'pdf', description: 'Portable Document Format' };
        const fakePlugins = [
          { name: 'Chrome PDF Plugin', filename: 'internal-pdf-viewer', description: 'Portable Document Format', length: 1, 0: pdfMime },
          { name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai', description: ' ', length: 1, 0: pdfMime },
          { name: 'Native Client', filename: 'internal-nacl-plugin', description: ' ', length: 1, 0: pdfMime }
        ];
        Object.setPrototypeOf(fakePlugins, PluginArray.prototype);
        Object.defineProperty(navigator, 'plugins', { get: () => fakePlugins });
      }
      if (navigator.mimeTypes.length === 0) {
        const fakeMimes = [
          { type: 'application/pdf', suffixes: 'pdf', description: 'Portable Document Format', enabledPlugin: navigator.plugins[0] }
        ];
        Object.setPrototypeOf(fakeMimes, MimeTypeArray.prototype);
        Object.defineProperty(navigator, 'mimeTypes', { get: () => fakeMimes });
      }
      // Spoof pdfViewerEnabled (headless=new has false)
      Object.defineProperty(navigator, 'pdfViewerEnabled', { get: () => true, configurable: true });

      // Mock languages
      Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en'] });

      // Patch permissions
      const origQuery = window.navigator.permissions.query;
      window.navigator.permissions.query = (params) =>
        params.name === 'notifications'
          ? Promise.resolve({ state: Notification.permission })
          : origQuery(params);

      // Spoof userAgentData high-entropy values from __cellStealth global
      // (injected by wrapper from DEVCELL_STEALTH_ARCH / DEVCELL_STEALTH_PLATFORM).
      // CDP userAgentMetadata handles HTTP headers; this is belt-and-suspenders for JS.
      if (typeof NavigatorUAData !== 'undefined' && window.__cellStealth) {
        const origGetHigh = NavigatorUAData.prototype.getHighEntropyValues;
        Object.defineProperty(NavigatorUAData.prototype, 'getHighEntropyValues', {
          value: async function(hints) {
            const values = await origGetHigh.call(this, hints);
            values.architecture = window.__cellStealth.arch;
            if (window.__cellStealth.platform) values.platform = window.__cellStealth.platform;
            return values;
          },
          writable: true,
          configurable: true
        });
      }

      // Spoof navigator.platform from __cellStealth (always runs).
      // __cellFp override below can supersede this when fingerprint.json is present.
      if (window.__cellStealth) {
        const _stealthNavPlatform = window.__cellStealth.platform === 'macOS' ? 'MacIntel'
          : window.__cellStealth.arch === 'arm' ? 'Linux aarch64' : 'Linux x86_64';
        Object.defineProperty(Navigator.prototype, 'platform', {
          get: () => _stealthNavPlatform,
          configurable: true
        });
      }

      // Spoof platform + userAgentData from cell login fingerprint.
      // window.__cellFp is injected by the patchright-mcp-cell preamble init script
      // when $HOME/.playwright/fingerprint.json exists (written by `cell login` on macOS).
      if (window.__cellFp) {
        // navigator.platform -> "MacIntel" (overrides __cellStealth spoof above)
        Object.defineProperty(Navigator.prototype, 'platform', {
          get: () => window.__cellFp.platform || 'MacIntel',
          configurable: true
        });

        if (typeof NavigatorUAData !== 'undefined') {
          // navigator.userAgentData.platform -> "macOS"
          const _fpPlatformDesc = Object.getOwnPropertyDescriptor(NavigatorUAData.prototype, 'platform');
          if (_fpPlatformDesc) {
            Object.defineProperty(NavigatorUAData.prototype, 'platform', {
              get: () => window.__cellFp.uaPlatform || 'macOS',
              configurable: true
            });
          }

          // navigator.userAgentData.brands -> Chrome brands
          const _fpBrandsDesc = Object.getOwnPropertyDescriptor(NavigatorUAData.prototype, 'brands');
          if (_fpBrandsDesc && window.__cellFp.brands) {
            Object.defineProperty(NavigatorUAData.prototype, 'brands', {
              get: () => window.__cellFp.brands,
              configurable: true
            });
          }

          // Extend existing getHighEntropyValues to also return macOS platform + brands
          const _fpOrigGetHigh = NavigatorUAData.prototype.getHighEntropyValues;
          if (_fpOrigGetHigh) {
            Object.defineProperty(NavigatorUAData.prototype, 'getHighEntropyValues', {
              value: async function(hints) {
                const values = await _fpOrigGetHigh.call(this, hints);
                if (window.__cellFp.uaPlatform) values.platform = window.__cellFp.uaPlatform;
                if (window.__cellFp.brands) values.brands = window.__cellFp.brands;
                return values;
              },
              writable: true,
              configurable: true
            });
          }
        }
      }

      // --- Web Share API stubs (noWebShare signal) ---
      if (!navigator.share) {
        navigator.share = function(data) {
          return Promise.reject(new DOMException('Share canceled', 'AbortError'));
        };
      }
      if (!navigator.canShare) {
        navigator.canShare = function(data) { return true; };
      }

      // --- Media devices mock (headless has 0 devices -> bot signal) ---
      if (navigator.mediaDevices) {
        const _origEnum = navigator.mediaDevices.enumerateDevices;
        navigator.mediaDevices.enumerateDevices = async function() {
          const real = await _origEnum.call(this);
          if (real.length > 0) return real;
          return [
            { deviceId: 'default', kind: 'audioinput', label: "", groupId: 'default' },
            { deviceId: 'communications', kind: 'audiooutput', label: "", groupId: 'default' },
            { deviceId: 'default', kind: 'videoinput', label: "", groupId: 'camera1' }
          ];
        };
      }

      // Spoof WebGL renderer + parameters (hide SwiftShader fingerprint)
      // Use Object.defineProperty on WebGL prototypes -- works on ALL contexts
      // regardless of how they're created (Canvas, OffscreenCanvas, iframe).
      // Proxy-wrapping getContext gets bypassed by CreepJS; prototype patching doesn't.
      // CELL-70: Linux-appropriate WebGL strings. The previous values
      // ('Intel Inc.' / 'Intel Iris OpenGL Engine') are macOS Intel iGPU
      // strings that contradict the spoofed Linux aarch64 UA — amiunique
      // scores that combination at 1.22% similarity. Real Chrome on
      // Linux with software rasterization reports 'Google Inc. (Google)'
      // as the unmasked vendor and an ANGLE/SwiftShader renderer.
      const _wglVendor = 'Google Inc. (Google)';
      const _wglRenderer = 'ANGLE (Google, Vulkan 1.3.0 (SwiftShader Device (Subzero) (0x0000C0DE)), SwiftShader driver)';
      // Intel-realistic parameter overrides (SwiftShader defaults in comments)
      const _wglParams = {
        37445: _wglVendor,   // UNMASKED_VENDOR_WEBGL
        37446: _wglRenderer, // UNMASKED_RENDERER_WEBGL
        3379:  16384,        // MAX_TEXTURE_SIZE (SwiftShader: 8192)
        3386:  'viewport',   // MAX_VIEWPORT_DIMS -- special handling below
        34076: 16384,        // MAX_CUBE_MAP_TEXTURE_SIZE (SwiftShader: 8192)
        34024: 16384,        // MAX_RENDERBUFFER_SIZE (SwiftShader: 8192)
        34047: 16,           // MAX_TEXTURE_MAX_ANISOTROPY_EXT
        36349: 1024,         // MAX_FRAGMENT_UNIFORM_VECTORS (SwiftShader: 221)
        36347: 1024,         // MAX_VERTEX_UNIFORM_VECTORS (SwiftShader: 256)
        36348: 30,           // MAX_VARYING_VECTORS (SwiftShader: 15)
        36183: 8,            // MAX_SAMPLES (SwiftShader: 4)
        7936:  'WebKit',     // VENDOR
        7937:  'WebKit WebGL', // RENDERER
        7938:  'WebGL 1.0 (OpenGL ES 2.0 Chromium)', // VERSION
        35724: 'WebGL GLSL ES 1.0 (OpenGL ES GLSL ES 1.0 Chromium)', // SHADING_LANGUAGE_VERSION
      };
      const _wgl2Extras = {
        7938:  'WebGL 2.0 (OpenGL ES 3.0 Chromium)',
        35724: 'WebGL GLSL ES 3.00 (OpenGL ES GLSL ES 3.0 Chromium)',
        32883: 2048, 33000: 1048576, 33001: 1048576, 34852: 8,
        35657: 4096, 35658: 4096, 35071: 2048, 35077: 7,
        35659: 120, 35968: 4, 35978: 120, 35979: 4, 36063: 8,
        35371: 12, 35373: 12, 35374: 24, 35375: 24, 35376: 65536,
      };
      const _extraExts = ['EXT_texture_filter_anisotropic', 'WEBGL_compressed_texture_s3tc', 'WEBGL_compressed_texture_s3tc_srgb'];

      // Patch getParameter on WebGL prototypes directly
      function _patchWebGL(Proto, params) {
        const origGP = Proto.prototype.getParameter;
        const newGP = function(p) {
          if (p === 3386) return new Int32Array([16384, 16384]);
          if (p in params) return params[p];
          return origGP.call(this, p);
        };
        Object.defineProperty(Proto.prototype, 'getParameter', {
          value: newGP, writable: true, configurable: true, enumerable: true
        });
        _nativeFnNames.set(newGP, 'getParameter');

        const origGSE = Proto.prototype.getSupportedExtensions;
        const newGSE = function() {
          const exts = origGSE.call(this) || [];
          const set = new Set(exts);
          _extraExts.forEach(e => set.add(e));
          return [...set];
        };
        Object.defineProperty(Proto.prototype, 'getSupportedExtensions', {
          value: newGSE, writable: true, configurable: true, enumerable: true
        });
        _nativeFnNames.set(newGSE, 'getSupportedExtensions');

        const origGE = Proto.prototype.getExtension;
        const newGE = function(name) {
          const ext = origGE.call(this, name);
          if (!ext && name === 'EXT_texture_filter_anisotropic') {
            return { TEXTURE_MAX_ANISOTROPY_EXT: 34046, MAX_TEXTURE_MAX_ANISOTROPY_EXT: 34047 };
          }
          return ext;
        };
        Object.defineProperty(Proto.prototype, 'getExtension', {
          value: newGE, writable: true, configurable: true, enumerable: true
        });
        _nativeFnNames.set(newGE, 'getExtension');
      }

      const _wgl2AllParams = Object.assign({}, _wglParams, _wgl2Extras);
      _patchWebGL(WebGLRenderingContext, _wglParams);
      if (typeof WebGL2RenderingContext !== 'undefined') {
        _patchWebGL(WebGL2RenderingContext, _wgl2AllParams);
      }

      // Belt-and-suspenders: also patch the WebGL context instance returned
      // by getContext (CELL-169). The prototype-only patch above did NOT take
      // effect on arm64 Mesa/llvmpipe -- detection saw real "Google Inc. (Mesa)"
      // and "ANGLE (Mesa, llvmpipe)" instead of our 'Intel Inc.' /
      // 'Intel Iris OpenGL Engine' spoof. Likely cause: Chromium installs
      // getParameter as an own-property on the context instance that shadows
      // our prototype patch. Wrapping HTMLCanvasElement.prototype.getContext
      // lets us Object.defineProperty getParameter on the returned instance
      // too, covering both prototype-resolution and own-property cases.
      try {
        const _origGetContext = HTMLCanvasElement.prototype.getContext;
        const _newGetContext = function(type) {
          const ctx = _origGetContext.apply(this, arguments);
          if (ctx && typeof type === 'string' && /webgl/i.test(type) && typeof ctx.getParameter === 'function') {
            try {
              const _origCtxGP = ctx.getParameter.bind(ctx);
              const _params = (type === 'webgl2') ? _wgl2AllParams : _wglParams;
              const _newCtxGP = function(p) {
                if (p === 3386) return new Int32Array([16384, 16384]);
                if (p in _params) return _params[p];
                return _origCtxGP(p);
              };
              Object.defineProperty(ctx, 'getParameter', {
                value: _newCtxGP,
                writable: true,
                configurable: true,
                enumerable: true
              });
              _nativeFnNames.set(_newCtxGP, 'getParameter');
            } catch (e) { /* prototype patch above still applies */ }
          }
          return ctx;
        };
        Object.defineProperty(HTMLCanvasElement.prototype, 'getContext', {
          value: _newGetContext,
          writable: true,
          configurable: true
        });
        _nativeFnNames.set(_newGetContext, 'getContext');
      } catch (e) { /* prototype patch above still applies */ }

      // OffscreenCanvas instance-level patch (main thread) — mirrors HTMLCanvasElement
      // wrapper above. OffscreenCanvas WebGL contexts may also shadow prototype patches.
      try {
        if (typeof OffscreenCanvas !== 'undefined') {
          const _origOCGetContext = OffscreenCanvas.prototype.getContext;
          const _newOCGetContext = function(type) {
            const ctx = _origOCGetContext.apply(this, arguments);
            if (ctx && typeof type === 'string' && /webgl/i.test(type) && typeof ctx.getParameter === 'function') {
              try {
                const _origCtxGP = ctx.getParameter.bind(ctx);
                const _params = (type === 'webgl2') ? _wgl2AllParams : _wglParams;
                const _newCtxGP = function(p) {
                  if (p === 3386) return new Int32Array([16384, 16384]);
                  if (p in _params) return _params[p];
                  return _origCtxGP(p);
                };
                Object.defineProperty(ctx, 'getParameter', {
                  value: _newCtxGP,
                  writable: true,
                  configurable: true,
                  enumerable: true
                });
                _nativeFnNames.set(_newCtxGP, 'getParameter');
              } catch (e) {}
            }
            return ctx;
          };
          Object.defineProperty(OffscreenCanvas.prototype, 'getContext', {
            value: _newOCGetContext,
            writable: true,
            configurable: true
          });
          _nativeFnNames.set(_newOCGetContext, 'getContext');
        }
      } catch (e) {}

      // --- Patch Web Workers (spoof WebGL + UAData in worker scope) ---
      // Workers run in a separate global; init-script patches don't reach them.
      // Intercept Worker constructor to prepend spoof code into worker scripts.
      const _cellStealthJson = (typeof window !== 'undefined' && window.__cellStealth)
        ? JSON.stringify(window.__cellStealth)
        : '{"arch":"arm","platform":"Linux"}';
      const _workerPatch = `var __cellStealth = ''${"" + _cellStealthJson};
        (function() {
          if (typeof WebGLRenderingContext !== 'undefined') {
            // CELL-70: must match main-thread _wglVendor / _wglRenderer above.
            var params = {37445:'Google Inc. (Google)',37446:'ANGLE (Google, Vulkan 1.3.0 (SwiftShader Device (Subzero) (0x0000C0DE)), SwiftShader driver)',7936:'WebKit',7937:'WebKit WebGL',3379:16384,34076:16384,34024:16384,36183:8};
            var params2 = Object.assign({}, params, {7938:'WebGL 2.0 (OpenGL ES 3.0 Chromium)',35724:'WebGL GLSL ES 3.00 (OpenGL ES GLSL ES 3.0 Chromium)'});
            function patchGL(P) {
              var orig = P.prototype.getParameter;
              P.prototype.getParameter = function(p) {
                if (p === 3386) return new Int32Array([16384, 16384]);
                if (p in params) return params[p];
                return orig.call(this, p);
              };
            }
            patchGL(WebGLRenderingContext);
            if (typeof WebGL2RenderingContext !== 'undefined') patchGL(WebGL2RenderingContext);
            if (typeof OffscreenCanvas !== 'undefined') {
              var _origOCGetCtx = OffscreenCanvas.prototype.getContext;
              OffscreenCanvas.prototype.getContext = function(type) {
                var ctx = _origOCGetCtx.apply(this, arguments);
                if (ctx && typeof type === 'string' && /webgl/i.test(type) && typeof ctx.getParameter === 'function') {
                  try {
                    var _origGP = ctx.getParameter.bind(ctx);
                    var _p = (type === 'webgl2') ? params2 : params;
                    ctx.getParameter = function(p) {
                      if (p === 3386) return new Int32Array([16384, 16384]);
                      if (p in _p) return _p[p];
                      return _origGP(p);
                    };
                  } catch(e) {}
                }
                return ctx;
              };
            }
          }
          if (typeof NavigatorUAData !== 'undefined' && typeof __cellStealth !== 'undefined') {
            var origGetHigh = NavigatorUAData.prototype.getHighEntropyValues;
            Object.defineProperty(NavigatorUAData.prototype, 'getHighEntropyValues', {
              value: async function(hints) {
                var values = await origGetHigh.call(this, hints);
                values.architecture = __cellStealth.arch;
                if (__cellStealth.platform) values.platform = __cellStealth.platform;
                return values;
              },
              writable: true,
              configurable: true
            });
          }
          // Spoof navigator.platform in Workers (main thread is spoofed separately;
          // Workers inherit the real value, causing cross-context mismatch).
          if (typeof __cellStealth !== 'undefined') {
            var _wkPlatform = __cellStealth.platform === 'macOS' ? 'MacIntel'
              : __cellStealth.arch === 'arm' ? 'Linux aarch64' : 'Linux x86_64';
            if (typeof Navigator !== 'undefined' && Navigator.prototype) {
              Object.defineProperty(Navigator.prototype, 'platform', {
                get: function() { return _wkPlatform; },
                configurable: true
              });
            } else if (typeof navigator !== 'undefined') {
              Object.defineProperty(navigator, 'platform', {
                get: function() { return _wkPlatform; },
                configurable: true
              });
            }
          }
        })();\n`;
      const _origWorker = window.Worker;
      const _origBlob = window.Blob;
      window.Worker = function(url, opts) {
        try {
          // Handle Blob URLs -- read the blob content, prepend patch
          if (typeof url === 'string' && url.startsWith('blob:')) {
            const xhr = new XMLHttpRequest();
            xhr.open('GET', url, false);
            xhr.send();
            if (xhr.status === 200) {
              const blob = new _origBlob([_workerPatch + xhr.responseText], {type: 'application/javascript'});
              return new _origWorker(URL.createObjectURL(blob), opts);
            }
          }
          // Handle regular URLs -- fetch script, prepend patch
          if (typeof url === 'string' || (url instanceof URL)) {
            const urlStr = url instanceof URL ? url.href : url;
            const xhr = new XMLHttpRequest();
            xhr.open('GET', urlStr, false);
            xhr.send();
            if (xhr.status === 200) {
              const blob = new _origBlob([_workerPatch + xhr.responseText], {type: 'application/javascript'});
              return new _origWorker(URL.createObjectURL(blob), opts);
            }
          }
        } catch(e) {}
        return new _origWorker(url, opts);
      };
      window.Worker.prototype = _origWorker.prototype;
      _nativeFnNames.set(window.Worker, 'Worker');

      // --- Patch SharedWorker (same interception as Worker) ---
      // When ServiceWorkers are blocked, detection scripts fall back to SharedWorker.
      // Intercept SharedWorker constructor to inject the same spoof code.
      if (typeof SharedWorker !== 'undefined') {
        const _origSharedWorker = window.SharedWorker;
        window.SharedWorker = function(url, opts) {
          try {
            if (typeof url === 'string' && url.startsWith('blob:')) {
              const xhr = new XMLHttpRequest();
              xhr.open('GET', url, false);
              xhr.send();
              if (xhr.status === 200) {
                const blob = new _origBlob([_workerPatch + xhr.responseText], {type: 'application/javascript'});
                return new _origSharedWorker(URL.createObjectURL(blob), opts);
              }
            }
            if (typeof url === 'string' || (url instanceof URL)) {
              const urlStr = url instanceof URL ? url.href : url;
              const xhr = new XMLHttpRequest();
              xhr.open('GET', urlStr, false);
              xhr.send();
              if (xhr.status === 200) {
                const blob = new _origBlob([_workerPatch + xhr.responseText], {type: 'application/javascript'});
                return new _origSharedWorker(URL.createObjectURL(blob), opts);
              }
            }
          } catch(e) {}
          return new _origSharedWorker(url, opts);
        };
        window.SharedWorker.prototype = _origSharedWorker.prototype;
        _nativeFnNames.set(window.SharedWorker, 'SharedWorker');
      }

      // --- Patch document.createElement to catch unappended iframes ---
      // CreepJS creates iframes via createElement('iframe') and accesses
      // contentWindow WITHOUT appending to DOM. Our appendChild hook never fires.
      // Override contentWindow getter on each new iframe to auto-patch its window.
      const _origCreateElement = document.createElement.bind(document);
      document.createElement = function(tag, opts) {
        const el = _origCreateElement(tag, opts);
        if (tag.toLowerCase() === 'iframe') {
          const _origDesc = Object.getOwnPropertyDescriptor(HTMLIFrameElement.prototype, 'contentWindow');
          if (_origDesc && _origDesc.get) {
            const _origGet = _origDesc.get;
            Object.defineProperty(el, 'contentWindow', {
              get: function() {
                const w = _origGet.call(this);
                if (w) _patchIframeWindow(w);
                return w;
              },
              configurable: true
            });
          }
        }
        return el;
      };
      _nativeFnNames.set(document.createElement, 'createElement');

      // --- Patch iframes recursively (CreepJS uses nested "phantom" iframes) ---
      // CreepJS creates hidden iframes to access unpolluted prototypes.
      // Intercept appendChild/append to patch WebGL in each iframe window.
      function _patchIframeWindow(iWin) {
        try {
          if (!iWin || !iWin.WebGLRenderingContext) return;
          if (iWin.__wglPatched) return;
          iWin.__wglPatched = true;
          _patchWebGL(iWin.WebGLRenderingContext, _wglParams);
          if (iWin.WebGL2RenderingContext) _patchWebGL(iWin.WebGL2RenderingContext, _wgl2AllParams);
          // Recursively hook appendChild in iframe for nested iframes
          _hookAppendChild(iWin);
        } catch(e) {}
      }
      function _scanForIframes(node) {
        if (!node) return;
        const tag = node.tagName;
        if (tag === 'IFRAME') {
          try { _patchIframeWindow(node.contentWindow); } catch(e) {}
        }
        if (node.querySelectorAll) {
          try {
            node.querySelectorAll('iframe').forEach(function(iframe) {
              try { _patchIframeWindow(iframe.contentWindow); } catch(e) {}
            });
          } catch(e) {}
        }
      }
      function _collectIframes(node) {
        const iframes = [];
        if (!node) return iframes;
        if (node.tagName === 'IFRAME') iframes.push(node);
        if (node.querySelectorAll) {
          try { node.querySelectorAll('iframe').forEach(function(f) { iframes.push(f); }); } catch(e) {}
        }
        return iframes;
      }
      function _hookAppendChild(win) {
        try {
          const Proto = win.Node.prototype;
          const origAC = Proto.appendChild;
          Proto.appendChild = function(node) {
            // Collect iframes BEFORE append (DocumentFragment empties after)
            const iframes = _collectIframes(node);
            const result = origAC.call(this, node);
            // After append, contentWindow is available
            iframes.forEach(function(f) { try { _patchIframeWindow(f.contentWindow); } catch(e) {} });
            return result;
          };
          _nativeFnNames.set(Proto.appendChild, 'appendChild');
          // Also hook insertBefore
          const origIB = Proto.insertBefore;
          Proto.insertBefore = function(node, ref) {
            const iframes = _collectIframes(node);
            const result = origIB.call(this, node, ref);
            iframes.forEach(function(f) { try { _patchIframeWindow(f.contentWindow); } catch(e) {} });
            return result;
          };
          _nativeFnNames.set(Proto.insertBefore, 'insertBefore');
        } catch(e) {}
      }
      _hookAppendChild(window);
      window.__wglPatched = true;

      // --- Screen, viewport, and window dimension spoofs ---
      // Ensures consistent dimensions even if Xvfb resolution changes.
      // With X11/Xvfb at 1920x1080 these match reality but act as safety net.

      // Screen prototype
      const screenDims = { width: 1920, height: 1080, availWidth: 1920, availHeight: 1045 };
      for (const [prop, val] of Object.entries(screenDims)) {
        Object.defineProperty(Screen.prototype, prop, {
          get: () => val, configurable: true
        });
      }
      for (const prop of ['colorDepth', 'pixelDepth']) {
        Object.defineProperty(Screen.prototype, prop, {
          get: () => 24, configurable: true
        });
      }

      // Window dimensions -- realistic maximized Chrome on 1920x1080
      // outerWidth > innerWidth is normal (scrollbar), outerHeight > innerHeight (chrome UI)
      const vpDims = {
        outerWidth: 1920, outerHeight: 1040,
        innerWidth: 1903, innerHeight: 969,
        screenX: 0, screenY: 0, screenLeft: 0, screenTop: 0
      };
      for (const [prop, val] of Object.entries(vpDims)) {
        Object.defineProperty(window, prop, {
          get: () => val, configurable: true
        });
      }

      // VisualViewport -- matches innerWidth/innerHeight
      if (window.visualViewport) {
        for (const [prop, val] of Object.entries({
          width: 1903, height: 969,
          offsetLeft: 0, offsetTop: 0,
          pageLeft: 0, pageTop: 0, scale: 1
        })) {
          Object.defineProperty(window.visualViewport, prop, {
            get: () => val, configurable: true
          });
        }
      }

      // ScreenOrientation -- 1920x1080 = landscape
      if (screen.orientation) {
        Object.defineProperty(screen.orientation, 'type', {
          get: () => 'landscape-primary', configurable: true
        });
        Object.defineProperty(screen.orientation, 'angle', {
          get: () => 0, configurable: true
        });
      }

      // matchMedia -- proxy dimension queries to match our spoofed viewport.
      // CSS @media is compositor-side (ozone's real screen), but matchMedia
      // is JS-side. We override to be consistent with our viewport spoofs.
      const _origMM = window.matchMedia;
      const _vw = 1903, _vh = 969, _dw = 1920, _dh = 1080;
      window.matchMedia = function(q) {
        const r = _origMM.call(window, q);
        // Only intercept dimension queries
        if (!/(?:width|height)/.test(q)) return r;
        // Evaluate query against our dimensions
        let m = true;
        q.replace(/\(\s*(min-|max-)?(device-)?(width|height)\s*:\s*(\d+)/g,
          (_, prefix, device, dim, val) => {
            const v = parseInt(val);
            const ref = device
              ? (dim === 'width' ? _dw : _dh)
              : (dim === 'width' ? _vw : _vh);
            if (prefix === 'min-') m = m && ref >= v;
            else if (prefix === 'max-') m = m && ref <= v;
            else m = m && ref === v;
          });
        return new Proxy(r, {
          get(t, p) {
            if (p === 'matches') return m;
            const v = t[p]; return typeof v === 'function' ? v.bind(t) : v;
          }
        });
      };

      // Register remaining spoofed functions for toString
      if (navigator.share) _nativeFnNames.set(navigator.share, 'share');
      if (navigator.canShare) _nativeFnNames.set(navigator.canShare, 'canShare');
      if (navigator.mediaDevices && navigator.mediaDevices.enumerateDevices) {
        _nativeFnNames.set(navigator.mediaDevices.enumerateDevices, 'enumerateDevices');
      }
      if (window.navigator.permissions.query) {
        _nativeFnNames.set(window.navigator.permissions.query, 'query');
      }
      if (window.matchMedia) _nativeFnNames.set(window.matchMedia, 'matchMedia');

      // --- Notification API (some detectors check permission state) ---
      if (typeof Notification !== 'undefined') {
        Object.defineProperty(Notification, 'permission', {
          get: () => 'default', configurable: true
        });
      }

      // --- Fix hasKnownBgColor: headless returns rgb(255,0,0) for CSS ActiveText ---
      const _origGetCS = window.getComputedStyle;
      window.getComputedStyle = function(el, pseudo) {
        const result = _origGetCS.call(window, el, pseudo);
        if (el && el.getAttribute && el.getAttribute('style')?.includes('ActiveText')) {
          return new Proxy(result, {
            get(target, prop) {
              if (prop === 'backgroundColor') return 'rgb(0, 102, 204)';
              const v = target[prop];
              return typeof v === 'function' ? v.bind(target) : v;
            }
          });
        }
        return result;
      };
      _nativeFnNames.set(window.getComputedStyle, 'getComputedStyle');

      // prefers-color-scheme: no longer overridden.
      // Under X11/Xvfb the compositor reports light mode consistently.
      // Previously forced dark in matchMedia causing mediaConsistent: false
      // (CSS @media hash != matchMedia hash). Removing fixes the mismatch.

      // --- Fix noContentIndex: stub ContentIndex class ---
      if (!('ContentIndex' in window)) {
        window.ContentIndex = function ContentIndex() {};
        _nativeFnNames.set(window.ContentIndex, 'ContentIndex');
      }

      // --- Fix noContactsManager: stub ContactsManager class ---
      if (!('ContactsManager' in window)) {
        window.ContactsManager = function ContactsManager() {};
        _nativeFnNames.set(window.ContactsManager, 'ContactsManager');
      }

      // --- NetworkInformation -- realistic 4G WiFi connection profile ---
      if (navigator.connection) {
        var _connProps = {
          effectiveType: '4g',
          downlink: 10.5,
          downlinkMax: Infinity,
          rtt: 50,
          saveData: false,
          type: 'wifi',
        };
        for (var _cp in _connProps) {
          (function(k, v) {
            Object.defineProperty(navigator.connection, k, {
              get: function() { return v; }, configurable: true
            });
          })(_cp, _connProps[_cp]);
        }
      }

      // --- hardwareConcurrency: container CPU count leaks as bot signal ---
      // Spoof to 8 (common mid-range laptop value).
      Object.defineProperty(navigator, 'hardwareConcurrency', {
        get: () => 8, configurable: true
      });

      // --- speechSynthesis.getVoices() returns [] on headless -- bot signal ---
      try { if (window.speechSynthesis) {
        var _voiceProto = (function() {
          var v = window.speechSynthesis.getVoices();
          return v.length > 0 ? Object.getPrototypeOf(v[0]) : Object.prototype;
        })();
        var _fakeVoices = [
          { voiceURI: 'Google US English', name: 'Google US English', lang: 'en-US', localService: false, default: true },
          { voiceURI: 'Google UK English Female', name: 'Google UK English Female', lang: 'en-GB', localService: false, default: false },
          { voiceURI: 'Google UK English Male', name: 'Google UK English Male', lang: 'en-GB', localService: false, default: false },
          { voiceURI: 'Google Deutsch', name: 'Google Deutsch', lang: 'de-DE', localService: false, default: false },
          { voiceURI: 'Google espa\u00f1ol', name: 'Google espa\u00f1ol', lang: 'es-ES', localService: false, default: false },
          { voiceURI: 'Google fran\u00e7ais', name: 'Google fran\u00e7ais', lang: 'fr-FR', localService: false, default: false },
        ].map(function(v) { return Object.assign(Object.create(_voiceProto), v); });
        var _origGV = window.speechSynthesis.getVoices.bind(window.speechSynthesis);
        window.speechSynthesis.getVoices = function() {
          var real = _origGV();
          return real.length > 0 ? real : _fakeVoices;
        };
        _nativeFnNames.set(window.speechSynthesis.getVoices, 'getVoices');
      } } catch(e) { /* speechSynthesis spoof is non-critical */ }

      // --- Battery API spoof (charging:true + level:1.0 = classic VM signal) ---
      // Real laptop: discharging, ~70% level, ~2h remaining.
      // Use fixed-but-plausible values so repeated calls return the same object.
      (function() {
        var _level = 0.67 + (Math.floor(Date.now() / 86400000) % 20) / 100;
        var _dtime = 6300 + (Math.floor(Date.now() / 3600000) % 60) * 60;
        var _battery = {
          charging: false,
          chargingTime: Infinity,
          dischargingTime: _dtime,
          level: _level,
          addEventListener: function() {},
          removeEventListener: function() {},
          dispatchEvent: function() { return false; },
        };
        Object.defineProperty(navigator, 'getBattery', {
          value: function() { return Promise.resolve(_battery); },
          configurable: true, writable: true
        });
        _nativeFnNames.set(navigator.getBattery, 'getBattery');
      })();

      // --- Canvas noise (identical hash across all 5 contexts = cluster signal) ---
      // Inject subtle per-session noise into canvas readback. Noise is stable
      // within a session (same seed) but unique per browser launch.
      // Only touches the alpha-zero (transparent) pixels to avoid visible artifacts.
      (function() {
        var _ns = (Math.random() * 0x7FFFFFFF) | 0;
        function _nb(i) {
          var x = (_ns ^ (i * 1664525 + 1013904223)) & 0xFF;
          return (x & 1);
        }
        var _origTDU = HTMLCanvasElement.prototype.toDataURL;
        HTMLCanvasElement.prototype.toDataURL = function(type, q) {
          var ctx = this.getContext && this.getContext('2d');
          if (ctx && this.width > 0 && this.height > 0) {
            var id = ctx.getImageData(0, 0, this.width, this.height);
            var d = id.data;
            for (var i = 0; i < d.length; i += 4) {
              if (d[i+3] > 0) { d[i] = Math.max(0, d[i] - _nb(i)); }
            }
            ctx.putImageData(id, 0, 0);
          }
          return _origTDU.call(this, type, q);
        };
        _nativeFnNames.set(HTMLCanvasElement.prototype.toDataURL, 'toDataURL');

        var _origGID = CanvasRenderingContext2D.prototype.getImageData;
        CanvasRenderingContext2D.prototype.getImageData = function(sx, sy, sw, sh) {
          var id = _origGID.call(this, sx, sy, sw, sh);
          var d = id.data;
          for (var i = 0; i < d.length; i += 4) {
            if (d[i+3] > 0) { d[i] = Math.max(0, d[i] - _nb(i)); }
          }
          return id;
        };
        _nativeFnNames.set(CanvasRenderingContext2D.prototype.getImageData, 'getImageData');
      })();

      // --- Audio fingerprint noise (OfflineAudioContext oscillator hash) ---
      // Fingerprinting reads AudioBuffer.getChannelData() after rendering an oscillator.
      // Add tiny per-session float noise (< 1e-7) -- inaudible, changes the hash.
      // Patch both AudioBuffer (OfflineAudioContext result) and AnalyserNode readbacks.
      (function() {
        var _as = (Math.random() * 0x7FFFFFFF) | 0;
        function _af(i) {
          var x = (_as ^ (i * 22695477 + 1)) >>> 0;
          return (x & 0xFF) * 1e-9 - 1.275e-7;
        }

        // AudioBuffer.getChannelData -- main audio fingerprint vector
        if (typeof AudioBuffer !== 'undefined') {
          var _origGCD = AudioBuffer.prototype.getChannelData;
          AudioBuffer.prototype.getChannelData = function(ch) {
            var data = _origGCD.call(this, ch);
            for (var i = 0; i < data.length; i++) {
              data[i] = Math.max(-1, Math.min(1, data[i] + _af(i)));
            }
            return data;
          };
          _nativeFnNames.set(AudioBuffer.prototype.getChannelData, 'getChannelData');
        }

        // AnalyserNode.getFloatFrequencyData + getByteFrequencyData
        if (typeof AnalyserNode !== 'undefined') {
          var _origGFFD = AnalyserNode.prototype.getFloatFrequencyData;
          AnalyserNode.prototype.getFloatFrequencyData = function(arr) {
            _origGFFD.call(this, arr);
            for (var i = 0; i < arr.length; i++) arr[i] += _af(i) * 1e4;
          };
          _nativeFnNames.set(AnalyserNode.prototype.getFloatFrequencyData, 'getFloatFrequencyData');

          var _origGBFD = AnalyserNode.prototype.getByteFrequencyData;
          AnalyserNode.prototype.getByteFrequencyData = function(arr) {
            _origGBFD.call(this, arr);
            for (var i = 0; i < arr.length; i++) {
              arr[i] = Math.max(0, Math.min(255, arr[i] + (_af(i) > 0 ? 1 : 0)));
            }
          };
          _nativeFnNames.set(AnalyserNode.prototype.getByteFrequencyData, 'getByteFrequencyData');
        }
      })();

      // --- Font enumeration spoof (Windows/macOS fonts absent on Linux = fingerprint) ---
      // Detection: measure text width with "Calibri,sans-serif" vs "sans-serif" baseline.
      // If equal -> font absent. We apply per-font width factors so probed fonts read
      // as present. Factors are approximate ratio of real font width to sans-serif fallback.
      // Also patched on OffscreenCanvas (used by CreepJS and similar scanners).
      (function() {
        var _fonts = {
          'calibri':                0.880,
          'calibri light':          0.835,
          'cambria':                0.982,
          'cambria math':           0.982,
          'consolas':               0.930,
          'constantia':             0.983,
          'corbel':                 0.928,
          'franklin gothic medium': 0.914,
          'segoe ui':               0.938,
          'segoe ui light':         0.894,
          'segoe ui semibold':      0.948,
          'palatino linotype':      1.025,
          'book antiqua':           1.010,
          'garamond':               0.856,
          'ms sans serif':          0.946,
          'ms serif':               1.015,
          'helvetica neue':         0.938,
          'lucida grande':          0.971,
          'lucida console':         0.886,
          'optima':                 0.942,
          'gill sans':              0.918,
          'apple sd gothic neo':    0.910,
          'apple chancery':         1.030,
          'monaco':                 0.893,
          'menlo':                  0.901,
          'andale mono':            0.878,
        };

        function _matchFont(fontStr) {
          var lower = (fontStr || "").toLowerCase();
          for (var name in _fonts) {
            if (lower.indexOf(name) !== -1) return _fonts[name];
          }
          return null;
        }

        function _patchMeasureText(Proto) {
          if (!Proto || !Proto.prototype || !Proto.prototype.measureText) return;
          var _orig = Proto.prototype.measureText;
          Proto.prototype.measureText = function(text) {
            var m = _orig.call(this, text);
            var factor = _matchFont(this.font);
            if (factor === null) return m;
            var w = m.width * factor;
            return new Proxy(m, {
              get: function(t, p) {
                if (p === 'width') return w;
                var v = t[p];
                return typeof v === 'function' ? v.bind(t) : v;
              }
            });
          };
          _nativeFnNames.set(Proto.prototype.measureText, 'measureText');
        }

        _patchMeasureText(CanvasRenderingContext2D);
        if (typeof OffscreenCanvasRenderingContext2D !== 'undefined') {
          _patchMeasureText(OffscreenCanvasRenderingContext2D);
        }
      })();
    '';
  };

  # Keep-alive init script -- prevents session timeouts by simulating user activity.
  # Scrolls 10px up/down every 60 seconds after 60 seconds of no user interaction.
  # Resets timer on any scroll, click, keypress, or mouse movement.
  keepAliveInitScript = pkgs.writeTextFile {
    name = "keep-alive-init.js";
    text = ''
      (function() {
        var _kaTimer = null;
        var _KA_IDLE_MS = 240000; // 4 min -- refresh before 5-min JWT TTLs expire
        function _kaTick() {
          // Silent favicon fetch: sends cookies to the server so it can
          // see authenticated activity without reloading the page.
          // Uses GET (not HEAD) -- many CDN/WAF configs (e.g. Akamai) return
          // 503 for HEAD requests. Falls back to a GET on the current URL
          // if the favicon CDN is cross-origin (won't carry auth cookies).
          var _faviconEl = document.querySelector('link[rel~="icon"]');
          var _faviconUrl = _faviconEl ? _faviconEl.href : null;
          var _sameOrigin = _faviconUrl && _faviconUrl.startsWith(location.origin);
          var _fetchUrl = _sameOrigin ? _faviconUrl : location.href;
          fetch(_fetchUrl, { method: 'GET', credentials: 'include', cache: 'no-store' })
            .catch(function() {});
          // Subtle scroll jitter so the page registers user-like activity.
          window.scrollBy(0, 10);
          setTimeout(function() { window.scrollBy(0, -10); }, 500);
          _kaTimer = setTimeout(_kaTick, _KA_IDLE_MS);
        }
        function _kaReset() {
          if (_kaTimer) clearTimeout(_kaTimer);
          _kaTimer = setTimeout(_kaTick, _KA_IDLE_MS);
        }
        ['scroll', 'click', 'keydown', 'mousemove'].forEach(function(evt) {
          window.addEventListener(evt, _kaReset, { passive: true });
        });
        _kaReset();
      })();
    '';
  };

  # Network capture init script -- wraps window.fetch and XMLHttpRequest to record
  # request/response bodies into an in-page buffer. INERT by default (HAR alone
  # doesn't reliably capture bodies under CSP / streaming / opaque responses;
  # this fills that gap when we need it).
  #
  # Usage from page eval:
  #   window.__cellNet.enable()           // start capturing (default cap: 200 entries, 256KB body)
  #   window.__cellNet.enable({ max: 500, maxBody: 1048576, urlPattern: 'api' })
  #   window.__cellNet.entries            // array of captured entries
  #   window.__cellNet.dump()             // returns entries + clears buffer
  #   window.__cellNet.disable()          // stop capturing, leaves buffer intact
  #   window.__cellNet.clear()            // clear buffer only
  #
  # Re-runs on every frame/navigation (init-script behavior) -- guards against
  # double-wrapping so toString() stays clean and stealth isn't broken.
  networkCaptureInitScript = pkgs.writeTextFile {
    name = "network-capture-init.js";
    text = ''
      (function() {
        if (window.__cellNet && window.__cellNet.__installed) return;

        var state = {
          enabled: false,
          entries: [],
          max: 200,
          maxBody: 262144,        // 256 KB per body
          urlPattern: null,       // optional substring or RegExp
          __installed: true
        };

        function _match(url) {
          if (!state.urlPattern) return true;
          if (state.urlPattern instanceof RegExp) return state.urlPattern.test(url);
          return String(url).indexOf(String(state.urlPattern)) !== -1;
        }
        function _trim(s) {
          if (s == null) return null;
          if (typeof s !== 'string') {
            try { s = String(s); } catch (e) { return '[unstringifiable]'; }
          }
          if (s.length > state.maxBody) return s.slice(0, state.maxBody) + '...[truncated ' + (s.length - state.maxBody) + ']';
          return s;
        }
        function _push(entry) {
          if (state.entries.length >= state.max) state.entries.shift();
          state.entries.push(entry);
        }
        function _headersToObj(h) {
          var out = {};
          if (!h) return out;
          try {
            if (typeof h.forEach === 'function') {
              h.forEach(function(v, k) { out[k] = v; });
              return out;
            }
            if (Array.isArray(h)) {
              h.forEach(function(p) { out[p[0]] = p[1]; });
              return out;
            }
            if (typeof h === 'object') return Object.assign({}, h);
          } catch (e) {}
          return out;
        }

        // --- fetch wrapper ---
        var _origFetch = window.fetch;
        if (typeof _origFetch === 'function') {
          var _wrappedFetch = function(input, init) {
            if (!state.enabled) return _origFetch.apply(this, arguments);
            var url = (typeof input === 'string') ? input : (input && input.url) || "";
            if (!_match(url)) return _origFetch.apply(this, arguments);
            var method = (init && init.method) || (input && input.method) || 'GET';
            var reqHeaders = _headersToObj((init && init.headers) || (input && input.headers));
            var reqBody = (init && init.body != null) ? _trim(init.body) : null;
            var started = Date.now();
            return _origFetch.apply(this, arguments).then(function(resp) {
              var cloned;
              try { cloned = resp.clone(); } catch (e) { cloned = null; }
              var entry = {
                kind: 'fetch',
                method: method,
                url: url,
                status: resp.status,
                startedAt: started,
                durationMs: Date.now() - started,
                reqHeaders: reqHeaders,
                reqBody: reqBody,
                resHeaders: _headersToObj(resp.headers),
                resBody: null
              };
              if (cloned) {
                cloned.text().then(function(t) { entry.resBody = _trim(t); }).catch(function() {});
              }
              _push(entry);
              return resp;
            }).catch(function(err) {
              _push({
                kind: 'fetch', method: method, url: url, status: 0,
                startedAt: started, durationMs: Date.now() - started,
                reqHeaders: reqHeaders, reqBody: reqBody, error: String(err)
              });
              throw err;
            });
          };
          _wrappedFetch.toString = function() { return _origFetch.toString(); };
          window.fetch = _wrappedFetch;
        }

        // --- XMLHttpRequest wrapper ---
        if (typeof XMLHttpRequest !== 'undefined') {
          var _origOpen = XMLHttpRequest.prototype.open;
          var _origSend = XMLHttpRequest.prototype.send;
          var _origSetHeader = XMLHttpRequest.prototype.setRequestHeader;
          XMLHttpRequest.prototype.open = function(method, url) {
            this.__cellNet = { method: method, url: url, reqHeaders: {}, startedAt: 0 };
            return _origOpen.apply(this, arguments);
          };
          XMLHttpRequest.prototype.setRequestHeader = function(k, v) {
            if (this.__cellNet) this.__cellNet.reqHeaders[k] = v;
            return _origSetHeader.apply(this, arguments);
          };
          XMLHttpRequest.prototype.send = function(body) {
            var ctx = this.__cellNet;
            if (state.enabled && ctx && _match(ctx.url)) {
              ctx.startedAt = Date.now();
              ctx.reqBody = body != null ? _trim(body) : null;
              var xhr = this;
              this.addEventListener('loadend', function() {
                var resHeaders = {};
                try {
                  var raw = xhr.getAllResponseHeaders() || "";
                  raw.trim().split(/[\r\n]+/).forEach(function(line) {
                    var idx = line.indexOf(':');
                    if (idx > 0) resHeaders[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
                  });
                } catch (e) {}
                var resBody = null;
                try {
                  if (xhr.responseType === "" || xhr.responseType === 'text') resBody = _trim(xhr.responseText);
                  else if (xhr.responseType === 'json') resBody = _trim(JSON.stringify(xhr.response));
                  else resBody = '[binary responseType=' + xhr.responseType + ']';
                } catch (e) { resBody = '[unreadable: ' + String(e) + ']'; }
                _push({
                  kind: 'xhr',
                  method: ctx.method,
                  url: ctx.url,
                  status: xhr.status,
                  startedAt: ctx.startedAt,
                  durationMs: Date.now() - ctx.startedAt,
                  reqHeaders: ctx.reqHeaders,
                  reqBody: ctx.reqBody,
                  resHeaders: resHeaders,
                  resBody: resBody
                });
              });
            }
            return _origSend.apply(this, arguments);
          };
        }

        window.__cellNet = {
          __installed: true,
          get enabled() { return state.enabled; },
          get entries() { return state.entries; },
          enable: function(opts) {
            opts = opts || {};
            if (typeof opts.max === 'number') state.max = opts.max;
            if (typeof opts.maxBody === 'number') state.maxBody = opts.maxBody;
            if (opts.urlPattern != null) state.urlPattern = opts.urlPattern;
            state.enabled = true;
            return { enabled: true, max: state.max, maxBody: state.maxBody, urlPattern: String(state.urlPattern) };
          },
          disable: function() { state.enabled = false; return { enabled: false, captured: state.entries.length }; },
          clear: function() { var n = state.entries.length; state.entries = []; return { cleared: n }; },
          dump: function() { var out = state.entries.slice(); state.entries = []; return out; }
        };
      })();
    '';
  };

  # chromium-singleton-sweep -- CELL-62 shared helper invoked by both
  # patchright-mcp-cell and the interactive chromiumWrapper just before they
  # exec chrome. Sweeps the stale Singleton triple when the recorded owner
  # PID is dead (or recycled to a non-chrome process), leaves the triple
  # alone when a genuine live chrome holds the lock.
  #
  # The boot-time 22-chromium-singleton.sh fragment handles cross-container
  # restart cleanup; this helper handles mid-session crash recovery that the
  # boot fragment can't catch.
  chromiumSingletonSweep = pkgs.writeShellScript "chromium-singleton-sweep" ''
    #!${pkgs.bash}/bin/bash
    # Usage: chromium-singleton-sweep <user-data-dir>
    set -u
    _dir="''${1:?usage: chromium-singleton-sweep <user-data-dir>}"
    [ -d "$_dir" ] || exit 0
    _lock="$_dir/SingletonLock"
    # No lock present -- nothing to do (Chromium will create one cleanly).
    [ -L "$_lock" ] || [ -e "$_lock" ] || exit 0
    _target=$(readlink "$_lock" 2>/dev/null || true)
    _pid=""
    _comm=""
    _alive=0
    _sweep=1
    if [ -n "$_target" ]; then
      # Chromium target format: <hostname>-<pid>. Hostname can contain dashes
      # (e.g. cell-devcell-786), so split on the LAST dash for the PID.
      _pid="''${_target##*-}"
      case "$_pid" in
        ""|*[!0-9]*) ;;  # non-numeric -> treat as stale
        *)
          if kill -0 "$_pid" 2>/dev/null; then
            _alive=1
            _comm=$(cat "/proc/$_pid/comm" 2>/dev/null || true)
            case "$_comm" in
              chrome|chromium|Chromium*|chrome*)
                # Genuine live chrome owner -- never touch its lock.
                exit 0 ;;
            esac
          fi
          ;;
      esac
    fi
    [ "$_sweep" -eq 1 ] || exit 0
    # Capture SingletonSocket target before unlinking so we can also clean
    # the orphan socket dir at $HOME/tmp/.org.chromium.Chromium.<rand>/.
    _sock_target=$(readlink "$_dir/SingletonSocket" 2>/dev/null || true)
    rm -f "$_dir/SingletonLock" "$_dir/SingletonCookie" "$_dir/SingletonSocket"
    if [ -n "$_sock_target" ]; then
      _sock_dir=$(dirname "$_sock_target")
      case "$_sock_dir" in
        */.org.chromium.Chromium.*) rm -rf "$_sock_dir" 2>/dev/null || true ;;
      esac
    fi
    printf 'chromium-singleton-sweep: removed stale Singleton{Lock,Cookie,Socket} in %s (was pid=%s alive=%s comm=%s)\n' \
      "$_dir" "''${_pid:-?}" "$_alive" "''${_comm:-?}" >&2
  '';

  # patchright-mcp-cell wrapper -- bundles LD_LIBRARY_PATH, secrets, user-data-dir,
  # and auto-discovers config.json + stealth-init.js from co-located share/ dir.
  patchrightMcpCell = let
    wrapperScript = pkgs.writeShellScript "patchright-mcp-cell-inner" ''
      export PLAYWRIGHT_BROWSERS_PATH="${browsers}"
      export PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
      export LD_LIBRARY_PATH="${runtimeLibPath}''${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
      # Mesa Lavapipe -- software Vulkan ICD for WebGL via ANGLE->Vulkan->lvp
      export VK_ICD_FILENAMES="${pkgs.mesa.drivers}/share/vulkan/icd.d/lvp_icd.${pkgs.stdenv.hostPlatform.uname.processor}.json"

      # Always use config and init-script from co-located share/ dir.
      # Strip any stale --config/--init-script from caller args (e.g. Claude Code
      # caching old nix store hashes) so the bundled versions always win.
      _SELF="$(readlink -f "$0")"
      _SHARE="$(dirname "$(dirname "$_SELF")")/share/patchright"
      _CLEAN_ARGS=()
      _skip=false
      for _a in "$@"; do
        if $_skip; then _skip=false; continue; fi
        case "$_a" in
          --config|--init-script) _skip=true; continue ;;
        esac
        _CLEAN_ARGS+=("$_a")
      done
      set -- "''${_CLEAN_ARGS[@]}"
      _EXTRA_ARGS=()

      # Generate runtime config with dynamic timezone from $TZ.
      # Merges static nix config with runtime-only contextOptions.
      _RUNTIME_CONFIG=$(mktemp /tmp/pw-config-XXXXXX.json)
      trap 'rm -f "$_RUNTIME_CONFIG" "$SECRETS_FILE" "$_STEALTH_INIT"' EXIT
      # Convert LANG (e.g. en_US.UTF-8) to Playwright locale (en-US).
      _PW_LOCALE="''${LANG%%.*}"        # strip .UTF-8
      _PW_LOCALE="''${_PW_LOCALE//_/-}" # en_US -> en-US
      : "''${_PW_LOCALE:=en-US}"        # default
      _NEED_RUNTIME=false
      [ -n "''${TZ:-}" ] && [ "$TZ" != "UTC" ] && _NEED_RUNTIME=true
      [ "$_PW_LOCALE" != "en-US" ] && _NEED_RUNTIME=true
      if [ -f "$_SHARE/config.json" ] && $_NEED_RUNTIME; then
        ${pkgs.jq}/bin/jq \
          --arg tz "''${TZ:-UTC}" \
          --arg loc "$_PW_LOCALE" \
          '.browser.contextOptions.timezoneId = $tz | .browser.contextOptions.locale = $loc' \
          "$_SHARE/config.json" > "$_RUNTIME_CONFIG"
        _EXTRA_ARGS+=(--config "$_RUNTIME_CONFIG")
      elif [ -f "$_SHARE/config.json" ]; then
        _EXTRA_ARGS+=(--config "$_SHARE/config.json")
      fi

      # Stealth identity: merge userAgentMetadata from DEVCELL_STEALTH_ARCH /
      # DEVCELL_STEALTH_PLATFORM env vars. This is the SINGLE SOURCE OF TRUTH
      # for CDP-level client-hints (Sec-CH-UA-Arch, Sec-CH-UA-Platform, etc.)
      # and must run BEFORE the fingerprint overlay so cell login can override.
      _STEALTH_ARCH="''${DEVCELL_STEALTH_ARCH:-arm}"
      _STEALTH_PLATFORM="''${DEVCELL_STEALTH_PLATFORM:-Linux}"
      # Detect real Chromium version from the browser binary so UA + brands match.
      _CHROME_BIN=$(find "''${PLAYWRIGHT_BROWSERS_PATH:-/dev/null}" -name 'chrome' -type f 2>/dev/null | tail -1)
      _CHROME_FULL_VER="141.0.0.0"
      if [ -n "$_CHROME_BIN" ] && [ -x "$_CHROME_BIN" ]; then
        _CHROME_FULL_VER=$("$_CHROME_BIN" --version 2>/dev/null | grep -oP '\d+\.\d+\.\d+\.\d+' || echo "141.0.0.0")
      fi
      _CHROME_MAJOR="''${_CHROME_FULL_VER%%.*}"
      # Build UA string from arch + platform + detected Chrome version.
      case "$_STEALTH_PLATFORM" in
        macOS)   _STEALTH_UA="Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/$_CHROME_FULL_VER Safari/537.36" ;;
        Windows) _STEALTH_UA="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/$_CHROME_FULL_VER Safari/537.36" ;;
        *)
          if [ "$_STEALTH_ARCH" = "arm" ]; then
            _STEALTH_UA="Mozilla/5.0 (X11; Linux aarch64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/$_CHROME_FULL_VER Safari/537.36"
          else
            _STEALTH_UA="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/$_CHROME_FULL_VER Safari/537.36"
          fi ;;
      esac
      _STEALTH_BASE="$_SHARE/config.json"
      [ -s "$_RUNTIME_CONFIG" ] && _STEALTH_BASE="$_RUNTIME_CONFIG"
      if [ -f "$_STEALTH_BASE" ]; then
        ${pkgs.jq}/bin/jq \
          --arg arch "$_STEALTH_ARCH" \
          --arg plat "$_STEALTH_PLATFORM" \
          --arg ua "$_STEALTH_UA" \
          --arg cmajor "$_CHROME_MAJOR" \
          --arg cfull "$_CHROME_FULL_VER" \
          '.browser.contextOptions.userAgent = $ua
          | .browser.launchOptions.args = ((.browser.launchOptions.args // []) + ["--user-agent=" + $ua])
          | .browser.contextOptions.userAgentMetadata = (
            (.browser.contextOptions.userAgentMetadata // {}) + {
              "platform": $plat,
              "platformVersion": "",
              "architecture": $arch,
              "bitness": "64",
              "model": "",
              "mobile": false,
              "brands": [
                {"brand": "Chromium", "version": $cmajor},
                {"brand": "Not?A_Brand", "version": "8"}
              ],
              "fullVersionList": [
                {"brand": "Chromium", "version": $cfull},
                {"brand": "Not?A_Brand", "version": "8.0.0.0"}
              ]
            }
          )' "$_STEALTH_BASE" > "$_RUNTIME_CONFIG.stealth"
        mv "$_RUNTIME_CONFIG.stealth" "$_RUNTIME_CONFIG"
        _EXTRA_ARGS=()
        _EXTRA_ARGS+=(--config "$_RUNTIME_CONFIG")
      fi

      # Inject __cellStealth global into init-script preamble so JS spoofs
      # read arch/platform from the same source as CDP userAgentMetadata.
      _STEALTH_PREAMBLE="window.__cellStealth = { arch: \"$_STEALTH_ARCH\", platform: \"$_STEALTH_PLATFORM\" };"
      _STEALTH_INIT=$(mktemp /tmp/pw-stealth-preamble-XXXXXX.js)
      printf '%s\n' "$_STEALTH_PREAMBLE" > "$_STEALTH_INIT"
      if [ -f "$_SHARE/stealth-init.js" ]; then
        cat "$_SHARE/stealth-init.js" >> "$_STEALTH_INIT"
      fi
      _EXTRA_ARGS+=(--init-script "$_STEALTH_INIT")

      # Inject macOS fingerprint from $HOME/.playwright/fingerprint.json if present
      # (written by `cell login` on macOS -- new layout). Merges userAgent into the
      # runtime config and prepends a preamble init script that sets window.__cellFp
      # before stealth-init.js runs.
      _FP_FILE="$HOME/.playwright/fingerprint.json"
      if [ -f "$_FP_FILE" ]; then
        _UA=$(${pkgs.jq}/bin/jq -r '.userAgent // empty' "$_FP_FILE")
        if [ -n "$_UA" ]; then
          # Determine base config for merge: prefer already-generated _RUNTIME_CONFIG
          # if it has content, otherwise fall back to the static share config.
          _FP_CONFIG=$(mktemp /tmp/pw-fp-config-XXXXXX.json)
          # Build userAgentMetadata from fingerprint fields so CDP overrides the actual
          # sec-ch-ua-* HTTP request headers (not just JS-visible navigator.userAgentData).
          # uaPlatform ("macOS") maps to the Client Hints platform string.
          # version is the full browser version for sec-ch-ua-full-version.
          # brands are used for sec-ch-ua header value.
          _BASE_CONFIG="$_SHARE/config.json"
          [ -s "$_RUNTIME_CONFIG" ] && _BASE_CONFIG="$_RUNTIME_CONFIG"
          ${pkgs.jq}/bin/jq -n \
            --arg ua "$_UA" \
            --arg stealthArch "$_STEALTH_ARCH" \
            --arg cmajor "$_CHROME_MAJOR" \
            --arg cfull "$_CHROME_FULL_VER" \
            --slurpfile fp "$_FP_FILE" \
            --slurpfile cfg "$_BASE_CONFIG" \
            '($cfg[0]) as $cfg | ($fp[0]) as $fp |
             ($fp.uaPlatform // "macOS") as $platform |
             ($fp.version // "") as $ver |
             ($fp.brands // []) as $brands |
             $cfg
             | .browser.contextOptions.userAgent = $ua
             | .browser.contextOptions.userAgentMetadata = {
                 "platform": $platform,
                 "platformVersion": "",
                 "architecture": $stealthArch,
                 "bitness": "64",
                 "model": "",
                 "mobile": false,
                 "brands": (if ($brands | length) > 0 then $brands else [
                   {"brand": "Chromium", "version": $cmajor},
                   {"brand": "Not?A_Brand", "version": "8"}
                 ] end),
                 "fullVersionList": (if ($brands | length) > 0 then $brands else [
                   {"brand": "Chromium", "version": ($ver // $cfull)},
                   {"brand": "Not?A_Brand", "version": "8.0.0.0"}
                 ] end)
               }
             | .browser.launchOptions.args = ((.browser.launchOptions.args // []) + ["--user-agent=" + $ua])
            ' > "$_FP_CONFIG"

          # Replace any existing --config in _EXTRA_ARGS with the merged config.
          _NEW_EXTRA_ARGS=()
          _skip_next=false
          for _arg in "''${_EXTRA_ARGS[@]}"; do
            if $_skip_next; then _skip_next=false; continue; fi
            if [ "$_arg" = "--config" ]; then _skip_next=true; continue; fi
            _NEW_EXTRA_ARGS+=("$_arg")
          done
          _EXTRA_ARGS=("''${_NEW_EXTRA_ARGS[@]}")
          _EXTRA_ARGS+=(--config "$_FP_CONFIG")

          # Write preamble init script: window.__cellFp = <full fingerprint JSON>;
          _FP_PREAMBLE=$(mktemp /tmp/pw-fp-preamble-XXXXXX.js)
          printf 'window.__cellFp = ' > "$_FP_PREAMBLE"
          ${pkgs.jq}/bin/jq '.' "$_FP_FILE" >> "$_FP_PREAMBLE"
          printf ';\n' >> "$_FP_PREAMBLE"

          # Prepend preamble BEFORE stealth-init.js so it is available to all init scripts.
          _EXTRA_ARGS+=(--init-script "$_FP_PREAMBLE")
          trap 'rm -f "$_RUNTIME_CONFIG" "$SECRETS_FILE" "$_STEALTH_INIT" "$_FP_CONFIG" "$_FP_PREAMBLE"' EXIT
        fi
      fi

      [ -f "$_SHARE/stealth-init.js" ] && _EXTRA_ARGS+=(--init-script "$_SHARE/stealth-init.js")
      [ -f "$_SHARE/keep-alive-init.js" ] && _EXTRA_ARGS+=(--init-script "$_SHARE/keep-alive-init.js")
      [ -f "$_SHARE/network-capture-init.js" ] && _EXTRA_ARGS+=(--init-script "$_SHARE/network-capture-init.js")

      # Output dir for screenshots / page dumps / console logs.
      # Default: <project>/.devcell/playwright-mcp/ instead of patchright's <project>/.playwright-mcp/.
      # Override via PLAYWRIGHT_MCP_OUTPUT_DIR. Caller-supplied --output-dir in "$@" still wins (later wins).
      _OUTPUT_DIR="''${PLAYWRIGHT_MCP_OUTPUT_DIR:-''${USER_WORKING_DIR:-$PWD}/.devcell/playwright-mcp}"
      mkdir -p "$_OUTPUT_DIR"
      _EXTRA_ARGS+=(--output-dir "$_OUTPUT_DIR")

      SECRETS_FILE=$(mktemp /tmp/pw-secrets-XXXXXX.env)

      _ENV_FILE="''${USER_WORKING_DIR:-}/.env"
      if [ -f "$_ENV_FILE" ]; then
        while IFS= read -r _line || [ -n "$_line" ]; do
          [[ -z "$_line" || "$_line" == '#'* ]] && continue
          _key="''${_line%%=*}"
          _key="''${_key#export }"
          [ -z "$_key" ] && continue
          if _val=$(printenv "$_key" 2>/dev/null); then
            printf '%s=%s\n' "$_key" "$_val"
          fi
        done < "$_ENV_FILE" >> "$SECRETS_FILE"
      fi

      # Shared-browser mode: if the interactive chromium wrapper is already
      # running (with --remote-debugging-port=9222), attach to it via CDP
      # instead of launching a second chromium. This makes xdg-open OAuth
      # flows and MCP browser automation share one browser + cookie jar.
      # Stealth init scripts still pass through and apply to new pages.
      _CDP_URL="http://127.0.0.1:9222"
      if ${pkgs.curl}/bin/curl -sf --max-time 1 "$_CDP_URL/json/version" >/dev/null 2>&1; then
        exec mcp-server-patchright --cdp-endpoint "$_CDP_URL" --secrets "$SECRETS_FILE" "''${_EXTRA_ARGS[@]}" "$@"
      fi

      # CELL-74 layout: Playwright-format state under ~/.playwright/,
      # Chromium-format profiles under ~/.chrome/<app>/. No legacy fallback.
      STORAGE_STATE="$HOME/.playwright/storage-state.json"
      if [ -f "$STORAGE_STATE" ]; then
        USER_DATA_DIR="''${PLAYWRIGHT_MCP_USER_DATA_DIR:-$HOME/.chrome/''${APP_NAME:-cell}}"
        mkdir -p "$USER_DATA_DIR"
        ${chromiumSingletonSweep} "$USER_DATA_DIR"
        mcp-server-patchright --no-sandbox --user-data-dir "$USER_DATA_DIR" --storage-state "$STORAGE_STATE" --secrets "$SECRETS_FILE" "''${_EXTRA_ARGS[@]}" "$@"
      else
        USER_DATA_DIR="''${PLAYWRIGHT_MCP_USER_DATA_DIR:-$HOME/.chrome/''${APP_NAME:-cell}}"

        # CELL-62: chromium's WebAppDatabase::OnAllDataAndMetadataRead hits a
        # compiler-emitted brk #0 (__builtin_unreachable) when the persisted
        # sync data/metadata records are out of sync. The most likely cause is
        # abrupt termination (container SIGKILL, OOM, host shutdown) while
        # chromium was mid-write. Once a profile enters this state, every
        # subsequent launch SIGTRAPs deterministically.
        #
        # Mitigation: detect unclean prior exit via two signals, archive the
        # profile aside, let chromium recreate it fresh. Keep the 3 most
        # recent archives. Verbose logging gated on DEVCELL_DEBUG=true.
        _pwlog() {
          [ "''${DEVCELL_DEBUG:-false}" = "true" ] && \
            printf 'patchright-mcp-cell: %s\n' "$*" >&2
          return 0
        }
        _profile_looks_crashed() {
          local _udd="$1"
          local _prefs="$_udd/Default/Preferences"
          # Signal A -- canonical: profile.exit_type set to anything but Normal
          # by chromium itself (default state is Crashed, only flipped to
          # Normal on graceful shutdown).
          if [ -f "$_prefs" ] && \
             ${pkgs.jq}/bin/jq -e '.profile.exit_type != "Normal"' "$_prefs" >/dev/null 2>&1; then
            _pwlog "crash heuristic A trip -- Preferences exit_type != Normal ($_prefs)"
            return 0
          fi
          # Signal B -- crashpad pending dump newer than Local State. Catches
          # kills where exit_type managed to flip to Normal but a child
          # process crashed during shutdown flush.
          local _crashdir="$HOME/.config/chromium/Crash Reports/pending"
          local _localstate="$_udd/Local State"
          if [ -d "$_crashdir" ] && [ -f "$_localstate" ] && \
             find "$_crashdir" -name '*.dmp' -newer "$_localstate" -print -quit 2>/dev/null | grep -q .; then
            _pwlog "crash heuristic B trip -- crashpad dump newer than $_localstate"
            return 0
          fi
          return 1
        }

        if _profile_looks_crashed "$USER_DATA_DIR"; then
          _ts=$(date -u +%Y%m%dT%H%M%SZ)
          _archive="''${USER_DATA_DIR}.crashed-''${_ts}"
          if mv "$USER_DATA_DIR" "$_archive" 2>/dev/null; then
            _pwlog "archived unclean profile: $USER_DATA_DIR -> $_archive"
          else
            _pwlog "archive failed (mv returned non-zero) -- proceeding with existing profile dir"
          fi
          # Rotate: keep the 3 newest .crashed-* archives, prune the rest.
          ls -dt -- "''${USER_DATA_DIR}.crashed-"* 2>/dev/null | tail -n +4 | while IFS= read -r _old; do
            [ -d "$_old" ] || continue
            if rm -rf -- "$_old"; then
              _pwlog "pruned old archive: $_old"
            fi
          done
        fi

        mkdir -p "$USER_DATA_DIR"
        # CELL-62: sweep stale SingletonLock/Cookie/Socket from a prior crash.
        ${chromiumSingletonSweep} "$USER_DATA_DIR"
        mcp-server-patchright --no-sandbox --user-data-dir "$USER_DATA_DIR" --secrets "$SECRETS_FILE" "''${_EXTRA_ARGS[@]}" "$@"
      fi
    '';
  in pkgs.runCommandLocal "patchright-mcp-cell" {} ''
    mkdir -p $out/bin $out/share/patchright
    cp ${wrapperScript} $out/bin/patchright-mcp-cell
    chmod +x $out/bin/patchright-mcp-cell
    cp ${patchrightConfig} $out/share/patchright/config.json
    cp ${stealthInitScript} $out/share/patchright/stealth-init.js
    cp ${keepAliveInitScript} $out/share/patchright/keep-alive-init.js
    cp ${networkCaptureInitScript} $out/share/patchright/network-capture-init.js
  '';

  # Interactive chromium wrapper -- reads CHROMIUM_PROFILE_PATH at runtime so each
  # container can have an isolated profile even when sharing CELL_HOME.
  # --remote-debugging-port=9222 exposes a CDP endpoint so:
  #   1) Playwright/MCP/CDP clients can attach via connectOverCDP('http://127.0.0.1:9222')
  #   2) The patchright-mcp-cell wrapper auto-detects and attaches to this browser
  #      instead of launching its own, so xdg-open OAuth flows and MCP browser
  #      automation share one chromium instance + one set of cookies/sessions.
  # Bound to 127.0.0.1 only -- exposing CDP on 0.0.0.0 is a remote-code-execution surface.
  chromiumWrapper = pkgs.writeShellScriptBin "chromium" ''
    _profile="''${CHROMIUM_PROFILE_PATH:-$HOME/.chrome/''${APP_NAME:-default}}"
    mkdir -p "$_profile"
    # CELL-62: sweep stale SingletonLock/Cookie/Socket left by a crashed
    # peer (this wrapper and patchright-mcp-cell share the same profile dir).
    ${chromiumSingletonSweep} "$_profile"
    exec ${pkgs.chromium}/bin/chromium \
      --user-data-dir="$_profile" \
      --remote-debugging-port=9222 \
      --remote-debugging-address=127.0.0.1 \
      --no-sandbox \
      --disable-infobars \
      --disable-gpu \
      --disable-dev-shm-usage \
      ${lib.concatStringsSep " " extensionArgs} \
      "$@"
  '';

in {
  imports = [ ./extensions.nix ];

  options.devcell.modules.scraping = {
    enable = lib.mkEnableOption "Patchright stealth browser (Chromium + stealth patches + MCP)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Patchright stealth browser MCP — anti-bot Chromium for Cloudflare/Kasada-grade sites";
        mcpServers = [ "playwright" ];
        sizeMb = 700;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [
      patchrightMcp
      patchrightMcpCell
      chromiumWrapper
    ];

    home.sessionVariables = {
      # Patchright uses its own bundled Chromium (with webdriver stealth patches).
      # Do NOT set PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH -- it overrides the patched binary.
      # The interactive chromium wrapper above uses pkgs.chromium for manual browsing.
      PLAYWRIGHT_BROWSERS_PATH = "${browsers}";
      PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = "1";
    };

    # Contribute patchright MCP server to the system-level managed-mcp.json.
    # Patchright = stealth Playwright fork -- patches CDP Runtime.enable, adds
    # playwright-extra + puppeteer-extra-plugin-stealth (triple stealth stack).
    # ${VAR} in string values -> literal ${VAR} in JSON -> Claude Code expands at runtime.
    devcell.managedMcp.servers.playwright = {
      command = "${mcpCfg.nixBinPrefix}/patchright-mcp-cell";
      args = [
        "--browser" "chromium"
        # No --config or --init-script here: the wrapper auto-discovers them
        # from share/patchright/ in the nix profile, which always resolves to
        # the latest generation. This avoids nix store hash pinning in MCP args.
      ];
    };
  };
}
