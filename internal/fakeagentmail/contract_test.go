package fakeagentmail_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/langri-sha/terraform-provider-agentmail/internal/fakeagentmail"
)

var undeclaredStatuses = map[string]string{
	"* 401":                              "agentmail-docs fern/pages/resources/errors.mdx lists 401 missing_authorization, unknown_api_key and api_key_expired; no route declares 401",
	"POST /v0/pods/{pod_id}/inboxes 404": "the same errors.mdx answers a missing resource with not_found, and the other pod-scoped inbox routes declare 404 for a missing pod",
	"GET /v0/pods/{pod_id}/lists/{direction}/{type} 404":       "the entry routes under the same prefix declare 404 for a missing pod",
	"POST /v0/pods/{pod_id}/lists/{direction}/{type} 404":      "the entry routes under the same prefix declare 404 for a missing pod",
	"GET /v0/inboxes/{inbox_id}/lists/{direction}/{type} 404":  "the entry routes under the same prefix declare 404 for a missing inbox",
	"POST /v0/inboxes/{inbox_id}/lists/{direction}/{type} 404": "the entry routes under the same prefix declare 404 for a missing inbox",
	"POST /v0/api-keys 403":                                    "CreateBearerApiKeyRequest: \"an expires_at later than the authenticating key's own returns 403\"; permissions.mdx answers a missing permission or an escalation with 403",
	"DELETE /v0/lists/{direction}/{type}/{entry} 403":          "ListEntryBase.read_only: the entry \"cannot be deleted via the API\"; the status is not documented",
}

type openAPI struct {
	doc map[string]any
}

func loadOpenAPI(t *testing.T) *openAPI {
	t.Helper()
	raw, err := os.ReadFile("testdata/openapi.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return &openAPI{doc: doc}
}

func (o *openAPI) operation(method, route string) (map[string]any, bool) {
	paths, _ := o.doc["paths"].(map[string]any)
	item, _ := paths[route].(map[string]any)
	op, ok := item[strings.ToLower(method)].(map[string]any)
	return op, ok
}

func (o *openAPI) schema(name string) map[string]any {
	schemas := o.doc["components"].(map[string]any)["schemas"].(map[string]any)
	s, _ := schemas[name].(map[string]any)
	return s
}

func jsonSchema(content any) (map[string]any, bool) {
	c, _ := content.(map[string]any)
	media, _ := c["application/json"].(map[string]any)
	s, ok := media["schema"].(map[string]any)
	return s, ok
}

func (o *openAPI) resolve(s map[string]any) map[string]any {
	for {
		ref, ok := s["$ref"].(string)
		if !ok {
			return s
		}
		s = o.schema(strings.TrimPrefix(ref, "#/components/schemas/"))
	}
}

func (o *openAPI) check(s map[string]any, v any, at string) []string {
	if v == nil {
		if nullable, _ := s["nullable"].(bool); nullable {
			return nil
		}
		if nullable, _ := o.resolve(s)["nullable"].(bool); nullable {
			return nil
		}
		return []string{at + ": null where the document does not allow it"}
	}
	s = o.resolve(s)
	if branches, ok := s["oneOf"].([]any); ok {
		var all []string
		for i, b := range branches {
			problems := o.check(b.(map[string]any), v, at)
			if len(problems) == 0 {
				return nil
			}
			all = append(all, fmt.Sprintf("oneOf[%d]: %s", i, strings.Join(problems, "; ")))
		}
		return []string{at + ": matches no oneOf branch: " + strings.Join(all, " | ")}
	}
	properties, required, additional, isObject := o.objectShape(s)
	if isObject {
		return o.checkObject(properties, required, additional, v, at)
	}
	switch s["type"] {
	case "array":
		items, ok := v.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: %T, want an array", at, v)}
		}
		var problems []string
		for i, item := range items {
			problems = append(problems, o.check(s["items"].(map[string]any), item, fmt.Sprintf("%s[%d]", at, i))...)
		}
		return problems
	case "string":
		str, ok := v.(string)
		if !ok {
			return []string{fmt.Sprintf("%s: %T, want a string", at, v)}
		}
		if enum, ok := s["enum"].([]any); ok && !contains(enum, str) {
			return []string{fmt.Sprintf("%s: %q is not one of %v", at, str, enum)}
		}
		if c, ok := s["const"].(string); ok && str != c {
			return []string{fmt.Sprintf("%s: %q, want %q", at, str, c)}
		}
		if s["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, str); err != nil {
				return []string{fmt.Sprintf("%s: %q is not a date-time", at, str)}
			}
		}
	case "number", "integer":
		n, ok := v.(json.Number)
		if !ok {
			return []string{fmt.Sprintf("%s: %T, want a number", at, v)}
		}
		if s["type"] == "integer" {
			if _, err := strconv.ParseInt(n.String(), 10, 64); err != nil {
				return []string{fmt.Sprintf("%s: %s is not an integer", at, n)}
			}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return []string{fmt.Sprintf("%s: %T, want a boolean", at, v)}
		}
	}
	return nil
}

