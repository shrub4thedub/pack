package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultRepo = "https://github.com/shrub4thedub/pack-repo"
	packDir     = ".pack"
)

type Config struct {
	Sources []string
}

func main() {
	// Ensure pack directory structure exists
	if err := ensurePackDirExists(); err != nil {
		fmt.Printf("failed to create pack directory: %v\n", err)
		os.Exit(1)
	}

	var rootCmd = &cobra.Command{
		Use:   "pack",
		Short: "a package manager using boxlang",
		Long:  `pack fetches and executes boxlang installation recipes from a repository.`,
	}
	rootCmd.SetHelpCommand(&cobra.Command{
		Use:    "no-help",
		Hidden: true,
	})

	var openCmd = &cobra.Command{
		Use:   "open [package|help]",
		Short: "install a package",
		Args:  cobra.ExactArgs(1),
		Run:   openPackage,
	}

	var closeCmd = &cobra.Command{
		Use:   "close [package|help]",
		Short: "uninstall a package",
		Args:  cobra.ExactArgs(1),
		Run:   closePackage,
	}

	var peekCmd = &cobra.Command{
		Use:   "peek [package|help]",
		Short: "show package information",
		Args:  cobra.ExactArgs(1),
		Run:   peekPackage,
	}

	var addSourceCmd = &cobra.Command{
		Use:   "add-source [url|help]",
		Short: "add a repository source",
		Args:  cobra.ExactArgs(1),
		Run:   addSource,
	}

	var listCmd = &cobra.Command{
		Use:   "list [help]",
		Short: "list installed packages",
		Args:  cobra.MaximumNArgs(1),
		Run:   listPackages,
	}

	var updateCmd = &cobra.Command{
		Use:   "update [help]",
		Short: "check for and install package updates",
		Args:  cobra.MaximumNArgs(1),
		Run:   updatePackages,
	}

	var helpCmd = &cobra.Command{
		Use:   "help",
		Short: "show help information",
		Args:  cobra.NoArgs,
		Run:   showHelp,
	}

	rootCmd.AddCommand(openCmd)
	rootCmd.AddCommand(closeCmd)
	rootCmd.AddCommand(peekCmd)
	rootCmd.AddCommand(addSourceCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(helpCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}


func openPackage(cmd *cobra.Command, args []string) {
	if args[0] == "help" {
		showOpenHelp()
		return
	}
	
	packageName := args[0]
	fmt.Printf("opening package: %s\n", packageName)
	
	if err := executePackageScript(packageName, false); err != nil {
		fmt.Printf("error opening package %s: %v\n", packageName, err)
		os.Exit(1)
	}
}

func closePackage(cmd *cobra.Command, args []string) {
	if args[0] == "help" {
		showCloseHelp()
		return
	}
	
	packageName := args[0]
	fmt.Printf("closing package: %s\n", packageName)
	
	if err := executeUninstallScript(packageName); err != nil {
		fmt.Printf("error closing package %s: %v\n", packageName, err)
		os.Exit(1)
	}
}

func peekPackage(cmd *cobra.Command, args []string) {
	if args[0] == "help" {
		showPeekHelp()
		return
	}
	
	packageName := args[0]
	
	if err := showPackageInfo(packageName); err != nil {
		fmt.Printf("error showing package info for %s: %v\n", packageName, err)
		os.Exit(1)
	}
}

func addSource(cmd *cobra.Command, args []string) {
	if args[0] == "help" {
		showAddSourceHelp()
		return
	}
	
	sourceURL := args[0]
	
	if err := addSourceToConfig(sourceURL); err != nil {
		fmt.Printf("error adding source: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Printf("added source: %s\n", sourceURL)
}

func executePackageScript(packageName string, uninstall bool) error {
	// Uninstall is now handled by executeUninstallScript
	if uninstall {
		return executeUninstallScript(packageName)
	}
	// Create temporary directory for script
	tempDir, err := os.MkdirTemp("", "pack-"+packageName)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Download or copy script using multi-source selection
	scriptPath := filepath.Join(tempDir, packageName+".box")
	
	selectedSource, err := downloadFromSources(packageName, scriptPath)
	if err != nil {
		return fmt.Errorf("failed to download script: %v", err)
	}
	
	// Note: selectedSource.Name used to be stored as sourceURL, now using repoName instead

	// Verify recipe integrity
	fmt.Println("verifying recipe integrity...")
	if err := verifyRecipeIntegrity(scriptPath); err != nil {
		fmt.Printf("⚠️  warning: %v\n", err)
		fmt.Print("continue anyway? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %v", err)
		}
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			return fmt.Errorf("installation cancelled due to verification failure")
		}
	} else {
		fmt.Println("✓ recipe integrity verified")
	}

	// Show recipe and get user confirmation
	if err := showRecipeAndConfirm(scriptPath); err != nil {
		return err
	}

	// Find box executable
	boxPath, err := findBoxExecutable()
	if err != nil {
		return fmt.Errorf("box executable not found: %v", err)
	}

	// Execute script
	cmdArgs := []string{scriptPath}

	execCmd := exec.Command(boxPath, cmdArgs...)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Stdin = os.Stdin

	err = execCmd.Run()
	if err != nil {
		return err
	}

	// Create or update lock file after successful installation
	fmt.Println("creating lock file...")
	
	// Extract source URL from recipe
	recipeSourceURL, err := extractRecipeURL(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to extract source URL: %v\n", err)
		recipeSourceURL = "unknown"
	}
	
	// Detect source type and get version information
	sourceType, sourceVersion, err := detectSourceTypeAndVersion(recipeSourceURL)
	if err != nil {
		fmt.Printf("warning: failed to detect source type: %v\n", err)
		sourceType = "unknown"
		sourceVersion = "unknown"
	}
	
	// Calculate recipe version (content hash)
	recipeVersion, err := calculateRecipeVersion(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to calculate recipe version: %v\n", err)
		recipeVersion = "unknown"
	}
	
	// Construct recipe URL from selected source
	recipeURL := constructRecipeURL(selectedSource, packageName)
	
	// Calculate actual SHA256 for lock file
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to read script for hash: %v\n", err)
	}
	contentWithoutSHA256, err := removeCSHA256Field(content)
	if err != nil {
		fmt.Printf("warning: failed to remove SHA256 field: %v\n", err)
	}
	recipeSHA256 := calculateSHA256(contentWithoutSHA256)
	
	// Get repo name from selected source
	repoName := selectedSource.Name
	
	if err := createLockFile(packageName, repoName, recipeSourceURL, sourceType, sourceVersion, recipeVersion, recipeURL, recipeSHA256); err != nil {
		fmt.Printf("warning: failed to create lock file: %v\n", err)
	} else {
		fmt.Println("✓ lockfile created")
	}

	return nil
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func downloadFile(url, filepath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func findBoxExecutable() (string, error) {
	// Try to find box in PATH
	if boxPath, err := exec.LookPath("box"); err == nil {
		return boxPath, nil
	}

	// Try relative path to boxlang directory
	currentDir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Try ../boxlang/box (assuming pack is sibling to boxlang)
	relativePath := filepath.Join(filepath.Dir(currentDir), "boxlang", "box")
	if _, err := os.Stat(relativePath); err == nil {
		absPath, err := filepath.Abs(relativePath)
		if err == nil {
			return absPath, nil
		}
	}

	// Try ./box in current directory
	localPath := "./box"
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	return "", fmt.Errorf("box executable not found in PATH or relative paths")
}

func showRecipeAndConfirm(scriptPath string) error {
	// Read and display the script content
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script: %v", err)
	}

	fmt.Println("recipe:")
	fmt.Println("-------")
	fmt.Println(string(content))
	fmt.Println("-------")
	
	for {
		fmt.Print("proceed? [y/e/n]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %v", err)
		}
		
		response = strings.TrimSpace(strings.ToLower(response))
		
		switch response {
		case "y", "yes", "":
			return nil
		case "n", "no":
			return fmt.Errorf("cancelled")
		case "e", "edit":
			if err := editScript(scriptPath); err != nil {
				fmt.Printf("error editing script: %v\n", err)
				continue
			}
			// Show the updated script and ask again
			return showRecipeAndConfirm(scriptPath)
		default:
			fmt.Println("enter y/e/n")
		}
	}
}

func editScript(scriptPath string) error {
	// Try to find an editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		// Try common editors
		for _, e := range []string{"vim", "nano", "vi"} {
			if _, err := exec.LookPath(e); err == nil {
				editor = e
				break
			}
		}
	}
	if editor == "" {
		return fmt.Errorf("no editor found")
	}

	// Launch editor
	cmd := exec.Command(editor, scriptPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func showPackageInfo(packageName string) error {
	// Create temporary directory for script
	tempDir, err := os.MkdirTemp("", "pack-peek-"+packageName)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Download or copy script using multi-source selection
	scriptPath := filepath.Join(tempDir, packageName+".box")
	
	_, err = downloadFromSources(packageName, scriptPath)
	if err != nil {
		return fmt.Errorf("failed to download script: %v", err)
	}

	// Parse and display package information
	return parseAndDisplayPackageInfo(scriptPath, packageName)
}

func parseAndDisplayPackageInfo(scriptPath, packageName string) error {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script: %v", err)
	}

	lines := strings.Split(string(content), "\n")
	var pkgData map[string]string
	var inDataBlock bool
	var blockIndent int

	// Parse data block
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Check for data block start
		if strings.HasPrefix(trimmed, "[data") {
			inDataBlock = true
			pkgData = make(map[string]string)
			// Calculate indentation level
			blockIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			continue
		}
		
		// Check for block end
		if inDataBlock && trimmed == "end" {
			break
		}
		
		// Check for any other block start (should end data block)
		if inDataBlock && strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[data") {
			break
		}
		
		// Parse data block content
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			// Only process lines that are indented more than the block header
			lineIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			if lineIndent > blockIndent {
				parts := strings.SplitN(trimmed, " ", 2)
				if len(parts) >= 1 {
					key := parts[0]
					var value string
					if len(parts) == 2 {
						value = strings.Trim(parts[1], "\"")
					}
					pkgData[key] = value
				}
			}
		}
	}

	// Display package information
	fmt.Printf("package: %s\n", packageName)
	fmt.Println("--------")
	
	if pkgData == nil || len(pkgData) == 0 {
		fmt.Println("no package information available")
		return nil
	}

	// Display fields in a simple format
	if name, ok := pkgData["name"]; ok {
		fmt.Printf("name: %s\n", name)
	}
	if desc, ok := pkgData["desc"]; ok {
		fmt.Printf("desc: %s\n", desc)
	}
	if ver, ok := pkgData["ver"]; ok {
		fmt.Printf("version: %s\n", ver)
	}
	if os, ok := pkgData["os"]; ok {
		fmt.Printf("os: %s\n", os)
	}
	if author, ok := pkgData["author"]; ok {
		fmt.Printf("author: %s\n", author)
	}
	if url, ok := pkgData["url"]; ok {
		fmt.Printf("url: %s\n", url)
	}
	if license, ok := pkgData["license"]; ok {
		fmt.Printf("license: %s\n", license)
	}

	// Display any other fields
	for key, value := range pkgData {
		switch key {
		case "name", "desc", "ver", "os", "author", "url", "license":
			// Already displayed above
		default:
			fmt.Printf("%s: %s\n", key, value)
		}
	}

	return nil
}

func getPackDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	
	packPath := filepath.Join(homeDir, packDir)
	return packPath, nil
}

func getConfigPath() (string, error) {
	packPath, err := getPackDir()
	if err != nil {
		return "", err
	}
	
	configPath := filepath.Join(packPath, "config")
	return configPath, nil
}

func getLocalRepoPath() (string, error) {
	packPath, err := getPackDir()
	if err != nil {
		return "", err
	}
	
	localPath := filepath.Join(packPath, "local")
	return localPath, nil
}

func getStorePath() (string, error) {
	packPath, err := getPackDir()
	if err != nil {
		return "", err
	}
	
	storePath := filepath.Join(packPath, "store")
	return storePath, nil
}

func ensurePackDirExists() error {
	packPath, err := getPackDir()
	if err != nil {
		return err
	}
	
	// Create main pack directory
	if err := os.MkdirAll(packPath, 0755); err != nil {
		return err
	}
	
	// Create subdirectories
	subdirs := []string{"locks", "config", "tmp", "local", "store"}
	for _, subdir := range subdirs {
		subdirPath := filepath.Join(packPath, subdir)
		if err := os.MkdirAll(subdirPath, 0755); err != nil {
			return err
		}
	}
	
	return nil
}

func ensureConfigExists() error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}
	
	configFile := filepath.Join(configPath, "sources.box")
	
	// Check if config file exists
	if _, err := os.Stat(configFile); err == nil {
		return nil // Config exists
	}
	
	// Create default config
	defaultConfig := `[data -c sources]
  repo ` + defaultRepo + `
end`
	
	return os.WriteFile(configFile, []byte(defaultConfig), 0644)
}

