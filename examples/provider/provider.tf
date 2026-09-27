terraform {
  required_providers {
    dremio = {
      source  = "nitra/dremio"
      version = "~> 0.5"
    }
  }
}

provider "dremio" {
  dremio_url = "http://localhost:9047"
  username   = var.dremio_username
  password   = var.dremio_password
}
