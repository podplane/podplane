// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"encoding/json"
	"os"
	"testing"
)

// TestWithValuesFileIncludesRouteWhenSet checks generated image, environment, and route values.
func TestWithValuesFileIncludesRouteWhenSet(t *testing.T) {
	t.Parallel()
	err := withValuesFile("example.com/app:latest", map[string]string{"FOO": "bar"}, "hello.example.com", "/api", 8443, func(valuesPath string) error {
		raw, err := os.ReadFile(valuesPath)
		if err != nil {
			return err
		}
		var values map[string]any
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		images, ok := values["images"].(map[string]any)
		if !ok {
			t.Fatalf("images = %T, want object", values["images"])
		}
		if got := images["app"]; got != "example.com/app:latest" {
			t.Fatalf("images.app = %v, want example.com/app:latest", got)
		}
		app, ok := values["app"].(map[string]any)
		if !ok {
			t.Fatalf("app = %T, want object", values["app"])
		}
		env, ok := app["env"].(map[string]any)
		if !ok {
			t.Fatalf("app.env = %T, want object", app["env"])
		}
		if got := env["FOO"]; got != "bar" {
			t.Fatalf("app.env.FOO = %v, want bar", got)
		}
		route, ok := values["route"].(map[string]any)
		if !ok {
			t.Fatalf("route = %T, want object", values["route"])
		}
		if got := route["hostname"]; got != "hello.example.com" {
			t.Fatalf("route.hostname = %v, want hello.example.com", got)
		}
		if got := route["path"]; got != "/api" {
			t.Fatalf("route.path = %v, want /api", got)
		}
		if got := route["port"]; got != float64(8443) {
			t.Fatalf("route.port = %v, want 8443", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestWithValuesFileOmitsRouteWhenUnset checks omission of unspecified route values.
func TestWithValuesFileOmitsRouteWhenUnset(t *testing.T) {
	t.Parallel()
	err := withValuesFile("example.com/app:latest", nil, "", "", 0, func(valuesPath string) error {
		raw, err := os.ReadFile(valuesPath)
		if err != nil {
			return err
		}
		var values map[string]any
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		if _, ok := values["route"]; ok {
			t.Fatalf("route should be omitted when hostname and path are unset: %#v", values)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestWithValuesFileOmitsImageWhenUnset checks omission of images when no image override is supplied.
func TestWithValuesFileOmitsImageWhenUnset(t *testing.T) {
	t.Parallel()
	err := withValuesFile("", nil, "", "", 0, func(valuesPath string) error {
		raw, err := os.ReadFile(valuesPath)
		if err != nil {
			return err
		}
		var values map[string]any
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		if _, ok := values["images"]; ok {
			t.Fatalf("images should be omitted when --image is unset: %#v", values)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
