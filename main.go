package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/brndnsvr/remote-diff-tool/internal/analyze"
	"github.com/brndnsvr/remote-diff-tool/internal/collect"
	"github.com/brndnsvr/remote-diff-tool/internal/config"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	serversStr     string
	filesStr       string
	dirsStr        string
	outputDir      string
	saveDiffs      bool
	diffDir        string
	logFile        string
	logLevel       string
	maxConcurrency int
)

// main.go (Replace the setupLogging function)

func setupLogging() {
	level, err := log.ParseLevel(logLevel)
	if err != nil {
		log.Warnf("Invalid log level '%s', defaulting to 'info'", logLevel)
		level = log.InfoLevel
	}
	log.SetLevel(level)
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})

	// Default to stderr initially
	log.SetOutput(os.Stderr)

	// Determine log file path
	effectiveLogFile := logFile // Use user-provided path if available
	if effectiveLogFile == "" {
		// Default path construction within ./logs/ subdirectory
		defaultLogDir := "logs"
		if err := os.MkdirAll(defaultLogDir, 0755); err != nil {
			log.Errorf("Failed to create default log directory %s: %v. Logging to stderr.", defaultLogDir, err)
			return // Keep logging to stderr if dir creation fails
		}
		effectiveLogFile = filepath.Join(defaultLogDir, fmt.Sprintf("remote_diff_%s.log", time.Now().Format("20060102_150405")))
		log.Infof("Logging to default file: %s", effectiveLogFile)
	} else {
		// Ensure directory exists for user-specified log file
		logDir := filepath.Dir(effectiveLogFile)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			log.Errorf("Failed to create log directory %s for specified log file: %v. Logging to stderr.", logDir, err)
			return // Keep logging to stderr
		}
		log.Infof("Logging to specified file: %s", effectiveLogFile)
	}

	// Open and set the log file
	file, err := os.OpenFile(effectiveLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0660)
	if err == nil {
		log.SetOutput(file) // Log only to file
		// If you want both file and stderr:
		// log.SetOutput(io.MultiWriter(os.Stderr, file))
	} else {
		log.Errorf("Failed to open log file %s: %v. Logging to stderr.", effectiveLogFile, err)
		// Fallback to stderr already set
	}
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "remote-diff-tool",
		Short: "Collects files from remote servers and analyzes differences.",
		Long: `Remote Filesystem Diff Tool
Handles:
1. Concurrent collection of files/dirs from remote servers via SSH.
2. Efficient comparison using checksums and parallel diffing.`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			setupLogging()
		},
	}

	rootCmd.PersistentFlags().StringVarP(&outputDir, "output-dir", "o", ".", "Directory to store collected files and config")
	rootCmd.PersistentFlags().IntVarP(&maxConcurrency, "concurrency", "c", 10, "Maximum number of concurrent server operations")
	rootCmd.PersistentFlags().StringVar(&logFile, "log-file", "", "Path to log file (defaults to remote_diff_YYYYMMDD_HHMMSS.log)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")

	collectCmd := &cobra.Command{
		Use:   "collect",
		Short: "Collect files from remote servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadOrInitializeConfig(outputDir, serversStr, filesStr, dirsStr, true)
			if err != nil {
				return err
			}
			log.Infof("Starting collection with concurrency %d", maxConcurrency)
			success := collect.RunCollection(cfg, outputDir, maxConcurrency)
			if !success {
				return fmt.Errorf("collection completed with errors")
			}
			log.Info("Collection finished successfully")
			return nil
		},
	}
	collectCmd.Flags().StringVarP(&serversStr, "servers", "s", "", "Comma-separated list of server hostnames (required if no config.json)")
	collectCmd.Flags().StringVarP(&filesStr, "files", "f", "", "Comma-separated list of absolute file paths")
	collectCmd.Flags().StringVarP(&dirsStr, "dirs", "d", "", "Comma-separated list of absolute directory paths")

	analyzeCmd := &cobra.Command{
		Use:   "analyze",
		Short: "Analyze differences between collected files",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadOrInitializeConfig(outputDir, "", "", "", false) // Don't overwrite if reading for analyze
			if err != nil {
				log.Errorf("Failed to load config: %v. Did you run 'collect' first?", err)
				return err
			}
			log.Infof("Starting analysis with concurrency %d", maxConcurrency)
			diffFound, err := analyze.RunAnalysis(cfg, outputDir, diffDir, saveDiffs, maxConcurrency)
			if err != nil {
				return fmt.Errorf("analysis failed: %w", err)
			}
			if diffFound {
				log.Warn("Analysis finished: Differences found.")
				// Optionally exit with non-zero status if differences found
				// os.Exit(1)
			} else {
				log.Info("Analysis finished: No differences found.")
			}
			return nil
		},
	}
	analyzeCmd.Flags().BoolVar(&saveDiffs, "save-diffs", false, "Save diff outputs to files")
	analyzeCmd.Flags().StringVar(&diffDir, "diff-dir", "./diff_output", "Directory to store diff files")

	allCmd := &cobra.Command{
		Use:   "all",
		Short: "Perform both collection and analysis",
		RunE: func(cmd *cobra.Command, args []string) error {
			// --- Collection Phase ---
			cfg, err := config.LoadOrInitializeConfig(outputDir, serversStr, filesStr, dirsStr, true)
			if err != nil {
				return err
			}
			log.Infof("Starting collection (part of 'all') with concurrency %d", maxConcurrency)
			success := collect.RunCollection(cfg, outputDir, maxConcurrency)
			if !success {
				return fmt.Errorf("collection step failed, aborting analysis")
			}
			log.Info("Collection finished successfully")

			// --- Analysis Phase ---
			// Re-read config in case it was just created/updated
			cfg, err = config.LoadOrInitializeConfig(outputDir, "", "", "", false)
			if err != nil {
				log.Errorf("Failed to load config for analysis: %v", err)
				return err
			}
			log.Infof("Starting analysis (part of 'all') with concurrency %d", maxConcurrency)
			diffFound, err := analyze.RunAnalysis(cfg, outputDir, diffDir, saveDiffs, maxConcurrency)
			if err != nil {
				return fmt.Errorf("analysis step failed: %w", err)
			}
			if diffFound {
				log.Warn("Analysis finished: Differences found.")
			} else {
				log.Info("Analysis finished: No differences found.")
			}
			return nil
		},
	}
	// Inherit flags from collect and analyze where applicable
	allCmd.Flags().StringVarP(&serversStr, "servers", "s", "", "Comma-separated list of server hostnames (required if no config.json)")
	allCmd.Flags().StringVarP(&filesStr, "files", "f", "", "Comma-separated list of absolute file paths")
	allCmd.Flags().StringVarP(&dirsStr, "dirs", "d", "", "Comma-separated list of absolute directory paths")
	allCmd.Flags().BoolVar(&saveDiffs, "save-diffs", false, "Save diff outputs to files")
	allCmd.Flags().StringVar(&diffDir, "diff-dir", "./diff_output", "Directory to store diff files")

	rootCmd.AddCommand(collectCmd, analyzeCmd, allCmd)

	if err := rootCmd.Execute(); err != nil {
		log.Errorf("Error: %v", err)
		os.Exit(1)
	}
}
