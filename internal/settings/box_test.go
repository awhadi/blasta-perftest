package settings

import "testing"

// A secret sealed by an earlier version must stay readable: the derivation of the key
// is fixed forever (including its label), or every saved SMTP password, SSO client
// secret and bot-check key would become undecryptable after an upgrade.
func TestSealedSecretsStayReadable(t *testing.T) {
	b, err := NewBox([]byte("0123456789abcdef-golden-key"))
	if err != nil {
		t.Fatal(err)
	}
	const sealedByV1 = "v1:ZxWRoJk5NrEcRC0C/kYzdEFBFAvcZukk6xeixtwOoQ8Tgdn3MVBnQHjW1bdJU9g"
	got, err := b.Open(sealedByV1)
	if err != nil || got != "golden-secret-value" {
		t.Fatalf("a secret sealed by an earlier version can no longer be opened: %q %v", got, err)
	}
	// And a fresh round trip still works.
	s, _ := b.Seal("another one")
	if v, err := b.Open(s); err != nil || v != "another one" {
		t.Errorf("round trip: %q %v", v, err)
	}
}
