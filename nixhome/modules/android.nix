# android.nix — Android SDK, development, and app reverse-engineering tools
#
# Provides: ADB, apktool, jadx + decompiler complements (cfr, dex2jar,
# enjarify, procyon, androguard); APK acquisition and packaging (apkeep,
# bundletool, apksigner); static triage (apkleaks, apkid, quark-engine);
# dynamic analysis (mitmproxy, mitmproxy2swagger, frida-tools, jnitrace,
# scrcpy); rooted-device firmware work (payload-dumper-go, abootimg, avbroot —
# sparse-image and boot-image tools come from android-tools, see below);
# Android SDK/build-tools/emulator (x86_64)
#
# NOTE on platform support: the Android SDK (aapt, build-tools, emulator) is
# x86_64-linux only in nixpkgs and is skipped on aarch64-linux. Everything else
# works on every platform: adb, the decompiler toolkit (jadx, cfr, dex2jar,
# enjarify, procyon, androguard — pure Java/Python), apktool (aarch64 gets
# the aapt-free variant; see apktoolNoAapt below), and the whole RE toolchain
# below (Rust/Go/Java/Python — every attribute verified to evaluate and
# substitute on both aarch64-linux and x86_64-linux).
# For the SDK/emulator on aarch64, use a physical device + ADB over USB, or a
# cloud emulator (Firebase Test Lab).
#
# NOTE on emulator: Running the Android emulator requires KVM (/dev/kvm).
# On Linux hosts, pass --device /dev/kvm to docker run.
#
# NOTE on what is missing: these have no nixpkgs attribute as of nixos-25.11 and
# still need pip/npm in the cell — objection, apk-mitm, hermes-dec (React Native
# Hermes bytecode), mobsf, reflutter/blutter (Flutter), uber-apk-signer, jd-gui.
# smali/baksmali have no standalone package either, but apktool bundles both.
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.android;
  isX86Linux = pkgs.stdenv.hostPlatform.system == "x86_64-linux";
  isAarch64Linux = pkgs.stdenv.hostPlatform.system == "aarch64-linux";

  # apktool for aarch64-linux. nixpkgs' apktool refuses to build here because it
  # puts x86_64-only `aapt` on PATH — but that dependency is unnecessary. The jar
  # itself needs no aapt to *decode* (`apktool d`), and for *build* (`apktool b`)
  # it extracts its own bundled x86_64 aapt2, which runs under this devcell's
  # x86_64 emulation (Docker Desktop / Rosetta). So we reuse the exact same jar
  # (src + hash from nixpkgs) and just drop the aapt PATH-prefix from the wrapper.
  # Verified on aarch64: decode + build + re-assembly all produce valid APKs.
  # Caveat: on a bare ARM host with no x86_64 emulation, only `apktool d` works;
  # `apktool b` would need a native aapt2 supplied via --aapt2.
  apktoolNoAapt = pkgs.apktool.overrideAttrs (_: {
    installPhase = ''
      install -D ''${src} "$out/libexec/apktool/apktool.jar"
      mkdir -p "$out/bin"
      makeWrapper "${pkgs.jdk_headless}/bin/java" "$out/bin/apktool" \
          --add-flags "-jar $out/libexec/apktool/apktool.jar"
    '';
  });

  # Android SDK composition via androidenv.
  # System images are NOT included — download via sdkmanager after first run:
  #   sdkmanager "system-images;android-35;google_apis;x86_64"
  #   avdmanager create avd -n pixel9 -k "system-images;android-35;google_apis;x86_64" -d pixel_9
  androidSdk = pkgs.androidenv.composeAndroidPackages {
    platformToolsVersion = "35.0.2";
    buildToolsVersions = ["35.0.0"];
    platformVersions = ["35"];
    includeEmulator = true;
    emulatorVersion = "35.3.12";
    includeSystemImages = false;
    useGoogleAPIs = true;
    useGoogleTVAddOns = false;
    extraLicenses = [
      "android-sdk-license"
      "android-sdk-preview-license"
      "google-gdk-license"
    ];
  };
