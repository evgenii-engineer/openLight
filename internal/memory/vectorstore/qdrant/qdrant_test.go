package qdrant

import (
	"strings"
	"testing"
)

func TestParseEndpointAcceptsTheFormsPeopleActuallyWrite(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort int
	}{
		{"http://127.0.0.1:6334", "127.0.0.1", 6334},
		{"127.0.0.1:6334", "127.0.0.1", 6334},
		{"qdrant.local", "qdrant.local", 6334},
		{"https://qdrant.internal:16334", "qdrant.internal", 16334},
		// A bare host defaults to the gRPC port, not the REST one — the
		// client we use speaks gRPC.
		{"http://brain-node", "brain-node", 6334},
	}
	for _, tc := range cases {
		host, port, err := parseEndpoint(tc.in)
		if err != nil {
			t.Fatalf("parseEndpoint(%q): %v", tc.in, err)
		}
		if host != tc.wantHost || port != tc.wantPort {
			t.Fatalf("parseEndpoint(%q) = %s:%d, want %s:%d", tc.in, host, port, tc.wantHost, tc.wantPort)
		}
	}
}

func TestParseEndpointRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "   ", "http://"} {
		if _, _, err := parseEndpoint(in); err == nil {
			t.Fatalf("parseEndpoint(%q) should have failed", in)
		}
	}
}

func TestPointIDRoundTripsThroughTheUUIDForm(t *testing.T) {
	// Chunk ids are 128-bit hex; Qdrant only accepts UUIDs or unsigned
	// integers, so the adapter must map between the two losslessly.
	id := "0123456789abcdef0123456789abcdef"

	uuid, err := hexToUUID(id)
	if err != nil {
		t.Fatalf("hexToUUID: %v", err)
	}
	if uuid != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("uuid = %q", uuid)
	}
	if back := uuidToHex(uuid); back != id {
		t.Fatalf("round trip lost data: %q -> %q", id, back)
	}
}

func TestHexToUUIDRejectsNonChunkIDs(t *testing.T) {
	for _, id := range []string{"", "short", strings.Repeat("z", 32), strings.Repeat("a", 31)} {
		if _, err := hexToUUID(id); err == nil {
			t.Fatalf("hexToUUID(%q) should have failed", id)
		}
	}
}

func TestNewRequiresACollection(t *testing.T) {
	if _, err := New(Options{URL: "http://127.0.0.1:6334"}); err == nil {
		t.Fatal("expected an error when no collection is configured")
	}
}

func TestNewDoesNotDialEagerly(t *testing.T) {
	// Construction must succeed against a dead endpoint: an offline
	// Qdrant has to degrade memory, not break agent startup.
	store, err := New(Options{URL: "http://127.0.0.1:1", Collection: "openlight_memory"})
	if err != nil {
		t.Fatalf("New against an unreachable endpoint: %v", err)
	}
	defer func() { _ = store.Close() }()
}