func loadConfig() (*Config, error) {
	if err := ensureConfigExists(); err != nil {
		return nil, err
	}
	
	configPath, err := getConfigPath()
	if err != nil {
		return nil, err
	}
	
	configFile := filepath.Join(configPath, "sources.box")
	content, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}
	
	config := &Config{}
	lines := strings.Split(string(content), "\n")
	var inDataBlock bool
	var blockIndent int
	
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		if strings.HasPrefix(trimmed, "[data") && strings.Contains(trimmed, "sources") {
			inDataBlock = true
			blockIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			continue
		}
		
		if inDataBlock && trimmed == "end" {
			break
		}
		
		if inDataBlock && strings.HasPrefix(trimmed, "[") && !strings.Contains(trimmed, "sources") {
			break
		}
		
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			lineIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			if lineIndent > blockIndent {
				parts := strings.SplitN(trimmed, " ", 2)
				if len(parts) >= 2 {
					config.Sources = append(config.Sources, parts[1])
				}
			}
		}
	}
	
	return config, nil
}

func saveConfig(config *Config) error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}
	
	configFile := filepath.Join(configPath, "sources.box")
	
	var content strings.Builder
	content.WriteString("[data -c sources]\n")
	
	for _, source := range config.Sources {
		content.WriteString("  repo " + source + "\n")
	}
	
	content.WriteString("end\n")
	
	return os.WriteFile(configFile, []byte(content.String()), 0644)
}

func addSourceToConfig(sourceURL string) error {
	config, err := loadConfig()
	if err != nil {
		return err
	}
	
	// Check if source already exists
	for _, existing := range config.Sources {
		if existing == sourceURL {
			return fmt.Errorf("source already exists")
		}
	}
	
	config.Sources = append(config.Sources, sourceURL)
	return saveConfig(config)
}

// PackageSource represents a source where a package is available
type PackageSource struct {
	Name string
	URL  string
	Type string // "remote" or "local"
}

// findAvailableSources finds all sources where a package is available
func findAvailableSources(packageName string) ([]PackageSource, error) {
	var availableSources []PackageSource
	
	// Check local source if it exists
	localRepoPath, err := getLocalRepoPath()
	if err == nil {
		localPackagePath := filepath.Join(localRepoPath, packageName+".box")
		if _, err := os.Stat(localPackagePath); err == nil {
			availableSources = append(availableSources, PackageSource{
				Name: "local",
				URL:  localPackagePath,
				Type: "local",
			})
		}
	}
	
	// Check configured remote sources
	config, err := loadConfig()
	if err == nil && len(config.Sources) > 0 {
		for _, source := range config.Sources {
			// Try raw github content URL format
			scriptURL := fmt.Sprintf("%s/raw/main/%s.box", source, packageName)
			if strings.Contains(source, "raw.githubusercontent.com") {
				scriptURL = fmt.Sprintf("%s/%s.box", source, packageName)
			}
			
			// Test if package exists at this source (lightweight check)
			if testPackageExists(scriptURL) {
				availableSources = append(availableSources, PackageSource{
					Name: source,
					URL:  scriptURL,
					Type: "remote",
				})
			}
		}
	}
	
	return availableSources, nil
}

