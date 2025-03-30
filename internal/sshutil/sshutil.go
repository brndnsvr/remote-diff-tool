package sshutil

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

// Client wraps ssh.Client and sftp.Client
type Client struct {
	Hostname   string
	sshClient  *ssh.Client
	sftpClient *sftp.Client
}

// Connect establishes an SSH connection
func Connect(hostname, username, keyPath, keyPassphrase string) (*Client, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read private key %s", keyPath)
	}

	var signer ssh.Signer
	if keyPassphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(keyPassphrase))
		if err != nil {
			// Check if the error is specifically about passphrase needed but not provided correctly
			if errors.Is(err, &ssh.PassphraseMissingError{}) {
				return nil, errors.Wrapf(err, "private key %s requires a passphrase (check SSHKEYPIN)", keyPath)
			}
			return nil, errors.Wrapf(err, "failed to parse encrypted private key %s", keyPath)
		}
	} else {
		signer, err = ssh.ParsePrivateKey(key)
		if err != nil {
			// Check if it needed a passphrase
			if _, ok := err.(*ssh.PassphraseMissingError); ok {
				return nil, errors.Wrapf(err, "private key %s seems to require a passphrase, but SSHKEYPIN was not provided or is empty", keyPath)
			}
			return nil, errors.Wrapf(err, "failed to parse private key %s", keyPath)
		}
	}

	sshConfig := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Use ssh.FixedHostKey or knownhosts for production
		Timeout:         15 * time.Second,            // Connection timeout
	}

	var sshClient *ssh.Client
	var connErr error
	maxRetries := 3
	retryDelay := 2 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Infof("Connecting to %s@%s (attempt %d/%d)...", username, hostname, attempt, maxRetries)
		conn, err := net.DialTimeout("tcp", hostname+":22", sshConfig.Timeout)
		if err != nil {
			connErr = errors.Wrapf(err, "failed to dial %s", hostname)
			if attempt < maxRetries {
				log.Warnf("Dial failed: %v. Retrying in %v...", connErr, retryDelay)
				time.Sleep(retryDelay)
				continue
			}
			return nil, connErr // Final attempt failed
		}

		sshConn, chans, reqs, err := ssh.NewClientConn(conn, hostname+":22", sshConfig)
		if err != nil {
			connErr = errors.Wrapf(err, "failed to establish SSH connection to %s", hostname)
			conn.Close() // Close the underlying net.Conn
			if attempt < maxRetries {
				log.Warnf("SSH handshake failed: %v. Retrying in %v...", connErr, retryDelay)
				time.Sleep(retryDelay)
				continue
			}
			return nil, connErr // Final attempt failed
		}
		sshClient = ssh.NewClient(sshConn, chans, reqs)
		connErr = nil // Success
		break         // Exit retry loop on success
	}

	if connErr != nil {
		return nil, errors.Wrapf(connErr, "failed to connect to %s after %d attempts", hostname, maxRetries)
	}

	log.Infof("Successfully connected to %s", hostname)

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, errors.Wrap(err, "failed to create SFTP client")
	}
	log.Debugf("SFTP client created for %s", hostname)

	return &Client{
		Hostname:   hostname,
		sshClient:  sshClient,
		sftpClient: sftpClient,
	}, nil
}

// Close closes the SFTP and SSH connections
func (c *Client) Close() {
	if c.sftpClient != nil {
		log.Debugf("Closing SFTP client for %s", c.Hostname)
		c.sftpClient.Close()
		c.sftpClient = nil
	}
	if c.sshClient != nil {
		log.Debugf("Closing SSH client for %s", c.Hostname)
		c.sshClient.Close()
		c.sshClient = nil
	}
}

