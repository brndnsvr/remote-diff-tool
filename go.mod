module github.com/brndnsvr/remote-diff-tool

go 1.20 // Or later

require (
	github.com/pkg/errors v0.9.1
	github.com/pkg/sftp v1.13.6
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/cobra v1.8.0
	golang.org/x/crypto v0.21.0 // Check for latest secure version
	golang.org/x/sync v0.6.0    // For semaphore
)

