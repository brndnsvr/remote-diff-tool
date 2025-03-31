# Remote Diff Tool

A robust command-line utility for collecting and comparing files across multiple remote servers via SSH. This tool helps system administrators, DevOps engineers, and IT professionals efficiently identify configuration inconsistencies and file differences across server infrastructure.

## Overview

Remote Diff Tool simplifies the process of verifying file consistency across multiple servers, which is crucial for maintaining reliable distributed systems. The tool operates in two main phases:

1. **Collection**: Securely connects to remote servers via SSH to collect specified files and directories
2. **Analysis**: Compares collected files to identify differences, using both checksums and content diffing

## Key Features

- **Secure Remote Collection**: Uses SSH for secure file retrieval from remote servers
- **Concurrent Processing**: Handles multiple servers in parallel for efficient operation
- **Flexible Configuration**: Supports both command-line and config file options
- **Comprehensive Comparison**: Uses checksums for quick matching and detailed diffs for in-depth analysis
- **Granular Logging**: Configurable logging levels with output to both console and files
- **Detailed Diff Output**: Generates and optionally saves unified diff files for easy review
- **Sudo Support**: Handles file access requiring elevated permissions on remote systems
- **Error Handling**: Robust error detection and reporting throughout the process

## Prerequisites

- Go 1.20 or later
- SSH access to all target servers
- Appropriate file permissions on remote servers
- For some operations: sudo access on remote servers

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/brndnsvr/remote-diff-tool.git

# Navigate to the repository directory
cd remote-diff-tool

# Build the binary
go build

# Optionally, install to your GOPATH
go install
```

### Using Go Install

```bash
go install github.com/brndnsvr/remote-diff-tool@latest
```

## Configuration

### Environment Variables

The tool requires the following environment variables for SSH authentication:

| Variable | Description | Required |
|----------|-------------|----------|
| `SSHUSER` | SSH username to use when connecting to remote servers | Yes |
| `SSHKEYPATH` | Path to SSH private key file (supports ~ expansion) | Yes |
| `SSHKEYPIN` | Passphrase for the SSH key (if the key is encrypted) | No |

Example setup:

```bash
export SSHUSER="username"
export SSHKEYPATH="~/.ssh/id_rsa"
export SSHKEYPIN="your-key-passphrase"  # Only if your key is encrypted
```

### Configuration File

The tool automatically generates and updates a configuration file (`config.json`) when you run the `collect` or `all` commands. This file is stored in the `<output-dir>/conf/` directory.

Example `config.json`:

```json
{
  "servers": ["server1.example.com", "server2.example.com"],
  "files": ["/etc/nginx/nginx.conf", "/etc/hosts"],
  "dirs": ["/etc/apache2/sites-enabled", "/opt/app/config"]
}
```

Note: SSH credentials are not stored in the config file for security reasons.

## Usage

### Basic Commands

The tool provides three main commands:

#### 1. Collect Files

```bash
remote-diff-tool collect -s "server1.example.com,server2.example.com" \
  -f "/etc/nginx/nginx.conf,/etc/hosts" \
  -d "/etc/apache2/sites-enabled,/opt/app/config"
```

This command connects to the specified servers and collects the listed files and directories.

#### 2. Analyze Differences

```bash
remote-diff-tool analyze --save-diffs --diff-dir ./diff_output
```

This command analyzes the previously collected files and identifies any differences. Use `--save-diffs` to save the detailed differences to files.

#### 3. Run Both Operations (All)

```bash
remote-diff-tool all -s "server1.example.com,server2.example.com" \
  -f "/etc/nginx/nginx.conf" -d "/etc/apache2/sites-enabled" \
  --save-diffs --diff-dir ./diff_output