// testPackageExists does a lightweight test to see if a package exists at a URL
func testPackageExists(url string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// promptSourceSelection lets user choose from multiple sources
func promptSourceSelection(packageName string, sources []PackageSource) (PackageSource, error) {
	fmt.Printf("\nPackage '%s' is available from multiple sources:\n\n", packageName)
	
	for i, source := range sources {
		if source.Type == "local" {
			fmt.Printf("%d) Local repository\n", i+1)
		} else {
			fmt.Printf("%d) %s\n", i+1, source.Name)
		}
	}
	
	fmt.Print("\nSelect source [1]: ")
	
	var input string
	fmt.Scanln(&input)
	
	if input == "" {
		input = "1"
	}
	
	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(sources) {
		return PackageSource{}, fmt.Errorf("invalid selection")
	}
	
	return sources[choice-1], nil
}

func downloadFromSources(packageName string, scriptPath string) (PackageSource, error) {
	// Find all available sources
	sources, err := findAvailableSources(packageName)
	if err != nil {
		return PackageSource{}, err
	}
	
	if len(sources) == 0 {
		return PackageSource{}, fmt.Errorf("package '%s' not found in any configured source", packageName)
	}
	
	var selectedSource PackageSource
	
	if len(sources) == 1 {
		// Only one source available, use it
		selectedSource = sources[0]
		if selectedSource.Type == "local" {
			fmt.Printf("Found package in local repository\n")
		} else {
			fmt.Printf("Found package at %s\n", selectedSource.Name)
		}
	} else {
		// Multiple sources available, let user choose
		selectedSource, err = promptSourceSelection(packageName, sources)
		if err != nil {
			return PackageSource{}, err
		}
	}
	
	fmt.Printf("Using source: %s\n", selectedSource.Name)
	
	// Download from selected source
	if selectedSource.Type == "local" {
		err = copyFile(selectedSource.URL, scriptPath)
	} else {
		err = downloadFile(selectedSource.URL, scriptPath)
	}
	
	return selectedSource, err
}

// calculateSHA256 calculates the SHA256 hash of file contents
func calculateSHA256(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// extractSHA256FromRecipe parses the sha256 field from a recipe's data block
func extractSHA256FromRecipe(scriptPath string) (string, error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", fmt.Errorf("failed to read script: %v", err)
	}

	lines := strings.Split(string(content), "\n")
	var inDataBlock bool
	var blockIndent int

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Check for data block start
		if strings.HasPrefix(trimmed, "[data") {
			inDataBlock = true
			blockIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			continue
		}
		
		// Check for block end
		if inDataBlock && trimmed == "end" {
			break
		}
		
		// Check for any other block start
		if inDataBlock && strings.HasPrefix(trimmed, "[") {
			break
		}
		
		// Parse data block content for sha256 field
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			lineIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			if lineIndent > blockIndent {
				parts := strings.SplitN(trimmed, " ", 2)
				if len(parts) >= 2 && parts[0] == "sha256" {
					return strings.TrimSpace(parts[1]), nil
				}
			}
		}
	}
	
	return "", fmt.Errorf("sha256 field not found in recipe data block")
}

// verifyRecipeIntegrity verifies the SHA256 hash of a recipe
func verifyRecipeIntegrity(scriptPath string) error {
	// Read the file content
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script: %v", err)
	}
	
	// Extract expected hash from recipe
	expectedHash, err := extractSHA256FromRecipe(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to extract expected hash: %v", err)
	}
	
	// Calculate actual hash excluding the SHA256 field
	contentWithoutSHA256, err := removeCSHA256Field(content)
	if err != nil {
		return fmt.Errorf("failed to remove SHA256 field: %v", err)
	}
	actualHash := calculateSHA256(contentWithoutSHA256)
	
	// Compare hashes
	if actualHash != expectedHash {
		return fmt.Errorf("SHA256 verification failed: expected %s, got %s", expectedHash, actualHash)
	}
	
	return nil
}

// getLockFilePath returns the path to a package's lock file
func getLockFilePath(packageName string) (string, error) {
	packPath, err := getPackDir()
	if err != nil {
		return "", err
	}
	
	lockPath := filepath.Join(packPath, "locks", packageName+".lock")
	return lockPath, nil
}

// createLockFile creates a lock file for an installed package with comprehensive update information
func createLockFile(packageName, repo, sourceURL, sourceType, sourceVersion, recipeVersion, recipeURL, recipeSHA256 string) error {
	lockFilePath, err := getLockFilePath(packageName)
	if err != nil {
		return err
	}
	
	// Get store and symlink paths
	storePath, err := getStorePath()
	if err != nil {
		return err
	}
	packageStorePath := filepath.Join(storePath, packageName)
	
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	symlinkPath := filepath.Join(homeDir, ".local/bin", packageName)
	configDir := filepath.Join(homeDir, ".config", packageName)
	
	// Create comprehensive lock file content in box syntax
	lockContent := fmt.Sprintf(`[data -c lock]
  package %s
  repo %s
  source %s
  source_type %s
  source_version %s
  recipe_version %s
  recipe_url %s
  installed_at %s
  store_path %s
  symlink_path %s
  config_dir %s
  sha256 %s
end
`, packageName, repo, sourceURL, sourceType, sourceVersion, recipeVersion, recipeURL, time.Now().UTC().Format(time.RFC3339), packageStorePath, symlinkPath, configDir, recipeSHA256)
	
	return os.WriteFile(lockFilePath, []byte(lockContent), 0644)
}

// extractRecipeURL extracts the URL from the recipe data block
func extractRecipeURL(scriptPath string) (string, error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	inDataBlock := false
	
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		if strings.HasPrefix(trimmed, "[data") && strings.Contains(trimmed, "pkg") {
			inDataBlock = true
			continue
		}
		
		if inDataBlock && trimmed == "end" {
			break
		}
		
		if inDataBlock && strings.HasPrefix(trimmed, "url ") {
			url := strings.TrimSpace(strings.TrimPrefix(trimmed, "url"))
			// Remove surrounding quotes if present
			url = strings.Trim(url, "\"'")
			return url, nil
		}
	}
	
	return "", fmt.Errorf("url not found in recipe")
}

// detectSourceTypeAndVersion determines the source type and captures version information
func detectSourceTypeAndVersion(sourceURL string) (sourceType, sourceVersion string, err error) {
	// Determine source type based on URL patterns
	if strings.Contains(sourceURL, "github.com") || strings.Contains(sourceURL, "gitlab.com") || strings.HasSuffix(sourceURL, ".git") {
		sourceType = "git"
		// For git sources, get the latest commit hash
		sourceVersion, err = getGitHeadCommit(sourceURL)
		if err != nil {
			sourceVersion = "unknown"
			err = nil // Don't fail installation for version detection issues
		}
	} else {
		sourceType = "download"
		// For direct downloads, we'll calculate hash after download
		sourceVersion = "pending" // Will be updated after file download
	}
	
	return sourceType, sourceVersion, nil
}

