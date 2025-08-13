package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	defaultRepo = "https://github.com/shrub4thedub/pack-repo"
	configDir   = ".config/pack"
	configFile  = "config.box"
)

type Config struct {
	Sources []string
}

func main() {
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
	
	if useLocal {
		// Use local test repository
		localScriptPath := filepath.Join("test-repo", packageName+".box")
		if err := copyFile(localScriptPath, scriptPath); err != nil {
			return fmt.Errorf("failed to copy local script: %v", err)
		}
	} else {
		// Try to find script in configured sources
		if err := downloadFromSources(packageName, scriptPath); err != nil {
			return fmt.Errorf("failed to download script: %v", err)
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

	return execCmd.Run()
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
		// Use local test repository
		localScriptPath := filepath.Join("test-repo", packageName+".box")
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

func getConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	
	configPath := filepath.Join(homeDir, configDir, configFile)
	return configPath, nil
}

func ensureConfigExists() error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}
	
	// Check if config file exists
	if _, err := os.Stat(configPath); err == nil {
		return nil // Config exists
	}
	
	// Create config directory
	configDirPath := filepath.Dir(configPath)
	if err := os.MkdirAll(configDirPath, 0755); err != nil {
		return err
	}
	
	// Create default config
	defaultConfig := `[data -c sources]
  repo ` + defaultRepo + `
end`
	
	return os.WriteFile(configPath, []byte(defaultConfig), 0644)
}

func loadConfig() (*Config, error) {
	if err := ensureConfigExists(); err != nil {
		return nil, err
	}
	
	configPath, err := getConfigPath()
	if err != nil {
		return nil, err
	}
	
	content, err := os.ReadFile(configPath)
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
	
	var content strings.Builder
	content.WriteString("[data -c sources]\n")
	
	for _, source := range config.Sources {
		content.WriteString("  repo " + source + "\n")
	}
	
	content.WriteString("end\n")
	
	return os.WriteFile(configPath, []byte(content.String()), 0644)
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