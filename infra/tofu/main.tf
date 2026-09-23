# OpenTofu stack: the whole ChainForge platform on a local Docker daemon.
# Zero cost, no cloud account: `tofu init && tofu apply`. Swap the provider for a cloud one to go remote.
terraform {
  required_version = ">= 1.6.0"
  required_providers {
    docker = { source = "kreuzwerker/docker", version = "~> 3.0" }
  }
}

provider "docker" {}

variable "registry" {
  type        = string
  default     = "ghcr.io/chainforge"
  description = "Image registry namespace (GHCR by default)."
}
variable "tag" {
  type    = string
  default = "latest"
}
variable "api_key" {
  type      = string
  default   = "dev-key-change-me"
  sensitive = true
}
variable "signer_secret" {
  type      = string
  sensitive = true
  validation {
    condition     = length(var.signer_secret) >= 16
    error_message = "signer_secret must be at least 16 characters."
  }
}
variable "signer_tx_chains" {
  type        = string
  default     = "1337"
  description = "Chain ids the signer may sign transactions for (empty disables transaction signing)."
}
variable "signer_tx_max_value" {
  type        = string
  default     = "1000000000000000"
  description = "Per-transaction value cap in wei."
}
variable "signer_keys" {
  type        = string
  sensitive   = true
  description = "id=64hex pairs, comma separated."
}

resource "docker_network" "net" {
  name = "chainforge"
}

locals {
  images = toset(["gateway", "chainsim", "signer", "engine", "web"])
}

resource "docker_image" "img" {
  for_each     = local.images
  name         = "${var.registry}/${each.key}:${var.tag}"
  keep_locally = true
}

# Shared Unix-socket directory: the ONLY path to the simulation engine.
resource "docker_volume" "engine_sock" {
  name = "chainforge-engine-sock"
}

resource "docker_container" "sock_init" {
  name     = "engine-sock-init"
  image    = "busybox:1.36"
  command  = ["sh", "-c", "chown 65532:65532 /run/engine && chmod 700 /run/engine"]
  must_run = false
  rm       = false
  volumes {
    volume_name    = docker_volume.engine_sock.name
    container_path = "/run/engine"
  }
}

resource "docker_container" "engine" {
  name         = "engine"
  image        = docker_image.img["engine"].image_id
  restart      = "unless-stopped"
  read_only    = true
  network_mode = "none" # unreachable except through the socket
  capabilities { drop = ["ALL"] }
  env        = ["ENGINE_LISTEN=unix:///run/engine/engine.sock"]
  depends_on = [docker_container.sock_init]
  volumes {
    volume_name    = docker_volume.engine_sock.name
    container_path = "/run/engine"
  }
}

resource "docker_container" "chainsim" {
  name    = "chainsim"
  image   = docker_image.img["chainsim"].image_id
  restart = "unless-stopped"
  env     = ["SIM_ADDR=:8545", "SIM_BLOCK_MS=1500", "SIM_REORG_EVERY=12"]
  networks_advanced { name = docker_network.net.name }
}

resource "docker_container" "signer" {
  name      = "signer"
  image     = docker_image.img["signer"].image_id
  restart   = "unless-stopped"
  read_only = true
  env = [
    "SIGNER_SHARED_SECRET=${var.signer_secret}",
    "SIGNER_KEYS=${var.signer_keys}",
    "SIGNER_TX_CHAINS=${var.signer_tx_chains}",
    "SIGNER_TX_MAX_VALUE=${var.signer_tx_max_value}",
  ]
  capabilities { drop = ["ALL"] }
  networks_advanced { name = docker_network.net.name } # no published port: only reachable from the gateway
}

resource "docker_container" "gateway" {
  name       = "gateway"
  image      = docker_image.img["gateway"].image_id
  restart    = "unless-stopped"
  read_only  = true
  depends_on = [docker_container.chainsim, docker_container.signer, docker_container.engine]
  capabilities { drop = ["ALL"] }
  volumes {
    volume_name    = docker_volume.engine_sock.name
    container_path = "/run/engine"
  }
  env = [
    "GATEWAY_ADDR=:8080",
    "GATEWAY_UPSTREAMS=http://chainsim:8545",
    "GATEWAY_API_KEYS=dev=${var.api_key}",
    "SIGNER_URL=http://signer:8081",
    "ENGINE_ADDR=unix:///run/engine/engine.sock",
    "RELAY_URLS=http://chainsim:8545",
    "SIGNER_SHARED_SECRET=${var.signer_secret}",
  ]
  ports {
    internal = 8080
    external = 8080
  }
  networks_advanced { name = docker_network.net.name }
}

resource "docker_container" "web" {
  name       = "web"
  image      = docker_image.img["web"].image_id
  restart    = "unless-stopped"
  depends_on = [docker_container.gateway]
  ports {
    internal = 8080
    external = 8088
  }
  networks_advanced { name = docker_network.net.name }
}

output "console_url" { value = "http://localhost:8088" }
output "gateway_url" { value = "http://localhost:8080" }
