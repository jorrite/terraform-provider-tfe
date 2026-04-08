// Copyright IBM Corp. 2018, 2025
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &resourceTFEPublicRegistryModule{}
var _ resource.ResourceWithConfigure = &resourceTFEPublicRegistryModule{}
var _ resource.ResourceWithImportState = &resourceTFEPublicRegistryModule{}
var _ resource.ResourceWithModifyPlan = &resourceTFEPublicRegistryModule{}

func NewPublicRegistryModuleResource() resource.Resource {
	return &resourceTFEPublicRegistryModule{}
}

// resourceTFEPublicRegistryModule implements the tfe_public_registry_module resource type.
type resourceTFEPublicRegistryModule struct {
	config ConfiguredClient
}

type modelTFEPublicRegistryModuleVCSRepo struct {
	Identifier        types.String `tfsdk:"identifier"`
	GHAInstallationID types.String `tfsdk:"github_app_installation_id"`
	OAuthTokenID      types.String `tfsdk:"oauth_token_id"`
}

type modelTFEPublicRegistryModule struct {
	ID             types.String                         `tfsdk:"id"`
	Organization   types.String                         `tfsdk:"organization"`
	Name           types.String                         `tfsdk:"name"`
	Namespace      types.String                         `tfsdk:"namespace"`
	ModuleProvider types.String                         `tfsdk:"module_provider"`
	VCSRepo        *modelTFEPublicRegistryModuleVCSRepo `tfsdk:"vcs_repo"`
}

// publicRegistryModuleCreateRequest is the JSON payload for creating a public registry module.
type publicRegistryModuleCreateRequest struct {
	Data publicRegistryModuleCreateData `json:"data"`
}

type publicRegistryModuleCreateData struct {
	Attributes       publicRegistryModuleCreateAttrs `json:"attributes"`
	OrganizationName string                          `json:"organization_name"`
}

type publicRegistryModuleCreateAttrs struct {
	VCSRepo publicRegistryModuleCreateVCSRepo `json:"vcs_repo"`
}

type publicRegistryModuleCreateVCSRepo struct {
	Identifier        string `json:"identifier"`
	GHAInstallationID string `json:"github_app_installation_id,omitempty"`
	OAuthTokenID      string `json:"oauth_token_id,omitempty"`
}

