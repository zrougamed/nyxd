package network

import "testing"

func TestParseDockerPublish(t *testing.T) {
	cases := []struct {
		in   string
		want PortMapping
	}{
		{"8080:80", PortMapping{8080, 80, "tcp"}},
		{"127.0.0.1:9000:443", PortMapping{9000, 443, "tcp"}},
		{"53:53/udp", PortMapping{53, 53, "udp"}},
		{"80", PortMapping{80, 80, "tcp"}},
	}
	for _, tc := range cases {
		got, err := ParseDockerPublish(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %+v want %+v", tc.in, got, tc.want)
		}
	}
}

func TestParseDockerPublishErrors(t *testing.T) {
	for _, in := range []string{"", "x:y", "1:2:3:4", "0:80", "80:0", "80/tcp/extra"} {
		if _, err := ParseDockerPublish(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}
