// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfgen

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// safeName converts arbitrary name parts into a Terraform-safe identifier.
func safeName(parts ...string) string {
	joined := strings.Join(parts, "_")
	joined = strings.ReplaceAll(joined, "-", "_")
	joined = unsafeName.ReplaceAllString(joined, "_")
	joined = strings.Trim(joined, "_")
	if joined == "" {
		return "default"
	}
	return strings.ToLower(joined)
}

// quote returns an HCL-compatible quoted string literal.
func quote(s string) string {
	return strconv.Quote(s)
}

// sortedKeys returns map keys in lexical order for deterministic generation.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

type hclDocument struct {
	blocks []hclBlock
}

type hclBlock struct {
	Type   string
	Labels []string
	Body   hclBody
}

type hclBody struct {
	items []hclItem
}

type hclItem struct {
	attr  *hclAttribute
	block *hclBlock
}

type hclAttribute struct {
	Name  string
	Value hclValue
}

type hclValue interface {
	renderHCL(indent int) string
}

type hclExpression string

type hclString string

type hclHeredoc string

type hclNumber int

type hclBool bool

type hclList []hclValue

type hclObject []hclObjectField

type hclInlineObject []hclObjectField

type hclObjectField struct {
	Key      string
	BareName bool
	Value    hclValue
}

// AddBlock appends a top-level block to the document.
func (d *hclDocument) AddBlock(block hclBlock) {
	d.blocks = append(d.blocks, block)
}

// String renders the document as HCL with stable block ordering.
func (d hclDocument) String() string {
	var b strings.Builder
	for i, block := range d.blocks {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(block.renderHCL(0))
	}
	return b.String()
}

// Attr appends an attribute assignment to the body.
func (b *hclBody) Attr(name string, value hclValue) {
	b.items = append(b.items, hclItem{attr: &hclAttribute{Name: name, Value: value}})
}

// Block appends a nested block to the body.
func (b *hclBody) Block(block hclBlock) {
	b.items = append(b.items, hclItem{block: &block})
}

// block creates an HCL block with optional labels.
func block(blockType string, labels ...string) hclBlock {
	return hclBlock{Type: blockType, Labels: labels}
}

// expr creates a raw HCL expression value.
func expr(value string) hclExpression {
	return hclExpression(value)
}

// str creates a quoted HCL string value.
func str(value string) hclString {
	return hclString(value)
}

// heredoc creates an indented HCL heredoc value.
func heredoc(value string) hclHeredoc {
	return hclHeredoc(value)
}

// num creates a numeric HCL value.
func num(value int) hclNumber {
	return hclNumber(value)
}

// boolean creates a boolean HCL value.
func boolean(value bool) hclBool {
	return hclBool(value)
}

// list creates an HCL list value.
func list(values ...hclValue) hclList {
	return hclList(values)
}

// stringValueList creates an HCL list of quoted strings.
func stringValueList(values []string) hclList {
	out := make(hclList, 0, len(values))
	for _, value := range values {
		out = append(out, str(value))
	}
	return out
}

// object creates an HCL object value with ordered fields.
func object(fields ...hclObjectField) hclObject {
	return hclObject(fields)
}

// inlineObject creates a compact single-line HCL object value.
func inlineObject(fields ...hclObjectField) hclInlineObject {
	return hclInlineObject(fields)
}

// field creates one ordered HCL object field.
func field(key string, value hclValue) hclObjectField {
	return hclObjectField{Key: key, Value: value}
}

// identField creates one ordered HCL object field with an identifier key.
func identField(key string, value hclValue) hclObjectField {
	return hclObjectField{Key: key, BareName: true, Value: value}
}

// renderHCL renders a block and its body at the requested indentation level.
func (b hclBlock) renderHCL(indent int) string {
	var out strings.Builder
	pad := strings.Repeat(" ", indent)
	out.WriteString(pad)
	out.WriteString(b.Type)
	for _, label := range b.Labels {
		out.WriteString(" ")
		out.WriteString(quote(label))
	}
	out.WriteString(" {\n")
	out.WriteString(b.Body.renderHCL(indent + 2))
	out.WriteString(pad)
	out.WriteString("}\n")
	return out.String()
}

// renderHCL renders body items at the requested indentation level.
func (b hclBody) renderHCL(indent int) string {
	var out strings.Builder
	for i, item := range b.items {
		if i > 0 && item.block != nil {
			out.WriteString("\n")
		}
		if item.attr != nil {
			out.WriteString(item.attr.renderHCL(indent))
		}
		if item.block != nil {
			out.WriteString(item.block.renderHCL(indent))
		}
	}
	return out.String()
}

// renderHCL renders an attribute assignment at the requested indentation level.
func (a hclAttribute) renderHCL(indent int) string {
	return fmt.Sprintf("%s%s = %s\n", strings.Repeat(" ", indent), a.Name, a.Value.renderHCL(indent))
}

// renderHCL renders a raw HCL expression.
func (e hclExpression) renderHCL(indent int) string {
	value := string(e)
	if !strings.Contains(value, "\n") {
		return value
	}
	return strings.ReplaceAll(value, "\n", "\n"+strings.Repeat(" ", indent))
}

// renderHCL renders a quoted string.
func (s hclString) renderHCL(int) string {
	return quote(string(s))
}

// renderHCL renders an indented heredoc with stable whitespace.
func (h hclHeredoc) renderHCL(indent int) string {
	const delimiter = "USERDATA"
	pad := strings.Repeat(" ", indent)
	content := strings.TrimSuffix(string(h), "\n")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = pad + line
		}
	}
	return "<<-" + delimiter + "\n" + strings.Join(lines, "\n") + "\n" + pad + delimiter
}

// renderHCL renders a number.
func (n hclNumber) renderHCL(int) string {
	return strconv.Itoa(int(n))
}

// renderHCL renders a boolean.
func (b hclBool) renderHCL(int) string {
	if b {
		return "true"
	}
	return "false"
}

// renderHCL renders a list, using single-line output for scalar lists.
func (l hclList) renderHCL(indent int) string {
	if len(l) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(l))
	for _, value := range l {
		parts = append(parts, value.renderHCL(indent))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// renderHCL renders an object with stable field ordering supplied by the
// caller.
func (o hclObject) renderHCL(indent int) string {
	if len(o) == 0 {
		return "{}"
	}
	var out strings.Builder
	out.WriteString("{\n")
	fieldIndent := indent + 2
	for _, field := range o {
		out.WriteString(strings.Repeat(" ", fieldIndent))
		if field.BareName {
			out.WriteString(field.Key)
		} else {
			out.WriteString(quote(field.Key))
		}
		out.WriteString(" = ")
		out.WriteString(field.Value.renderHCL(fieldIndent))
		out.WriteString("\n")
	}
	out.WriteString(strings.Repeat(" ", indent))
	out.WriteString("}")
	return out.String()
}

// renderHCL renders an object on one line for compact nested values.
func (o hclInlineObject) renderHCL(indent int) string {
	if len(o) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(o))
	for _, field := range o {
		key := quote(field.Key)
		if field.BareName {
			key = field.Key
		}
		parts = append(parts, key+" = "+field.Value.renderHCL(indent))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}
