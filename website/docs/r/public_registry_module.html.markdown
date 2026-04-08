---
layout: "tfe"
page_title: "Terraform Enterprise: tfe_public_registry_module"
description: |-
  Manages public registry modules published from VCS via HCP Terraform
---

# tfe_public_registry_module

Publishes a Terraform module to the public Terraform registry from a VCS repository via HCP Terraform. The module name and provider are inferred from the repository name, which must follow the `terraform-<PROVIDER>-<NAME>` naming convention.

This resource manages modules on the **public** Terraform registry (registry.terraform.io), not the private registry within HCP Terraform. For private registry modules, use [`tfe_registry_module`](registry_module.html).

~> **Note**: The organization must have a claimed namespace on the public registry. See the [public namespace documentation](https://developer.hashicorp.com/terraform/cloud-docs/users-teams-organizations/organizations/public-namespace) for details.

~> **Note**: This resource does not support updates. Any change to the configuration will destroy and recreate the module.

## Example Usage

Publish a module using GitHub App:

```hcl
resource "tfe_organization" "example" {
  name  = "my-org-name"
  email = "admin@company.com"
}

data "tfe_github_app_installation" "gha_installation" {
  name = "YOUR_GH_NAME"
}

resource "tfe_public_registry_module" "example" {
  organization = tfe_organization.example.name
  vcs_repo {
    identifier                 = "my-org/terraform-aws-my-module"
    github_app_installation_id = data.tfe_github_app_installation.gha_installation.id
  }
}
```

Publish a module using OAuth:

```hcl
resource "tfe_organization" "example" {
  name  = "my-org-name"
  email = "admin@company.com"
}

resource "tfe_oauth_client" "example" {
  organization     = tfe_organization.example.name
  api_url          = "https://api.github.com"
  http_url         = "https://github.com"
  oauth_token      = "my-vcs-provider-token"
  service_provider = "github"
}

resource "tfe_public_registry_module" "example" {
  organization = tfe_organization.example.name
  vcs_repo {
    identifier     = "my-org/terraform-aws-my-module"
    oauth_token_id = tfe_oauth_client.example.oauth_token_id
  }
}
```

## Argument Reference

The following arguments are supported:

* `organization` - (Optional) The name of the organization associated with the registry module. If omitted, organization must be defined in the provider config.
* `vcs_repo` - (Required) Settings for the registry module's VCS repository. Forces a new resource if changed.

The `vcs_repo` block supports:

* `identifier` - (Required) A reference to your VCS repository in the format `<organization>/<repository>` where `<organization>` and `<repository>` refer to the organization and repository in your VCS provider. The repository name must follow the `terraform-<PROVIDER>-<NAME>` naming convention.
* `oauth_token_id` - (Optional) Token ID of the VCS Connection (OAuth Connection Token) to use. This conflicts with `github_app_installation_id` and exactly one must be specified.
* `github_app_installation_id` - (Optional) The installation ID of the GitHub App. This conflicts with `oauth_token_id` and exactly one must be specified.

## Attributes Reference

* `id` - The ID of the public registry module (format: `namespace/name/provider`).
* `name` - The name of the module, inferred from the VCS repository name.
* `module_provider` - The provider of the module, inferred from the VCS repository name.
* `namespace` - The namespace of the module on the public registry.

## Import

Public registry modules can be imported using `<ORGANIZATION>/<NAMESPACE>/<NAME>/<PROVIDER>` as the import ID. For example:

```shell
terraform import tfe_public_registry_module.example my-org-name/my-org-name/my-module/aws
```

~> **Note**: When importing, VCS repository details (`vcs_repo`) are not available from the public registry. You must ensure your configuration matches the actual VCS settings, or use `ignore_changes` for the `vcs_repo` block.