// getGitHeadCommit gets the HEAD commit hash from a git repository
func getGitHeadCommit(repoURL string) (string, error) {
	cmd := exec.Command("git", "ls-remote", repoURL, "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git commit: %v", err)
	}
	
	// Parse the output to get the commit hash
	parts := strings.Fields(string(output))
	if len(parts) > 0 {
		return parts[0][:8], nil // Return short commit hash (8 chars)
	}
	
	return "", fmt.Errorf("no commit hash found")
}

// calculateRecipeVersion calculates the hash of recipe content excluding sha256 field
func calculateRecipeVersion(scriptPath string) (string, error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", err
	}
	
	contentWithoutSHA256, err := removeCSHA256Field(content)
	if err != nil {
		return "", err
	}
	
	return calculateSHA256(contentWithoutSHA256), nil
}

// constructRecipeURL constructs the recipe URL based on selected source
func constructRecipeURL(selectedSource PackageSource, packageName string) string {
	if selectedSource.Type == "local" {
		return "local"
	}
	
	// For remote sources, construct the recipe URL
	return fmt.Sprintf("%s/raw/main/%s.box", selectedSource.Name, packageName)
}

// getSourceCommit gets the latest commit hash from a git repository URL
func getSourceCommit(sourceURL string) (string, error) {
	// For now, return a placeholder. In a real implementation, this would
	// use git ls-remote or similar to get the actual commit hash
	cmd := exec.Command("git", "ls-remote", sourceURL, "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "unknown", nil // Don't fail if we can't get commit info
	}
	
	// Parse the output to get the commit hash
	parts := strings.Fields(string(output))
	if len(parts) > 0 {
		return parts[0][:8], nil // Return short commit hash
	}
	
	return "unknown", nil
}

// extractPackageInfo extracts version and SHA256 from a recipe
func extractPackageInfo(scriptPath string) (version, sha256 string, err error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read script: %v", err)
	}

	lines := strings.Split(string(content), "\n")
	var inDataBlock bool
	var blockIndent int

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Check for data block start
		if strings.HasPrefix(trimmed, "[data") {
			inDataBlock = true
			blockIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			continue
		}
		
		// Check for block end
		if inDataBlock && trimmed == "end" {
			break
		}
		
		// Check for any other block start
		if inDataBlock && strings.HasPrefix(trimmed, "[") {
			break
		}
		
		// Parse data block content
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			lineIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			if lineIndent > blockIndent {
				parts := strings.SplitN(trimmed, " ", 2)
				if len(parts) >= 2 {
					key := parts[0]
					value := strings.TrimSpace(parts[1])
					switch key {
					case "ver":
						version = value
					case "sha256":
						sha256 = value
					}
				}
			}
		}
	}
	
	if version == "" {
		version = "unknown"
	}
	if sha256 == "" {
		sha256 = "unknown"
	}
	
	return version, sha256, nil
}

// removeCSHA256Field removes the sha256 field from file content for hash calculation
func removeCSHA256Field(content []byte) ([]byte, error) {
	lines := strings.Split(string(content), "\n")
	var filteredLines []string

	for _, line := range lines {
		// Simple approach: skip any line that contains "sha256" as the first field after whitespace
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && strings.HasPrefix(trimmed, "sha256 ") {
			// Skip this line
			continue
		}
		// Add all other lines
		filteredLines = append(filteredLines, line)
	}
	
	return []byte(strings.Join(filteredLines, "\n")), nil
}

// executeUninstallScript runs the universal uninstaller with the package's lock file
func executeUninstallScript(packageName string) error {
	// Check if lock file exists
	lockFilePath, err := getLockFilePath(packageName)
	if err != nil {
		return fmt.Errorf("failed to get lock file path: %v", err)
	}
	
	if _, err := os.Stat(lockFilePath); os.IsNotExist(err) {
		return fmt.Errorf("package %s is not installed (no lock file found)", packageName)
	}
	
	// Create temporary directory for uninstall script
	tempDir, err := os.MkdirTemp("", "pack-uninstall-"+packageName)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	// Read lock file content and create a combined uninstaller script
	lockContent, err := os.ReadFile(lockFilePath)
	if err != nil {
		return fmt.Errorf("failed to read lock file: %v", err)
	}
	
	// Create combined uninstaller script with embedded lock data
	combinedScript := string(lockContent) + `
[fn uninstall]
  echo "uninstalling ${lock.package}..."
  
  echo "removing symlink at: ${lock.symlink_path}"
  delete ${lock.symlink_path}
  echo "removed ${lock.package} symlink"
  
  echo "removing store directory: ${lock.store_path}"
  delete ${lock.store_path}
  echo "removed ${lock.package} store files"
  
  echo "config directory ${lock.config_dir} will be preserved (remove manually if desired)"
  echo "${lock.package} uninstallation complete!"
end

[main]
  uninstall
end
`
	
	uninstallScriptPath := filepath.Join(tempDir, "uninstall.box")
	if err := os.WriteFile(uninstallScriptPath, []byte(combinedScript), 0644); err != nil {
		return fmt.Errorf("failed to create combined uninstall script: %v", err)
	}
	
	// Find box executable
	boxPath, err := findBoxExecutable()
	if err != nil {
		return fmt.Errorf("box executable not found: %v", err)
	}
	
	// Execute uninstall script
	execCmd := exec.Command(boxPath, uninstallScriptPath)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Stdin = os.Stdin
	execCmd.Dir = tempDir // Set working directory so import can find package.lock
	
	err = execCmd.Run()
	if err != nil {
		return err
	}
	
	// Remove lock file after successful uninstall
	fmt.Println("removing lockfile...")
	if err := os.Remove(lockFilePath); err != nil {
		fmt.Printf("warning: failed to remove lockfile: %v\n", err)
	} else {
		fmt.Println("✓ lockfile removed")
	}
	
	return nil
}