in {
  options.devcell.modules.android = {
    enable = lib.mkEnableOption "Android SDK + ADB + build-tools + app reverse-engineering toolkit (apktool, jadx, cfr, dex2jar, enjarify, procyon, androguard, apkeep, bundletool, apksigner, apkleaks, apkid, quark-engine, mitmproxy(+2swagger), frida-tools, jnitrace, scrcpy, payload-dumper-go, simg2img, abootimg, avbroot on all arch; full SDK + emulator x86_64 only)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Android dev: ADB+fastboot + app RE toolkit (apktool, jadx, cfr, dex2jar, enjarify, procyon, androguard, apkeep, bundletool, apksigner, apkleaks, apkid, quark-engine, mitmproxy, frida-tools, jnitrace, scrcpy, OTA/boot-image tools) all arch; Android SDK + emulator (x86_64 only)";
        mcpServers = [ ];
        sizeMb = 2750;  # x86_64 with full SDK; aarch64 ~850 MB (adb + RE toolkit, no SDK)
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages =
      [ pkgs.android-tools ]  # adb + fastboot, compiled from source (all platforms)
      # Reverse-engineering toolkit — pure Java/Python, works on all platforms
      # including aarch64-linux (Apple Silicon Docker). Different decompiler
      # backends catch what jadx misses.
      ++ [
        pkgs.jadx              # DEX/APK decompiler to readable Java/Kotlin (primary)
        pkgs.cfr               # Java decompiler, better lambdas/switches than jadx
        pkgs.dex2jar           # DEX->JAR conversion (feeds cfr/procyon JAR input)
        pkgs.enjarify          # Google's DEX->JAR fallback for edge cases
        pkgs.procyon           # Java decompiler, strongest on generics (Apollo GraphQL)
        pkgs.androguard        # Python DEX/APK analysis (manifest, calls, signatures)
      ]
      # apktool — APK decompile/recompile, smali, resource decoding.
      # x86_64 uses the stock package (native aapt); aarch64 uses the aapt-free
      # variant above (bundled aapt2 via emulation). See apktoolNoAapt note.
      ++ lib.optionals isX86Linux [ pkgs.apktool ]
      ++ lib.optionals isAarch64Linux [ apktoolNoAapt ]
      # APK acquisition and (re)packaging. APKPure serves split APKs/XAPK, so
      # bundletool is what turns an apkeep download into something installable;
      # apksigner re-signs whatever apktool rebuilds. On x86_64 the SDK also
      # ships apksigner, but under build-tools/ — androidsdk only symlinks
      # platform-tools, emulator and cmdline-tools into bin/, so no collision.
      ++ [
        pkgs.apkeep      # download APKs from Google Play / APKPure (use: apkeep -a com.example.app -d apk-pure .)
        pkgs.bundletool  # split APK / .aab -> installable APK set (use: bundletool build-apks)
        pkgs.apksigner   # sign and verify APKs after a rebuild (use: apksigner sign --ks key.jks app.apk)
      ]
      # Static triage — what the APK ships and what it leaks, before any device
      # work. apkleaks alone often recovers most of the API surface.
      ++ [
        pkgs.apkleaks      # hunt URIs, endpoints and secrets in a DEX (use: apkleaks -f app.apk)
        pkgs.apkid         # identify packer/obfuscator/anti-debug (use: apkid app.apk)
        pkgs.quark-engine  # behaviour scoring over the API call graph (use: quark -a app.apk)
      ]
      # Dynamic analysis — capture traffic, then instrument the running app.
      # mitmproxy2swagger converts captured flows straight into an OpenAPI spec.
      ++ [
        pkgs.mitmproxy          # TLS intercepting proxy (use: mitmdump -w flows)
        pkgs.mitmproxy2swagger  # mitmproxy flows -> OpenAPI 3 spec (use: mitmproxy2swagger -i flows -o spec.yml)
        pkgs.frida-tools        # instrumentation CLI + frida-apk gadget patcher (use: frida -U -f com.example.app -l hook.js)
        pkgs.jnitrace           # Frida-based JNI call tracer for native libs (use: jnitrace -l libfoo.so com.example.app)
        pkgs.scrcpy             # mirror and control the device over ADB (use: scrcpy)
      ]
      # Rooted-device firmware work: unpack an OTA, patch boot for Magisk.
      # Deliberately NOT pkgs.simg2img — android-tools above already ships
      # simg2img/img2simg/append2simg, and a second copy is a hard
      # home-manager-path collision, not a silent shadow. android-tools also
      # covers mkbootimg/unpack_bootimg and avbtool; abootimg is kept for its
      # initrd pack/unpack, avbroot for OTA patching (different tool, no
      # overlap with avbtool).
      ++ [
        pkgs.payload-dumper-go  # extract partition images from an OTA payload.bin
        pkgs.abootimg           # unpack/repack boot.img + initrd (use: abootimg -x boot.img)
        pkgs.avbroot            # root an A/B OTA with Magisk, preserving Verified Boot (use: avbroot ota patch)
      ]
      ++ lib.optionals isX86Linux [
        androidSdk.androidsdk  # full SDK + build-tools + emulator (x86_64 only)
      ];

    # ANDROID_HOME is the canonical SDK root; ANDROID_SDK_ROOT is the legacy alias.
    # Both are needed because different tools check different vars.
    # Only set on x86_64-linux where the SDK is actually installed.
    home.sessionVariables = lib.mkIf isX86Linux {
      ANDROID_HOME = "${androidSdk.androidsdk}/libexec/android-sdk";
      ANDROID_SDK_ROOT = "${androidSdk.androidsdk}/libexec/android-sdk";
    };
  };
}
