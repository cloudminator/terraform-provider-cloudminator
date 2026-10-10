resource "cloudminator_vpc" "main" {
  name   = "main"
  region = "eu-central-1"
}

resource "cloudminator_subnet" "app" {
  name   = "app"
  vpc_id = cloudminator_vpc.main.id
}
