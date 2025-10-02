package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Config represents the central configuration shared by the orchestrator and
// agents.
type Config struct {
	Host       string                 `json:"host,omitempty"`
	Port       int                    `json:"port,omitempty"`
	Control    ControlConfig          `json:"control,omitempty"`
	NFSServers map[string]NFSServer   `json:"nfs_servers"`
	Agents     map[string]AgentConfig `json:"agents"`
	Pools      map[string]PoolConfig  `json:"pools,omitempty"`
}

// ControlConfig declares authentication tokens for control-plane operations
// such as submitting jobs to the daemon.
type ControlConfig struct {
	Tokens []string `json:"tokens"`
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

	var cfg *Config
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".conf", ".ini":
		cfg, err = parsePlainConfig(f)
	default:
		cfg, err = parseJSONConfig(f)
	}
	if err != nil {
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
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseJSONConfig(r io.Reader) (*Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type plainSection struct {
	kind string
	name string
}

func parsePlainConfig(r io.Reader) (*Config, error) {
	scanner := bufio.NewScanner(r)
	cfg := &Config{}
	section := plainSection{kind: "root"}

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("config line %d: malformed section header", lineNo)
			}
			header := strings.TrimSpace(line[1 : len(line)-1])
			if header == "" {
				return nil, fmt.Errorf("config line %d: empty section header", lineNo)
			}
			parts := strings.SplitN(header, " ", 2)
			sectionKind := strings.ToLower(strings.TrimSpace(parts[0]))
			sectionName := ""
			if len(parts) > 1 {
				sectionName = strings.TrimSpace(parts[1])
				if strings.HasPrefix(sectionName, "\"") || strings.HasPrefix(sectionName, "'") || strings.HasPrefix(sectionName, "`") {
					unquoted, err := strconv.Unquote(sectionName)
					if err != nil {
						return nil, fmt.Errorf("config line %d: %v", lineNo, err)
					}
					sectionName = unquoted
				}
			}
			switch sectionKind {
			case "control":
				if sectionName != "" {
					return nil, fmt.Errorf("config line %d: control section must not have a name", lineNo)
				}
			case "nfs":
				if sectionName == "" {
					return nil, fmt.Errorf("config line %d: nfs section requires a name", lineNo)
				}
				if cfg.NFSServers == nil {
					cfg.NFSServers = map[string]NFSServer{}
				}
				if _, exists := cfg.NFSServers[sectionName]; !exists {
					cfg.NFSServers[sectionName] = NFSServer{}
				}
			case "agent":
				if sectionName == "" {
					return nil, fmt.Errorf("config line %d: agent section requires an id", lineNo)
				}
				if cfg.Agents == nil {
					cfg.Agents = map[string]AgentConfig{}
				}
				if _, exists := cfg.Agents[sectionName]; !exists {
					cfg.Agents[sectionName] = AgentConfig{}
				}
			case "pool":
				if sectionName == "" {
					return nil, fmt.Errorf("config line %d: pool section requires a name", lineNo)
				}
				if cfg.Pools == nil {
					cfg.Pools = map[string]PoolConfig{}
				}
				if _, exists := cfg.Pools[sectionName]; !exists {
					cfg.Pools[sectionName] = PoolConfig{}
				}
			default:
				return nil, fmt.Errorf("config line %d: unknown section %q", lineNo, sectionKind)
			}
			section = plainSection{kind: sectionKind, name: sectionName}
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("config line %d: expected key = value", lineNo)
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") || strings.HasPrefix(value, "`") {
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return nil, fmt.Errorf("config line %d: %v", lineNo, err)
			}
			value = unquoted
		}

		switch section.kind {
		case "root":
			switch key {
			case "host":
				cfg.Host = value
			case "port":
				if value == "" {
					cfg.Port = 0
					break
				}
				port, err := strconv.Atoi(value)
				if err != nil {
					return nil, fmt.Errorf("config line %d: invalid port %q", lineNo, value)
				}
				cfg.Port = port
			default:
				return nil, fmt.Errorf("config line %d: unknown top-level key %q", lineNo, key)
			}
		case "control":
			if key != "token" {
				return nil, fmt.Errorf("config line %d: unknown control key %q", lineNo, key)
			}
			cfg.Control.Tokens = append(cfg.Control.Tokens, value)
		case "nfs":
			server := cfg.NFSServers[section.name]
			switch key {
			case "host":
				server.Host = value
			case "export_path":
				server.ExportPath = value
			case "mount_point":
				server.MountPoint = value
			case "option":
				server.Options = append(server.Options, value)
			default:
				return nil, fmt.Errorf("config line %d: unknown nfs key %q", lineNo, key)
			}
			cfg.NFSServers[section.name] = server
		case "agent":
			agent := cfg.Agents[section.name]
			switch key {
			case "token":
				agent.Token = value
			case "nfs":
				agent.NFSServers = append(agent.NFSServers, value)
			case "pool":
				agent.Pools = append(agent.Pools, value)
			default:
				return nil, fmt.Errorf("config line %d: unknown agent key %q", lineNo, key)
			}
			cfg.Agents[section.name] = agent
		case "pool":
			pool := cfg.Pools[section.name]
			switch key {
			case "token":
				pool.Token = value
			case "agent":
				pool.Agents = append(pool.Agents, value)
			case "nfs":
				pool.NFSServers = append(pool.NFSServers, value)
			default:
				return nil, fmt.Errorf("config line %d: unknown pool key %q", lineNo, key)
			}
			cfg.Pools[section.name] = pool
		default:
			return nil, fmt.Errorf("config line %d: no active section for key %q", lineNo, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return cfg, nil
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

// ControlTokens returns the list of tokens that authorize control-plane
// operations such as submitting jobs to the daemon.
func (c *Config) ControlTokens() []string {
	if c == nil {
		return nil
	}
	tokens := []string{}
	seen := map[string]struct{}{}
	for _, token := range c.Control.Tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
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
	for _, token := range c.Control.Tokens {
		if strings.TrimSpace(token) == "" {
			return errors.New("control tokens must not be empty")
		}
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
