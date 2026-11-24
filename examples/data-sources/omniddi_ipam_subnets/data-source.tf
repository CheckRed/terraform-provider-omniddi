data "omniddi_ipam_subnets" "lab" {
  search = "lab"
}

data "omniddi_ipam_subnets" "owned" {
  scope_node_id = 42

  filters = [
    { field = "owner", operator = "EQ", value = "netops" },
  ]

  tag_filters = {
    env = "prod"
  }
}