// RunCommand executes a command on the remote server
func (c *Client) RunCommand(command string, sudo bool) (string, string, error) {
	session, err := c.sshClient.NewSession()
	if err != nil {
		return "", "", errors.Wrap(err, "failed to create SSH session")
	}
	defer session.Close()

	if sudo {
		command = "sudo " + command
	}

	log.Debugf("Executing on %s: %s", c.Hostname, command)

	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	err = session.Run(command) // Use Run for commands that finish

	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()

	if err != nil {
		// Check if it's ExitError to get status code
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			log.Warnf("Command on %s exited with status %d: %s", c.Hostname, exitErr.ExitStatus(), command)
			log.Debugf("Stderr: %s", stderr)
			// Return the error, let caller decide how to handle non-zero exit
			return stdout, stderr, fmt.Errorf("command exited with status %d: %w", exitErr.ExitStatus(), err)
		}
		// Other errors (network, etc.)
		return stdout, stderr, errors.Wrapf(err, "failed to run command '%s'", command)
	}

	log.Debugf("Command finished successfully on %s: %s", c.Hostname, command)
	return stdout, stderr, nil
}

// UploadFile uploads a local file to a remote path using SFTP
func (c *Client) UploadFile(localPath, remotePath string) error {
	log.Debugf("Uploading %s to %s:%s", localPath, c.Hostname, remotePath)

	localFile, err := os.Open(localPath)
	if err != nil {
		return errors.Wrapf(err, "failed to open local file %s for upload", localPath)
	}
	defer localFile.Close()

	// Ensure remote directory exists
	remoteDir := filepath.Dir(remotePath)
	if err := c.sftpClient.MkdirAll(remoteDir); err != nil {
		// MkdirAll returns nil if directory already exists
		// Check for other errors if necessary
		log.Warnf("Could not ensure remote directory %s exists (maybe OK): %v", remoteDir, err)
	}

	remoteFile, err := c.sftpClient.Create(remotePath)
	if err != nil {
		return errors.Wrapf(err, "failed to create remote file %s:%s", c.Hostname, remotePath)
	}
	defer remoteFile.Close()

	bytesCopied, err := io.Copy(remoteFile, localFile)
	if err != nil {
		return errors.Wrapf(err, "failed to copy data to remote file %s:%s", c.Hostname, remotePath)
	}

	log.Debugf("Successfully uploaded %d bytes to %s:%s", bytesCopied, c.Hostname, remotePath)
	return nil
}

// DownloadFile downloads a remote file to a local path using SFTP
func (c *Client) DownloadFile(remotePath, localPath string) error {
	log.Debugf("Downloading %s:%s to %s", c.Hostname, remotePath, localPath)

	remoteFile, err := c.sftpClient.Open(remotePath)
	if err != nil {
		return errors.Wrapf(err, "failed to open remote file %s:%s", c.Hostname, remotePath)
	}
	defer remoteFile.Close()

	// Ensure local directory exists
	localDir := filepath.Dir(localPath)
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return errors.Wrapf(err, "failed to create local directory %s", localDir)
	}

	localFile, err := os.Create(localPath)
	if err != nil {
		return errors.Wrapf(err, "failed to create local file %s", localPath)
	}
	defer localFile.Close()

	bytesCopied, err := io.Copy(localFile, remoteFile)
	if err != nil {
		// Clean up potentially incomplete local file on error
		localFile.Close()
		os.Remove(localPath)
		return errors.Wrapf(err, "failed to copy data from remote file %s:%s", c.Hostname, remotePath)
	}

	log.Debugf("Successfully downloaded %d bytes from %s:%s to %s", bytesCopied, c.Hostname, remotePath, localPath)
	return nil
}

// CheckSudoAccess tries to run a harmless sudo command without a password
func (c *Client) CheckSudoAccess() bool {
	log.Infof("Checking passwordless sudo access on %s...", c.Hostname)
	_, stderr, err := c.RunCommand("-n true", true) // sudo -n true
	if err == nil {
		log.Infof("User %s has passwordless sudo access on %s", c.sshClient.User(), c.Hostname)
		return true
	}
	log.Warnf("User %s may not have passwordless sudo access on %s (command failed: %v, stderr: %s)", c.sshClient.User(), c.Hostname, err, stderr)
	return false
}
