package util

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

// GenerateCollectionScript creates the shell script content
func GenerateCollectionScript(filePaths, dirPaths []string, username string) string {
	// Using a template might be cleaner for more complex scripts
	var script strings.Builder

	remoteBaseDir := fmt.Sprintf("/home/%s/remote_backup", username) // Use ~ doesn't always expand in non-interactive shell
	remoteTarFile := fmt.Sprintf("/home/%s/remote_backup.tar.gz", username)

	script.WriteString(`#!/bin/bash
set -e # Exit on first error

echo "Cleaning up previous backup (if any)..."
sudo rm -rf ` + remoteBaseDir + ` ` + remoteTarFile + `

echo "Creating backup directory structure..."
mkdir -p ` + remoteBaseDir + "\n")

	// Create parent directories within the backup structure
	createdDirs := make(map[string]bool) // Avoid duplicate mkdir commands
	for _, p := range filePaths {
		dir := filepath.Dir(p)
		if dir != "/" && dir != "." && !createdDirs[dir] { // Avoid root and relative root
			script.WriteString(fmt.Sprintf("mkdir -p %s%s\n", remoteBaseDir, dir))
			createdDirs[dir] = true
		}
	}
	for _, p := range dirPaths {
		p = strings.TrimRight(p, "/") // Ensure consistent path format
		if p != "/" && p != "." && !createdDirs[p] {
			script.WriteString(fmt.Sprintf("mkdir -p %s%s\n", remoteBaseDir, p))
			createdDirs[p] = true
		}
	}

	script.WriteString("\n# Copy individual files\n")
	for _, p := range filePaths {
		script.WriteString(fmt.Sprintf(`echo "Copying file %s"
if [ -f %q ]; then
    sudo cp -p %q %q # -p preserves mode and timestamps
else
    echo "WARNING: File %s not found"
    # Create a marker file to indicate absence
    touch %q.MISSING
fi
`, p, p, p, remoteBaseDir+p, p, remoteBaseDir+p))
	}

	script.WriteString("\n# Copy directory contents\n")
	for _, p := range dirPaths {
		p = strings.TrimRight(p, "/") // Ensure consistent path format
		script.WriteString(fmt.Sprintf(`echo "Copying directory contents %s"
if [ -d %q ]; then
    # Use find to copy contents, preserving structure relative to remoteBaseDir
    # Note: This copies contents INTO the target dir, mirroring find's behavior
    cd %q && sudo find . -mindepth 1 -print0 | sudo cpio -pdum0 %q 2>/dev/null || echo "Warning: cpio encountered errors in %s"
    # Alternative using cp -a (archive mode) if available and preferred:
    # sudo cp -aT %q %q # -T treats source as file/dir, not contents
else
    echo "WARNING: Directory %s not found"
    touch %qDIRECTORY.MISSING
fi
`, p, p, p, remoteBaseDir+p, p, p, remoteBaseDir+p, p, remoteBaseDir+p))
	}

	script.WriteString(fmt.Sprintf(`
# Set broad read permissions for the user to tar it up
echo "Setting permissions for tarring..."
sudo chmod -R u+rX,go-w %s || echo "Warning: chmod failed on backup dir"

# Create tar archive (run as user, not sudo)
echo "Creating tar archive..."
cd %s # Go into the base directory for relative paths in tar
tar czf %s . # Tar contents of current dir (.)

echo "Collection script finished."
`, remoteBaseDir, remoteBaseDir, remoteTarFile))

	return script.String()
}

// ExtractTarGz extracts a .tar.gz file to a destination directory
func ExtractTarGz(gzipStream io.Reader, dest string) error {
	uncompressedStream, err := gzip.NewReader(gzipStream)
	if err != nil {
		return errors.Wrap(err, "failed to create gzip reader")
	}
	defer uncompressedStream.Close()

	tarReader := tar.NewReader(uncompressedStream)

	for {
		header, err := tarReader.Next()

		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return errors.Wrap(err, "failed to read tar header")
		}

		target := filepath.Join(dest, header.Name)
		// Basic path sanitization
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path in tar: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return errors.Wrapf(err, "failed to create directory %s", target)
			}
		case tar.TypeReg:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return errors.Wrapf(err, "failed to create parent directory for file %s", target)
			}

			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return errors.Wrapf(err, "failed to create file %s", target)
			}
			// Use defer to ensure file is closed even if copy fails
			func() {
				defer outFile.Close()
				if _, err := io.Copy(outFile, tarReader); err != nil {
					// Update outer error variable; avoid shadowing
					err = errors.Wrapf(err, "failed to copy data to file %s", target)
				}
			}()
			if err != nil {
				return err // Return error from the copy if any
			}

		case tar.TypeSymlink:
			log.Warnf("Skipping symlink extraction: %s -> %s", target, header.Linkname)
			// Optional: Implement symlink creation if needed, carefully handling targets
		case tar.TypeLink:
			log.Warnf("Skipping hardlink extraction: %s -> %s", target, header.Linkname)
			// Optional: Implement hardlink creation if needed

		default:
			log.Warnf("Unsupported tar entry type %c for file %s", header.Typeflag, header.Name)
		}
	}
	return nil
}

// CalculateSHA256 calculates the SHA256 checksum of a file
func CalculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to open file %s for checksum", filePath)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", errors.Wrapf(err, "failed to read file %s for checksum", filePath)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
