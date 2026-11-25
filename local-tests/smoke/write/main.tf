terraform {
  required_providers {
    omniddi = {
      source  = "checkred/omniddi"
      version = ">= 1.0.0"
    }
  }
}

variable "view_name" {
  description = "Existing DNS view to create the test zone in."
  type        = string
}

variable "zone_name" {
  description = "Name of the throwaway test zone; smoke.sh makes it unique per run."
  type        = string
}

provider "omniddi" {}

data "omniddi_dns_view" "target" {
  name = var.view_name
}

resource "omniddi_dns_zone" "smoke" {
  view_id             = data.omniddi_dns_view.target.id
  name                = var.zone_name
  ttl                 = 300
  email               = "hostmaster@example.test"
  deletion_protection = false
}

resource "omniddi_dns_record" "a" {
  zone_id = omniddi_dns_zone.smoke.id
  name    = "www"
  type    = "A"
  ttl     = 300
  values  = ["192.0.2.10", "192.0.2.11"]
}

resource "omniddi_dns_record" "txt" {
  zone_id = omniddi_dns_zone.smoke.id
  name    = "@"
  type    = "TXT"
  values  = ["terraform-provider-omniddi smoke test"]
}

output "zone_id" {
  description = "Id of the test zone."
  value       = omniddi_dns_zone.smoke.id
}
