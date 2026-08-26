# vm — QEMU virtualisation and disk-image tooling
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.vm;
in {
  options.devcell.modules.vm = {
    enable = lib.mkEnableOption "QEMU virtualisation: full system emulation (x86, ARM), disk image utilities, UEFI firmware";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "QEMU system emulation (x86_64, aarch64), qemu-img/qemu-nbd utilities, OVMF UEFI firmware, swtpm TPM emulator, genisoimage ISO creation";
        mcpServers = [];
        sizeMb = 800;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = with pkgs; [
      qemu       # full system emulation + utilities (use: qemu-system-x86_64, qemu-system-aarch64, qemu-img, qemu-nbd)
      virtiofsd  # vhost-user virtio-fs daemon — shares host directories into QEMU guests (use: virtiofsd)
      powershell # cross-platform shell — lint and run Windows provisioning scripts on the host (use: pwsh)
      cdrkit     # ISO 9660/UDF image creation — provides genisoimage/mkisofs for building bootable ISOs (use: genisoimage)
      msitools   # Windows Installer inspection — extracts MSI payloads and tables host-side (use: msiextract, msiinfo)
      swtpm      # software TPM 2.0 emulator — required for Windows 11 guests
    ] ++ lib.optionals pkgs.stdenv.isx86_64 [
      OVMF       # UEFI firmware for x86_64 VMs — depends on syslinux (x86-only)
    ];
  };
}
