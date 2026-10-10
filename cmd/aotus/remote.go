package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"aotus/internal/client"
)

func (a *app) whoami() error {
	m, err := a.c.Me(a.ctx)
	if err != nil {
		return err
	}
	where := "this computer"
	if r := a.c.Remote(); r != "" {
		where = r
	}
	if m.Method == "token" {
		fmt.Fprintf(a.out, "on %s: the local owner (token)\n", where)
		return nil
	}
	fmt.Fprintf(a.out, "on %s: %s from %s, role %s\n", where, m.Login, m.Device, m.Role)
	return nil
}

// connections handles `connections` and `connection add|rm|test|use`.
func (a *app) connections(args []string) error {
	saved, err := client.LoadConnections(a.connectionsPath)
	if err != nil {
		return err
	}
	if args[0] == "connections" || len(args) == 1 {
		if args[0] == "connection" {
			return usageError{"connection needs a subcommand: add, rm, test or use"}
		}
		t := table(a.out)
		fmt.Fprintln(t, "NAME\tADDRESS\t")
		active := saved.Active == "" || strings.EqualFold(saved.Active, client.LocalName)
		mark := map[bool]string{true: "(desktop opens this)", false: ""}
		fmt.Fprintf(t, "%s\tthis computer\t%s\n", client.LocalName, mark[active])
		for _, c := range saved.List {
			fmt.Fprintf(t, "%s\t%s\t%s\n", c.Name, c.Address, mark[strings.EqualFold(saved.Active, c.Name)])
		}
		return t.Flush()
	}
	switch sub, rest := args[1], args[2:]; sub {
	case "add":
		if len(rest) != 2 {
			return usageError{"usage: aotus connection add NAME HOST:PORT"}
		}
		if err := saved.Add(rest[0], rest[1]); err != nil {
			return err
		}
		if err := saved.Save(a.connectionsPath); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "saved %s. Try it: aotus connection test %s\n", rest[0], rest[0])
		return nil
	case "rm":
		name, err := one(rest, "connection rm NAME")
		if err != nil {
			return err
		}
		if !saved.Remove(name) {
			return fmt.Errorf("there is no saved connection called %q", name)
		}
		return saved.Save(a.connectionsPath)
	case "use":
		name, err := one(rest, "connection use NAME|local")
		if err != nil {
			return err
		}
		if !strings.EqualFold(name, client.LocalName) {
			if _, ok := saved.Get(name); !ok {
				return fmt.Errorf("there is no saved connection called %q", name)
			}
		} else {
			name = ""
		}
		saved.Active = name
		return saved.Save(a.connectionsPath)
	case "test":
		name, err := one(rest, "connection test NAME")
		if err != nil {
			return err
		}
		conn, ok := saved.Get(name)
		if !ok {
			return fmt.Errorf("there is no saved connection called %q", name)
		}
		c, err := client.Dial(a.ctx, conn)
		if err != nil {
			return err
		}
		a.c = c
		if err := a.status(); err != nil {
			return err
		}
		return a.whoami()
	}
	return usageError{"connection needs a subcommand: add, rm, test or use"}
}

// access handles the allow-list commands, which only the owner may use.
func (a *app) access(args []string) error {
	if len(args) == 0 {
		return usageError{"access needs a subcommand: list, allow, deny, share or unshare"}
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "list":
		acc, err := a.c.Access(a.ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "owner: %s\n", acc.Owner)
		t := table(a.out)
		fmt.Fprintln(t, "LOGIN\tADDED BY\tPROFILES SHARED")
		for _, e := range acc.Entries {
			fmt.Fprintf(t, "%s\t%s\t%s\n", e.Login, e.AddedBy, strings.Join(e.Profiles, ", "))
		}
		return t.Flush()
	case "allow":
		fs := flag.NewFlagSet("access allow", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		ack := fs.Bool("acknowledge", false, "")
		// The login comes first, then the flag.
		if len(rest) == 0 {
			return usageError{"usage: aotus access allow LOGIN --acknowledge"}
		}
		login := rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return usageError{"usage: aotus access allow LOGIN --acknowledge"}
		}
		if !*ack {
			acc, err := a.c.Access(a.ctx)
			if err != nil {
				return err
			}
			fmt.Fprintln(a.out, strings.ReplaceAll(acc.SharingNotice, "<login>", login))
			return usageError{"read the notice above; if you accept it, run the same command with --acknowledge"}
		}
		if err := a.c.AllowLogin(a.ctx, login, true); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s may now call this daemon (read-only until you share a profile: aotus access share PROFILE %s)\n", login, login)
		return nil
	case "deny":
		login, err := one(rest, "access deny LOGIN")
		if err != nil {
			return err
		}
		return a.c.DenyLogin(a.ctx, login)
	case "share", "unshare":
		if len(rest) != 2 {
			return usageError{fmt.Sprintf("usage: aotus access %s PROFILE LOGIN", sub)}
		}
		if sub == "share" {
			return a.c.ShareProfile(a.ctx, rest[0], rest[1])
		}
		return a.c.UnshareProfile(a.ctx, rest[0], rest[1])
	}
	return usageError{"access needs a subcommand: list, allow, deny, share or unshare"}
}
