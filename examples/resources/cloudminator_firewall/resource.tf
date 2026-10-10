resource "cloudminator_firewall" "https" {
  name      = "https"
  subnet_id = cloudminator_subnet.app.id
  protocol  = "tcp"
  port      = "443"
  source    = "0.0.0.0/0"
  comment   = "public HTTPS"
}
