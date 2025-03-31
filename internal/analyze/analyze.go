package analyze

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/brndnsvr/remote-diff-tool/internal/config"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
)

type fileComparisonResult struct {
	FilePath string
	IsDiff   bool
	Diffs    map[string]string // map[comparisonPair]diffOutput, e.g., "server1_vs_server2" -> "diff..."
	Errors   []string          // Errors encountered during comparison
}

// compareSingleFile performs checksum and content diff for one file path across servers
func compareSingleFile(
	filePath string,
	servers []string,
	manifest *config.Manifest,
	baseOutputDir string, // This is the main output dir (e.g., ".")
	saveDiffs bool,
	diffDir string,
	resultChan chan<- fileComparisonResult,
) {
	log.Debugf("Comparing file: %s", filePath)
	result := fileComparisonResult{FilePath: filePath}
	checksums := make(map[string]string)
	filePaths := make(map[string]string) // server -> absolute local path
	errorsFound := []string{}
	foundOnAll := true
	var firstChecksum string
	allMatch := true

	// 1. Gather checksums and check existence from manifest
	for i, server := range servers {
		info, exists := manifest.GetFileInfo(server, filePath)
		if !exists || info.Error != "" || info.Checksum == "" {
			msg := fmt.Sprintf("File %s not found or has error on server %s", filePath, server)
			if exists && info.Error != "" {
				msg = fmt.Sprintf("File %s has error on server %s: %s", filePath, server, info.Error)
			}
			log.Warn(msg)
			errorsFound = append(errorsFound, msg)
			foundOnAll = false
			// Continue checking other servers, but comparison won't happen
			continue // Don't record checksum if missing/error
		}

		// Store checksum
		checksums[server] = info.Checksum

		// --- PATH UPDATED TO INCLUDE CollectedFilesBaseDir ---
		// Construct the full path to the local file within the collected-files structure
		filePaths[server] = filepath.Join(baseOutputDir, config.CollectedFilesBaseDir, fmt.Sprintf("files-%s", server), filepath.FromSlash(filePath)) // Use local path separator
		// --- END OF PATH UPDATE ---

		// Compare checksum with the first one found
		if i == 0 {
			firstChecksum = info.Checksum
		} else if info.Checksum != firstChecksum {
			allMatch = false
		}
	}

	result.Errors = errorsFound

	// If not found on all servers, cannot compare
	if !foundOnAll {
		log.Warnf("Skipping comparison for %s: File not present or has errors on all servers.", filePath)
		result.IsDiff = true // Treat as different if not consistently present/valid
		resultChan <- result
		return
	}

	// 2. Compare checksums
	if allMatch {
		log.Infof("Checksums match for %s across all servers.", filePath)
		result.IsDiff = false
		resultChan <- result
		return
	}

	// 3. Checksums differ, perform content diff
	log.Infof("Checksums differ for %s. Performing content diff...", filePath)
	result.IsDiff = true // Mark as different
	result.Diffs = make(map[string]string)

	// Pairwise comparison using external `diff` command
	for i := 0; i < len(servers); i++ {
		for j := i + 1; j < len(servers); j++ {
			server1 := servers[i]
			server2 := servers[j]
			path1 := filePaths[server1]
			path2 := filePaths[server2]

			// Check if local files exist before diffing (they should based on manifest check)
			if _, err := os.Stat(path1); os.IsNotExist(err) {
				msg := fmt.Sprintf("Local file missing for diff: %s", path1)
				log.Error(msg)
				result.Errors = append(result.Errors, msg)
				continue
			}
			if _, err := os.Stat(path2); os.IsNotExist(err) {
				msg := fmt.Sprintf("Local file missing for diff: %s", path2)
				log.Error(msg)
				result.Errors = append(result.Errors, msg)
				continue
			}

			cmd := exec.Command("diff", "-u", path1, path2) // -u for unified diff format
			var out bytes.Buffer
			cmd.Stdout = &out
			err := cmd.Run()

			diffOutput := out.String()

			if err != nil {
				// `diff` exits with status 1 if files differ, 0 if same, >1 on error
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
					// This is expected when files differ
					log.Infof("Differences found between %s:%s and %s:%s", server1, filePath, server2, filePath)
					comparisonKey := fmt.Sprintf("%s_vs_%s", server1, server2)
					result.Diffs[comparisonKey] = diffOutput

					// Save diff if requested
					if saveDiffs && diffDir != "" {
						diffFileName := fmt.Sprintf("%s__%s_vs_%s.diff", strings.ReplaceAll(filePath, "/", "_"), server1, server2)
						diffFilePath := filepath.Join(diffDir, diffFileName)
						if err := os.MkdirAll(filepath.Dir(diffFilePath), 0755); err != nil {
							log.Errorf("Failed to create diff output directory %s: %v", filepath.Dir(diffFilePath), err)
						} else {
							if err := os.WriteFile(diffFilePath, []byte(diffOutput), 0644); err != nil {
								log.Errorf("Failed to write diff file %s: %v", diffFilePath, err)
							} else {
								log.Debugf("Diff saved to %s", diffFilePath)
							}
						}
					}

				} else {
					// Actual error running diff command
					msg := fmt.Sprintf("Error running diff for %s vs %s: %v", path1, path2, err)
					log.Errorf(msg)
					result.Errors = append(result.Errors, msg)
				}
			} else {
				// Diff exit code 0 means files are identical, contradicting checksum diff. Log warning.
				log.Warnf("Checksums differed but 'diff' command reported no differences for %s between %s and %s. Check file contents.", filePath, server1, server2)
				// Could still store an empty diff if needed: result.Diffs[comparisonKey] = ""
			}
		}
	}

	resultChan <- result
}

