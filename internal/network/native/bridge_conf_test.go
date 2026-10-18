//go:build linux

package native

import (
	"io"
	"log/slog"
	"testing"

	"github.com/zrougamed/nyxd/internal/network"
)

func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBridgeConfFromOpts(t *testing.T) {
	def, err := bridgeConfFromOpts(nil)
	if err != nil || def.Bridge != BridgeName {
		t.Fatalf("%+v %v", def, err)
	}
	def, err = bridgeConfFromOpts(&network.SetupOptions{})
	if err != nil || def.Bridge != BridgeName {
		t.Fatalf("%+v %v", def, err)
	}
	_, err = bridgeConfFromOpts(&network.SetupOptions{Bridge: "nyxbr-x"})
	if err == nil {
		t.Fatal("missing subnet/gateway")
	}
	_, err = bridgeConfFromOpts(&network.SetupOptions{
		Bridge: "nyxbr-x", Subnet: "bad", Gateway: "10.1.0.1",
	})
	if err == nil {
		t.Fatal("bad subnet")
	}
	_, err = bridgeConfFromOpts(&network.SetupOptions{
		Bridge: "nyxbr-x", Subnet: "10.1.0.0/16", Gateway: "nope",
	})
	if err == nil {
		t.Fatal("bad gateway")
	}
	got, err := bridgeConfFromOpts(&network.SetupOptions{
		Bridge: "nyxbr-x", Subnet: "10.1.0.0/16", Gateway: "10.1.0.1",
	})
	if err != nil || got.Bridge != "nyxbr-x" || got.Gateway != "10.1.0.1" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestEgressNATCommentFor(t *testing.T) {
	if egressNATCommentFor(defaultBridgeConf()) != egressNATComment {
		t.Fatal("default")
	}
	c := bridgeConf{Bridge: "nyxbr-runner", Subnet: "10.89.0.0/16", Gateway: "10.89.0.1"}
	if egressNATCommentFor(c) != egressNATComment+"-nyxbr-runner" {
		t.Fatal(egressNATCommentFor(c))
	}
}

func TestRememberForgetBridge(t *testing.T) {
	rememberBridge("c-test", bridgeConf{Bridge: "b", Subnet: "10.0.0.0/24", Gateway: "10.0.0.1"})
	got, ok := forgetBridge("c-test")
	if !ok || got.Bridge != "b" {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := forgetBridge("c-test"); ok {
		t.Fatal("second forget")
	}
}

func TestEnsureBridgeIsolationNoPanic(t *testing.T) {
	quiet := slogDiscard()
	// Seed two ready bridges so isolation walks the pairwise drop path.
	bridgeReadyMu.Lock()
	bridgeReady["nyxbr-iso-a"] = nil
	bridgeReady["nyxbr-iso-b"] = nil
	bridgeReadyMu.Unlock()
	t.Cleanup(func() {
		bridgeReadyMu.Lock()
		delete(bridgeReady, "nyxbr-iso-a")
		delete(bridgeReady, "nyxbr-iso-b")
		bridgeReadyMu.Unlock()
	})
	ensureBridgeIsolation(bridgeConf{Bridge: "nyxbr-iso-a", Subnet: "10.1.0.0/16", Gateway: "10.1.0.1"}, quiet)
	// Idempotent when rules already exist (or nft unavailable — must not panic).
	ensureBridgeIsolation(bridgeConf{Bridge: "nyxbr-iso-a", Subnet: "10.1.0.0/16", Gateway: "10.1.0.1"}, quiet)
}
