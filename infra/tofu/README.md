# OpenTofu (Terraform-compatible, no license needed)

    brew install opentofu        # or: https://opentofu.org/docs/intro/install/
    cp terraform.tfvars.example terraform.tfvars
    tofu init && tofu plan && tofu apply

Images come from GHCR (published by CI) — or build locally and pass `-var registry=chainforge -var tag=dev`.
Only the gateway and web publish ports; the signer is reachable solely on the private Docker network.
