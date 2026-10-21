package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestStatsNoStream(t *testing.T) {
	body := `{"items":[{"id":"web-long-name-here","short_id":"abc123def456","name":"web-long-name-here","status":"running","cpu_percent":12.5,"mem_usage":1048576,"mem_limit":67108864,"mem_percent":1.56,"net_rx":100,"net_tx":200,"block_read":0,"block_write":0,"pids":3}]}`
	sock := stub(t, routes(map[string]http.HandlerFunc{
		"/stats": reply(200, body),
	}))
	out, errOut, err := nyx(t, "-socket", sock, "stats", "--no-stream")
	if err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	if !strings.Contains(out, "CONTAINER ID") || !strings.Contains(out, "abc123def456") {
		t.Fatalf("out=%q", out)
	}
	if !strings.Contains(out, "12.50%") {
		t.Fatalf("cpu missing: %q", out)
	}
}

func TestStatsErrorPaths(t *testing.T) {
	sock := stub(t, reply(500, "boom"))
	if _, _, err := nyx(t, "-socket", sock, "stats", "--no-stream"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
	sock = stub(t, reply(200, "{"))
	if _, _, err := nyx(t, "-socket", sock, "stats", "--no-stream"); err == nil {
		t.Fatal("bad json")
	}
	if _, _, err := nyx(t, "-socket", sock, "stats", "--bogus"); err == nil {
		t.Fatal("unknown flag")
	}
}

func TestInspectJSON(t *testing.T) {
	payload := `{"id":"web","short_id":"aabbccddeeff","image":"alpine:3.20","status":"running","pid":42}`
	sock := stub(t, routes(map[string]http.HandlerFunc{
		"/inspect": reply(200, payload),
	}))
	out, errOut, err := nyx(t, "-socket", sock, "inspect", "web")
	if err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("decode %v: %s", err, out)
	}
	if len(arr) != 1 || arr[0]["id"] != "web" {
		t.Fatalf("%v", arr)
	}
	if _, _, err := nyx(t, "-socket", sock, "inspect"); err == nil {
		t.Fatal("missing id")
	}
	if _, _, err := nyx(t, "-socket", sock, "inspect", "--format", "web"); err == nil {
		t.Fatal("unknown flag")
	}
	sock = stub(t, reply(404, "no matching container"))
	if _, _, err := nyx(t, "-socket", sock, "inspect", "gone"); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("err=%v", err)
	}
}

func TestContainerAliasesStatsInspect(t *testing.T) {
	sock := stub(t, routes(map[string]http.HandlerFunc{
		"/stats":   reply(200, `{"items":[]}`),
		"/inspect": reply(200, `{"id":"c","status":"running"}`),
	}))
	if _, _, err := nyx(t, "-socket", sock, "container", "stats", "--no-stream"); err != nil {
		t.Fatal(err)
	}
	if out, _, err := nyx(t, "-socket", sock, "container", "inspect", "c"); err != nil || !strings.Contains(out, `"id": "c"`) {
		t.Fatalf("%v %s", err, out)
	}
}

func TestComposeStatsNoStream(t *testing.T) {
	cfile := writeCompose(t)
	sock := stub(t, routes(map[string]http.HandlerFunc{
		"/stats":      reply(200, `{"items":[{"id":"p-s","short_id":"112233445566","name":"p-s","status":"running","cpu_percent":1,"mem_usage":1024,"mem_limit":2048,"mem_percent":50,"pids":1}]}`),
		"/containers": reply(200, `{"containers":["p-s"]}`),
	}))
	out, errOut, err := nyx(t, "-socket", sock, "compose", "stats", "--no-stream", "-f", cfile, "--project", "p")
	if err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	if !strings.Contains(out, "p-s") && !strings.Contains(out, "112233445566") {
		t.Fatalf("out=%q", out)
	}
}

func TestComposeStatsEmptyStack(t *testing.T) {
	cfile := writeCompose(t)
	sock := stub(t, routes(map[string]http.HandlerFunc{
		"/containers": reply(200, `{"containers":[]}`),
	}))
	_, errOut, err := nyx(t, "-socket", sock, "compose", "stats", "--no-stream", "-f", cfile, "--project", "p")
	if err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	if !strings.Contains(errOut, "no containers for project") {
		t.Fatalf("stderr=%q", errOut)
	}
}

func TestComposeStatsUnknownService(t *testing.T) {
	cfile := writeCompose(t)
	sock := stub(t, reply(200, `{"containers":[]}`))
	if _, _, err := nyx(t, "-socket", sock, "compose", "stats", "-f", cfile, "nosuch"); err == nil || !strings.Contains(err.Error(), "unknown service") {
		t.Fatalf("err=%v", err)
	}
}

func TestHumanBytes(t *testing.T) {
	if humanBytes(500) != "500B" {
		t.Fatal(humanBytes(500))
	}
	if humanBytes(1536) != "1.50KiB" {
		t.Fatal(humanBytes(1536))
	}
	if humanBytes(10<<20) != "10.0MiB" {
		t.Fatal(humanBytes(10 << 20))
	}
	if humanBytes(100<<30) == "" {
		t.Fatal("large")
	}
}

func TestPrintStatsRowErrorAndTrunc(t *testing.T) {
	printStatsHeader()
	printStatsRow(statsItem{ID: "verylongcontaineridname", ShortID: "", Name: "verylongnameshouldtruncatehere", Err: "x"}, false)
	printStatsRow(statsItem{
		ID: "id", ShortID: "short", Name: "n", CPUPerc: 1, MemUsage: 100, MemLimit: 200,
		MemPerc: 50, NetRx: 1, NetTx: 2, BlockRead: 3, BlockWrite: 4, PIDs: 5,
	}, true)
	clearStatsScreen(2)
	_ = isTTY(os.Stdout)
}
