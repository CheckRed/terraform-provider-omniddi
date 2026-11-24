data "omniddi_ipam_containers" "all" {}

data "omniddi_ipam_containers" "top_level" {
  recursive = false
}

data "omniddi_ipam_containers" "below_corp" {
  parent_id = 42
  search    = "datacenter"
}