// getFilesToCompare finds the intersection of files present in the manifest for all servers
func getFilesToCompare(servers []string, manifest *config.Manifest) []string {
	if len(servers) == 0 {
		return []string{}
	}

	fileCounts := make(map[string]int) // filePath -> count of servers it appears on
	allFiles := make(map[string]bool)  // Set of all unique filePaths across all servers

	manifest.Mu.RLock() // Lock manifest for reading
	defer manifest.Mu.RUnlock()

	for _, server := range servers {
		serverFiles, ok := manifest.FilesByServer[server]
		if !ok {
			log.Warnf("No files found in manifest for server: %s", server)
			continue // Skip server if it's not in the manifest
		}
		for filePath, info := range serverFiles {
			if info.Error == "" { // Only count valid files
				fileCounts[filePath]++
				allFiles[filePath] = true
			}
		}
	}

	commonFiles := []string{}
	numServers := len(servers)
	for filePath := range allFiles {
		if count, ok := fileCounts[filePath]; ok && count == numServers {
			commonFiles = append(commonFiles, filePath)
		} else {
			// Log files present only on some servers
			presentOn := []string{}
			missingOn := []string{}
			for _, server := range servers {
				// Ensure we re-check inside the map safely
				var info config.FileInfo
				var exists bool
				if serverData, serverOK := manifest.FilesByServer[server]; serverOK {
					info, exists = serverData[filePath]
				}

				if exists && info.Error == "" {
					presentOn = append(presentOn, server)
				} else {
					missingOn = append(missingOn, server)
				}
			}
			log.Warnf("File %s is not present/valid on all servers. Present: [%s], Missing/Error: [%s]. Skipping comparison.",
				filePath, strings.Join(presentOn, ","), strings.Join(missingOn, ","))

		}
	}

	sort.Strings(commonFiles) // Sort for consistent order
	return commonFiles
}

