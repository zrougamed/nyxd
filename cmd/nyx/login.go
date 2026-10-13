package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/registryauth"
	"golang.org/x/term"
)

const loginUsage = "usage: nyx login [-u USER] [-p PASS | --password-stdin] [--authfile PATH] [REGISTRY]"

// doLogin checks credentials against the registry and stores them in nyx's
// auth file (or --authfile). Logins made with docker or podman are read
// without this; see registryauth.
func doLogin(args []string) error {
	var user, pass, authfile, registry string
	var fromStdin, passSet bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(a, "=")
		value := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "-u", "--username":
			user, err = value()
		case "-p", "--password":
			pass, err = value()
			passSet = true
		case "--password-stdin":
			fromStdin = true
		case "--authfile":
			authfile, err = value()
		case "-h", "--help":
			return errors.New(loginUsage)
		default:
			if strings.HasPrefix(a, "-") || registry != "" {
				return errors.New(loginUsage)
			}
			registry = a
		}
		if err != nil {
			return err
		}
	}
	if passSet && fromStdin {
		return errors.New("--password and --password-stdin are mutually exclusive")
	}
	host := image.NormalizeRegistryHost(registry)

	in := bufio.NewReader(os.Stdin)
	tty := term.IsTerminal(int(os.Stdin.Fd()))
	if fromStdin {
		if user == "" {
			return errors.New("--password-stdin needs --username")
		}
		b, err := io.ReadAll(io.LimitReader(in, 64<<10))
		if err != nil {
			return err
		}
		pass = strings.TrimRight(string(b), "\r\n")
	}
	if user == "" {
		if !tty {
			return errors.New("no username: use --username (and --password-stdin) when stdin is not a terminal")
		}
		fmt.Fprint(os.Stderr, "Username: ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		user = strings.TrimSpace(line)
	}
	if !passSet && !fromStdin {
		if !tty {
			return errors.New("no password: use --password-stdin when stdin is not a terminal")
		}
		fmt.Fprint(os.Stderr, "Password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		pass = string(b)
	} else if passSet {
		fmt.Fprintln(os.Stderr, "WARNING! Using --password via the CLI is insecure. Use --password-stdin.")
	}
	if user == "" || pass == "" {
		return errors.New("username and password are required")
	}

	auth := image.RegistryAuth{Username: user, Password: pass}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := image.CheckLogin(ctx, host, auth); err != nil {
		return err
	}
	if authfile == "" {
		var err error
		if authfile, err = registryauth.NyxFile(); err != nil {
			return err
		}
	}
	if err := registryauth.Save(authfile, host, auth); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Credentials saved to %s\n", authfile)
	fmt.Println("Login Succeeded")
	return nil
}

// doLogout removes a login from nyx's auth file (or --authfile).
func doLogout(args []string) error {
	const usage = "usage: nyx logout [--authfile PATH] [REGISTRY]"
	var authfile, registry string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--authfile":
			if i+1 >= len(args) {
				return errors.New(usage)
			}
			i++
			authfile = args[i]
		case strings.HasPrefix(a, "--authfile="):
			authfile = strings.TrimPrefix(a, "--authfile=")
		case strings.HasPrefix(a, "-") || registry != "":
			return errors.New(usage)
		default:
			registry = a
		}
	}
	host := image.NormalizeRegistryHost(registry)
	if authfile == "" {
		var err error
		if authfile, err = registryauth.NyxFile(); err != nil {
			return err
		}
	}
	removed, err := registryauth.Remove(authfile, host)
	if err != nil {
		return err
	}
	if removed {
		fmt.Printf("Removed login credentials for %s\n", host)
		return nil
	}
	if _, from, ok, _ := registryauth.Lookup(host); ok {
		return fmt.Errorf("not logged in to %s with nyx; the credentials in use come from %s (log out with the tool that wrote them)", host, from)
	}
	return fmt.Errorf("not logged in to %s", host)
}
