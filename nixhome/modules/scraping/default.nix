# scraping/default.nix — Patchright MCP server + stealth Chromium automation
# Self-contained module: buildNpmPackage, playwright-driver browsers, stealth
# init script, config JSON, and wrapper script. No dependency on desktop/.
#
# Interactive browsing: nix chromium wrapper (--no-sandbox, per-app profile).
# Automation: Patchright's bundled Chromium (stealth — no webdriver leak).
# Do NOT set PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH — it overrides the patched binary.
{pkgs, lib, config, ...}:
let
  mcpCfg = config.devcell.managedMcp;

  # UA architecture — must match what Chromium puts in navigator.userAgent.
  # Chrome's "UA reduction" always reports "x86_64" regardless of real CPU,
  # but getHighEntropyValues().architecture leaks the real arch ("arm" on aarch64).
  # Detection scripts compare these and flag the mismatch.
  # Always "x86" because that's what Chrome's UA string claims.
  uaArch = "x86";

  # Chromium browser from playwright-driver — chromium only, no firefox/webkit/ffmpeg.
  # patchright-core reads browsers.json for expected revision (e.g. 1208) but nixpkgs
  # playwright-driver may ship a different revision (e.g. 1194). Bridge with symlinks.
  patchrightChromiumRevision = "1208";
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
    version = "0.0.68";
    src = pkgs.runCommandLocal "patchright-mcp-src" {} ''
      mkdir -p $out
      cp ${./patchright-mcp-package.json} $out/package.json
      cp ${./patchright-mcp-package-lock.json} $out/package-lock.json
    '';
    npmDepsHash = "sha256-3eQTPUgM58Pfb3WibUr4dUx3YkVOhgWBBu6I+4VEXL4=";
    npmPackFlags = [ "--ignore-scripts" ];
    npmFlags = [ "--ignore-scripts" ];
    dontNpmBuild = true;
    nativeBuildInputs = [ pkgs.makeWrapper ];

    postInstall = ''
      # Inject human-like mouse movement into browser_click handler.
      # Patches snapshot.js to add Bezier cursor trajectory before each click.
      SNAP="$out/lib/node_modules/nix-patchright-mcp-server/node_modules/patchright/lib/mcp/browser/tools/snapshot.js"
      if [ -f "$SNAP" ]; then
        # Add humanMove function before the module.exports line
        ${pkgs.gnused}/bin/sed -i '/^module.exports/i \
// --- Human mouse movement (injected by devcell nix patch) ---\
var __hmLastX = 960, __hmLastY = 540;\
async function __hmMove(page, tx, ty) {\
  var sx=__hmLastX, sy=__hmLastY, dist=Math.hypot(tx-sx,ty-sy);\
  if(dist<2){__hmLastX=tx;__hmLastY=ty;return;}\
  if(dist<50){var st=5+~~(Math.random()*5);for(var i=1;i<=st;i++){var t=i/st,e=t*t*(3-2*t);await page.mouse.move(sx+(tx-sx)*e+(Math.random()-.5)*2,sy+(ty-sy)*e+(Math.random()-.5)*2);await new Promise(r=>setTimeout(r,5+Math.random()*10));}await page.mouse.move(tx,ty);__hmLastX=tx;__hmLastY=ty;return;}\
  var steps=Math.max(30,~~(dist/5)+~~(Math.random()*20)),dur=200+dist*1.0+Math.random()*250;\
  var ang=Math.atan2(ty-sy,tx-sx),perp=ang+Math.PI/2;\
  var arcMag=dist*(0.08+Math.random()*0.15)*(Math.random()>.5?1:-1);\
  var cp1t=0.2+Math.random()*0.15,cp2t=0.65+Math.random()*0.15;\
  var cx1=sx+(tx-sx)*cp1t+Math.cos(perp)*arcMag,cy1=sy+(ty-sy)*cp1t+Math.sin(perp)*arcMag;\
  var cx2=sx+(tx-sx)*cp2t+Math.cos(perp)*arcMag*0.6,cy2=sy+(ty-sy)*cp2t+Math.sin(perp)*arcMag*0.6;\
  var ov=4+(dist/200)*5+Math.random()*4,ox=tx+Math.cos(ang)*ov,oy=ty+Math.sin(ang)*ov;\
  for(var i2=0;i2<=steps;i2++){var t2=i2/steps,e2;if(t2<.5)e2=16*t2*t2*t2*t2*t2;else{var f=-2*t2+2;e2=1-f*f*f*f*f/2;}\
  var x2,y2;if(t2<.88){var b=Math.min(e2/.88,1),u=1-b;x2=u*u*u*sx+3*u*u*b*cx1+3*u*b*b*cx2+b*b*b*ox;y2=u*u*u*sy+3*u*u*b*cy1+3*u*b*b*cy2+b*b*b*oy;}else{var c=(t2-.88)/.12,ce=c*c*(3-2*c);x2=ox+(tx-ox)*ce;y2=oy+(ty-oy)*ce;}\
  var tr=0.5+(1-Math.sin(t2*Math.PI))*1.5;x2+=(Math.random()-.5)*tr;y2+=(Math.random()-.5)*tr;\
  await page.mouse.move(x2,y2);var spd=0.3+Math.sin(t2*Math.PI)*1.0;await new Promise(r=>setTimeout(r,(dur/steps)/spd+Math.random()*3));}\
  await page.mouse.move(tx,ty);__hmLastX=tx;__hmLastY=ty;\
}\
// --- End human mouse movement ---' "$SNAP"

        # Patch click: add mouse movement before locator.click
        ${pkgs.gnused}/bin/sed -i 's/await locator\.click(options);/{ const __b = await locator.boundingBox(); if (__b) { const __tx = __b.x + __b.width * (0.35 + Math.random() * 0.3); const __ty = __b.y + __b.height * (0.35 + Math.random() * 0.3); await __hmMove(tab.page, __tx, __ty); await new Promise(r => setTimeout(r, 30 + Math.random() * 120)); } await locator.click(options); if (typeof __hmLastX !== "undefined" \&\& locator.boundingBox) { try { const __ab = await locator.boundingBox(); if (__ab) { __hmLastX = __ab.x + __ab.width\/2; __hmLastY = __ab.y + __ab.height\/2; } } catch(e){} } }/' "$SNAP"

        # Same for dblclick
        ${pkgs.gnused}/bin/sed -i 's/await locator\.dblclick(options);/{ const __b = await locator.boundingBox(); if (__b) { const __tx = __b.x + __b.width * (0.35 + Math.random() * 0.3); const __ty = __b.y + __b.height * (0.35 + Math.random() * 0.3); await __hmMove(tab.page, __tx, __ty); await new Promise(r => setTimeout(r, 30 + Math.random() * 80)); } await locator.dblclick(options); }/' "$SNAP"

        echo "Patched snapshot.js with human mouse movement"
      else
        echo "WARNING: snapshot.js not found at $SNAP"
      fi

      bin="$out/lib/node_modules/nix-patchright-mcp-server/node_modules/.bin"
      makeWrapper "$bin/mcp-server-patchright" "$out/bin/mcp-server-patchright" \
        --chdir "$bin" \
        --set PLAYWRIGHT_BROWSERS_PATH "${browsers}" \
        --set PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD "1"
    '';
  };

  # Static LD_LIBRARY_PATH fallback for the patchright-mcp-cell wrapper.
  # This wrapper is a nix derivation baked at eval time — can't source files at runtime.
  # All other contexts (entrypoint services, interactive shells) use the full-closure
  # /opt/devcell/.nix-ld-library-path generated by home.activation.generateNixLdPath.
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
    xorg.libX11       # libX11 + libX11-xcb — core X11 client lib (Electron SIGTRAP without it)
    xorg.libXcomposite
    xorg.libXcursor
    xorg.libXdamage
    xorg.libXext
    xorg.libXfixes
    xorg.libXi
    xorg.libXrandr
    xorg.libXtst
    xorg.libxkbfile
    libgbm        # GBM buffer manager — mesa itself does NOT provide libgbm.so
    mesa          # Mesa 3D — llvmpipe software rasterizer
    cairo
    pango
    alsa-lib
    pulseaudio    # PulseAudio client lib
    gcc.cc.lib    # libgomp (OpenMP runtime)
    gtk3          # GTK 3 — needed by Electron/Chromium-based GUI apps
  ];
  runtimeLibPath = pkgs.lib.makeLibraryPath runtimeLibs;

  # Patchright MCP config — Chromium launch args for X11 display.
  # No --ozone-platform needed (auto-detects X11 from DISPLAY).
  # WebGL via Mesa Lavapipe: ANGLE → Vulkan → lvp (CPU software renderer).
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
      ];
      # Block ServiceWorkers — they run in a separate scope unreachable by init-script.
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

      // Mock chrome.runtime
      window.chrome = {
        runtime: { connect: function(){}, sendMessage: function(){} },
        loadTimes: function() { return {}; },
        csi: function() { return {}; }
      };

      // --- Fix toString leaks (must be early — WebGL patching uses _nativeFnNames) ---
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

      // Fix plugins + mimeTypes — headless Chrome may have empty arrays.
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

      // Spoof userAgentData high-entropy values — Chromium's userAgent says "x86_64"
      // (UA reduction) but getHighEntropyValues() leaks the real arch on arm64.
      // Detection scripts compare these and flag the mismatch.
      // Architecture value injected at nix build time: "${uaArch}"
      // Must use Object.defineProperty on prototype — direct assignment is a no-op
      // because the property is non-writable on NavigatorUAData.prototype.
      if (typeof NavigatorUAData !== 'undefined') {
        const origGetHigh = NavigatorUAData.prototype.getHighEntropyValues;
        Object.defineProperty(NavigatorUAData.prototype, 'getHighEntropyValues', {
          value: async function(hints) {
            const values = await origGetHigh.call(this, hints);
            values.architecture = '${uaArch}';
            return values;
          },
          writable: true,
          configurable: true
        });
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

      // --- Media devices mock (headless has 0 devices → bot signal) ---
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
      // Use Object.defineProperty on WebGL prototypes — works on ALL contexts
      // regardless of how they're created (Canvas, OffscreenCanvas, iframe).
      // Proxy-wrapping getContext gets bypassed by CreepJS; prototype patching doesn't.
      const _wglVendor = 'Intel Inc.';
      const _wglRenderer = 'Intel Iris OpenGL Engine';
      // Intel-realistic parameter overrides (SwiftShader defaults in comments)
      const _wglParams = {
        37445: _wglVendor,   // UNMASKED_VENDOR_WEBGL
        37446: _wglRenderer, // UNMASKED_RENDERER_WEBGL
        3379:  16384,        // MAX_TEXTURE_SIZE (SwiftShader: 8192)
        3386:  'viewport',   // MAX_VIEWPORT_DIMS — special handling below
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

      // --- Patch Web Workers (spoof WebGL + UAData in worker scope) ---
      // Workers run in a separate global; init-script patches don't reach them.
      // Intercept Worker constructor to prepend spoof code into worker scripts.
      const _workerPatch = `
        (function() {
          if (typeof WebGLRenderingContext !== 'undefined') {
            var params = {37445:'Intel Inc.',37446:'Intel Iris OpenGL Engine',7936:'WebKit',7937:'WebKit WebGL',3379:16384,34076:16384,34024:16384,36183:8};
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
          }
          if (typeof NavigatorUAData !== 'undefined') {
            var origGetHigh = NavigatorUAData.prototype.getHighEntropyValues;
            Object.defineProperty(NavigatorUAData.prototype, 'getHighEntropyValues', {
              value: async function(hints) {
                var values = await origGetHigh.call(this, hints);
                values.architecture = '${uaArch}';
                return values;
              },
              writable: true,
              configurable: true
            });
          }
        })();\n`;
      const _origWorker = window.Worker;
      const _origBlob = window.Blob;
      window.Worker = function(url, opts) {
        try {
          // Handle Blob URLs — read the blob content, prepend patch
          if (typeof url === 'string' && url.startsWith('blob:')) {
            const xhr = new XMLHttpRequest();
            xhr.open('GET', url, false);
            xhr.send();
            if (xhr.status === 200) {
              const blob = new _origBlob([_workerPatch + xhr.responseText], {type: 'application/javascript'});
              return new _origWorker(URL.createObjectURL(blob), opts);
            }
          }
          // Handle regular URLs — fetch script, prepend patch
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

      // Window dimensions — realistic maximized Chrome on 1920x1080
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

      // VisualViewport — matches innerWidth/innerHeight
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

      // ScreenOrientation — 1920x1080 = landscape
      if (screen.orientation) {
        Object.defineProperty(screen.orientation, 'type', {
          get: () => 'landscape-primary', configurable: true
        });
        Object.defineProperty(screen.orientation, 'angle', {
          get: () => 0, configurable: true
        });
      }

      // matchMedia — proxy dimension queries to match our spoofed viewport.
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

      // --- Fix noDownlinkMax: mock NetworkInformation.downlinkMax ---
      if (navigator.connection) {
        Object.defineProperty(navigator.connection, 'downlinkMax', {
          get: () => Infinity,
          configurable: true
        });
      }
    '';
  };

  # Keep-alive init script — prevents session timeouts by simulating user activity.
  # Scrolls 10px up/down every 60 seconds after 60 seconds of no user interaction.
  # Resets timer on any scroll, click, keypress, or mouse movement.
  keepAliveInitScript = pkgs.writeTextFile {
    name = "keep-alive-init.js";
    text = ''
      (function() {
        let _kaTimer = null;
        function _kaReset() {
          if (_kaTimer) clearTimeout(_kaTimer);
          _kaTimer = setTimeout(function _kaTick() {
            window.scrollBy(0, 10);
            setTimeout(function() { window.scrollBy(0, -10); }, 500);
            _kaTimer = setTimeout(_kaTick, 60000);
          }, 60000);
        }
        ['scroll', 'click', 'keydown', 'mousemove'].forEach(function(evt) {
          window.addEventListener(evt, _kaReset, { passive: true });
        });
        _kaReset();
      })();
    '';
  };

  # patchright-mcp-cell wrapper — bundles LD_LIBRARY_PATH, secrets, user-data-dir,
  # and auto-discovers config.json + stealth-init.js from co-located share/ dir.
  patchrightMcpCell = let
    wrapperScript = pkgs.writeShellScript "patchright-mcp-cell-inner" ''
      export PLAYWRIGHT_BROWSERS_PATH="${browsers}"
      export PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
      export LD_LIBRARY_PATH="${runtimeLibPath}''${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
      # Mesa Lavapipe — software Vulkan ICD for WebGL via ANGLE→Vulkan→lvp
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
      trap 'rm -f "$_RUNTIME_CONFIG" "$SECRETS_FILE"' EXIT
      # Convert LANG (e.g. en_US.UTF-8) to Playwright locale (en-US).
      _PW_LOCALE="''${LANG%%.*}"        # strip .UTF-8
      _PW_LOCALE="''${_PW_LOCALE//_/-}" # en_US → en-US
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

      [ -f "$_SHARE/stealth-init.js" ] && _EXTRA_ARGS+=(--init-script "$_SHARE/stealth-init.js")
      [ -f "$_SHARE/keep-alive-init.js" ] && _EXTRA_ARGS+=(--init-script "$_SHARE/keep-alive-init.js")

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

      STORAGE_STATE="$HOME/storage-state.json"
      if [ -f "$STORAGE_STATE" ]; then
        mcp-server-patchright --no-sandbox --isolated --storage-state "$STORAGE_STATE" --secrets "$SECRETS_FILE" "''${_EXTRA_ARGS[@]}" "$@"
      else
        USER_DATA_DIR="''${PLAYWRIGHT_MCP_USER_DATA_DIR:-$HOME/.playwright-''${APP_NAME:-cell}}"
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
  '';

  # Interactive chromium wrapper — reads CHROMIUM_PROFILE_PATH at runtime so each
  # container can have an isolated profile even when sharing CELL_HOME.
  chromiumWrapper = pkgs.writeShellScriptBin "chromium" ''
    exec ${pkgs.chromium}/bin/chromium \
      --user-data-dir="''${CHROMIUM_PROFILE_PATH:-$HOME/.chrome-''${APP_NAME:-default}}" \
      --no-sandbox \
      --disable-gpu \
      --disable-dev-shm-usage \
      "$@"
  '';

in {
  config = {
    home.packages = [
      patchrightMcp
      patchrightMcpCell
      chromiumWrapper
    ];

    home.sessionVariables = {
      # Patchright uses its own bundled Chromium (with webdriver stealth patches).
      # Do NOT set PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH — it overrides the patched binary.
      # The interactive chromium wrapper above uses pkgs.chromium for manual browsing.
      PLAYWRIGHT_BROWSERS_PATH = "${browsers}";
      PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = "1";
    };

    # Contribute patchright MCP server to the system-level managed-mcp.json.
    # Patchright = stealth Playwright fork — patches CDP Runtime.enable, adds
    # playwright-extra + puppeteer-extra-plugin-stealth (triple stealth stack).
    # ${VAR} in string values → literal ${VAR} in JSON → Claude Code expands at runtime.
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
