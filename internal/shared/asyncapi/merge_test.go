package asyncapi_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"shopnexus/internal/shared/asyncapi"
	"shopnexus/internal/shared/specmerge"
)

// writeTree lays out a minimal module root: go.mod, an OpenAPI base carrying the
// schema an event refers to, an AsyncAPI base, and one fragment per module.
func writeTree(t *testing.T, fragments map[string]string) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write("go.mod", "module shopnexus\n\ngo 1.26\n")
	write("api/openapi.base.yaml", `
openapi: 3.1.0
paths: {}
components:
  schemas:
    Message:
      type: object
      properties:
        id: { type: string }
        author: { $ref: '#/components/schemas/Account' }
    Account:
      type: object
      properties:
        id: { type: string }
    Unrelated:
      type: object
`)
	write("api/asyncapi.base.yaml", `
asyncapi: 3.0.0
info:
  title: Test
  version: 1.0.0
channels:
  userStream:
    address: /api/v1/ws
    messages: {}
operations:
  receiveUserEvents:
    action: receive
    channel:
      $ref: '#/channels/userStream'
`)
	for rel, body := range fragments {
		write(rel, body)
	}
	return root
}

const chatFragment = `
components:
  messages:
    MessageCreated:
      name: chat.message_created
      contentType: application/json
      payload:
        type: object
        required: [code, at, data]
        properties:
          code: { type: string, const: chat.message_created }
          at:   { type: string, format: date-time }
          data: { $ref: '#/components/schemas/Message' }
`

func TestMergeWiresMessagesIntoTheChannel(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/module/chat/api/asyncapi/message.yaml": chatFragment,
	})

	doc, err := asyncapi.MergeDoc(root)
	if err != nil {
		t.Fatalf("MergeDoc: %v", err)
	}

	channel := specmerge.Child(specmerge.Child(doc, "channels"), "userStream")
	msgs := specmerge.Child(channel, "messages")
	ref, ok := msgs["MessageCreated"].(specmerge.Doc)
	if !ok {
		t.Fatalf("channel messages = %#v, want a MessageCreated $ref", msgs)
	}
	if got := ref["$ref"]; got != "#/components/messages/MessageCreated" {
		t.Errorf("$ref = %v", got)
	}
}

// An event payload may only point at a schema OpenAPI already publishes, and the
// generated document has to be self-contained, so the closure is copied in.
func TestMergeCopiesReferencedSchemaClosure(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/module/chat/api/asyncapi/message.yaml": chatFragment,
	})

	doc, err := asyncapi.MergeDoc(root)
	if err != nil {
		t.Fatalf("MergeDoc: %v", err)
	}

	schemas := specmerge.Child(specmerge.Child(doc, "components"), "schemas")
	if _, ok := schemas["Message"]; !ok {
		t.Error("Message was not copied from the OpenAPI document")
	}
	// Account is reached only through Message.author — the copy must be transitive.
	if _, ok := schemas["Account"]; !ok {
		t.Error("Account was not copied; the closure is not transitive")
	}
	// Unrelated is referenced by nothing, so copying it would bloat every consumer.
	if _, ok := schemas["Unrelated"]; ok {
		t.Error("Unrelated was copied; only the referenced closure belongs here")
	}
}

func TestMergeRejectsAnUnknownSchemaRef(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/module/chat/api/asyncapi/message.yaml": strings.ReplaceAll(
			chatFragment, "#/components/schemas/Message", "#/components/schemas/Nonexistent"),
	})

	_, err := asyncapi.MergeDoc(root)
	if err == nil {
		t.Fatal("MergeDoc succeeded; a ref to a schema OpenAPI does not publish must fail")
	}
	if !strings.Contains(err.Error(), "Nonexistent") {
		t.Errorf("err = %v, want it to name the missing schema", err)
	}
}

func TestMergeRejectsDuplicateMessageAcrossModules(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/module/chat/api/asyncapi/message.yaml": chatFragment,
		"internal/module/order/api/asyncapi/offer.yaml":  chatFragment,
	})

	_, err := asyncapi.MergeDoc(root)
	if err == nil {
		t.Fatal("MergeDoc succeeded; one flat namespace means a duplicate must fail")
	}
	if !strings.Contains(err.Error(), "MessageCreated") {
		t.Errorf("err = %v, want it to name the duplicate", err)
	}
}

func TestMessageCodes(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/module/chat/api/asyncapi/message.yaml": chatFragment,
	})

	doc, err := asyncapi.MergeDoc(root)
	if err != nil {
		t.Fatalf("MergeDoc: %v", err)
	}
	got := asyncapi.MessageCodes(doc)
	if !slices.Equal(got, []string{"chat.message_created"}) {
		t.Errorf("MessageCodes = %v, want [chat.message_created]", got)
	}
}
