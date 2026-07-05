# android.nix — Android SDK and development tools
#
# Provides: Android SDK, ADB, build tools, apktool, jadx
#
# NOTE on platform support: Android SDK packages (aapt, build-tools, emulator)
# are x86_64-linux only in nixpkgs — they are marked badPlatforms for aarch64-linux.
# On aarch64-linux (Apple Silicon Docker, ARM servers) this module is a no-op.
# Use a physical device + ADB over USB, or a cloud emulator (Firebase Test Lab).
#
# NOTE on emulator: Running the Android emulator requires KVM (/dev/kvm).
# On Linux hosts, pass --device /dev/kvm to docker run.
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.android;
  isX86Linux = pkgs.stdenv.hostPlatform.system == "x86_64-linux";

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
    enable = lib.mkEnableOption "Android SDK + ADB + build-tools + apktool + jadx (x86_64 SDK only on aarch64)";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Android dev: ADB+fastboot (all arch), Android SDK + emulator + apktool + jadx (x86_64 only)";
        mcpServers = [ ];
        sizeMb = 2500;  # x86_64 with full SDK; aarch64 ~50 MB (adb only)
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages =
      [ pkgs.android-tools ]  # adb + fastboot, compiled from source (all platforms)
      ++ lib.optionals isX86Linux [
        androidSdk.androidsdk  # full SDK + build-tools + emulator (x86_64 only)
        pkgs.apktool           # APK decompile/recompile (reverse engineering / QA)
        pkgs.jadx              # DEX/APK decompiler to readable Java/Kotlin
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
