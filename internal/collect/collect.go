package collect

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/brndnsvr/remote-diff-tool/internal/config"
	"github.com/brndnsvr/remote-diff-tool/internal/sshutil"
	"github.com/brndnsvr/remote-diff-tool/internal/util"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
)

const remoteScriptPath = "tmp/collect_files_%d.sh" // Use /tmp, add timestamp
const remoteTarFilename = "remote_backup.tar.gz"   // Relative to user home

// collectFromServer handles the collection process for a single server
func collectFromServer(server string, cfg *config.Config, outputDir string, manifest *config.Manifest) error {
	log.Infof("[%s] Starting collection", server)

	// 1. Connect
	sshClient, err := sshutil.Connect(server, cfg.SSHConfig.Username, cfg.SSHConfig.KeyPath, cfg.SSHConfig.KeyPassphrase)
	if err != nil {
		return errors.Wrap(err, "failed to connect")
	}
	defer sshClient.Close()

	// Optional: Check sudo access early
	sshClient.CheckSudoAccess()

	// 2. Prepare and Upload Script
	scriptContent := util.GenerateCollectionScript(cfg.Files, cfg.Dirs, cfg.SSHConfig.Username)
	localScript, err := os.CreateTemp("", "collect_script_*.sh")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary script file")
	}
	localScriptPath := localScript.Name()
	defer os.Remove(localScriptPath) // Clean up local temp file

	if _, err := localScript.WriteString(scriptContent); err != nil {
		localScript.Close()
		return errors.Wrap(err, "failed to write to temporary script file")
	}
	localScript.Close() // Close before uploading

	// Use unique remote script name to avoid conflicts if run concurrently by same user
	// Script needs to be in a place the user can write to, like /tmp or $HOME
	remoteHomeDir := fmt.Sprintf("/home/%s", cfg.SSHConfig.Username)
	timestamp := time.Now().UnixNano()
	remoteScript := fmt.Sprintf("/tmp/collect_files_%d.sh", timestamp)

	if err := sshClient.UploadFile(localScriptPath, remoteScript); err != nil {
		return errors.Wrapf(err, "failed to upload script to %s", remoteScript)
	}
	log.Debugf("[%s] Collection script uploaded to %s", server, remoteScript)

	// 3. Make Script Executable
	_, _, err = sshClient.RunCommand(fmt.Sprintf("chmod +x %s", remoteScript), false) // No sudo needed for user's own file usually
	if err != nil {
		// Don't fail immediately on chmod error, script execution might still work
		log.Warnf("[%s] Failed to chmod script (continuing anyway): %v", server, err)
	}

	// 4. Run Script
	log.Infof("[%s] Running collection script...", server)
	stdout, stderr, err := sshClient.RunCommand(remoteScript, false) // Script uses sudo internally where needed
	log.Debugf("[%s] Script stdout:\n%s", server, stdout)
	if err != nil {
		log.Errorf("[%s] Collection script stderr:\n%s", server, stderr)
		// Attempt cleanup even if script failed
		cleanupErr := cleanupRemoteFiles(sshClient, remoteScript, remoteHomeDir)
		log.Warnf("[%s] Cleanup after script failure result: %v", server, cleanupErr)
		return errors.Wrapf(err, "collection script execution failed")
	}
	log.Infof("[%s] Collection script finished successfully.", server)

	// 5. Download Tarball
	remoteTarPath := fmt.Sprintf("%s/%s", remoteHomeDir, remoteTarFilename)
	localTarPath := filepath.Join(os.TempDir(), fmt.Sprintf("remote_backup_%s_%d.tar.gz", server, timestamp))
	log.Infof("[%s] Downloading %s...", server, remoteTarPath)
	err = sshClient.DownloadFile(remoteTarPath, localTarPath)
	defer os.Remove(localTarPath) // Clean up local tarball
	if err != nil {
		// Attempt cleanup even if download failed
		cleanupErr := cleanupRemoteFiles(sshClient, remoteScript, remoteHomeDir)
		log.Warnf("[%s] Cleanup after download failure result: %v", server, cleanupErr)
		return errors.Wrapf(err, "failed to download tarball %s", remoteTarPath)
	}
	log.Infof("[%s] Tarball downloaded to %s", server, localTarPath)

	// 6. Extract Tarball Locally
	// --- PATH UPDATED TO INCLUDE CollectedFilesBaseDir ---
	serverOutputDir := filepath.Join(outputDir, config.CollectedFilesBaseDir, fmt.Sprintf("files-%s", server))
	// --- END OF PATH UPDATE ---

	if err := os.RemoveAll(serverOutputDir); err != nil { // Clear previous contents
		log.Warnf("[%s] Failed to clear previous output directory %s: %v", server, serverOutputDir, err)
	}
	// MkdirAll ensures the nested structure <outputDir>/collected-files/files-<server>/ is created
	if err := os.MkdirAll(serverOutputDir, 0755); err != nil {
		return errors.Wrapf(err, "failed to create server output directory %s", serverOutputDir)
	}

	log.Infof("[%s] Extracting tarball to %s...", server, serverOutputDir)
	tarFile, err := os.Open(localTarPath)
	if err != nil {
		return errors.Wrapf(err, "failed to open local tarball %s", localTarPath)
	}
	err = util.ExtractTarGz(tarFile, serverOutputDir) // Pass the correct nested path
	tarFile.Close()                                   // Close file handle
	if err != nil {
		return errors.Wrapf(err, "failed to extract tarball %s", localTarPath)
	}

	// 7. Calculate Checksums and Update Manifest
	log.Infof("[%s] Calculating checksums for files in %s...", server, serverOutputDir)
	// The filepath.WalkDir and filepath.Rel logic here should still work correctly
	// as filepath.Rel calculates the path relative to the first argument (serverOutputDir)
	err = filepath.WalkDir(serverOutputDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Errorf("[%s] Error accessing path %s during walk: %v", server, path, err)
			return err // Propagate walk error
		}
		if !d.IsDir() {
			relativePath, _ := filepath.Rel(serverOutputDir, path)
			// Convert to forward slashes for consistency in manifest
			relativePath = filepath.ToSlash(relativePath)

			// Check if it's one of our MISSING marker files
			if strings.HasSuffix(relativePath, ".MISSING") || strings.HasSuffix(relativePath, "DIRECTORY.MISSING") {
				originalPath := strings.TrimSuffix(strings.TrimSuffix(relativePath, ".MISSING"), "DIRECTORY.MISSING")
				log.Warnf("[%s] Marked as missing on remote: %s", server, originalPath)
				manifest.AddFile(server, originalPath, "", "Missing on remote")
				return nil // Don't checksum marker files
			}

			checksum, csErr := util.CalculateSHA256(path)
			if csErr != nil {
				log.Errorf("[%s] Failed to calculate checksum for %s: %v", server, relativePath, csErr)
				// Record error in manifest
				manifest.AddFile(server, relativePath, "", csErr.Error())
			} else {
				log.Debugf("[%s] Checksum %s: %s", server, relativePath, checksum)
				manifest.AddFile(server, relativePath, checksum, "")
			}
		}
		return nil // Continue walking
	})
	if err != nil {
		log.Errorf("[%s] Error walking directory %s for checksums: %v", server, serverOutputDir, err)
		// Decide if this should be a fatal error for the server
	}

	// 8. Remote Cleanup
	log.Infof("[%s] Cleaning up remote files...", server)
	if err := cleanupRemoteFiles(sshClient, remoteScript, remoteHomeDir); err != nil {
		log.Warnf("[%s] Remote cleanup failed: %v", server, err) // Log but don't fail the whole process
	}

	log.Infof("[%s] Collection finished successfully", server)
	return nil
}

