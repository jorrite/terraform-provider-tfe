// Copyright IBM Corp. 2018, 2025
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccTFEPublicRegistryModule_vcsGitHubApp(t *testing.T) {
	rInt := rand.New(rand.NewSource(time.Now().UnixNano())).Int()
	orgName := fmt.Sprintf("tst-terraform-%d", rInt)

	moduleName := getRegistryModuleName()
	moduleProvider := getRegistryModuleProvider()

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheckTFERegistryModule(t)
			testAccGHAInstallationPreCheck(t)
		},
		ProtoV6ProviderFactories: testAccMuxedProviders,
		CheckDestroy:             testAccCheckTFEPublicRegistryModuleDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccTFEPublicRegistryModule_vcsGitHubApp(rInt),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckTFEPublicRegistryModuleExists("tfe_public_registry_module.foobar"),
					resource.TestCheckResourceAttrSet("tfe_public_registry_module.foobar", "id"),
					resource.TestCheckResourceAttr("tfe_public_registry_module.foobar", "organization", orgName),
					resource.TestCheckResourceAttr("tfe_public_registry_module.foobar", "name", moduleName),
					resource.TestCheckResourceAttr("tfe_public_registry_module.foobar", "module_provider", moduleProvider),
					resource.TestCheckResourceAttr("tfe_public_registry_module.foobar", "vcs_repo.identifier", envGithubRegistryModuleIdentifer),
					resource.TestCheckResourceAttrSet("tfe_public_registry_module.foobar", "vcs_repo.github_app_installation_id"),
				),
			},
		},
	})
}

func TestAccTFEPublicRegistryModule_vcsOAuth(t *testing.T) {
	rInt := rand.New(rand.NewSource(time.Now().UnixNano())).Int()
	orgName := fmt.Sprintf("tst-terraform-%d", rInt)

	moduleName := getRegistryModuleName()
	moduleProvider := getRegistryModuleProvider()

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheckTFERegistryModule(t)
		},
		ProtoV6ProviderFactories: testAccMuxedProviders,
		CheckDestroy:             testAccCheckTFEPublicRegistryModuleDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccTFEPublicRegistryModule_vcsOAuth(rInt),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckTFEPublicRegistryModuleExists("tfe_public_registry_module.foobar"),
					resource.TestCheckResourceAttrSet("tfe_public_registry_module.foobar", "id"),
					resource.TestCheckResourceAttr("tfe_public_registry_module.foobar", "organization", orgName),
					resource.TestCheckResourceAttr("tfe_public_registry_module.foobar", "name", moduleName),
					resource.TestCheckResourceAttr("tfe_public_registry_module.foobar", "module_provider", moduleProvider),
					resource.TestCheckResourceAttr("tfe_public_registry_module.foobar", "vcs_repo.identifier", envGithubRegistryModuleIdentifer),
					resource.TestCheckResourceAttrSet("tfe_public_registry_module.foobar", "vcs_repo.oauth_token_id"),
				),
			},
		},
	})
}

func TestAccTFEPublicRegistryModule_importByID(t *testing.T) {
	rInt := rand.New(rand.NewSource(time.Now().UnixNano())).Int()
	orgName := fmt.Sprintf("tst-terraform-%d", rInt)

	moduleName := getRegistryModuleName()
	moduleProvider := getRegistryModuleProvider()

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheckTFERegistryModule(t)
			testAccGHAInstallationPreCheck(t)
		},
		ProtoV6ProviderFactories: testAccMuxedProviders,
		CheckDestroy:             testAccCheckTFEPublicRegistryModuleDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccTFEPublicRegistryModule_vcsGitHubApp(rInt),
			},
			{
				ResourceName:      "tfe_public_registry_module.foobar",
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("%s/%s/%s/%s", orgName, orgName, moduleName, moduleProvider),
				ImportStateVerify: true,
				// VCS repo details are not available from the public registry read endpoint
				ImportStateVerifyIgnore: []string{"vcs_repo"},
			},
		},
	})
}

func testAccCheckTFEPublicRegistryModuleExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No instance ID is set")
		}

		namespace := rs.Primary.Attributes["namespace"]
		name := rs.Primary.Attributes["name"]
		moduleProvider := rs.Primary.Attributes["module_provider"]

		entry, err := readPublicRegistryModule(ctx, namespace, name, moduleProvider)
		if err != nil {
			return fmt.Errorf("Error reading public registry module: %s", err)
		}
		if entry == nil {
			return fmt.Errorf("Public registry module %s/%s/%s not found", namespace, name, moduleProvider)
		}

		return nil
	}
}

func testAccCheckTFEPublicRegistryModuleDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "tfe_public_registry_module" {
			continue
		}

		id := rs.Primary.ID
		if id == "" {
			return fmt.Errorf("No instance ID is set")
		}

		organization := rs.Primary.Attributes["organization"]
		namespace := rs.Primary.Attributes["namespace"]
		name := rs.Primary.Attributes["name"]
		moduleProvider := rs.Primary.Attributes["module_provider"]

		// Try to verify via the delete endpoint (check if it still exists)
		baseURL := testAccConfiguredClient.Client.BaseURL()
		deleteURL := fmt.Sprintf("%s://%s/api/v2/organizations/%s/registry/modules/%s/%s/%s",
			baseURL.Scheme, baseURL.Host,
			url.PathEscape(organization),
			url.PathEscape(namespace),
			url.PathEscape(name),
			url.PathEscape(moduleProvider))

		httpReq, err := http.NewRequest("GET", deleteURL, nil)
		if err != nil {
			continue
		}
		httpReq.Header.Set("Authorization", "Bearer "+testAccConfiguredClient.Token)

		httpResp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			continue
		}
		httpResp.Body.Close()

		// Also check the public registry (may take time to propagate)
		entry, _ := readPublicRegistryModule(ctx, namespace, name, moduleProvider)
		if entry != nil {
			return fmt.Errorf("Public registry module %s still exists", id)
		}
	}

	return nil
}

func testAccTFEPublicRegistryModule_vcsGitHubApp(rInt int) string {
	return fmt.Sprintf(`
resource "tfe_organization" "foobar" {
  name  = "tst-terraform-%d"
  email = "admin@company.com"
}

resource "tfe_public_registry_module" "foobar" {
  organization = tfe_organization.foobar.id
  vcs_repo {
    identifier                 = "%s"
    github_app_installation_id = "%s"
  }
}`,
		rInt,
		envGithubRegistryModuleIdentifer,
		envGithubAppInstallationID)
}

func testAccTFEPublicRegistryModule_vcsOAuth(rInt int) string {
	return fmt.Sprintf(`
resource "tfe_organization" "foobar" {
  name  = "tst-terraform-%d"
  email = "admin@company.com"
}

resource "tfe_oauth_client" "foobar" {
  organization     = tfe_organization.foobar.name
  api_url          = "https://api.github.com"
  http_url         = "https://github.com"
  oauth_token      = "%s"
  service_provider = "github"
}

resource "tfe_public_registry_module" "foobar" {
  organization = tfe_organization.foobar.name
  vcs_repo {
    identifier     = "%s"
    oauth_token_id = tfe_oauth_client.foobar.oauth_token_id
  }
}`,
		rInt,
		envGithubToken,
		envGithubRegistryModuleIdentifer)
}

// testReadPublicRegistryModuleJSON is a helper for verifying the registry.terraform.io response structure.
func testReadPublicRegistryModuleJSON(namespace string) ([]registryModuleEntry, error) {
	listURL := fmt.Sprintf(
		"https://registry.terraform.io/v3/modules?filter[namespace]=%s&page[size]=50",
		url.QueryEscape(namespace),
	)

	resp, err := http.Get(listURL) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var entries []registryModuleEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, err
	}

	return entries, nil
}
