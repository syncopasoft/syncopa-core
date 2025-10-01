package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Config represents the central configuration shared by the orchestrator and
// agents.
type Config struct {
	Host       string                 `json:"host,omitempty"`
	Port       int                    `json:"port,omitempty"`
	NFSServers map[string]NFSServer   `json:"nfs_servers"`
	Agents     map[string]AgentConfig `json:"agents"`
	Pools      map[string]PoolConfig  `json:"pools,omitempty"`
}

// NFSServer describes an available NFS export.
type NFSServer struct {
	Host       string   `json:"host"`
	ExportPath string   `json:"export_path"`
	MountPoint string   `json:"mount_point"`
	Options    []string `json:"options,omitempty"`
}

// AgentConfig defines authentication and resource mapping for a single agent.
type AgentConfig struct {
	Token      string   `json:"token"`
	NFSServers []string `json:"nfs_servers,omitempty"`
	Pools      []string `json:"pools,omitempty"`
}

// PoolConfig groups agents that share credentials and NFS mappings.
type PoolConfig struct {
	Token      string   `json:"token,omitempty"`
	Agents     []string `json:"agents"`
	NFSServers []string `json:"nfs_servers,omitempty"`
}

// NFSMount is a resolved mount specification delivered to agents.
type NFSMount struct {
	Name       string   `json:"name"`
	Remote     string   `json:"remote"`
	MountPoint string   `json:"mount_point"`
	Options    []string `json:"options,omitempty"`
}

// Load reads a configuration file from the provided path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.NFSServers == nil {
		cfg.NFSServers = map[string]NFSServer{}
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentConfig{}
	}
	if cfg.Pools == nil {
		cfg.Pools = map[string]PoolConfig{}
	}
	return &cfg, nil
}

// DefaultPort returns the configured port or the provided fallback when unset.
func (c *Config) DefaultPort(fallback int) int {
	if c == nil {
		return fallback
	}
	if c.Port != 0 {
		return c.Port
	}
	return fallback
}

// AgentAllowedTokens returns the list of tokens the given agent may use.
func (c *Config) AgentAllowedTokens(agentID string) []string {
	if c == nil {
		return nil
	}
	seen := map[string]struct{}{}
	addToken := func(token string) {
		token = strings.TrimSpace(token)
		if token == "" {
			return
		}
		seen[token] = struct{}{}
	}
	if agent, ok := c.Agents[agentID]; ok {
		addToken(agent.Token)
		for _, poolName := range agent.Pools {
			if pool, ok := c.Pools[poolName]; ok {
				addToken(pool.Token)
			}
		}
	}
	for _, pool := range c.Pools {
		for _, member := range pool.Agents {
			if member == agentID {
				addToken(pool.Token)
				break
			}
		}
	}
	tokens := make([]string, 0, len(seen))
	for token := range seen {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}

// ResolveNFSMounts returns the full mount configuration for the provided
// agent.
func (c *Config) ResolveNFSMounts(agentID string) ([]NFSMount, error) {
	if c == nil {
		return nil, nil
	}
	seen := map[string]struct{}{}
	mounts := []NFSMount{}
	addServer := func(name string) error {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil
		}
		if _, ok := seen[name]; ok {
			return nil
		}
		server, ok := c.NFSServers[name]
		if !ok {
			return fmt.Errorf("unknown NFS server %q", name)
		}
		if strings.TrimSpace(server.Host) == "" {
			return fmt.Errorf("nfs server %q missing host", name)
		}
		if strings.TrimSpace(server.ExportPath) == "" {
			return fmt.Errorf("nfs server %q missing export path", name)
		}
		if strings.TrimSpace(server.MountPoint) == "" {
			return fmt.Errorf("nfs server %q missing mount point", name)
		}
		export := server.ExportPath
		if !strings.HasPrefix(export, "/") {
			export = "/" + export
		}
		remote := fmt.Sprintf("%s:%s", server.Host, export)
		mount := NFSMount{
			Name:       name,
			Remote:     remote,
			MountPoint: server.MountPoint,
			Options:    append([]string(nil), server.Options...),
		}
		mounts = append(mounts, mount)
		seen[name] = struct{}{}
		return nil
	}
	if agent, ok := c.Agents[agentID]; ok {
		for _, srv := range agent.NFSServers {
			if err := addServer(srv); err != nil {
				return nil, err
			}
		}
		for _, poolName := range agent.Pools {
			if pool, ok := c.Pools[poolName]; ok {
				for _, srv := range pool.NFSServers {
					if err := addServer(srv); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	for _, pool := range c.Pools {
		if !pool.hasAgent(agentID) {
			continue
		}
		for _, srv := range pool.NFSServers {
			if err := addServer(srv); err != nil {
				return nil, err
			}
		}
	}
	return mounts, nil
}

func (p PoolConfig) hasAgent(agentID string) bool {
	for _, a := range p.Agents {
		if a == agentID {
			return true
		}
	}
	return false
}

// Validate ensures the configuration is internally consistent.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	for name, server := range c.NFSServers {
		if strings.TrimSpace(server.Host) == "" {
			return fmt.Errorf("nfs server %q missing host", name)
		}
		if strings.TrimSpace(server.ExportPath) == "" {
			return fmt.Errorf("nfs server %q missing export path", name)
		}
		if strings.TrimSpace(server.MountPoint) == "" {
			return fmt.Errorf("nfs server %q missing mount point", name)
		}
	}
	return nil
}
