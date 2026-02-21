# macOS VM Builder using UTM (Apple Virtualization)
#
# Usage: task build
#   or: task create → (manual setup) → task provision

packer {
  required_plugins {
    utm = {
      version = ">= 4.0.0"
      source  = "github.com/naveenrajm7/utm"
    }
  }
}

variable "vm_name" {
  type    = string
  default = "devcell-macos"
}

variable "source_vm" {
  type    = string
  default = "macos-base"
}

variable "username" {
  type    = string
  default = "vagrant"
}

variable "password" {
  type      = string
  default   = "vagrant"
  sensitive = true
}

source "utm-utm" "macos" {
  source_path      = pathexpand("~/Library/Containers/com.utmapp.UTM/Data/Documents/${var.source_vm}.utm")
  vm_name          = var.vm_name
  ssh_username     = var.username
  ssh_password     = var.password
  ssh_timeout      = "5m"
  shutdown_command = "echo '${var.password}' | sudo -S shutdown -h now"
}

build {
  sources = ["source.utm-utm.macos"]

  provisioner "shell" {
    scripts = [
      "scripts/base-setup.sh",
      "scripts/vagrant-setup.sh",
      "scripts/homebrew-setup.sh",
      "scripts/asdf-setup.sh",
      "scripts/cleanup.sh",
    ]
  }

  post-processor "utm-zip" {
    output              = "output/${var.vm_name}.box"
    keep_input_artifact = true
  }
}
