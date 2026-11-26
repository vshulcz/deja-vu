package main

import (
	"fmt"
	"os"

	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/usage"
	"github.com/vshulcz/deja-vu/internal/vault"
)

const (
	envDejaClient      = "DEJA_CLIENT"
	envDejaClientToken = "DEJA_CLIENT_TOKEN" //nolint:gosec // environment variable name, not a credential
)

var accessExemptCommands = map[string]bool{
	"version": true, "--version": true, "-version": true,
	"help": true, "--help": true, "-h": true,
	"completion": true, "update": true,
	"install": true, "uninstall": true,
	"init": true, "lock": true, "unlock": true,
	"doctor": true, "sources": true, "rules": true,
	"remember": true, "log": true, "mcp": true,
}

func activeClientForPolicy() string {
	if c := mcpClientName(); c != "" {
		return c
	}
	if c := os.Getenv(envDejaClient); c != "" {
		return c
	}
	return "cli"
}

func cliClientName() string {
	if c := os.Getenv(envDejaClient); c != "" {
		return c
	}
	return "cli"
}

func hookClientName() string {
	if c := os.Getenv(envDejaClient); c != "" {
		return c
	}
	return "hook"
}

func mcpActiveClientName() string {
	return mcpActiveClientNameFor(currentMCPClientSession())
}

func mcpActiveClientNameFor(sess *mcpClientSession) string {
	if sess != nil {
		if c := sess.clientName(); c != "" {
			return c
		}
	} else if c := mcpClientName(); c != "" {
		return c
	}
	if c := os.Getenv(envDejaClient); c != "" {
		return c
	}
	return ""
}

func mcpActiveClientTokenFor(sess *mcpClientSession) string {
	if sess != nil {
		if tok := sess.tokenValue(); tok != "" {
			return tok
		}
	}
	return os.Getenv(envDejaClientToken)
}

func shouldAuditAllow(pol policy.Policy, action string) bool {
	return action == "write" || pol.RequireClients || len(pol.Clients) > 0 || len(pol.ClientTokens) > 0 || vault.Enabled()
}

func enforceClientPolicyWithToken(dir, surface, client, token, action, projectScope string) error {
	pol := policy.Load()
	var ok bool
	var reason string
	if action == "write" {
		ok, reason = pol.AllowsClientWrite(surface, client, token, projectScope)
	} else {
		ok, reason = pol.AllowsClientRead(surface, client, token, projectScope)
	}
	if !ok {
		usage.RecordAccess(dir, surface, client, action, projectScope, "deny", reason)
		if p := policy.Path(); p != "" {
			return fmt.Errorf("%s — see %s", reason, p)
		}
		return fmt.Errorf("%s", reason)
	}
	if shouldAuditAllow(pol, action) {
		usage.RecordAccess(dir, surface, client, action, projectScope, "allow", "")
	}
	return nil
}

func enforceClientPolicy(dir, surface, client, action, projectScope string) error {
	token := os.Getenv(envDejaClientToken)
	if surface == "mcp" {
		token = mcpActiveClientTokenFor(currentMCPClientSession())
	}
	return enforceClientPolicyWithToken(dir, surface, client, token, action, projectScope)
}

func checkSurfaceAccess(dir, surface, client, action, projectScope string) error {
	if _, err := vault.CheckUnlocked(dir); err != nil {
		usage.RecordAccess(dir, surface, client, action, projectScope, "locked", err.Error())
		return err
	}
	return enforceClientPolicy(dir, surface, client, action, projectScope)
}

func checkCLIAccess(dir, action, projectScope string) error {
	return checkSurfaceAccess(dir, "cli", cliClientName(), action, projectScope)
}

func checkMCPAccess(dir, action, projectScope string) error {
	return checkSurfaceAccess(dir, "mcp", mcpActiveClientName(), action, projectScope)
}

func hookAccessAllowed(dir string) bool {
	client := hookClientName()
	if _, err := vault.CheckUnlocked(dir); err != nil {
		usage.RecordAccess(dir, "hook", client, "read", "", "locked", err.Error())
		return false
	}
	pol := policy.Load()
	if ok, reason := pol.AllowsClientRead("hook", client, os.Getenv(envDejaClientToken), ""); !ok {
		usage.RecordAccess(dir, "hook", client, "read", "", "deny", reason)
		return false
	}
	if shouldAuditAllow(pol, "read") {
		usage.RecordAccess(dir, "hook", client, "read", "", "allow", "")
	}
	return true
}
