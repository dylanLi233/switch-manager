package credential

import "testing"

func TestCredentialValidate(t *testing.T) {
	t.Parallel()
	password := Credential{
		ID: "cred-1", Name: "shared", Type: TypePassword, Username: "admin",
		EncryptedSecret: []byte("ciphertext"), KeyVersion: "v1",
	}
	if err := password.Validate(); err != nil {
		t.Fatalf("password Validate() error = %v", err)
	}

	privateKey := Credential{
		ID: "cred-2", Name: "key", Type: TypeSSHPrivateKey, Username: "admin",
		EncryptedPrivateKey: []byte("ciphertext"), KeyVersion: "v1",
	}
	if err := privateKey.Validate(); err != nil {
		t.Fatalf("private key Validate() error = %v", err)
	}

	invalid := []Credential{
		{ID: "cred-1", Name: "bad", Type: TypePassword, Username: "admin", KeyVersion: "v1"},
		{ID: "cred-1", Name: "bad", Type: TypeSSHPrivateKey, Username: "admin", KeyVersion: "v1"},
		{ID: "cred-1", Name: "bad", Type: Type("TOKEN"), Username: "admin", EncryptedSecret: []byte("x"), KeyVersion: "v1"},
		{ID: "cred-1", Name: "bad", Type: TypePassword, Username: "", EncryptedSecret: []byte("x"), KeyVersion: "v1"},
		{ID: "cred-1", Name: "bad", Type: TypePassword, Username: "admin", EncryptedSecret: []byte("x")},
		{ID: "cred-1", Name: "bad", Type: TypePassword, Username: "admin", EncryptedSecret: []byte("x"), EncryptedPassphrase: []byte("y"), KeyVersion: "v1"},
	}
	for _, c := range invalid {
		if err := c.Validate(); err == nil {
			t.Fatalf("Validate(%+v) expected error", c)
		}
	}
}
