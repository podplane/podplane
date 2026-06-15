// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"encoding/json"
	"os"
	"testing"
)

func TestWithValuesFileIncludesRouteWhenSet(t *testing.T) {
	t.Parallel()
	err := withValuesFile("example.com/app:latest", nil, "hello.example.com", "/api", 8443, func(valuesPath string) error {
		raw, err := os.ReadFile(valuesPath)
		if err != nil {
			return err
		}
		var values map[string]any
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
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
