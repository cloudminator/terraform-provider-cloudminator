data "cloudminator_images" "eu" {
  region = "eu-central-1"
}

output "image_ids" {
  value = data.cloudminator_images.eu.images[*].id
}
