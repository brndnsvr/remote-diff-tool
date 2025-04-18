package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

// --- UPDATED CONSTANTS ---
const ConfigDir = "conf"
const CollectedFilesBaseDir = "collected-files"
const ConfigFileName = "config.json"
const ManifestFileName = "manifest.json"

// --- END OF UPDATED CONSTANTS ---

// SSHCredentials holds the SSH authentication details
type SSHCredentials struct {
	Username      string
	KeyPath       string
	KeyPassphrase string
}

// Config holds the application configuration
type Config struct {
	Servers   []string       `json:"servers"`
	Files     []string       `json:"files"`
	Dirs      []string       `json:"dirs"`
	SSHConfig SSHCredentials `json:"-"` // Loaded from ENV, not saved in config.json
}

// FileInfo holds metadata about a collected file, including its checksum
type FileInfo struct {
	Path     string `json:"path"`            // Relative path within the server's collection dir
	Checksum string `json:"checksum"`        // SHA-256 checksum
	Error    string `json:"error,omitempty"` // Record if there was an error fetching/checksumming
}

// Manifest holds the checksums for all collected files from all servers
type Manifest struct {
	Mu            sync.RWMutex                   `json:"-"`               // Use exported field for cross-package access
	FilesByServer map[string]map[string]FileInfo `json:"files_by_server"` // server -> relativePath -> FileInfo
}

func NewManifest() *Manifest {
	return &Manifest{
		FilesByServer: make(map[string]map[string]FileInfo),
	}
}

// AddFile adds or updates file info in the manifest safely.
func (m *Manifest) AddFile(server, relativePath, checksum, fileError string) {
	m.Mu.Lock()         // Use exported field Mu
	defer m.Mu.Unlock() // Use exported field Mu

	if _, ok := m.FilesByServer[server]; !ok {
		m.FilesByServer[server] = make(map[string]FileInfo)
	}
	m.FilesByServer[server][relativePath] = FileInfo{
		Path:     relativePath,
		Checksum: checksum,
		Error:    fileError,
	}
}

// GetFileInfo retrieves file info safely.
func (m *Manifest) GetFileInfo(server, relativePath string) (FileInfo, bool) {
	m.Mu.RLock()         // Use exported field Mu
	defer m.Mu.RUnlock() // Use exported field Mu

	serverFiles, ok := m.FilesByServer[server]
	if !ok {
		return FileInfo{}, false
	}
	fileInfo, ok := serverFiles[relativePath]
	return fileInfo, ok
}

// getConfigPath helper function
func getConfigPath(outputDir string) string {
	return filepath.Join(outputDir, ConfigDir, ConfigFileName)
}

// getManifestPath helper function
func getManifestPath(outputDir string) string {
	return filepath.Join(outputDir, CollectedFilesBaseDir, ManifestFileName)
}

// Save persists the manifest to disk in the correct subfolder.
func (m *Manifest) Save(outputDir string) error {
	m.Mu.RLock()         // Use exported field Mu
	defer m.Mu.RUnlock() // Use exported field Mu

	manifestPath := getManifestPath(outputDir) // Use helper
	manifestDir := filepath.Dir(manifestPath)
	if err := os.MkdirAll(manifestDir, 0755); err != nil { // Ensure <outputDir>/collected-files/ exists
		return errors.Wrapf(err, "failed to create manifest directory %s", manifestDir)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to marshal manifest")
	}
	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		return errors.Wrapf(err, "failed to write manifest file %s", manifestPath)
	}
	log.Infof("Manifest saved to %s", manifestPath)
	return nil
}

// LoadManifest loads the manifest from disk from the correct subfolder.
func LoadManifest(outputDir string) (*Manifest, error) {
	manifestPath := getManifestPath(outputDir) // Use helper
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Warnf("Manifest file %s not found, creating new one.", manifestPath)
			return NewManifest(), nil // Return empty manifest if not found
		}
		return nil, errors.Wrapf(err, "failed to read manifest file %s", manifestPath)
	}

	var manifest Manifest
	// Initialize map before unmarshaling into it
	manifest.FilesByServer = make(map[string]map[string]FileInfo)
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal manifest file %s", manifestPath)
	}
	log.Infof("Manifest loaded from %s", manifestPath)
	return &manifest, nil
}

