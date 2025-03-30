module github.com/brndnsvr/remote-diff-tool

go 1.20 // Or your Go version, e.g., 1.21

require (
	github.com/pkg/errors v0.9.1
	github.com/pkg/sftp v1.13.6
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/cobra v1.8.0
	golang.org/x/crypto v0.21.0 // Use latest stable/secure version
	golang.org/x/sync v0.6.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/sys v0.18.0 // indirect
)

// === Add the following indirect dependencies (go mod tidy will manage these) ===
// It's okay if these aren't here initially, 'go mod tidy' adds them.
// But if tidy keeps failing, adding them explicitly can sometimes help diagnose.
// Example structure (versions may vary):
// require (
//     github.com/inconshreveable/mousetrap v1.1.0 // indirect
//     github.com/kr/fs v0.1.0 // indirect
//     github.com/kr/text v0.2.0 // indirect
//     github.com/spf13/pflag v1.0.5 // indirect
//     golang.org/x/sys v0.18.0 // indirect
// )
