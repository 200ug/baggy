package internal

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/sftp"
	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
)

var (
	ConfigPath string = ".config/wsftp.conf"
)

func init() {
	// resolve home path only once during module init
	userHome, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("[!] user home could not be resolved: %s\n", err)
		os.Exit(1)
	}
	fullConfigPath := filepath.Join(userHome, ConfigPath)
	ConfigPath = fullConfigPath
}

type UserRemoteConfig struct {
	User           string `json:"user"`
	Hostname       string `json:"hostname"`
	PrivKeyPath    string `json:"privkey_path"`
	KnownHostsPath string `json:"knownhosts_path"`
	Port           int    `json:"port"`
	StorageRoot    string `json:"storage_root"` // remote's root path
	Salt           []byte `json:"salt"`         // shared across devices via remote
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
	Conn   *ssh.Client
	SFTP   *sftp.Client
	Config *UserRemoteConfig
}

// Initializes a completely fresh remote connection config, verifies the SSH + SFTP connection
// and finally persists the config (as UserRemoteConfig) to ~/.config/wsftp.conf. The overwrite
// parameter exists solely for automated unit tests.
func NewRemoteConn(compact string, privKeyPath string, knownHostsPath string, overwrite bool) (*RemoteConn, error) {
	if !overwrite {
		if _, statErr := os.Stat(ConfigPath); statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		} else if statErr == nil {
			reader := bufio.NewReader(os.Stdin)
			for {
				fmt.Printf("[?] existing config found, overwrite? [y/n]: ")
				res, err := reader.ReadString('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return nil, fmt.Errorf("user input prompt failed")
				}
				res = strings.ToLower(strings.TrimSpace(res))
				if res == "y" || res == "yes" {
					break
				} else if res == "n" || res == "no" {
					return nil, fmt.Errorf("init aborted by user")
				}
			}
		}
	}

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
		return nil, fmt.Errorf("invalid port: %v", err)
	}

	urc := &UserRemoteConfig{
		User:           user,
		Hostname:       hostname,
		PrivKeyPath:    privKeyPath,
		KnownHostsPath: knownHostsPath,
		Port:           port,
		StorageRoot:    storageRoot,
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

	cleanup := func() {
		remoteConn.SFTP.Close()
		remoteConn.Conn.Close()
		_ = os.Remove(ConfigPath)
	}

	// ensure storage root exists on remote
	if err = remoteConn.SFTP.MkdirAll(storageRoot); err != nil {
		cleanup()
		return nil, err
	}

	// pull existing salt or generate and push a new one
	salt, err := remoteConn.pullSalt()
	if err != nil {
		cleanup()
		return nil, err
	}
	if salt == nil {
		salt = make([]byte, 16)
		if _, err = rand.Read(salt); err != nil {
			cleanup()
			return nil, err
		}
		if err = remoteConn.pushSalt(salt); err != nil {
			cleanup()
			return nil, err
		}
	}

	urc.Salt = salt
	if err = urc.WriteToFile(); err != nil {
		cleanup()
		return nil, err
	}
	remoteConn.Config.Salt = salt

	return remoteConn, nil
}

// Load *existing* remote connection configuration from ~/.config/wsftp.conf into RemoteConn.
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

	kh, err := knownhosts.NewDB(config.KnownHostsPath)
	if err != nil {
		return nil, err
	}

	addr := fmt.Sprintf("%s:%d", config.Hostname, config.Port)
	sshCfg := &ssh.ClientConfig{
		User:              config.User,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   kh.HostKeyCallback(),
		HostKeyAlgorithms: kh.HostKeyAlgorithms(addr),
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

	return &RemoteConn{Conn: sshClient, SFTP: sftpClient, Config: config}, nil
}

func (rc *RemoteConn) PushFile(localPath, remotePath string) error {
	if err := rc.SFTP.MkdirAll(path.Dir(remotePath)); err != nil {
		return err
	}
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := rc.SFTP.Create(remotePath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

func (rc *RemoteConn) PullFile(remotePath, localPath string) error {
	src, err := rc.SFTP.Open(remotePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

func (rc *RemoteConn) PushMetafileRemote(localRoot string, meta *Metadata) error {
	remotePath := path.Join(rc.Config.StorageRoot, filepath.Base(localRoot), Metafile)
	if err := rc.SFTP.MkdirAll(path.Dir(remotePath)); err != nil {
		return err
	}
	f, err := rc.SFTP.Create(remotePath)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(meta)
}

func (rc *RemoteConn) pushSalt(salt []byte) error {
	f, err := rc.SFTP.Create(path.Join(rc.Config.StorageRoot, "salt"))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(salt)

	return err
}

func (rc *RemoteConn) pullSalt() ([]byte, error) {
	p := path.Join(rc.Config.StorageRoot, "salt")
	if _, err := rc.SFTP.Lstat(p); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	f, err := rc.SFTP.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return io.ReadAll(f)
}

func (rc *RemoteConn) DeleteRemoteFile(remotePath string) error {
	if err := rc.SFTP.Remove(remotePath); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

func (rc *RemoteConn) PullRemoteMetafile(localRoot string) (*Metadata, error) {
	remotePath := path.Join(rc.Config.StorageRoot, filepath.Base(localRoot), Metafile)

	if _, err := rc.SFTP.Lstat(remotePath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no remote metafile yet (first sync)
		}
		return nil, err
	}

	f, err := rc.SFTP.Open(remotePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var meta Metadata
	if err = json.NewDecoder(f).Decode(&meta); err != nil {
		return nil, err
	}

	return &meta, nil
}
