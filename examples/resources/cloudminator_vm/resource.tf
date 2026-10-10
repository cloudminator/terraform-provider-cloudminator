resource "cloudminator_ssh_key" "me" {
  name       = "me"
  public_key = file("~/.ssh/id_ed25519.pub")
}

resource "cloudminator_vm" "web" {
  name      = "web-1"
  image     = "debian-13"
  size      = "2c-4g"
  disk_gb   = 40
  subnet_id = cloudminator_subnet.app.id
  ssh_keys  = [cloudminator_ssh_key.me.public_key]
  user      = "debian"

  user_data = <<-EOT
    #cloud-config
    packages: [nginx]
  EOT

  tags = {
    role = "web"
  }
}