// GetSSHCredentialsFromEnv loads SSH details from environment variables
func GetSSHCredentialsFromEnv() (SSHCredentials, error) {
	creds := SSHCredentials{
		Username:      os.Getenv("SSHUSER"),
		KeyPath:       os.Getenv("SSHKEYPATH"),
		KeyPassphrase: os.Getenv("SSHKEYPIN"), // Optional
	}

	var missing []string
	if creds.Username == "" {
		missing = append(missing, "SSHUSER")
	}
	if creds.KeyPath == "" {
		missing = append(missing, "SSHKEYPATH")
	}
	// KeyPassphrase is optional

	if len(missing) > 0 {
		return creds, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	// Expand tilde ~ in key path
	if strings.HasPrefix(creds.KeyPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return creds, errors.Wrap(err, "failed to get user home directory to expand key path")
		}
		creds.KeyPath = filepath.Join(homeDir, creds.KeyPath[1:])
	}

	if _, err := os.Stat(creds.KeyPath); os.IsNotExist(err) {
		return creds, fmt.Errorf("ssh key file not found at %s", creds.KeyPath)
	}

	return creds, nil
}

// LoadOrInitializeConfig loads config from file or initializes from args
func LoadOrInitializeConfig(outputDir, serversStr, filesStr, dirsStr string, saveConfig bool) (*Config, error) {
	configPath := getConfigPath(outputDir) // Use helper
	cfg := &Config{}

	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read existing config file %s", configPath)
		}
		if err := json.Unmarshal(data, cfg); err != nil {
			log.Warnf("Failed to parse existing config file %s: %v. Proceeding with arguments.", configPath, err)
			// Reset cfg to avoid partial data
			cfg = &Config{}
		} else {
			log.Infof("Loaded existing configuration from %s", configPath)
		}
	} else if !os.IsNotExist(err) {
		return nil, errors.Wrapf(err, "failed to stat config file %s", configPath)
	}

	// Override or set from arguments if provided
	if serversStr != "" {
		cfg.Servers = strings.Split(serversStr, ",")
	}
	if filesStr != "" {
		cfg.Files = strings.Split(filesStr, ",")
	}
	if dirsStr != "" {
		cfg.Dirs = strings.Split(dirsStr, ",")
	}

	// Basic validation
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("no servers specified (use --servers or ensure valid %s exists)", configPath)
	}
	if len(cfg.Files) == 0 && len(cfg.Dirs) == 0 {
		return nil, fmt.Errorf("no files or directories specified (use --files/--dirs or ensure valid %s exists)", configPath)
	}

	// Clean paths (remove trailing slashes from dirs for consistency)
	cleanedDirs := []string{}
	for _, d := range cfg.Dirs {
		cleanedDirs = append(cleanedDirs, strings.TrimRight(d, "/"))
	}
	cfg.Dirs = cleanedDirs

	// Load SSH creds (always from ENV)
	sshConfig, err := GetSSHCredentialsFromEnv()
	if err != nil {
		return nil, err
	}
	cfg.SSHConfig = sshConfig

	log.Infof("Using configuration:")
	log.Infof("  Servers: %s", strings.Join(cfg.Servers, ", "))
	log.Infof("  Files: %s", strings.Join(cfg.Files, ", "))
	log.Infof("  Directories: %s", strings.Join(cfg.Dirs, ", "))

	// Save the potentially updated config if requested (e.g., during collect/all)
	if saveConfig {
		// Ensure the <outputDir>/conf directory exists before writing
		configDir := filepath.Dir(configPath)
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return nil, errors.Wrapf(err, "failed to create config directory %s", configDir)
		}

		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal config")
		}
		if err := os.WriteFile(configPath, data, 0644); err != nil {
			return nil, errors.Wrapf(err, "failed to write config file %s", configPath)
		}
		log.Infof("Configuration saved to %s", configPath)
	}

	return cfg, nil
}