// RunAnalysis orchestrates the file comparison process
func RunAnalysis(cfg *config.Config, outputDir, diffDir string, saveDiffs bool, maxConcurrency int) (bool, error) {
	log.Info("Starting analysis...")

	// 1. Load Manifest (Uses updated path via LoadManifest internally)
	manifest, err := config.LoadManifest(outputDir)
	if err != nil {
		return false, errors.Wrap(err, "failed to load manifest for analysis")
	}

	// --- PATH UPDATED FOR DIRECTORY CHECK ---
	// Verify collection directories exist for all servers in config
	log.Debugf("Verifying existence of collection directories in %s/%s/files-*", outputDir, config.CollectedFilesBaseDir)
	for _, server := range cfg.Servers {
		serverDir := filepath.Join(outputDir, config.CollectedFilesBaseDir, fmt.Sprintf("files-%s", server))
		if _, err := os.Stat(serverDir); os.IsNotExist(err) {
			return false, fmt.Errorf("collection directory %s not found. Run 'collect' first", serverDir)
		} else if err != nil {
			return false, errors.Wrapf(err, "failed to stat collection directory %s", serverDir)
		}
	}
	// --- END OF PATH UPDATE ---

	// 2. Determine Files to Compare (Intersection based on manifest)
	filesToCompare := getFilesToCompare(cfg.Servers, manifest)
	if len(filesToCompare) == 0 {
		log.Warn("No common files found across all servers based on the manifest. Analysis finished.")
		return false, nil // No diffs found as no files compared
	}
	log.Infof("Found %d common files to compare.", len(filesToCompare))

	// Prepare diff directory if saving
	if saveDiffs {
		if err := os.MkdirAll(diffDir, 0755); err != nil {
			return false, errors.Wrapf(err, "failed to create diff output directory %s", diffDir)
		}
		log.Infof("Saving diffs to %s", diffDir)
	}

	// 3. Parallel Comparison
	var wg sync.WaitGroup
	sem := semaphore.NewWeighted(int64(maxConcurrency)) // Limit concurrent diff processes
	resultChan := make(chan fileComparisonResult, len(filesToCompare))
	analysisErrors := []error{}
	var errMu sync.Mutex // Mutex for safely appending to analysisErrors

	for _, filePath := range filesToCompare {
		wg.Add(1)
		go func(fp string) {
			defer wg.Done()
			if err := sem.Acquire(context.Background(), 1); err != nil {
				log.Errorf("Failed to acquire semaphore for %s: %v", fp, err)
				errMu.Lock()
				analysisErrors = append(analysisErrors, errors.Wrapf(err, "semaphore error for %s", fp))
				errMu.Unlock()
				// Send a partial result indicating error? Or just log?
				return
			}
			defer sem.Release(1)

			compareSingleFile(fp, cfg.Servers, manifest, outputDir, saveDiffs, diffDir, resultChan) // Pass baseOutputDir

		}(filePath)
	}

	// Close the channel once all workers are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 4. Collect Results and Summarize
	totalCompared := 0
	totalDifferent := 0
	totalIdentical := 0
	anyDiffFound := false

	fmt.Println("\n===== Analysis Results =====") // Print separator before results start streaming

	for result := range resultChan {
		totalCompared++
		// Log errors encountered for this file path
		for _, errMsg := range result.Errors {
			log.Errorf("Error comparing %s: %s", result.FilePath, errMsg)
		}

		if result.IsDiff {
			anyDiffFound = true
			totalDifferent++
			fmt.Printf("\n--- Differences found in: %s ---\n", result.FilePath)
			// Print collected diffs to stdout
			// Sort keys for consistent output order
			keys := make([]string, 0, len(result.Diffs))
			for k := range result.Diffs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("--- Diff %s ---\n%s\n", k, result.Diffs[k])
			}
		} else {
			totalIdentical++
			fmt.Printf("--- Identical: %s ---\n", result.FilePath)
		}
	}

	fmt.Println("\n===== Analysis Summary =====")
	fmt.Printf("Total files compared: %d\n", totalCompared)
	fmt.Printf("Identical files:      %d\n", totalIdentical)
	fmt.Printf("Files with diffs:   %d\n", totalDifferent)

	// Report any general analysis errors
	errMu.Lock()
	finalError := analysisErrors // Copy slice under lock
	errMu.Unlock()
	if len(finalError) > 0 {
		log.Errorf("%d errors occurred during analysis phase:", len(finalError))
		for _, e := range finalError {
			log.Error(e)
		}
		return anyDiffFound, fmt.Errorf("analysis completed with %d errors", len(finalError))
	}

	log.Info("Analysis finished.")
	return anyDiffFound, nil
}
