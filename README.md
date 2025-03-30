# Remote Diff Tool

A powerful command-line tool for collecting and comparing files across multiple remote servers via SSH. This tool helps you efficiently identify differences in files and directories across your server infrastructure.

## Features

- Concurrent file collection from multiple remote servers
- Efficient file comparison using checksums
- Parallel diffing capabilities
- Configurable logging with multiple output options
- Flexible configuration through command-line or config file
- Support for both file and directory comparisons

## Prerequisites

- Go 1.20 or later
- SSH access to target servers
- Appropriate permissions on remote servers

## Installation

```bash
go install github.com/brndnsvr/remote-diff-tool@latest
```

## Usage

The tool provides three main commands:

### 1. Collect Files (`collect`)

Collects files from remote servers:

```bash
remote-diff-tool collect -s "server1,server2" -f "/path/to/file1,/path/to/file2" -d "/path/to/dir1,/path/to/dir2"
```

### 2. Analyze Differences (`analyze`)

Analyzes differences between collected files:

```bash
remote-diff-tool analyze --save-diffs --diff-dir ./diff_output
```

### 3. Run Both Operations (`all`)

Performs both collection and analysis in one command:

```bash
remote-diff-tool all -s "server1,server2" -f "/path/to/file1" --save-diffs
```

## Command Line Options

### Global Options
- `-o, --output-dir`: Directory to store collected files and config (default: ".")
- `-c, --concurrency`: Maximum number of concurrent server operations (default: 10)
- `--log-file`: Path to log file (defaults to remote_diff_YYYYMMDD_HHMMSS.log)
- `--log-level`: Log level (debug, info, warn, error) (default: "info")

### Collect Command Options
- `-s, --servers`: Comma-separated list of server hostnames (required if no config.json)
- `-f, --files`: Comma-separated list of absolute file paths
- `-d, --dirs`: Comma-separated list of absolute directory paths

### Analyze Command Options
- `--save-diffs`: Save diff outputs to files
- `--diff-dir`: Directory to store diff files (default: "./diff_output")

## Configuration

The tool can be configured either through command-line arguments or a config.json file. The config file is automatically created when running the collect command if it doesn't exist.

## Logging

Logs are written to both stderr and a log file by default. The log file name follows the pattern `remote_diff_YYYYMMDD_HHMMSS.log`. You can customize the log file location and level using the appropriate command-line options.

## Dependencies

- github.com/pkg/errors
- github.com/pkg/sftp
- github.com/sirupsen/logrus
- github.com/spf13/cobra
- golang.org/x/crypto
- golang.org/x/sync

## License

[TODO]

## Contributing

[TODO] 