// listPackages displays all installed packages with their version information
func listPackages(cmd *cobra.Command, args []string) {
	if len(args) > 0 && args[0] == "help" {
		showListHelp()
		return
	}
	
	packPath, err := getPackDir()
	if err != nil {
		fmt.Printf("error getting pack directory: %v\n", err)
		os.Exit(1)
	}

	locksDir := filepath.Join(packPath, "locks")
	if _, err := os.Stat(locksDir); os.IsNotExist(err) {
		fmt.Println("no packages installed")
		return
	}

	files, err := os.ReadDir(locksDir)
	if err != nil {
		fmt.Printf("error reading locks directory: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Println("no packages installed")
		return
	}

	fmt.Printf("%-15s %-12s %-30s %s\n", "package", "version", "source", "installed")
	fmt.Printf("%-15s %-12s %-30s %s\n", "-------", "-------", "------", "---------")

	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".lock") {
			continue
		}

		packageName := strings.TrimSuffix(file.Name(), ".lock")
		lockData, err := parseLockFile(filepath.Join(locksDir, file.Name()))
		if err != nil {
			fmt.Printf("%-15s %-12s %-30s %s\n", packageName, "error", "error", "error")
			continue
		}

		// Format the display
		version := lockData["source_version"]
		if len(version) > 12 {
			version = version[:12]
		}
		source := lockData["source"]
		if len(source) > 30 {
			source = source[:27] + "..."
		}
		installDate := lockData["installed_at"]
		if installDate != "" {
			// Parse and format the date
			if t, err := time.Parse(time.RFC3339, installDate); err == nil {
				installDate = t.Format("2006-01-02")
			}
		}

		fmt.Printf("%-15s %-12s %-30s %s\n", packageName, version, source, installDate)
	}
}

// updatePackages scans for updates and installs them with confirmation
func updatePackages(cmd *cobra.Command, args []string) {
	if len(args) > 0 && args[0] == "help" {
		showUpdateHelp()
		return
	}
	
	fmt.Println("scanning for package updates...")
	
	availableUpdates, err := scanForUpdates()
	if err != nil {
		fmt.Printf("error scanning for updates: %v\n", err)
		os.Exit(1)
	}

	if len(availableUpdates) == 0 {
		fmt.Println("all packages are up to date")
		return
	}

	// Display available updates
	fmt.Printf("\navailable updates:\n")
	for _, update := range availableUpdates {
		fmt.Printf("- %s: %s → %s (%s)\n", 
			update.PackageName, 
			update.CurrentVersion, 
			update.NewVersion, 
			update.UpdateType)
	}

	// Ask for confirmation
	fmt.Printf("\nupdate %d package(s)? [y/N]: ", len(availableUpdates))
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("error reading input: %v\n", err)
		os.Exit(1)
	}
	
	response = strings.TrimSpace(strings.ToLower(response))
	if response != "y" && response != "yes" {
		fmt.Println("update cancelled")
		return
	}

	// Perform updates
	fmt.Println("\nupdating packages...")
	for _, update := range availableUpdates {
		fmt.Printf("Updating %s...\n", update.PackageName)
		if err := updatePackageFromOriginalSource(update.PackageName); err != nil {
			fmt.Printf("error updating %s: %v\n", update.PackageName, err)
		} else {
			fmt.Printf("✓ %s updated successfully\n", update.PackageName)
		}
	}
	
	fmt.Println("\nupdate complete!")
}

// PackageUpdate represents an available update
type PackageUpdate struct {
	PackageName    string
	CurrentVersion string
	NewVersion     string
	UpdateType     string // "source updated", "recipe updated", "both updated"
}

// scanForUpdates checks all installed packages for available updates
func scanForUpdates() ([]PackageUpdate, error) {
	var updates []PackageUpdate

	packPath, err := getPackDir()
	if err != nil {
		return nil, err
	}

	locksDir := filepath.Join(packPath, "locks")
	if _, err := os.Stat(locksDir); os.IsNotExist(err) {
		return updates, nil // No packages installed
	}

	files, err := os.ReadDir(locksDir)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".lock") {
			continue
		}

		packageName := strings.TrimSuffix(file.Name(), ".lock")
		lockData, err := parseLockFile(filepath.Join(locksDir, file.Name()))
		if err != nil {
			continue // Skip problematic lock files
		}

		update, hasUpdate, err := checkPackageForUpdate(packageName, lockData)
		if err != nil {
			fmt.Printf("warning: failed to check updates for %s: %v\n", packageName, err)
			continue
		}

		if hasUpdate {
			updates = append(updates, update)
		}
	}

	return updates, nil
}

// parseLockFile parses a lock file and returns a map of key-value pairs
func parseLockFile(lockFilePath string) (map[string]string, error) {
	content, err := os.ReadFile(lockFilePath)
	if err != nil {
		return nil, err
	}

	lockData := make(map[string]string)
	lines := strings.Split(string(content), "\n")
	inDataBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		if strings.HasPrefix(trimmed, "[data") && strings.Contains(trimmed, "lock") {
			inDataBlock = true
			continue
		}
		
		if inDataBlock && trimmed == "end" {
			break
		}
		
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			parts := strings.SplitN(trimmed, " ", 2)
			if len(parts) >= 2 {
				lockData[parts[0]] = strings.TrimSpace(parts[1])
			}
		}
	}

	return lockData, nil
}

