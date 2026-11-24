terraform {
  required_providers {
    omniddi = {
      source = "checkred/omniddi"
    }
  }
}

provider "omniddi" {
  endpoint = "https://omniddi.example.com"
  username = "terraform-automation@example.com"
  # Set the password through the OMNIDDI_PASSWORD environment variable.

  # Omit to work in the account's landing network space.
  network_space_id = 2

  approval_mode = "fail"

  default_tags = {
    "Managed-By" = "terraform"
  }
}
