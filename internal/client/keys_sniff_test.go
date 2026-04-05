package client

import (
	"strings"
	"testing"
)

func TestSniffKeyFileFormat(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		wantErr   bool
		wantMatch string // substring of the error message
	}{
		{
			name:      "ed25519 certificate",
			data:      "ssh-ed25519-cert-v01@openssh.com AAAAIHNzaC1lZDI1NTE5LWNlcnQtdjAxQG9wZW5zc2guY29t... user@host\n",
			wantErr:   true,
			wantMatch: "SSH certificate",
		},
		{
			name:      "rsa certificate",
			data:      "ssh-rsa-cert-v01@openssh.com AAAAHHNzaC1yc2EtY2VydC12MDFAb3BlbnNzaC5jb20... user@host\n",
			wantErr:   true,
			wantMatch: "SSH certificate",
		},
		{
			name:      "ecdsa certificate",
			data:      "ecdsa-sha2-nistp256-cert-v01@openssh.com AAAAKGVjZHNhLXNoYTIt... user@host\n",
			wantErr:   true,
			wantMatch: "SSH certificate",
		},
		{
			name:      "ed25519 public key",
			data:      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJPpG4hFrxw7JOAppGdh0JrkNDNGxypfmwJxNFCWXnpG test@sshkey\n",
			wantErr:   true,
			wantMatch: "public key",
		},
		{
			name:      "rsa public key",
			data:      "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQ... user@host\n",
			wantErr:   true,
			wantMatch: "public key",
		},
		{
			name:      "ecdsa public key",
			data:      "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAA... user@host\n",
			wantErr:   true,
			wantMatch: "public key",
		},
		{
			name:      "rsa private key (PEM)",
			data:      "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEAt...\n-----END RSA PRIVATE KEY-----\n",
			wantErr:   true,
			wantMatch: "non-Ed25519",
		},
		{
			name:      "ec private key (PEM)",
			data:      "-----BEGIN EC PRIVATE KEY-----\nMHcCAQEEIGx...\n-----END EC PRIVATE KEY-----\n",
			wantErr:   true,
			wantMatch: "non-Ed25519",
		},
		{
			name:    "openssh ed25519 private key",
			data:    "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMw...\n-----END OPENSSH PRIVATE KEY-----\n",
			wantErr: false,
		},
		{
			name:    "unknown format falls through",
			data:    "some random content that is not a known format\n",
			wantErr: false, // our sniff is conservative — lets ssh.ParsePrivateKey produce the final error
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := sniffKeyFileFormat("/tmp/test_key", []byte(tc.data))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantMatch) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantMatch)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			}
		})
	}
}
