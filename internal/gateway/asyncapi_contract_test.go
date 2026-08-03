package gateway_test

import (
	"os"
	"slices"
	"testing"

	"shopnexus/api"
	"shopnexus/internal/shared/asyncapi"
	"shopnexus/internal/shared/specmerge"
)

// wantCodes is every event the backend may publish. Adding an event means adding it
// here and in a fragment; the two lists disagreeing is the drift this test exists to
// catch. Keep it sorted.
var wantCodes = []string{
	"chat.conversation_read",
	"chat.message_created",
	"chat.message_deleted",
	"chat.message_updated",
}

func TestAsyncAPIMessageCodes(t *testing.T) {
	doc := mergedAsyncAPI(t)

	got := asyncapi.MessageCodes(doc)
	if !slices.Equal(got, wantCodes) {
		t.Errorf("message codes drifted\n got: %v\nwant: %v", got, wantCodes)
	}
}

// Every message must be reachable from the channel, or a client generated from the
// document never learns about it.
func TestAsyncAPIChannelReferencesEveryMessage(t *testing.T) {
	doc := mergedAsyncAPI(t)

	messages := specmerge.Child(specmerge.Child(doc, "components"), "messages")
	channel := specmerge.Child(specmerge.Child(doc, "channels"), "userStream")
	wired := specmerge.Child(channel, "messages")

	if len(wired) != len(messages) {
		t.Fatalf("channel wires %d messages, components define %d", len(wired), len(messages))
	}
	for name := range messages {
		ref, ok := wired[name].(specmerge.Doc)
		if !ok {
			t.Errorf("message %q is not wired into the channel", name)
			continue
		}
		if got := ref["$ref"]; got != "#/components/messages/"+name {
			t.Errorf("message %q wired as %v", name, got)
		}
	}
}

// The socket lives under the same base path as every route, and a client builds its
// URL from the document.
func TestAsyncAPIServerPathMatchesBasePath(t *testing.T) {
	doc := mergedAsyncAPI(t)

	servers := specmerge.Child(doc, "servers")
	production, ok := servers["production"].(specmerge.Doc)
	if !ok {
		t.Fatal("servers.production is missing")
	}
	want := api.BasePath + "/ws"
	if got := production["pathname"]; got != want {
		t.Errorf("pathname = %v, want %v", got, want)
	}
}

// Every payload is the same envelope, so a client can switch on one field.
func TestAsyncAPIPayloadsShareTheEnvelope(t *testing.T) {
	doc := mergedAsyncAPI(t)
	messages := specmerge.Child(specmerge.Child(doc, "components"), "messages")

	for name, v := range messages {
		msg, ok := v.(specmerge.Doc)
		if !ok {
			t.Errorf("message %q is not a mapping", name)
			continue
		}
		payload := specmerge.Child(msg, "payload")
		props := specmerge.Child(payload, "properties")
		for _, field := range []string{"code", "at", "data"} {
			if _, ok := props[field]; !ok {
				t.Errorf("message %q payload has no %q", name, field)
			}
		}
		code := specmerge.Child(props, "code")
		if code["const"] != msg["name"] {
			t.Errorf("message %q: payload code const = %v, message name = %v", name, code["const"], msg["name"])
		}
	}
}

func mergedAsyncAPI(t *testing.T) specmerge.Doc {
	t.Helper()
	// specmerge.FindRoot needs an absolute starting point: filepath.Dir(".") answers
	// "." forever, so a relative "." can never walk up past the test's own directory.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	root, err := specmerge.FindRoot(cwd)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	doc, err := asyncapi.MergeDoc(root)
	if err != nil {
		t.Fatalf("MergeDoc: %v", err)
	}
	return doc
}