```

This command performs both collection and analysis in one operation.

### Command Line Options

#### Global Options

- `-o, --output-dir`: Directory to store collected files and config (default: ".")
- `-c, --concurrency`: Maximum number of concurrent server operations (default: 10)
- `--log-file`: Path to log file (defaults to `logs/remote_diff_YYYYMMDD_HHMMSS.log`)
- `--log-level`: Log level (debug, info, warn, error) (default: "info")

#### Collect Command Options

- `-s, --servers`: Comma-separated list of server hostnames (required if no config.json)
- `-f, --files`: Comma-separated list of absolute file paths to collect
- `-d, --dirs`: Comma-separated list of absolute directory paths to collect

#### Analyze Command Options

- `--save-diffs`: Save diff outputs to files (boolean flag)
- `--diff-dir`: Directory to store diff files (default: "./diff_output")

### Examples

#### Comparing Configuration Files Across Web Servers

```bash
remote-diff-tool all -s "web1.example.com,web2.example.com,web3.example.com" \
  -f "/etc/nginx/nginx.conf,/etc/nginx/conf.d/default.conf" \
  -d "/etc/nginx/sites-enabled" \
  --save-diffs
```

#### Comparing Application Configurations

```bash
remote-diff-tool all -s "app1.example.com,app2.example.com" \
  -d "/opt/myapp/config,/opt/myapp/scripts" \
  -o "./app-comparison" \
  --log-level debug
```

## Output Structure

The tool organizes collected files and analysis results in the following directory structure:

```
<output-dir>/
├── conf/
│   └── config.json                      # Tool configuration
├── collected-files/
│   ├── manifest.json                    # File manifest with checksums
│   ├── files-server1.example.com/       # Files from server1
│   │   └── ... (directory structure preserving file paths)
│   └── files-server2.example.com/       # Files from server2
│       └── ... (directory structure preserving file paths)
├── logs/
│   └── remote_diff_YYYYMMDD_HHMMSS.log  # Log file
└── diff_output/                         # (If --save-diffs is specified)
    └── ... (diff files)
```

## Technical Details

### Remote File Collection Process

1. Establishes SSH connection to each target server
2. Uploads a temporary collection script to the server
3. Executes the script with appropriate permissions (using sudo where necessary)
4. The script creates a tarball of the requested files and directories
5. Downloads the tarball to the local machine
6. Extracts the tarball preserving directory structure
7. Calculates SHA-256 checksums for all collected files
8. Updates the manifest with file metadata

### Analysis Process

1. Loads the manifest containing file information and checksums
2. Identifies files common to all servers
3. Performs initial comparison using checksums
4. For files with differing checksums, performs a detailed content comparison
5. Generates unified diff output (`diff -u`) for files with differences
6. Optionally saves diff files for later inspection

## Troubleshooting

### Common Issues

1. **SSH Connection Errors**:
   - Verify SSH key path and permissions
   - Ensure the remote server is accessible
   - Check if the SSH user has correct permissions

2. **File Access Errors**:
   - Ensure the SSH user has read permissions for target files
   - Check if sudo access is required and available

3. **Comparison Discrepancies**:
   - Files might be binary/non-text files
   - Check for encoding differences
   - Line ending differences (Windows vs Unix)

### Logging

Detailed logs are saved in the `logs` directory. Increase log verbosity with the `--log-level debug` option for troubleshooting.

## Dependencies

- [github.com/pkg/errors](https://github.com/pkg/errors) - Error handling
- [github.com/pkg/sftp](https://github.com/pkg/sftp) - SFTP client implementation
- [github.com/sirupsen/logrus](https://github.com/sirupsen/logrus) - Structured logging
- [github.com/spf13/cobra](https://github.com/spf13/cobra) - Command-line interface
- [golang.org/x/crypto/ssh](https://pkg.go.dev/golang.org/x/crypto/ssh) - SSH client implementation
- [golang.org/x/sync/semaphore](https://pkg.go.dev/golang.org/x/sync/semaphore) - Concurrency control

## Security Considerations

- SSH keys are used for authentication; passwords are not supported
- The tool temporarily creates files on remote servers during collection
- Files are cleaned up after collection (both script and temporary files)
- For sudo operations, the remote user must have passwordless sudo access
- Sensitive data is not persisted in configuration files

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

[MIT License](LICENSE)

## Author

Brandon Seaver ([@brndnsvr](https://github.com/brndnsvr))