// checkPackageForUpdate checks if a package has available updates
func checkPackageForUpdate(packageName string, lockData map[string]string) (PackageUpdate, bool, error) {
	update := PackageUpdate{
		PackageName:    packageName,
		CurrentVersion: lockData["source_version"],
	}

	var updateReasons []string

	// Check recipe for updates
	recipeURL := lockData["recipe_url"]
	if recipeURL != "" && recipeURL != "local" {
		// Download current recipe and compare hash
		currentRecipeVersion, err := getCurrentRecipeVersion(recipeURL)
		if err == nil && currentRecipeVersion != lockData["recipe_version"] {
			updateReasons = append(updateReasons, "recipe updated")
		}
	}

	// Check source for updates
	sourceURL := lockData["source"]
	sourceType := lockData["source_type"]
	if sourceURL != "" && sourceURL != "unknown" {
		newSourceVersion, err := getCurrentSourceVersion(sourceURL, sourceType)
		if err == nil && newSourceVersion != lockData["source_version"] {
			updateReasons = append(updateReasons, "source updated")
			update.NewVersion = newSourceVersion
		}
	}

	if len(updateReasons) == 0 {
		return update, false, nil
	}

	update.UpdateType = strings.Join(updateReasons, ", ")
	if update.NewVersion == "" {
		update.NewVersion = "latest"
	}

	return update, true, nil
}

// getCurrentRecipeVersion downloads a recipe and calculates its version hash
func getCurrentRecipeVersion(recipeURL string) (string, error) {
	// Create temp file to download recipe
	tempFile, err := os.CreateTemp("", "recipe-*.box")
	if err != nil {
		return "", err
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Download recipe
	if err := downloadFile(recipeURL, tempFile.Name()); err != nil {
		return "", err
	}

	// Calculate version hash
	return calculateRecipeVersion(tempFile.Name())
}

// getCurrentSourceVersion gets the current version of a source
func getCurrentSourceVersion(sourceURL, sourceType string) (string, error) {
	switch sourceType {
	case "git":
		return getGitHeadCommit(sourceURL)
	case "download":
		// For downloads, we'd need to download and hash, but that's expensive
		// For now, just indicate that we can't easily check
		return "latest", nil
	default:
		return "", fmt.Errorf("unknown source type: %s", sourceType)
	}
}

// updatePackageFromOriginalSource updates a package using the same source it was originally installed from
func updatePackageFromOriginalSource(packageName string) error {
	// Read the lock file to get original source info
	lockFilePath, err := getLockFilePath(packageName)
	if err != nil {
		return fmt.Errorf("failed to get lock file path: %v", err)
	}

	lockData, err := parseLockFile(lockFilePath)
	if err != nil {
		return fmt.Errorf("failed to parse lock file: %v", err)
	}

	originalRepo := lockData["repo"]
	if originalRepo == "" {
		return fmt.Errorf("no original repo found in lock file")
	}

	// Create temporary directory for script
	tempDir, err := os.MkdirTemp("", "pack-update-"+packageName)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Download script from original source
	scriptPath := filepath.Join(tempDir, packageName+".box")
	
	var selectedSource PackageSource
	if originalRepo == "local" {
		// Use local source
		localRepoPath, err := getLocalRepoPath()
		if err != nil {
			return fmt.Errorf("failed to get local repo path: %v", err)
		}
		localPackagePath := filepath.Join(localRepoPath, packageName+".box")
		if _, err := os.Stat(localPackagePath); err != nil {
			return fmt.Errorf("package not found in local repository: %v", err)
		}
		
		selectedSource = PackageSource{
			Name: "local",
			URL:  localPackagePath,
			Type: "local",
		}
		
		if err := copyFile(localPackagePath, scriptPath); err != nil {
			return fmt.Errorf("failed to copy from local source: %v", err)
		}
	} else {
		// Use remote source
		scriptURL := fmt.Sprintf("%s/raw/main/%s.box", originalRepo, packageName)
		
		selectedSource = PackageSource{
			Name: originalRepo,
			URL:  scriptURL,
			Type: "remote",
		}
		
		if err := downloadFile(scriptURL, scriptPath); err != nil {
			return fmt.Errorf("failed to download from original source: %v", err)
		}
	}
	
	fmt.Printf("Using original source: %s\n", selectedSource.Name)

	// Verify recipe integrity
	fmt.Println("Verifying recipe integrity...")
	if err := verifyRecipeIntegrity(scriptPath); err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
		fmt.Print("Continue anyway? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %v", err)
		}
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			return fmt.Errorf("update cancelled due to verification failure")
		}
	} else {
		fmt.Println("✓ recipe integrity verified")
	}

	// Show recipe and get user confirmation
	if err := showRecipeAndConfirm(scriptPath); err != nil {
		return err
	}

	// Find box executable
	boxPath, err := findBoxExecutable()
	if err != nil {
		return fmt.Errorf("box executable not found: %v", err)
	}

	// Execute script
	cmdArgs := []string{scriptPath}

	execCmd := exec.Command(boxPath, cmdArgs...)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Stdin = os.Stdin

	err = execCmd.Run()
	if err != nil {
		return err
	}

	// Create or update lock file after successful installation
	fmt.Println("updating lockfile...")
	
	// Extract source URL from recipe
	recipeSourceURL, err := extractRecipeURL(scriptPath)
	if err != nil {
		fmt.Printf("Warning: failed to extract source URL: %v\n", err)
		recipeSourceURL = "unknown"
	}
	
	// Detect source type and get version information
	sourceType, sourceVersion, err := detectSourceTypeAndVersion(recipeSourceURL)
	if err != nil {
		fmt.Printf("warning: failed to detect source type: %v\n", err)
		sourceType = "unknown"
		sourceVersion = "unknown"
	}
	
	// Calculate recipe version (content hash)
	recipeVersion, err := calculateRecipeVersion(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to calculate recipe version: %v\n", err)
		recipeVersion = "unknown"
	}
	
	// Construct recipe URL from selected source
	recipeURL := constructRecipeURL(selectedSource, packageName)
	
	// Calculate actual SHA256 for lock file
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to read script for hash: %v\n", err)
	}
	contentWithoutSHA256, err := removeCSHA256Field(content)
	if err != nil {
		fmt.Printf("warning: failed to remove SHA256 field: %v\n", err)
	}
	recipeSHA256 := calculateSHA256(contentWithoutSHA256)
	
	// Get repo name from selected source
	repoName := selectedSource.Name
	
	if err := createLockFile(packageName, repoName, recipeSourceURL, sourceType, sourceVersion, recipeVersion, recipeURL, recipeSHA256); err != nil {
		fmt.Printf("warning: failed to update lock file: %v\n", err)
	} else {
		fmt.Println("✓ lockfile updated")
	}

	return nil
}

