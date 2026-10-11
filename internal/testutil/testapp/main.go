// Command testapp is the only program in the images built by
// internal/testutil/daemon. It is a multi-call binary (like busybox): /bin/sh,
// /bin/echo, /bin/cat, ... are links to it and it acts on argv[0]. The shell
// understands just enough for tests: commands separated by ";" or "&&",
// $VAR expansion, single/double quotes, and "> file" / ">> file" redirects.
package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	name := filepath.Base(os.Args[0])
	if name == "testapp" && len(os.Args) > 1 {
		name, os.Args = os.Args[1], os.Args[1:]
	}
	if name == "sh" {
		if len(os.Args) == 3 && os.Args[1] == "-c" {
			os.Exit(script(os.Args[2]))
		}
		// Interactive: one command per line from stdin.
		sc := bufio.NewScanner(os.Stdin)
		code := 0
		for sc.Scan() {
			code = script(sc.Text())
		}
		os.Exit(code)
	}
	os.Exit(run(append([]string{name}, os.Args[1:]...), os.Stdout))
}

// script runs "a; b && c" and returns the last exit code.
func script(s string) int {
	code := 0
	for _, part := range strings.Split(s, ";") {
		for i, cmd := range strings.Split(part, "&&") {
			if i > 0 && code != 0 {
				break
			}
			code = command(strings.TrimSpace(cmd))
		}
	}
	return code
}

func command(s string) int {
	words := split(os.ExpandEnv(s))
	if len(words) == 0 {
		return 0
	}
	out := io.Writer(os.Stdout)
	for i := 0; i < len(words); i++ {
		if words[i] == ">" || words[i] == ">>" {
			if i+1 >= len(words) {
				fmt.Fprintln(os.Stderr, "sh: missing redirect target")
				return 2
			}
			flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
			if words[i] == ">>" {
				flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
			}
			f, err := os.OpenFile(words[i+1], flags, 0o644)
			if err != nil {
				fmt.Fprintln(os.Stderr, "sh:", err)
				return 1
			}
			defer f.Close()
			out = f
			words = append(words[:i], words[i+2:]...)
			i--
		}
	}
	return run(words, out)
}

func split(s string) []string {
	var words []string
	var cur strings.Builder
	quote, in := rune(0), false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				words, in = append(words, cur.String()), false
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if in {
		words = append(words, cur.String())
	}
	return words
}

func run(args []string, out io.Writer) int {
	switch args[0] {
	case "true":
		return 0
	case "false":
		return 1
	case "exit":
		if len(args) > 1 {
			n, _ := strconv.Atoi(args[1])
			os.Exit(n)
		}
		os.Exit(0)
	case "echo":
		fmt.Fprintln(out, strings.Join(args[1:], " "))
	case "cat":
		if len(args) == 1 {
			_, _ = io.Copy(out, os.Stdin)
		}
		for _, f := range args[1:] {
			b, err := os.ReadFile(f)
			if err != nil {
				fmt.Fprintln(os.Stderr, "cat:", err)
				return 1
			}
			_, _ = out.Write(b)
		}
	case "touch":
		for _, f := range args[1:] {
			if err := os.WriteFile(f, nil, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "touch:", err)
				return 1
			}
		}
	case "mkdir":
		for _, d := range args[1:] {
			if d != "-p" {
				if err := os.MkdirAll(d, 0o755); err != nil {
					fmt.Fprintln(os.Stderr, "mkdir:", err)
					return 1
				}
			}
		}
	case "id":
		fmt.Fprintln(out, os.Getuid())
	case "pwd":
		wd, _ := os.Getwd()
		fmt.Fprintln(out, wd)
	case "env":
		for _, e := range os.Environ() {
			fmt.Fprintln(out, e)
		}
	case "hostname":
		h, _ := os.Hostname()
		fmt.Fprintln(out, h)
	case "sleep":
		d := 3600.0
		if len(args) > 1 {
			d, _ = strconv.ParseFloat(args[1], 64)
		}
		time.Sleep(time.Duration(d * float64(time.Second)))
	case "ignoreterm":
		// Ignores SIGTERM: only SIGKILL stops it.
		signal.Ignore(syscall.SIGTERM)
		fmt.Fprintln(out, "ignoring TERM")
		time.Sleep(time.Hour)
	case "trapterm":
		// Exits 0 on SIGTERM after printing a line.
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGTERM)
		fmt.Fprintln(out, "ready")
		<-c
		fmt.Fprintln(out, "got TERM")
	case "serve":
		// serve PORT: HTTP 200 "ok" on every path.
		ln, err := net.Listen("tcp", "127.0.0.1:"+args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
			return 1
		}
		fmt.Fprintln(out, "serving")
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	case "chatty":
		// chatty N: N numbered lines, to stdout and stderr alternately.
		n, _ := strconv.Atoi(args[1])
		for i := 1; i <= n; i++ {
			w := out
			if i%2 == 0 {
				w = os.Stderr
			}
			fmt.Fprintf(w, "line %d\n", i)
		}
	default:
		fmt.Fprintf(os.Stderr, "%s: not found\n", args[0])
		return 127
	}
	return 0
}
