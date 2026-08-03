// Package asyncapi merges the base document and per-aggregate AsyncAPI fragments
// (internal/module/<module>/api/asyncapi/<aggregate>.yaml) into a single
// specification describing the WebSocket surface.
//
// A fragment contributes only components.messages and components.schemas, into one
// flat namespace across every module. There is exactly one channel and it lives in
// the base document: this package wires a $ref to every merged message into it, so a
// module never has to name the channel.
package asyncapi

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"shopnexus/internal/shared/openapi"
	"shopnexus/internal/shared/specmerge"
)

// channelName is the single channel every event travels on. One socket per account
// carries every module's facts, so there is nothing to parameterise.
const channelName = "userStream"

const schemaRefPrefix = "#/components/schemas/"

// MergeDoc returns the merged AsyncAPI document as a tree.
func MergeDoc(root string) (specmerge.Doc, error) {
	base, err := specmerge.Read(filepath.Join(root, "api", "asyncapi.base.yaml"))
	if err != nil {
		return nil, err
	}
	components := specmerge.Child(base, "components")
	messages := specmerge.Child(components, "messages")
	schemas := specmerge.Child(components, "schemas")

	frags, err := filepath.Glob(filepath.Join(root, "internal", "module", "*", "api", "asyncapi", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("glob asyncapi fragments: %w", err)
	}
	sort.Strings(frags)
	for _, f := range frags {
		frag, err := specmerge.Read(f)
		if err != nil {
			return nil, err
		}
		fragComponents := specmerge.Child(frag, "components")
		if err := specmerge.MergeInto(messages, specmerge.Child(fragComponents, "messages"), f, "message"); err != nil {
			return nil, err
		}
		if err := specmerge.MergeInto(schemas, specmerge.Child(fragComponents, "schemas"), f, "schema"); err != nil {
			return nil, err
		}
	}

	wireChannel(base, messages)

	if err := copySchemaClosure(root, base, schemas); err != nil {
		return nil, err
	}
	return base, nil
}

// Merge returns the merged document as deterministic YAML bytes.
func Merge(root string) ([]byte, error) {
	d, err := MergeDoc(root)
	if err != nil {
		return nil, err
	}
	return specmerge.RenderYAML(d)
}

// MessageCodes lists every message's name, sorted. It is what the contract test
// compares the Go event declarations against.
func MessageCodes(d specmerge.Doc) []string {
	messages := specmerge.Child(specmerge.Child(d, "components"), "messages")
	codes := make([]string, 0, len(messages))
	for _, v := range messages {
		msg, ok := v.(specmerge.Doc)
		if !ok {
			continue
		}
		if name, ok := msg["name"].(string); ok {
			codes = append(codes, name)
		}
	}
	sort.Strings(codes)
	return codes
}

// wireChannel points the single channel at every merged message.
func wireChannel(base, messages specmerge.Doc) {
	channel := specmerge.Child(specmerge.Child(base, "channels"), channelName)
	into := specmerge.Child(channel, "messages")
	for name := range messages {
		into[name] = specmerge.Doc{"$ref": "#/components/messages/" + name}
	}
}

// copySchemaClosure pulls every OpenAPI schema the document refers to, and
// everything those refer to in turn, into the AsyncAPI document.
//
// Two things fall out. The generated file is self-contained, so any tooling can read
// it. And an event can only carry a shape the REST API already publishes: a ref to
// something OpenAPI does not define fails here rather than shipping a second,
// divergent definition of the same entity.
func copySchemaClosure(root string, base, schemas specmerge.Doc) error {
	source, err := openapi.MergeDoc(root)
	if err != nil {
		return fmt.Errorf("merge openapi for schema closure: %w", err)
	}
	available := specmerge.Child(specmerge.Child(source, "components"), "schemas")

	// A schema already defined by a fragment wins: the closure only fills gaps.
	pending := refsIn(base)
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if _, have := schemas[name]; have {
			continue
		}
		def, ok := available[name]
		if !ok {
			return fmt.Errorf("asyncapi: schema %q is not published by openapi", name)
		}
		schemas[name] = def
		pending = append(pending, refsIn(def)...)
	}
	return nil
}

// refsIn walks any decoded YAML value and collects the schema names it $refs.
func refsIn(v any) []string {
	var out []string
	switch t := v.(type) {
	case specmerge.Doc:
		for k, child := range t {
			if k == "$ref" {
				if s, ok := child.(string); ok {
					if name, found := strings.CutPrefix(s, schemaRefPrefix); found {
						out = append(out, name)
					}
				}
				continue
			}
			out = append(out, refsIn(child)...)
		}
	case []any:
		for _, child := range t {
			out = append(out, refsIn(child)...)
		}
	}
	return out
}
