package control

import (
	"encoding/base64"
	"testing"
)

func TestDecodeRegistryConfig(t *testing.T) {
	h := base64.URLEncoding.EncodeToString([]byte(`{"ghcr.io":{"username":"u","password":"p"}}`))
	m, err := decodeRegistryConfig(h)
	if err != nil || m.For("ghcr.io/a/b").Password != "p" {
		t.Errorf("decode: %v %v", m, err)
	}
	for _, bad := range []string{"%%%", base64.URLEncoding.EncodeToString([]byte("[1]"))} {
		if _, err := decodeRegistryConfig(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
