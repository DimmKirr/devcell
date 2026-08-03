# android.nix — Android SDK and development tools
#
# Provides: ADB, apktool, jadx + decompiler complements (cfr, dex2jar,
# enjarify, procyon, androguard); Android SDK/build-tools/emulator (x86_64)
#
# NOTE on platform support: the Android SDK (aapt, build-tools, emulator) is
# x86_64-linux only in nixpkgs and is skipped on aarch64-linux. Everything else
# works on every platform: adb, the decompiler toolkit (jadx, cfr, dex2jar,
# enjarify, procyon, androguard — pure Java/Python), and apktool (aarch64 gets
# the aapt-free variant; see apktoolNoAapt below).
# For the SDK/emulator on aarch64, use a physical device + ADB over USB, or a
# cloud emulator (Firebase Test Lab).
#
# NOTE on emulator: Running the Android emulator requires KVM (/dev/kvm).
# On Linux hosts, pass --device /dev/kvm to docker run.
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
    enable = lib.mkEnableOption "Android SDK + ADB + build-tools + reverse-engineering toolkit (apktool + jadx, cfr, dex2jar, enjarify, procyon, androguard on all arch; full SDK + emulator x86_64 only)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Android dev: ADB+fastboot + RE toolkit (apktool, jadx, cfr, dex2jar, enjarify, procyon, androguard) all arch; Android SDK + emulator (x86_64 only)";
        mcpServers = [ ];
        sizeMb = 2500;  # x86_64 with full SDK; aarch64 ~600 MB (adb + decompilers, no SDK)
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