func cleanupRemoteFiles(sshClient *sshutil.Client, remoteScriptPath, remoteHomeDir string) error {
	remoteBackupDir := fmt.Sprintf("%s/remote_backup", remoteHomeDir)
	remoteTarPath := fmt.Sprintf("%s/%s", remoteHomeDir, remoteTarFilename)
	// Use sudo for rm -rf because parts of remote_backup might be owned by root
	command := fmt.Sprintf("rm -f %s && sudo rm -rf %s && rm -f %s", remoteScriptPath, remoteBackupDir, remoteTarPath)
	_, stderr, err := sshClient.RunCommand(command, false) // Run as user, sudo is embedded
	if err != nil {
		return errors.Wrapf(err, "remote cleanup command failed, stderr: %s", stderr)
	}
	return nil
}

// RunCollection orchestrates file collection from all servers concurrently
func RunCollection(cfg *config.Config, outputDir string, maxConcurrency int) bool {
	var wg sync.WaitGroup
	// Use a semaphore to limit concurrency
	sem := semaphore.NewWeighted(int64(maxConcurrency))
	errChan := make(chan error, len(cfg.Servers)) // Buffered channel to collect errors
	success := true                               // Track overall success

	// Create a shared manifest
	manifest := config.NewManifest()

	log.Infof("Starting collection from %d servers...", len(cfg.Servers))

	for _, server := range cfg.Servers {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			// Acquire semaphore, context for potential cancellation (optional)
			if err := sem.Acquire(context.Background(), 1); err != nil {
				log.Errorf("[%s] Failed to acquire semaphore: %v", s, err)
				errChan <- errors.Wrapf(err, "[%s] semaphore acquisition failed", s)
				return
			}
			defer sem.Release(1)

			// Execute collection for this server
			if err := collectFromServer(s, cfg, outputDir, manifest); err != nil {
				log.Errorf("[%s] Collection failed: %v", s, err)
				errChan <- errors.Wrapf(err, "[%s] collection error", s)
			}
		}(server)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan) // Close channel after all writers are done

	// Check for errors
	for err := range errChan {
		if err != nil {
			log.Error(err) // Log each specific error
			success = false
		}
	}

	if success {
		// Save the manifest only if all collections were successful (or adjust logic)
		if err := manifest.Save(outputDir); err != nil {
			log.Errorf("Failed to save manifest file: %v", err)
			success = false // Mark as failure if manifest cannot be saved
		}
	} else {
		log.Warn("Manifest not saved due to collection errors.")
	}

	return success
}
