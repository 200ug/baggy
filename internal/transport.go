package internal

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

/*
	sftp on top of ssh, minimal operations:
	 	- ReadFile/WriteFile for the plaintext metafile
	 	- Put/Get for encrypted blobs (the actual files)

	note: to prevent race conditions, we should write a `.lock` file to the server (and always check for its existence before doing ops)
*/

var (
	ConfigPath string = ".config/baggy.conf"
)

func init() {
	// resolve home path only once during module init
	userHome, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("user home could not be resolved: %s", err)
	}
	fullConfigPath := filepath.Join(userHome, ConfigPath)
	ConfigPath = fullConfigPath
}

type UserRemoteConfig struct {
	User 	 	string `json:"user"`
	Hostname 	string `json:"hostname"`
	PrivKeyPath string `json:"privkey_path"`
	Port 	 	int	   `json:"port"`
	StorageRoot string `json:"storage_root"` // root dir. for file sync
}

func UserRemoteConfigFromFile() (*UserRemoteConfig, error) {
	file, err := os.Open(ConfigPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var config UserRemoteConfig
	parser := json.NewDecoder(file)
	if err = parser.Decode(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

func (urc *UserRemoteConfig) WriteToFile() error {
	if err := os.MkdirAll(filepath.Dir(ConfigPath), 0o755); err != nil {
		return err
	}
	js, err := json.MarshalIndent(urc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath, js, 0o644)
}

type RemoteConn struct {
	Conn *ssh.Client
	SFTP *sftp.Client
}

// Initializes a completely fresh remote connection config, verifies the SSH + SFTP connection
// and finally persists the config (as UserRemoteConfig) to ~/.config/baggy.conf.
func NewRemoteConn(compact string, privKeyPath string) (*RemoteConn, error) {
	atIdx := strings.IndexByte(compact, '@')
	if atIdx <= 0 {
		return nil, fmt.Errorf("invalid compact string: missing '@'")
	}
	user := compact[:atIdx]
	parts := strings.SplitN(compact[atIdx+1:], ":", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid compact string: expected '<user>@<hostname>:<port>:<storage_root>'")
	}
	hostname, storageRoot := parts[0], parts[2]
	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid port: %w", err)
	}

	urc := &UserRemoteConfig{
		User:        user,
		Hostname:    hostname,
		PrivKeyPath: privKeyPath,
		Port:        port,
		StorageRoot: storageRoot,
	}

	if err = urc.WriteToFile(); err != nil {
		return nil, err
	}

	remoteConn, err := LoadRemoteConn()
	if err != nil {
		// config couldn't be verified -> remove it so we don't persist invalid state
		_ = os.Remove(ConfigPath)
		return nil, err
	}

	return remoteConn, nil
}

// Load *existing* remote connection configuration from ~/.config/baggy.conf into RemoteConn.
func LoadRemoteConn() (*RemoteConn, error) {
	config, err := UserRemoteConfigFromFile()
	if err != nil {
		return nil, err
	}

	keyBytes, err := os.ReadFile(config.PrivKeyPath)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, err
	}

	sshCfg := &ssh.ClientConfig{
		User: config.User,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: replace with known_hosts verification before prod. use
	}
	sshClient, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", config.Hostname, config.Port), sshCfg)
	if err != nil {
		return nil, err
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, err
	}

	return &RemoteConn{Conn: sshClient, SFTP: sftpClient}, nil
}

