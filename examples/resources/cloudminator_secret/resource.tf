variable "db_password" {
  type      = string
  sensitive = true
}

resource "cloudminator_secret" "db" {
  name = "app-db"
  data = {
    username = "app"
    password = var.db_password
  }
}
