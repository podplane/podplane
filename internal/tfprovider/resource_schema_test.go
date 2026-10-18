// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// TestProviderProtocolSchema verifies Terraform can actually load every resource.
func TestProviderProtocolSchema(t *testing.T) {
	server := providerserver.NewProtocol6(NewProvider())()
	resp, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("provider schema rejected: %s: %s", d.Summary, d.Detail)
		}
	}
	if _, ok := resp.ResourceSchemas["podplane_nstance_registration_key"]; !ok {
		t.Fatal("missing registration signer resource")
	}
}

// TestNetsySeedS3Schema verifies the S3 resource exposes prefix, not key.
func TestNetsySeedS3Schema(t *testing.T) {
	schema := resourceSchema(t, &netsySeedS3Resource{})
	assertStringAttribute(t, schema, "cluster_config_path", true, false)
	assertStringAttribute(t, schema, "seed_path", false, true)
	assertStringAttribute(t, schema, "values_file", false, true)
	assertStringAttribute(t, schema, "values_content", false, true)
	assertStringAttribute(t, schema, "bucket", true, false)
	assertStringAttribute(t, schema, "prefix", false, true)
	assertStringAttribute(t, schema, "region", false, true)
	assertStringAttribute(t, schema, "profile", false, true)
	for _, name := range []string{"values", "values_path", "generated_values", "route53_role_arn", "route53_hosted_zones"} {
		if _, ok := schema.Attributes[name]; ok {
			t.Fatalf("schema unexpectedly contains %q", name)
		}
	}
	if _, ok := schema.Attributes["key"]; ok {
		t.Fatalf("schema unexpectedly contains legacy key attribute")
	}
}

// TestNetsySeedGCSSchema verifies the GCS resource exposes prefix, not key.
func TestNetsySeedGCSSchema(t *testing.T) {
	schema := resourceSchema(t, &netsySeedGCSResource{})
	assertStringAttribute(t, schema, "cluster_config_path", true, false)
	assertStringAttribute(t, schema, "seed_path", false, true)
	assertStringAttribute(t, schema, "values_file", false, true)
	assertStringAttribute(t, schema, "values_content", false, true)
	assertStringAttribute(t, schema, "bucket", true, false)
	assertStringAttribute(t, schema, "prefix", false, true)
	assertStringAttribute(t, schema, "project", false, true)
	for _, name := range []string{"values", "values_path", "generated_values", "route53_role_arn", "route53_hosted_zones"} {
		if _, ok := schema.Attributes[name]; ok {
			t.Fatalf("schema unexpectedly contains %q", name)
		}
	}
	if _, ok := schema.Attributes["key"]; ok {
		t.Fatalf("schema unexpectedly contains legacy key attribute")
	}
}

// schemaResource is the subset of a resource needed by schema tests.
type schemaResource interface {
	Schema(context.Context, resource.SchemaRequest, *resource.SchemaResponse)
}

// resourceSchema returns a resource schema or fails the test if diagnostics are set.
func resourceSchema(t *testing.T, r schemaResource) resourceschema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// assertStringAttribute verifies a string attribute's required/optional flags.
func assertStringAttribute(t *testing.T, schema resourceschema.Schema, name string, required, optional bool) {
	t.Helper()
	attr, ok := schema.Attributes[name]
	if !ok {
		t.Fatalf("schema missing %q attribute", name)
	}
	stringAttr, ok := attr.(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("schema attribute %q has type %T, want StringAttribute", name, attr)
	}
	if stringAttr.Required != required || stringAttr.Optional != optional {
		t.Fatalf("schema attribute %q required/optional = %v/%v, want %v/%v", name, stringAttr.Required, stringAttr.Optional, required, optional)
	}
}