func (o *openAPI) objectShape(s map[string]any) (properties map[string]any, required []string, additional map[string]any, isObject bool) {
	properties = map[string]any{}
	parts := []map[string]any{s}
	for _, p := range asSlice(s["allOf"]) {
		parts = append(parts, o.resolve(p.(map[string]any)))
	}
	for _, p := range parts {
		if p["type"] == "object" || p["properties"] != nil {
			isObject = true
		}
		for k, v := range asMap(p["properties"]) {
			properties[k] = v
		}
		for _, r := range asSlice(p["required"]) {
			required = append(required, r.(string))
		}
		if a, ok := p["additionalProperties"].(map[string]any); ok {
			additional = a
		}
	}
	return properties, required, additional, isObject
}

func (o *openAPI) checkObject(properties map[string]any, required []string, additional map[string]any, v any, at string) []string {
	obj, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s: %T, want an object", at, v)}
	}
	var problems []string
	for _, r := range required {
		if _, ok := obj[r]; !ok {
			problems = append(problems, fmt.Sprintf("%s: required %q is missing", at, r))
		}
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch p, ok := properties[k].(map[string]any); {
		case ok:
			problems = append(problems, o.check(p, obj[k], at+"."+k)...)
		case additional != nil:
			problems = append(problems, o.check(additional, obj[k], at+"."+k)...)
		default:
			problems = append(problems, fmt.Sprintf("%s: %q is not in the document", at, k))
		}
	}
	return problems
}

func (o *openAPI) checkExchange(e fakeagentmail.Exchange) []string {
	name := fmt.Sprintf("%s %s", e.Method, e.Route)
	op, ok := o.operation(e.Method, e.Route)
	if !ok {
		return []string{fmt.Sprintf("%s (answered %d) is not in the document", name, e.Status)}
	}
	var problems []string
	if len(bytes.TrimSpace(e.Request)) > 0 {
		body, _ := op["requestBody"].(map[string]any)
		s, ok := jsonSchema(body["content"])
		if !ok {
			problems = append(problems, name+": sent a body the document does not define")
		} else {
			problems = append(problems, o.checkJSON(s, e.Request, name+" request")...)
		}
	}
	status := strconv.Itoa(e.Status)
	response, declared := asMap(op["responses"])[status].(map[string]any)
	if !declared {
		_, wildcard := undeclaredStatuses["* "+status]
		_, specific := undeclaredStatuses[name+" "+status]
		if !wildcard && !specific {
			return append(problems, fmt.Sprintf("%s answered %s, which the document does not declare", name, status))
		}
		return problems
	}
	s, hasBody := jsonSchema(response["content"])
	switch {
	case hasBody:
		problems = append(problems, o.checkJSON(s, e.Response, name+" "+status)...)
	case len(bytes.TrimSpace(e.Response)) > 0:
		problems = append(problems, fmt.Sprintf("%s %s: answered a body the document does not define", name, status))
	}
	return problems
}

func (o *openAPI) checkJSON(s map[string]any, raw []byte, at string) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []string{fmt.Sprintf("%s: not JSON: %v", at, err)}
	}
	return o.check(s, v, at)
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func contains(enum []any, s string) bool {
	for _, e := range enum {
		if e == s {
			return true
		}
	}
	return false
}

func TestEveryExchangeMatchesTheOpenAPIDocument(t *testing.T) {
	spec := loadOpenAPI(t)
	s := fakeagentmail.New(t)

	for _, exercise := range []func(*testing.T, *fakeagentmail.Server){
		exerciseInboxes,
		exerciseLists,
		exerciseAPIKeys,
	} {
		exercise(t, s)
	}

	exchanges := s.Exchanges()
	if len(exchanges) == 0 {
		t.Fatal("the exercises made no requests")
	}
	for _, e := range exchanges {
		for _, p := range spec.checkExchange(e) {
			t.Error(p)
		}
	}
}

func TestTheCheckerRejectsWhatTheDocumentDoesNotAllow(t *testing.T) {
	spec := loadOpenAPI(t)
	cases := map[string]fakeagentmail.Exchange{
		"an unknown route": {Method: "GET", Route: "/v0/nowhere", Status: 200},
		"an undeclared status": {
			Method: "GET", Route: "/v0/inboxes/{inbox_id}", Status: 418,
		},
		"an invented response field": {
			Method: "GET", Route: "/v0/inboxes/{inbox_id}", Status: 200,
			Response: []byte(`{"inbox_id":"a@agentmail.to","pod_id":"p","email":"a@agentmail.to","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","invented":true}`),
		},
		"a missing required response field": {
			Method: "GET", Route: "/v0/inboxes/{inbox_id}", Status: 200,
			Response: []byte(`{"inbox_id":"a@agentmail.to","email":"a@agentmail.to","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`),
		},
		"an invented request field": {
			Method: "PATCH", Route: "/v0/inboxes/{inbox_id}", Status: 404,
			Request:  []byte(`{"username":"renamed"}`),
			Response: []byte(`{"name":"NotFoundError","message":"Inbox not found"}`),
		},
		"a value outside an enum": {
			Method: "PATCH", Route: "/v0/inboxes/{inbox_id}", Status: 404,
			Request:  []byte(`{"status":"sleeping"}`),
			Response: []byte(`{"name":"NotFoundError","message":"Inbox not found"}`),
		},
		"a metadata value of the wrong type": {
			Method: "POST", Route: "/v0/inboxes", Status: 400,
			Request:  []byte(`{"metadata":{"nested":{"a":1}}}`),
			Response: []byte(`{"name":"ValidationError","errors":[]}`),
		},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			if problems := spec.checkExchange(e); len(problems) == 0 {
				t.Error("the checker found no problem")
			}
		})
	}
}
