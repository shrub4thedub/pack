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
		Long:  `pack fetches and executes boxlang installation scripts from a repository.`,
	}

	var openCmd = &cobra.Command{
		Use:   "open [package]",
		Short: "install a package",
		Args:  cobra.ExactArgs(1),
		Run:   openPackage,
	}

	var closeCmd = &cobra.Command{
		Use:   "close [package]",
		Short: "uninstall a package",
		Args:  cobra.ExactArgs(1),
		Run:   closePackage,
	}

	var peekCmd = &cobra.Command{
		Use:   "peek [package]",
		Short: "show package information",
		Args:  cobra.ExactArgs(1),
		Run:   peekPackage,
	}

	var addSourceCmd = &cobra.Command{
		Use:   "add-source [url]",
		Short: "add a repository source",
		Args:  cobra.ExactArgs(1),
		Run:   addSource,
	}

	// Add --local flag for testing
	rootCmd.PersistentFlags().BoolVar(&useLocal, "local", false, "use local test repository")

	rootCmd.AddCommand(openCmd)
	rootCmd.AddCommand(closeCmd)
	rootCmd.AddCommand(peekCmd)
	rootCmd.AddCommand(addSourceCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

var useLocal bool

func openPackage(cmd *cobra.Command, args []string) {
	packageName := args[0]
	fmt.Printf("opening package: %s\n", packageName)
	
	if err := executePackageScript(packageName, false); err != nil {
		fmt.Printf("error opening package %s: %v\n", packageName, err)
		os.Exit(1)
	}
}

func closePackage(cmd *cobra.Command, args []string) {
	packageName := args[0]
	fmt.Printf("closing package: %s\n", packageName)
	
	if err := executePackageScript(packageName, true); err != nil {
		fmt.Printf("error closing package %s: %v\n", packageName, err)
		os.Exit(1)
	}
}

func peekPackage(cmd *cobra.Command, args []string) {
	packageName := args[0]
	
	if err := showPackageInfo(packageName); err != nil {
		fmt.Printf("error showing package info for %s: %v\n", packageName, err)
		os.Exit(1)
	}
}

func addSource(cmd *cobra.Command, args []string) {
	sourceURL := args[0]
	
	if err := addSourceToConfig(sourceURL); err != nil {
		fmt.Printf("error adding source: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Printf("added source: %s\n", sourceURL)
}

func executePackageScript(packageName string, uninstall bool) error {
	// Create temporary directory for script
	tempDir, err := os.MkdirTemp("", "pack-"+packageName)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Download or copy script
	scriptPath := filepath.Join(tempDir, packageName+".box")
	
	var sourceURL string
	if useLocal {
		// Use local repository in ~/.pack/local
		localRepoPath, err := getLocalRepoPath()
		if err != nil {
			return fmt.Errorf("failed to get local repo path: %v", err)
		}
		localScriptPath := filepath.Join(localRepoPath, packageName+".box")
		if err := copyFile(localScriptPath, scriptPath); err != nil {
			return fmt.Errorf("failed to copy local script: %v", err)
		}
		sourceURL = "local"
	} else {
		// Try to find script in configured sources
		config, err := loadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %v", err)
		}
		if len(config.Sources) > 0 {
			sourceURL = config.Sources[0] // Use first source for lock file
		}
		
		if err := downloadFromSources(packageName, scriptPath); err != nil {
			return fmt.Errorf("failed to download script: %v", err)
		}
	}

	// Verify recipe integrity
	if !uninstall {
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
				return fmt.Errorf("installation cancelled due to verification failure")
			}
		} else {
			fmt.Println("✓ Recipe integrity verified")
		}
	}

	// Show recipe and get user confirmation
	if !uninstall {
		if err := showRecipeAndConfirm(scriptPath); err != nil {
			return err
		}
	}

	// Find box executable
	boxPath, err := findBoxExecutable()
	if err != nil {
		return fmt.Errorf("box executable not found: %v", err)
	}

	// Execute script
	var cmdArgs []string
	if uninstall {
		cmdArgs = []string{scriptPath, "uninstall"}
	} else {
		cmdArgs = []string{scriptPath}
	}

	execCmd := exec.Command(boxPath, cmdArgs...)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Stdin = os.Stdin

	err = execCmd.Run()
	if err != nil {
		return err
	}

	// Create or update lock file after successful installation
	if !uninstall {
		fmt.Println("Creating lock file...")
		
		// Extract package info from recipe
		version, recipeSHA256, err := extractPackageInfo(scriptPath)
		if err != nil {
			fmt.Printf("Warning: failed to extract package info: %v\n", err)
			version = "unknown"
			recipeSHA256 = "unknown"
		}
		
		// Get commit hash from source
		commit, err := getSourceCommit(sourceURL)
		if err != nil {
			commit = "unknown"
		}
		
		if err := createLockFile(packageName, version, sourceURL, commit, recipeSHA256); err != nil {
			fmt.Printf("Warning: failed to create lock file: %v\n", err)
		} else {
			fmt.Println("✓ Lock file created")
		}
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

	// Download or copy script
	scriptPath := filepath.Join(tempDir, packageName+".box")
	
	if useLocal {
		// Use local repository in ~/.pack/local
		localRepoPath, err := getLocalRepoPath()
		if err != nil {
			return fmt.Errorf("failed to get local repo path: %v", err)
		}
		localScriptPath := filepath.Join(localRepoPath, packageName+".box")
		if err := copyFile(localScriptPath, scriptPath); err != nil {
			return fmt.Errorf("failed to copy local script: %v", err)
		}
	} else {
		// Try to find script in configured sources
		if err := downloadFromSources(packageName, scriptPath); err != nil {
			return fmt.Errorf("failed to download script: %v", err)
		}
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
	subdirs := []string{"locks", "config", "tmp", "local"}
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

func downloadFromSources(packageName string, scriptPath string) error {
	config, err := loadConfig()
	if err != nil {
		return err
	}
	
	if len(config.Sources) == 0 {
		return fmt.Errorf("no sources configured")
	}
	
	var lastErr error
	for _, source := range config.Sources {
		// Try raw github content URL format
		scriptURL := fmt.Sprintf("%s/raw/main/%s.box", source, packageName)
		if strings.Contains(source, "raw.githubusercontent.com") {
			scriptURL = fmt.Sprintf("%s/%s.box", source, packageName)
		}
		
		err := downloadFile(scriptURL, scriptPath)
		if err == nil {
			return nil // Success
		}
		lastErr = err
	}
	
	return fmt.Errorf("package not found in any source: %v", lastErr)
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
	
	// Calculate actual hash
	actualHash := calculateSHA256(content)
	
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

// createLockFile creates a lock file for an installed package
func createLockFile(packageName, version, source, commit, recipeSHA256 string) error {
	lockFilePath, err := getLockFilePath(packageName)
	if err != nil {
		return err
	}
	
	// Get installation paths (assuming standard paths for now)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	installedTo := filepath.Join(homeDir, ".local/bin", packageName)
	configDir := filepath.Join(homeDir, ".config", packageName)
	
	// Create lock file content in box syntax
	lockContent := fmt.Sprintf(`[data -c lock]
  package %s
  version %s
  source %s
  commit %s
  installed_at %s
  recipe_sha256 %s
  installed_to %s
  config_dir %s
end
`, packageName, version, source, commit, time.Now().UTC().Format(time.RFC3339), recipeSHA256, installedTo, configDir)
	
	return os.WriteFile(lockFilePath, []byte(lockContent), 0644)
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