// registryModuleEntry represents a module from the registry.terraform.io/v3/modules API.
type registryModuleEntry struct {
	Address   string `json:"address"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	System    string `json:"system"`
	SourceURL string `json:"source-url"`
}

func (r *resourceTFEPublicRegistryModule) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_public_registry_module"
}

func (r *resourceTFEPublicRegistryModule) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Publishes a Terraform module to the public registry from a VCS repository via HCP Terraform. " +
			"The module name and provider are inferred from the repository name, which must follow " +
			"the terraform-<PROVIDER>-<NAME> naming convention.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The ID of the public registry module (format: namespace/name/provider).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"organization": schema.StringAttribute{
				Description: "Name of the organization. If omitted, organization must be defined in the provider config.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The name of the module. Computed from the VCS repository name (terraform-<PROVIDER>-<NAME> convention).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"namespace": schema.StringAttribute{
				Description: "The namespace of the module on the public registry.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"module_provider": schema.StringAttribute{
				Description: "The provider of the module. Computed from the VCS repository name (terraform-<PROVIDER>-<NAME> convention).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"vcs_repo": schema.SingleNestedAttribute{
				Description: "Settings for the registry module's VCS repository.",
				Required:    true,
				Attributes: map[string]schema.Attribute{
					"identifier": schema.StringAttribute{
						Description: "A reference to your VCS repository in the format <organization>/<repository>.",
						Required:    true,
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"oauth_token_id": schema.StringAttribute{
						Description: "Token ID of the VCS Connection (OAuth Connection Token) to use.",
						Optional:    true,
						Validators: []validator.String{
							stringvalidator.ExactlyOneOf(
								path.MatchRelative().AtParent().AtName("github_app_installation_id"),
							),
						},
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"github_app_installation_id": schema.StringAttribute{
						Description: "The installation ID of the GitHub App.",
						Optional:    true,
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
				},
			},
		},
	}
}

// Configure implements resource.ResourceWithConfigure
func (r *resourceTFEPublicRegistryModule) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(ConfiguredClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected resource Configure type",
			fmt.Sprintf("Expected tfe.ConfiguredClient, got %T. This is a bug in the tfe provider, so please report it on GitHub.", req.ProviderData),
		)
	}
	r.config = client
}

func (r *resourceTFEPublicRegistryModule) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	modifyPlanForDefaultOrganizationChange(ctx, r.config.Organization, req.State, req.Config, req.Plan, resp)
}

func (r *resourceTFEPublicRegistryModule) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan modelTFEPublicRegistryModule

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var organization string
	resp.Diagnostics.Append(r.config.dataOrDefaultOrganization(ctx, req.Plan, &organization)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Build the create request payload
	createReq := publicRegistryModuleCreateRequest{
		Data: publicRegistryModuleCreateData{
			OrganizationName: organization,
			Attributes: publicRegistryModuleCreateAttrs{
				VCSRepo: publicRegistryModuleCreateVCSRepo{
					Identifier: plan.VCSRepo.Identifier.ValueString(),
				},
			},
		},
	}

	if !plan.VCSRepo.GHAInstallationID.IsNull() && plan.VCSRepo.GHAInstallationID.ValueString() != "" {
		createReq.Data.Attributes.VCSRepo.GHAInstallationID = plan.VCSRepo.GHAInstallationID.ValueString()
	}
	if !plan.VCSRepo.OAuthTokenID.IsNull() && plan.VCSRepo.OAuthTokenID.ValueString() != "" {
		createReq.Data.Attributes.VCSRepo.OAuthTokenID = plan.VCSRepo.OAuthTokenID.ValueString()
	}

	// Derive name and provider from the repo identifier (terraform-<PROVIDER>-<NAME>)
	repoName := repoNameFromIdentifier(plan.VCSRepo.Identifier.ValueString())
	moduleName, moduleProvider, err := parseModuleRepoName(repoName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid repository name",
			fmt.Sprintf("Repository name %q does not follow the terraform-<PROVIDER>-<NAME> naming convention: %s", repoName, err),
		)
		return
	}

	tflog.Debug(ctx, fmt.Sprintf("Creating public registry module from repository %s", plan.VCSRepo.Identifier.ValueString()))

	// POST to /api/v2/organizations/{org}/registry/modules
	baseURL := r.config.Client.BaseURL()
	createURL := fmt.Sprintf("%s://%s/api/v2/organizations/%s/registry/modules",
		baseURL.Scheme, baseURL.Host, url.PathEscape(organization))

	body, err := json.Marshal(createReq)
	if err != nil {
		resp.Diagnostics.AddError("Unable to marshal create request", err.Error())
		return
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", createURL, bytes.NewReader(body))
	if err != nil {
		resp.Diagnostics.AddError("Unable to create HTTP request", err.Error())
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.config.Token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create public registry module", err.Error())
		return
	}
	defer httpResp.Body.Close()

	respBody, _ := io.ReadAll(httpResp.Body)

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		resp.Diagnostics.AddError(
			"Unable to create public registry module",
			fmt.Sprintf("API returned status %d: %s", httpResp.StatusCode, string(respBody)),
		)
		return
	}

	// The namespace is typically the organization name on the public registry.
	// Try to extract it from the response, fall back to the organization name.
	namespace := extractNamespaceFromResponse(respBody, organization)

	// Wait for the module to appear on the public registry
	tflog.Debug(ctx, "Waiting for module to appear on the public registry")
	deadline := time.Now().Add(5 * time.Minute)
	var found bool
	for time.Now().Before(deadline) {
		entry, err := readPublicRegistryModule(ctx, namespace, moduleName, moduleProvider)
		if err == nil && entry != nil {
			namespace = entry.Namespace
			found = true
			break
		}
		time.Sleep(5 * time.Second)
	}

	if !found {
		tflog.Warn(ctx, "Module not yet visible on public registry after creation; proceeding with derived values")
	}

	syntheticID := fmt.Sprintf("%s/%s/%s", namespace, moduleName, moduleProvider)

	result := modelTFEPublicRegistryModule{
		ID:             types.StringValue(syntheticID),
		Organization:   types.StringValue(organization),
		Name:           types.StringValue(moduleName),
		Namespace:      types.StringValue(namespace),
		ModuleProvider: types.StringValue(moduleProvider),
		VCSRepo:        plan.VCSRepo,
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &result)...)
}

func (r *resourceTFEPublicRegistryModule) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state modelTFEPublicRegistryModule

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Reading public registry module from registry.terraform.io")

	entry, err := readPublicRegistryModule(ctx, state.Namespace.ValueString(), state.Name.ValueString(), state.ModuleProvider.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read public registry module", err.Error())
		return
	}

	if entry == nil {
		tflog.Debug(ctx, "Public registry module no longer exists")
		resp.State.RemoveResource(ctx)
		return
	}

	// Update computed fields from the registry, preserve VCS repo from state
	state.Name = types.StringValue(entry.Name)
	state.Namespace = types.StringValue(entry.Namespace)
	state.ModuleProvider = types.StringValue(entry.System)
	state.ID = types.StringValue(fmt.Sprintf("%s/%s/%s", entry.Namespace, entry.Name, entry.System))

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *resourceTFEPublicRegistryModule) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "The update operation is not supported on this resource. All attributes require replacement.")
}

func (r *resourceTFEPublicRegistryModule) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state modelTFEPublicRegistryModule

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	organization := state.Organization.ValueString()
	namespace := state.Namespace.ValueString()
	name := state.Name.ValueString()
	moduleProvider := state.ModuleProvider.ValueString()

	tflog.Debug(ctx, fmt.Sprintf("Deleting public registry module %s/%s/%s", namespace, name, moduleProvider))

	// DELETE /api/v2/organizations/{org}/registry/modules/{namespace}/{name}/{provider}
	baseURL := r.config.Client.BaseURL()
	deleteURL := fmt.Sprintf("%s://%s/api/v2/organizations/%s/registry/modules/%s/%s/%s",
		baseURL.Scheme, baseURL.Host,
		url.PathEscape(organization),
		url.PathEscape(namespace),
		url.PathEscape(name),
		url.PathEscape(moduleProvider))

	httpReq, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create delete request", err.Error())
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.config.Token)
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete public registry module", err.Error())
		return
	}
	defer httpResp.Body.Close()

	// 204 No Content or 404 Not Found are both acceptable
	if httpResp.StatusCode != 204 && httpResp.StatusCode != 404 && httpResp.StatusCode != 200 {
		respBody, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError(
			"Unable to delete public registry module",
			fmt.Sprintf("API returned status %d: %s", httpResp.StatusCode, string(respBody)),
		)
	}
}

func (r *resourceTFEPublicRegistryModule) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Format: <ORGANIZATION>/<NAMESPACE>/<NAME>/<PROVIDER>
	s := strings.SplitN(req.ID, "/", 4)
	if len(s) != 4 {
		resp.Diagnostics.AddError(
			"Error importing public registry module",
			fmt.Sprintf("Invalid import format: %s (expected <ORGANIZATION>/<NAMESPACE>/<NAME>/<PROVIDER>)", req.ID),
		)
		return
	}

	organization := s[0]
	namespace := s[1]
	name := s[2]
	moduleProvider := s[3]

	// Verify the module exists on the public registry
	entry, err := readPublicRegistryModule(ctx, namespace, name, moduleProvider)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read public registry module during import", err.Error())
		return
	}
	if entry == nil {
		resp.Diagnostics.AddError(
			"Module not found",
			fmt.Sprintf("Module %s/%s/%s was not found on the public registry", namespace, name, moduleProvider),
		)
		return
	}

	syntheticID := fmt.Sprintf("%s/%s/%s", namespace, name, moduleProvider)

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), syntheticID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization"), organization)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("namespace"), namespace)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("module_provider"), moduleProvider)...)
}

// repoNameFromIdentifier extracts the repository name from a VCS identifier like "org/repo-name".
func repoNameFromIdentifier(identifier string) string {
	parts := strings.SplitN(identifier, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return identifier
}

// parseModuleRepoName parses a terraform-<PROVIDER>-<NAME> repo name into name and provider.
func parseModuleRepoName(repoName string) (name string, provider string, err error) {
	parts := strings.SplitN(repoName, "-", 3)
	if len(parts) != 3 || parts[0] != "terraform" {
		return "", "", fmt.Errorf("expected format terraform-<PROVIDER>-<NAME>, got %q", repoName)
	}
	return parts[2], parts[1], nil
}

// readPublicRegistryModule looks up a module on registry.terraform.io by namespace, name, and provider.
// Returns nil, nil if the module is not found.
func readPublicRegistryModule(ctx context.Context, namespace, name, provider string) (*registryModuleEntry, error) {
	pageNum := 1
	for {
		listURL := fmt.Sprintf(
			"https://registry.terraform.io/v3/modules?filter[namespace]=%s&page[number]=%d&page[size]=50",
			url.QueryEscape(namespace), pageNum,
		)

		httpReq, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		httpReq.Header.Set("Accept", "application/json")

		httpResp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("fetching modules from public registry: %w", err)
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != 200 {
			return nil, fmt.Errorf("public registry returned status %d", httpResp.StatusCode)
		}

		respBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading registry response: %w", err)
		}

		// The registry.terraform.io/v3/modules API returns a plain JSON array
		var entries []registryModuleEntry
		if err := json.Unmarshal(respBody, &entries); err != nil {
			// Fall back to trying a wrapped format {"data": [...]}
			var wrapped struct {
				Data []registryModuleEntry `json:"data"`
			}
			if err2 := json.Unmarshal(respBody, &wrapped); err2 != nil {
				return nil, fmt.Errorf("decoding registry response: %w (also tried wrapped: %w)", err, err2)
			}
			entries = wrapped.Data
		}

		for i := range entries {
			entry := &entries[i]
			if entry.Name == name && entry.System == provider {
				return entry, nil
			}
		}

		// If we got fewer results than page size, no more pages
		if len(entries) < 50 {
			break
		}
		pageNum++
	}

	return nil, nil
}

// extractNamespaceFromResponse tries to extract the namespace from the create API response.
// Falls back to the organization name if parsing fails.
func extractNamespaceFromResponse(respBody []byte, fallback string) string {
	// Try JSONAPI-style format: {"data": {"attributes": {"namespace": "..."}}}
	var resp struct {
		Data struct {
			Attributes struct {
				Namespace string `json:"namespace"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err == nil && resp.Data.Attributes.Namespace != "" {
		return resp.Data.Attributes.Namespace
	}

	return fallback
}
