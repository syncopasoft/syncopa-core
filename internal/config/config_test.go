package config

import "testing"

func TestAgentAllowedTokens(t *testing.T) {
	cfg := &Config{
		NFSServers: map[string]NFSServer{},
		Agents: map[string]AgentConfig{
			"agent-a": {Token: "token-a", Pools: []string{"pool-1"}},
			"agent-b": {Token: "token-b"},
		},
		Pools: map[string]PoolConfig{
			"pool-1": {Token: "pool-token", Agents: []string{"agent-a", "agent-c"}},
			"pool-2": {Token: "other", Agents: []string{"agent-b"}},
		},
	}

	tokens := cfg.AgentAllowedTokens("agent-a")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[0] != "pool-token" || tokens[1] != "token-a" {
		t.Fatalf("unexpected tokens: %v", tokens)
	}

	tokens = cfg.AgentAllowedTokens("agent-b")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestResolveNFSMounts(t *testing.T) {
	cfg := &Config{
		NFSServers: map[string]NFSServer{
			"primary": {Host: "nfs.example.com", ExportPath: "/data", MountPoint: "/mnt/data", Options: []string{"rw"}},
			"shared":  {Host: "nfs.example.com", ExportPath: "/shared", MountPoint: "/mnt/shared"},
		},
		Agents: map[string]AgentConfig{
			"agent-a": {NFSServers: []string{"primary"}, Pools: []string{"pool-1"}},
		},
		Pools: map[string]PoolConfig{
			"pool-1": {Agents: []string{"agent-a"}, NFSServers: []string{"shared"}},
		},
	}

	mounts, err := cfg.ResolveNFSMounts("agent-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d", len(mounts))
	}
	if mounts[0].Remote != "nfs.example.com:/data" {
		t.Fatalf("unexpected remote for mount 0: %s", mounts[0].Remote)
	}
	if mounts[1].MountPoint != "/mnt/shared" {
		t.Fatalf("unexpected mount point for mount 1: %s", mounts[1].MountPoint)
	}
}

func TestResolveNFSMountsUnknownServer(t *testing.T) {
	cfg := &Config{NFSServers: map[string]NFSServer{}, Agents: map[string]AgentConfig{
		"agent": {NFSServers: []string{"missing"}},
	}}
	if _, err := cfg.ResolveNFSMounts("agent"); err == nil {
		t.Fatalf("expected error for missing server")
	}
}

func TestControlTokens(t *testing.T) {
	cfg := &Config{Control: ControlConfig{Tokens: []string{" alpha ", "beta", "alpha"}}}
	tokens := cfg.ControlTokens()
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	if tokens[0] != "alpha" || tokens[1] != "beta" {
		t.Fatalf("unexpected tokens: %v", tokens)
	}
	cfg.Control.Tokens = append(cfg.Control.Tokens, "  ")
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for empty control token")
	}
}