// showHelp displays general help information
func showHelp(cmd *cobra.Command, args []string) {
	fmt.Println("pack - a package manager using boxlang")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack <command> [arguments]")
	fmt.Println()
	fmt.Println("COMMANDS:")
	fmt.Println("  open <package>     install a package")
	fmt.Println("  close <package>    uninstall a package")
	fmt.Println("  list               list installed packages")
	fmt.Println("  update             check for and install package updates")
	fmt.Println("  peek <package>     show package information")
	fmt.Println("  add-source <url>   add a repository source")
	fmt.Println("  help               show this help information")
	fmt.Println()
	fmt.Println("For command-specific help, use: pack <command> help")
	fmt.Println("Example: pack open help")
}

// showOpenHelp displays help for the open command
func showOpenHelp() {
	fmt.Println("pack open - install a package")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack open <package>")
	fmt.Println("  pack open help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  downloads and installs a package from configured sources.")
	fmt.Println("  if multiple sources have the package, you'll be prompted to choose.")
	fmt.Println()
	fmt.Println("  the installation process:")
	fmt.Println("  1. finds the package in available sources")
	fmt.Println("  2. downloads and verifies the recipe")
	fmt.Println("  3. shows the recipe for review")
	fmt.Println("  4. executes the installation script")
	fmt.Println("  5. creates a lockfile for tracking")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  pack open vim      # Install vim text editor")
	fmt.Println("  pack open pfetch   # Install pfetch system info tool")
}

// showCloseHelp displays help for the close command
func showCloseHelp() {
	fmt.Println("pack close - uninstall a package")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack close <package>")
	fmt.Println("  pack close help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  uninstalls a previously installed package using the universal")
	fmt.Println("  uninstaller with information from the package's lock file.")
	fmt.Println()
	fmt.Println("  the uninstallation process:")
	fmt.Println("  1. reads the package lock file")
	fmt.Println("  2. removes the installed binary")
	fmt.Println("  3. preserves configuration files")
	fmt.Println("  4. removes the lockfile")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  pack close vim     # Uninstall vim")
	fmt.Println("  pack close pfetch  # Uninstall pfetch")
}

// showListHelp displays help for the list command
func showListHelp() {
	fmt.Println("pack list - list installed packages")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack list")
	fmt.Println("  pack list help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  shows all installed packages with their version information,")
	fmt.Println("  source repository, and installation date.")
	fmt.Println()
	fmt.Println("  columns displayed:")
	fmt.Println("  - PACKAGE: Package name")
	fmt.Println("  - VERSION: Source version (git commit or content hash)")
	fmt.Println("  - SOURCE: Origin URL of the package")
	fmt.Println("  - INSTALLED: Installation date")
	fmt.Println()
	fmt.Println("EXAMPLE:")
	fmt.Println("  pack list")
}

// showUpdateHelp displays help for the update command
func showUpdateHelp() {
	fmt.Println("pack update - check for and install package updates")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack update")
	fmt.Println("  pack update help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  scans all installed packages for available updates by comparing")
	fmt.Println("  current versions with remote sources. Updates use the same")
	fmt.Println("  source repository that was used for original installation.")
	fmt.Println()
	fmt.Println("  the update process:")
	fmt.Println("  1. checks all packages for updates")
	fmt.Println("  2. shows available updates")
	fmt.Println("  3. asks for confirmation")
	fmt.Println("  4. updates all confirmed packages")
	fmt.Println()
	fmt.Println("  update detection:")
	fmt.Println("  - git packages: compares commit hashes")
	fmt.Println("  - recipe changes: compares recipe content")
	fmt.Println()
	fmt.Println("EXAMPLE:")
	fmt.Println("  pack update")
}

// showPeekHelp displays help for the peek command
func showPeekHelp() {
	fmt.Println("pack peek - show package information")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack peek <package>")
	fmt.Println("  pack peek help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  downloads and displays information about a package without")
	fmt.Println("  installing it. shows package metadata from the recipe.")
	fmt.Println()
	fmt.Println("  information displayed:")
	fmt.Println("  - package name and description")
	fmt.Println("  - version and supported operating systems")
	fmt.Println("  - source URL and license")
	fmt.Println("  - author information")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  pack peek vim      # Show vim package info")
	fmt.Println("  pack peek pfetch   # Show pfetch package info")
}

// showAddSourceHelp displays help for the add-source command
func showAddSourceHelp() {
	fmt.Println("pack add-source - add a repository source")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack add-source <url>")
	fmt.Println("  pack add-source help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  adds a new repository source for package discovery.")
	fmt.Println("  sources are searched when installing packages.")
	fmt.Println()
	fmt.Println("  supported source types:")
	fmt.Println("  - github repositories")
	fmt.Println("  - other git repositories with web access")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  pack add-source https://github.com/user/pack-repo")
	fmt.Println("  pack add-source https://gitlab.com/user/packages")
}
